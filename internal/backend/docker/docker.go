// Package docker implements the sandbox contract on top of the docker CLI.
// The container is a long-lived shell that lifier execs into; the agent is not
// the container's entrypoint, so a crashed agent does not destroy its sandbox.
package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/roshbhatia/lifier/internal/backend"
	"github.com/roshbhatia/lifier/internal/sandbox"
)

const (
	// Prefix keeps lifier containers distinguishable in a shared docker daemon.
	Prefix = "lifier-"
	// LabelSandbox is how List finds containers lifier owns.
	LabelSandbox = "lifier.sandbox"
	// DefaultImage ships amp and a shell. Build it from images/amp/Dockerfile.
	DefaultImage = "lifier/amp:latest"
)

// Backend drives one docker daemon. Binary may name a drop-in such as podman.
type Backend struct {
	Binary string
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
	probe := sandbox.Probe{Backend: b.Name(), Operations: sandbox.Operations()}
	if !backend.Available(b.Binary) {
		probe.Detail = b.Binary + " is not on PATH"
		return probe, nil
	}
	out, err := backend.Run(ctx, b.Binary, "version", "--format", "{{.Server.Version}}")
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
	if spec.Name == "" {
		return sandbox.Status{}, fmt.Errorf("sandbox name is required")
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
		"--label", "lifier.backend=docker",
		"--workdir", mount,
		"--init",
	}
	if spec.Workspace != "" {
		bind := spec.Workspace + ":" + mount
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
		args = append(args, "--label", key+"="+value)
	}
	args = append(args, image, "sleep", "infinity")

	sandbox.Progress(ctx, "create", "creating container "+container(spec.Name))
	if _, err := backend.Check(ctx, b.Binary, args...); err != nil {
		return sandbox.Status{}, err
	}
	return b.Status(ctx, sandbox.Ref{Name: spec.Name})
}

func (b *Backend) Start(ctx context.Context, ref sandbox.Ref) (sandbox.Status, error) {
	sandbox.Progress(ctx, "start", "starting "+container(ref.Name))
	if _, err := backend.Check(ctx, b.Binary, "start", container(ref.Name)); err != nil {
		return sandbox.Status{}, err
	}
	return b.Status(ctx, ref)
}

func (b *Backend) Exec(ctx context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
	if len(req.Argv) == 0 {
		return sandbox.ExecResult{}, fmt.Errorf("exec requires a command")
	}
	if len(req.Env) > 0 {
		if err := b.writeEnv(ctx, req.Name, req.Env); err != nil {
			return sandbox.ExecResult{}, err
		}
	}
	args := []string{"exec"}
	if req.Detach {
		args = append(args, "--detach")
	}
	if req.Workdir != "" {
		args = append(args, "--workdir", req.Workdir)
	}
	args = append(args, container(req.Name))
	args = append(args, backend.WrapCommand(req.Argv, req.LogFile)...)

	out, err := backend.Run(ctx, b.Binary, args...)
	if err != nil {
		return sandbox.ExecResult{}, err
	}
	return sandbox.ExecResult{ExitCode: out.ExitCode, Stdout: out.Stdout, Stderr: out.Stderr}, nil
}

func (b *Backend) writeEnv(ctx context.Context, name string, env map[string]string) error {
	out, err := backend.RunStdin(ctx, backend.EnvFile(env), b.Binary,
		"exec", "--interactive", container(name),
		"sh", "-c", "umask 077; cat > "+backend.EnvPath)
	if err != nil {
		return err
	}
	if out.ExitCode != 0 {
		return fmt.Errorf("write sandbox environment: %s", strings.TrimSpace(out.Stderr))
	}
	return nil
}

type inspected struct {
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
	out, err := backend.Run(ctx, b.Binary, "inspect", container(ref.Name))
	if err != nil {
		return status, err
	}
	if out.ExitCode != 0 {
		return status, nil
	}
	var records []inspected
	if err := json.Unmarshal([]byte(out.Stdout), &records); err != nil || len(records) == 0 {
		return status, fmt.Errorf("decode docker inspect for %s: %w", ref.Name, err)
	}
	record := records[0]
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
	out, err := backend.Check(ctx, b.Binary, "ps", "--all",
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
	lines := req.Lines
	if lines <= 0 {
		lines = 200
	}
	out, err := backend.Run(ctx, b.Binary, "exec", container(req.Name),
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
const LogPath = "/tmp/lifier.log"

func (b *Backend) Stop(ctx context.Context, ref sandbox.Ref) (sandbox.Status, error) {
	out, err := backend.Run(ctx, b.Binary, "stop", container(ref.Name))
	if err != nil {
		return sandbox.Status{}, err
	}
	if out.ExitCode != 0 && !strings.Contains(out.Stderr, "No such container") {
		return sandbox.Status{}, fmt.Errorf("stop %s: %s", ref.Name, strings.TrimSpace(out.Stderr))
	}
	return b.Status(ctx, ref)
}

func (b *Backend) Destroy(ctx context.Context, ref sandbox.Ref) (sandbox.Status, error) {
	out, err := backend.Run(ctx, b.Binary, "rm", "--force", "--volumes", container(ref.Name))
	if err != nil {
		return sandbox.Status{}, err
	}
	if out.ExitCode != 0 && !strings.Contains(out.Stderr, "No such container") {
		return sandbox.Status{}, fmt.Errorf("remove %s: %s", ref.Name, strings.TrimSpace(out.Stderr))
	}
	return sandbox.Status{Name: ref.Name, Backend: b.Name(), State: sandbox.StateAbsent}, nil
}
