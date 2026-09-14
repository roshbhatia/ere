// Package lima implements the sandbox contract on top of limactl. One lima
// driver covers every virtual-machine backend lifier targets: Virtualization
// .framework on macOS, and QEMU with KVM acceleration on Linux.
package lima

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"strings"

	"github.com/roshbhatia/lifier/internal/backend"
	"github.com/roshbhatia/lifier/internal/sandbox"
)

const (
	// Prefix keeps lifier instances apart from hand-made lima instances.
	Prefix = "lifier-"
	// LogPath is where a detached exec writes inside the guest.
	LogPath = "/tmp/lifier.log"
	// DefaultMountPoint is where the workspace appears in the guest.
	DefaultMountPoint = "/workspace"
)

// Backend drives limactl with one virtual-machine type.
type Backend struct {
	Binary string
	VMType string
	Base   string
}

// New returns a lima backend. An empty vmType picks the native hypervisor:
// vz on macOS, qemu (KVM-accelerated where available) elsewhere.
func New(binary, vmType, base string) *Backend {
	if binary == "" {
		binary = "limactl"
	}
	if vmType == "" {
		vmType = DefaultVMType()
	}
	if base == "" {
		base = "template://_images/ubuntu-lts"
	}
	return &Backend{Binary: binary, VMType: vmType, Base: base}
}

// DefaultVMType is the hypervisor lima uses natively on this host.
func DefaultVMType() string {
	if runtime.GOOS == "darwin" {
		return "vz"
	}
	return "qemu"
}

func (b *Backend) Name() string {
	if b.VMType == "vz" {
		return "vz"
	}
	return "qemu"
}

func instance(name string) string { return Prefix + name }

func (b *Backend) Probe(ctx context.Context) (sandbox.Probe, error) {
	probe := sandbox.Probe{Backend: b.Name(), Operations: sandbox.Operations()}
	if b.VMType == "vz" && runtime.GOOS != "darwin" {
		probe.Detail = "Virtualization.framework exists only on macOS"
		return probe, nil
	}
	if !backend.Available(b.Binary) {
		probe.Detail = b.Binary + " is not on PATH"
		return probe, nil
	}
	out, err := backend.Run(ctx, b.Binary, "--version")
	if err != nil {
		probe.Detail = err.Error()
		return probe, nil
	}
	if out.ExitCode != 0 {
		probe.Detail = strings.TrimSpace(out.Stderr)
		return probe, nil
	}
	detail := strings.TrimSpace(out.Stdout) + ", vmType " + b.VMType
	if b.VMType == "qemu" && runtime.GOOS == "linux" {
		if _, err := os.Stat("/dev/kvm"); err != nil {
			detail += " (no /dev/kvm: emulation only)"
		} else {
			detail += " (KVM accelerated)"
		}
	}
	probe.Available = true
	probe.Detail = detail
	return probe, nil
}

func (b *Backend) template(spec sandbox.Spec) string {
	mount := spec.MountPath
	if mount == "" {
		mount = DefaultMountPoint
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "base: %q\n", b.Base)
	fmt.Fprintf(&builder, "vmType: %q\n", b.VMType)
	if spec.CPUs > 0 {
		fmt.Fprintf(&builder, "cpus: %d\n", spec.CPUs)
	}
	if spec.MemoryMB > 0 {
		fmt.Fprintf(&builder, "memory: %q\n", strconv.Itoa(spec.MemoryMB)+"MiB")
	}
	if spec.DiskGB > 0 {
		fmt.Fprintf(&builder, "disk: %q\n", strconv.Itoa(spec.DiskGB)+"GiB")
	}
	// containerd is lima's default payload and costs minutes of first boot that
	// an agent sandbox never uses.
	builder.WriteString("containerd:\n  system: false\n  user: false\n")
	if spec.Workspace == "" {
		builder.WriteString("mounts: []\n")
	} else {
		builder.WriteString("mounts:\n")
		fmt.Fprintf(&builder, "  - location: %q\n", spec.Workspace)
		fmt.Fprintf(&builder, "    mountPoint: %q\n", mount)
		fmt.Fprintf(&builder, "    writable: %t\n", !spec.ReadOnly)
		if b.VMType == "vz" {
			builder.WriteString("mountType: \"virtiofs\"\n")
		}
	}
	return builder.String()
}

