// Package docker implements the sandbox contract on top of the docker CLI.
// Managed workloads use the container restart policy for process recovery.
package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/roshbhatia/ere/internal/backend"
	"github.com/roshbhatia/ere/internal/sandbox"
)

const (
	// Prefix keeps ere containers distinguishable in a shared docker daemon.
	Prefix = "ere-"
	// LabelSandbox is how List finds containers ere owns.
	LabelSandbox = "ere.sandbox"
	// DefaultImage ships amp and a shell. Build it from images/amp/Dockerfile.
	DefaultImage = "ere/amp:latest"
)

// Backend drives one docker daemon. Binary may name a drop-in such as podman.
type Backend struct {
	Binary  string
	Context string
}

// New returns a backend using the docker CLI.
func New(binary string) *Backend {
	if binary == "" {
		binary = "docker"
	}
	return &Backend{Binary: binary}
}

func (b *Backend) Name() string { return "docker" }

func container(name string) string { return Prefix + name }

func (b *Backend) Probe(ctx context.Context) (sandbox.Probe, error) {
	probe := sandbox.Probe{Backend: b.Name(), Operations: append(sandbox.Operations(), sandbox.OpValidate, sandbox.OpConnect), Contract: sandbox.ContractVersion, StorageModes: []string{"bind", "volume"}, Supervision: "container-restart", Retention: "binds and named volumes survive removal"}
	if !backend.Available(b.Binary) {
		probe.Detail = b.Binary + " is not on PATH"
		return probe, nil
	}
	out, err := b.run(ctx, "version", "--format", "{{.Server.Version}}")
	if err != nil {
		probe.Detail = err.Error()
		return probe, nil
	}
	if out.ExitCode != 0 {
		probe.Detail = strings.TrimSpace(out.Stderr)
		return probe, nil
	}
	probe.Available = true
	probe.Detail = "server " + strings.TrimSpace(out.Stdout)
	return probe, nil
}

func (b *Backend) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Status, error) {
	if _, err := b.Validate(ctx, spec); err != nil {
		return sandbox.Status{}, err
	}
	if spec.Storage.Kind != "" && spec.Storage.Kind != "bind" && spec.Storage.Kind != "volume" {
		return sandbox.Status{}, fmt.Errorf("docker supports bind or volume storage")
	}
	if spec.BootVolume != "" {
		return sandbox.Status{}, fmt.Errorf("docker does not support bootVolume")
	}
	workspace := spec.Workspace
	if spec.Storage.Source != "" {
		workspace = spec.Storage.Source
	}
	existing, err := b.Status(ctx, sandbox.Ref{Name: spec.Name})
	if err != nil {
		return existing, err
	}
	if existing.State != sandbox.StateAbsent {
		if existing.Digest != "" && existing.Digest != sandbox.Digest(spec) {
			return existing, fmt.Errorf("configuration changed: container replacement required; retained binds and named volumes survive rm")
		}
		if spec.Workload != nil && existing.Managed {
			if err := b.installEnv(ctx, existing.ResourceID, spec.Workload.Env); err != nil {
				return existing, err
			}
		}
		return existing, nil
	}
	image := spec.Image
	if image == "" {
		image = DefaultImage
	}
	mount := spec.MountPath
	if mount == "" {
		mount = "/workspace"
	}
	args := []string{
		"create",
		"--name", container(spec.Name),
		"--hostname", spec.Name,
		"--label", LabelSandbox + "=" + spec.Name,
		"--label", "ere.backend=docker",
		"--workdir", mount,
		"--init",
	}
	if workspace != "" {
		bind := workspace + ":" + mount
		if spec.ReadOnly {
			bind += ":ro"
		}
		args = append(args, "--volume", bind)
	}
	if spec.CPUs > 0 {
		args = append(args, "--cpus", strconv.Itoa(spec.CPUs))
	}
	if spec.MemoryMB > 0 {
		args = append(args, "--memory", strconv.Itoa(spec.MemoryMB)+"m")
	}
	for key, value := range spec.Labels {
		if key == LabelSandbox || key == "ere.backend" || key == "ere.owner" || key == "ere.digest" {
			continue
		}
		args = append(args, "--label", key+"="+value)
	}
	if spec.Architecture != "" {
		args = append(args, "--platform", "linux/"+spec.Architecture)
	}
	if spec.Workload != nil {
		id, err := backend.LoadIdentity("docker|"+b.Context, spec.Name)
		if err != nil {
			return sandbox.Status{}, err
		}
		id.UID = ""
		if err := id.Save(); err != nil {
			return sandbox.Status{}, err
		}
		args = append(args, "--restart", "unless-stopped", "--label", "ere.owner="+id.Owner, "--label", "ere.digest="+sandbox.Digest(spec))
		args = append(args, image, "sh", "-c", "set -a; . /var/lib/ere/env; set +a; "+backend.WorkloadScript(*spec.Workload))
	} else {
		args = append(args, image, "sleep", "infinity")
	}

	sandbox.Progress(ctx, "create", "creating container "+container(spec.Name))
	if _, err := b.check(ctx, args...); err != nil {
		return sandbox.Status{}, err
	}
	if spec.Workload != nil {
		status, err := b.Status(ctx, sandbox.Ref{Name: spec.Name})
		if err != nil {
			return status, err
		}
		if err := b.installEnv(ctx, status.ResourceID, spec.Workload.Env); err != nil {
			return status, err
		}
	}
	return b.Status(ctx, sandbox.Ref{Name: spec.Name})
}