func (b *Backend) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Status, error) {
	if spec.Name == "" {
		return sandbox.Status{}, fmt.Errorf("sandbox name is required")
	}
	file, err := os.CreateTemp("", "lifier-lima-*.yaml")
	if err != nil {
		return sandbox.Status{}, fmt.Errorf("write lima template: %w", err)
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if _, err := file.WriteString(b.template(spec)); err != nil {
		_ = file.Close()
		return sandbox.Status{}, fmt.Errorf("write lima template: %w", err)
	}
	if err := file.Close(); err != nil {
		return sandbox.Status{}, fmt.Errorf("write lima template: %w", err)
	}

	sandbox.Progress(ctx, "create", "creating lima instance "+instance(spec.Name))
	if _, err := backend.Check(ctx, b.Binary, "create", "--tty=false",
		"--name", instance(spec.Name), file.Name()); err != nil {
		return sandbox.Status{}, err
	}
	return b.Status(ctx, sandbox.Ref{Name: spec.Name})
}

func (b *Backend) Start(ctx context.Context, ref sandbox.Ref) (sandbox.Status, error) {
	sandbox.Progress(ctx, "start", "booting "+instance(ref.Name)+" (first boot pulls an image)")
	if _, err := backend.Check(ctx, b.Binary, "start", "--tty=false", instance(ref.Name)); err != nil {
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
	// limactl defaults the guest working directory to the host's, and lima maps
	// the host home into the guest, so an empty workdir would silently run the
	// command against host files.
	workdir := req.Workdir
	if workdir == "" {
		workdir = "/"
	}
	args := []string{"shell", "--tty=false", "--workdir", workdir}
	args = append(args, instance(req.Name), "--")
	if req.Detach {
		args = append(args, backend.WrapDetached(req.Argv, req.LogFile)...)
	} else {
		args = append(args, backend.WrapCommand(req.Argv, req.LogFile)...)
	}
	out, err := backend.Run(ctx, b.Binary, args...)
	if err != nil {
		return sandbox.ExecResult{}, err
	}
	return sandbox.ExecResult{ExitCode: out.ExitCode, Stdout: out.Stdout, Stderr: out.Stderr}, nil
}

func (b *Backend) writeEnv(ctx context.Context, name string, env map[string]string) error {
	out, err := backend.RunStdin(ctx, backend.EnvFile(env), b.Binary,
		"shell", "--tty=false", instance(name), "--",
		"sh", "-c", "umask 077; cat > "+backend.EnvPath)
	if err != nil {
		return err
	}
	if out.ExitCode != 0 {
		return fmt.Errorf("write sandbox environment: %s", strings.TrimSpace(out.Stderr))
	}
	return nil
}

type listed struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	VMType  string `json:"vmType"`
	Dir     string `json:"dir"`
	Arch    string `json:"arch"`
	Message string `json:"message"`
}

func (b *Backend) instances(ctx context.Context) ([]listed, error) {
	out, err := backend.Check(ctx, b.Binary, "list", "--json")
	if err != nil {
		return nil, err
	}
	var records []listed
	decoder := json.NewDecoder(strings.NewReader(out))
	for {
		var record listed
		if err := decoder.Decode(&record); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode lima list: %w", err)
		}
		records = append(records, record)
	}
	return records, nil
}

func (b *Backend) Status(ctx context.Context, ref sandbox.Ref) (sandbox.Status, error) {
	status := sandbox.Status{Name: ref.Name, Backend: b.Name(), State: sandbox.StateAbsent}
	records, err := b.instances(ctx)
	if err != nil {
		return status, err
	}
	for _, record := range records {
		if record.Name != instance(ref.Name) {
			continue
		}
		status.State = mapState(record.Status)
		status.Detail = record.Status
		status.Image = record.VMType + "/" + record.Arch
		status.Address = record.Dir
		return status, nil
	}
	return status, nil
}

func mapState(value string) sandbox.State {
	switch strings.ToLower(value) {
	case "running":
		return sandbox.StateRunning
	case "stopped":
		return sandbox.StateStopped
	case "starting":
		return sandbox.StateStarting
	case "broken":
		return sandbox.StateUnknown
	default:
		return sandbox.StateUnknown
	}
}

func (b *Backend) List(ctx context.Context) (sandbox.List, error) {
	records, err := b.instances(ctx)
	if err != nil {
		return sandbox.List{}, err
	}
	list := sandbox.List{Sandboxes: []sandbox.Status{}}
	for _, record := range records {
		if !strings.HasPrefix(record.Name, Prefix) || record.VMType != b.VMType {
			continue
		}
		list.Sandboxes = append(list.Sandboxes, sandbox.Status{
			Name:    strings.TrimPrefix(record.Name, Prefix),
			Backend: b.Name(),
			State:   mapState(record.Status),
			Image:   record.VMType + "/" + record.Arch,
			Address: record.Dir,
			Detail:  record.Status,
		})
	}
	return list, nil
}

func (b *Backend) Logs(ctx context.Context, req sandbox.LogRequest) (sandbox.Logs, error) {
	lines := req.Lines
	if lines <= 0 {
		lines = 200
	}
	out, err := backend.Run(ctx, b.Binary, "shell", "--tty=false", instance(req.Name), "--",
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

func (b *Backend) Stop(ctx context.Context, ref sandbox.Ref) (sandbox.Status, error) {
	out, err := backend.Run(ctx, b.Binary, "stop", "--tty=false", instance(ref.Name))
	if err != nil {
		return sandbox.Status{}, err
	}
	if out.ExitCode != 0 && !absent(out.Stderr) {
		return sandbox.Status{}, fmt.Errorf("stop %s: %s", ref.Name, strings.TrimSpace(out.Stderr))
	}
	return b.Status(ctx, ref)
}

func (b *Backend) Destroy(ctx context.Context, ref sandbox.Ref) (sandbox.Status, error) {
	out, err := backend.Run(ctx, b.Binary, "delete", "--tty=false", "--force", instance(ref.Name))
	if err != nil {
		return sandbox.Status{}, err
	}
	if out.ExitCode != 0 && !absent(out.Stderr) {
		return sandbox.Status{}, fmt.Errorf("delete %s: %s", ref.Name, strings.TrimSpace(out.Stderr))
	}
	return sandbox.Status{Name: ref.Name, Backend: b.Name(), State: sandbox.StateAbsent}, nil
}

func absent(stderr string) bool {
	return strings.Contains(stderr, "not found") || strings.Contains(stderr, "does not exist")
}