func (b *Backend) Start(ctx context.Context, ref sandbox.Ref) (sandbox.Status, error) {
	status, err := b.Status(ctx, ref)
	if err != nil {
		return status, err
	}
	if status.State == sandbox.StateAbsent {
		return status, nil
	}
	sandbox.Progress(ctx, "start", "starting "+container(ref.Name))
	if _, err := b.check(ctx, "start", status.ResourceID); err != nil {
		return sandbox.Status{}, err
	}
	return b.Status(ctx, ref)
}

func (b *Backend) Exec(ctx context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
	status, err := b.Status(ctx, sandbox.Ref{Name: req.Name})
	if err != nil {
		return sandbox.ExecResult{}, err
	}
	if status.State == sandbox.StateAbsent {
		return sandbox.ExecResult{}, fmt.Errorf("sandbox is absent")
	}
	script, err := backend.ExecScript(req)
	if err != nil {
		return sandbox.ExecResult{}, err
	}
	out, err := b.stdin(ctx, script, "exec", "-i", status.ResourceID, "sh", "-s")
	return sandbox.ExecResult{ExitCode: out.ExitCode, Stdout: out.Stdout, Stderr: out.Stderr}, err
}

type inspected struct {
	ID     string
	Name   string
	Config struct {
		Image  string
		Labels map[string]string
	}
	State struct {
		Status  string
		Running bool
	}
}

func (b *Backend) Status(ctx context.Context, ref sandbox.Ref) (sandbox.Status, error) {
	status := sandbox.Status{Name: ref.Name, Backend: b.Name(), State: sandbox.StateAbsent}
	out, err := b.run(ctx, "inspect", container(ref.Name))
	if err != nil {
		return status, err
	}
	if out.ExitCode != 0 {
		if strings.Contains(strings.ToLower(out.Stderr), "no such object") || strings.Contains(strings.ToLower(out.Stderr), "no such container") {
			return status, nil
		}
		return status, fmt.Errorf("docker inspect %s: %s", ref.Name, strings.TrimSpace(out.Stderr))
	}
	var records []inspected
	if err := json.Unmarshal([]byte(out.Stdout), &records); err != nil || len(records) == 0 {
		return status, fmt.Errorf("decode docker inspect for %s: %w", ref.Name, err)
	}
	record := records[0]
	if record.Config.Labels[LabelSandbox] != ref.Name {
		return status, fmt.Errorf("refusing foreign container %s", container(ref.Name))
	}
	status.ResourceID = record.ID
	status.Digest = record.Config.Labels["ere.digest"]
	status.Managed = status.Digest != ""
	if status.Managed {
		id, err := backend.LoadIdentity("docker|"+b.Context, ref.Name)
		if err != nil {
			return status, err
		}
		if record.Config.Labels["ere.owner"] != id.Owner || (id.UID != "" && id.UID != record.ID) {
			return status, fmt.Errorf("refusing replaced or foreign managed container")
		}
		if id.UID == "" {
			id.UID = record.ID
			if err := id.Save(); err != nil {
				return status, err
			}
		}
	}
	status.Image = record.Config.Image
	status.State = mapState(record.State.Status)
	status.Detail = record.State.Status
	return status, nil
}

func mapState(value string) sandbox.State {
	switch value {
	case "running":
		return sandbox.StateRunning
	case "created", "exited", "paused", "dead":
		return sandbox.StateStopped
	case "restarting":
		return sandbox.StateStarting
	default:
		return sandbox.StateUnknown
	}
}

func (b *Backend) List(ctx context.Context) (sandbox.List, error) {
	out, err := b.check(ctx, "ps", "--all",
		"--filter", "label="+LabelSandbox,
		"--format", "{{.Label \""+LabelSandbox+"\"}}\t{{.Image}}\t{{.State}}")
	if err != nil {
		return sandbox.List{}, err
	}
	list := sandbox.List{Sandboxes: []sandbox.Status{}}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			continue
		}
		list.Sandboxes = append(list.Sandboxes, sandbox.Status{
			Name:    fields[0],
			Backend: b.Name(),
			Image:   fields[1],
			State:   mapState(fields[2]),
			Detail:  fields[2],
		})
	}
	return list, nil
}

func (b *Backend) Logs(ctx context.Context, req sandbox.LogRequest) (sandbox.Logs, error) {
	status, err := b.Status(ctx, sandbox.Ref{Name: req.Name})
	if err != nil {
		return sandbox.Logs{}, err
	}
	if status.Managed {
		lines := req.Lines
		if lines <= 0 {
			lines = 200
		}
		out, err := b.run(ctx, "logs", "--tail", strconv.Itoa(lines), status.ResourceID)
		if err != nil {
			return sandbox.Logs{}, err
		}
		if out.ExitCode != 0 {
			return sandbox.Logs{}, fmt.Errorf("container logs: %s", out.Stderr)
		}
		return sandbox.Logs{Lines: strings.Split(strings.TrimRight(out.Stdout+out.Stderr, "\n"), "\n")}, nil
	}
	lines := req.Lines
	if lines <= 0 {
		lines = 200
	}
	out, err := b.run(ctx, "exec", container(req.Name),
		"sh", "-c", "tail -n "+strconv.Itoa(lines)+" "+LogPath+" 2>/dev/null || true")
	if err != nil {
		return sandbox.Logs{}, err
	}
	text := strings.TrimRight(out.Stdout, "\n")
	if text == "" {
		return sandbox.Logs{Lines: []string{}}, nil
	}
	return sandbox.Logs{Lines: strings.Split(text, "\n")}, nil
}

// LogPath is where a detached exec writes inside the container.
const LogPath = "/tmp/ere.log"

func (b *Backend) Stop(ctx context.Context, ref sandbox.Ref) (sandbox.Status, error) {
	status, err := b.Status(ctx, ref)
	if err != nil {
		return status, err
	}
	if status.State == sandbox.StateAbsent {
		return status, nil
	}
	out, err := b.run(ctx, "stop", status.ResourceID)
	if err != nil {
		return sandbox.Status{}, err
	}
	if out.ExitCode != 0 && !strings.Contains(out.Stderr, "No such container") {
		return sandbox.Status{}, fmt.Errorf("stop %s: %s", ref.Name, strings.TrimSpace(out.Stderr))
	}
	return b.Status(ctx, ref)
}

func (b *Backend) Destroy(ctx context.Context, ref sandbox.Ref) (sandbox.Status, error) {
	status, err := b.Status(ctx, ref)
	if err != nil {
		return status, err
	}
	if status.State == sandbox.StateAbsent {
		return status, nil
	}
	out, err := b.run(ctx, "rm", "--force", "--volumes", status.ResourceID)
	if err != nil {
		return sandbox.Status{}, err
	}
	if out.ExitCode != 0 && !strings.Contains(out.Stderr, "No such container") {
		return sandbox.Status{}, fmt.Errorf("remove %s: %s", ref.Name, strings.TrimSpace(out.Stderr))
	}
	return sandbox.Status{Name: ref.Name, Backend: b.Name(), State: sandbox.StateAbsent}, nil
}

func (b *Backend) args(args []string) []string {
	if b.Context != "" {
		return append([]string{"--context", b.Context}, args...)
	}
	return args
}

func (b *Backend) run(ctx context.Context, args ...string) (backend.Output, error) {
	return backend.Run(ctx, b.Binary, b.args(args)...)
}

func (b *Backend) stdin(ctx context.Context, input string, args ...string) (backend.Output, error) {
	return backend.RunStdin(ctx, input, b.Binary, b.args(args)...)
}

func (b *Backend) check(ctx context.Context, args ...string) (string, error) {
	return backend.Check(ctx, b.Binary, b.args(args)...)
}

func (b *Backend) Validate(ctx context.Context, spec sandbox.Spec) (sandbox.Plan, error) {
	err := sandbox.Validate(spec)
	if spec.Storage.Class != "" || spec.Storage.SizeGB != 0 || spec.DiskGB != 0 {
		return sandbox.Plan{}, fmt.Errorf("docker does not manage storage classes or disk sizes")
	}
	if spec.Storage.Kind == "bind" && spec.Storage.Source == "" && spec.Workspace == "" {
		return sandbox.Plan{}, fmt.Errorf("bind storage requires a host source")
	}
	if spec.Storage.Kind == "volume" && spec.Storage.Source == "" {
		return sandbox.Plan{}, fmt.Errorf("volume storage requires a named source")
	}
	if spec.Architecture != "" && spec.Architecture != "arm64" && spec.Architecture != "amd64" {
		return sandbox.Plan{}, fmt.Errorf("architecture must be amd64 or arm64")
	}
	if spec.Storage.Kind != "" && spec.Storage.Kind != "bind" && spec.Storage.Kind != "volume" {
		err = fmt.Errorf("unsupported storage kind %s", spec.Storage.Kind)
	}
	if spec.BootVolume != "" {
		err = fmt.Errorf("bootVolume is only supported by KubeVirt")
	}
	return sandbox.Plan{Name: spec.Name, Backend: b.Name(), Digest: sandbox.Digest(spec)}, err
}

func (b *Backend) installEnv(ctx context.Context, resourceID string, env map[string]string) error {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	data := []byte(backend.EnvFile(env))
	if err := tw.WriteHeader(&tar.Header{Name: "ere/", Mode: 0o700, Typeflag: tar.TypeDir}); err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: "ere/env", Mode: 0o600, Size: int64(len(data))}); err != nil {
		return err
	}
	if _, err := tw.Write(data); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	out, err := b.stdin(ctx, buf.String(), "cp", "-", resourceID+":/var/lib")
	if err != nil {
		return err
	}
	if out.ExitCode != 0 {
		return fmt.Errorf("install workload environment: %s", out.Stderr)
	}
	return nil
}
