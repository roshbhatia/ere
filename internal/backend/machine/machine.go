package machine

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/roshbhatia/ere/internal/backend"
	"github.com/roshbhatia/ere/internal/sandbox"
	"github.com/roshbhatia/go-utils/store"
)

type Backend struct{ Kind, Binary, Host, Project, Remote, VMType, SSHKey, SSHUser string }

func (b *Backend) Name() string { return b.Kind }
func (b *Backend) binary() string {
	if b.Binary != "" {
		return b.Binary
	}
	return b.Kind
}

func (b *Backend) scope() string {
	return b.Kind + "|" + b.binary() + "|" + b.SSHUser + "|" + b.Host + "|" + b.Remote + "|" + b.Project + "|" + os.Getenv("TART_HOME")
}

func (b *Backend) name(name string) string {
	if b.Kind == "incus" && b.Remote != "" {
		return b.Remote + ":ere-" + name
	}
	return "ere-" + name
}

func (b *Backend) args(args ...string) []string {
	if b.Kind == "incus" && b.Project != "" {
		return append([]string{"--project", b.Project}, args...)
	}
	return args
}

func (b *Backend) run(ctx context.Context, input string, args ...string) (string, error) {
	command := append([]string{b.binary()}, b.args(args...)...)
	if b.Kind == "tart" {
		command = append([]string{"env", "TART_NO_AUTO_PRUNE=1"}, command...)
	}
	out, err := backend.RunStdin(ctx, input, command[0], command[1:]...)
	if err != nil {
		return "", err
	}
	if out.ExitCode != 0 {
		return "", fmt.Errorf("%s %s: %s", b.Kind, args[0], strings.TrimSpace(out.Stderr))
	}
	return out.Stdout, nil
}

func (b *Backend) id(name string) (backend.Identity, error) {
	return backend.LoadIdentity(b.scope(), name)
}

func specPath(id backend.Identity) string {
	return filepath.Join(filepath.Dir(id.Path), id.Owner+"-spec.json")
}

func (b *Backend) load(name string) (sandbox.Spec, backend.Identity, error) {
	id, err := b.id(name)
	if err != nil {
		return sandbox.Spec{}, id, err
	}
	data, err := os.ReadFile(specPath(id))
	if err != nil {
		return sandbox.Spec{}, id, err
	}
	var spec sandbox.Spec
	err = json.Unmarshal(data, &spec)
	return spec, id, err
}

func (b *Backend) save(spec sandbox.Spec, id backend.Identity) error {
	data, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	return store.Write(specPath(id), data)
}

func (b *Backend) Probe(ctx context.Context) (sandbox.Probe, error) {
	p := sandbox.Probe{Backend: b.Kind, Contract: sandbox.ContractVersion, Operations: append(sandbox.Operations(), sandbox.OpValidate, sandbox.OpConnect), StorageModes: []string{"guest-disk"}, Supervision: "systemd", Retention: "guest disk survives stop; removal deletes the VM disk"}
	if b.Kind == "ssh" {
		p.Retention = "stop and removal affect only the dedicated workload; host and workspace are retained"
	}
	if b.Kind == "tart" {
		p.Supervision = "host-launchd and guest-launchd"
		if runtime.GOOS != "darwin" {
			p.Detail = "Tart requires macOS"
			return p, nil
		}
	}
	if !backend.Available(b.binary()) {
		p.Detail = b.binary() + " is not on PATH"
		return p, nil
	}
	if b.Kind == "ssh" && b.Host == "" {
		p.Detail = "SSH requires an explicit host"
		return p, nil
	}
	args := []string{"version"}
	if b.Kind == "ssh" {
		args = []string{"-V"}
	}
	if b.Kind == "tart" || b.Kind == "multipass" {
		args = []string{"--version"}
	}
	out, err := b.run(ctx, "", args...)
	if err != nil {
		p.Detail = err.Error()
		return p, nil
	}
	p.Available = true
	p.Detail = strings.TrimSpace(out)
	return p, nil
}

func (b *Backend) Validate(ctx context.Context, spec sandbox.Spec) (sandbox.Plan, error) {
	plan := sandbox.Plan{Name: spec.Name, Backend: b.Kind, Digest: sandbox.Digest(spec)}
	if err := sandbox.Validate(spec); err != nil {
		return plan, err
	}
	if spec.Workspace != "" || spec.Storage.Source != "" || spec.BootVolume != "" || spec.ReadOnly || spec.Storage.Class != "" || spec.Storage.SizeGB != 0 {
		return plan, fmt.Errorf("%s uses a guest workspace; host mounts and external disks are unsupported", b.Kind)
	}
	if spec.Storage.Kind != "" && spec.Storage.Kind != "guest-disk" {
		return plan, fmt.Errorf("%s requires guest-disk storage", b.Kind)
	}
	if b.Kind == "tart" && !strings.HasPrefix(spec.MountPath, "/Users/Shared/") {
		return plan, fmt.Errorf("tart workspace must be under /Users/Shared/")
	}
	if b.Kind == "ssh" {
		if b.Host == "" || strings.HasPrefix(b.Host, "-") || strings.ContainsAny(b.Host, " \n\r\t") {
			return plan, fmt.Errorf("explicit SSH host is required")
		}
		if spec.Image != "" || spec.CPUs != 0 || spec.MemoryMB != 0 || spec.DiskGB != 0 || spec.Architecture != "" {
			return plan, fmt.Errorf("SSH does not own host image or resources")
		}
	} else if spec.Image == "" {
		return plan, fmt.Errorf("%s requires an explicit image", b.Kind)
	}
	if spec.Architecture != "" && (b.Kind != "tart" || spec.Architecture != runtime.GOARCH) {
		return plan, fmt.Errorf("explicit architecture is only supported for Tart and must match the host")
	}
	return plan, nil
}

func (b *Backend) native(ctx context.Context, name string) (sandbox.State, error) {
	if b.Kind == "ssh" {
		return sandbox.StateRunning, nil
	}
	var raw string
	var err error
	switch b.Kind {
	case "incus":
		args := []string{"list", "--format", "json"}
		if b.Remote != "" {
			args = append(args, b.Remote+":")
		}
		raw, err = b.run(ctx, "", args...)
	case "tart":
		raw, err = b.run(ctx, "", "list", "--source", "local", "--format", "json")
	case "multipass":
		raw, err = b.run(ctx, "", "list", "--format", "json")
	default:
		return "", fmt.Errorf("unsupported machine provider")
	}
	if err != nil {
		return "", err
	}
	type item struct {
		Name    string `json:"name"`
		State   string `json:"state"`
		Status  string `json:"status"`
		Running bool   `json:"running"`
	}
	var items []item
	if b.Kind == "multipass" {
		var list struct {
			List []item `json:"list"`
		}
		err = json.Unmarshal([]byte(raw), &list)
		items = list.List
	} else {
		err = json.Unmarshal([]byte(raw), &items)
	}
	if err != nil {
		return "", err
	}
	for _, i := range items {
		if i.Name == "ere-"+name {
			status := i.State
			if status == "" {
				status = i.Status
			}
			switch strings.ToLower(status) {
			case "running":
				return sandbox.StateRunning, nil
			case "stopped", "suspended", "stoppedstopped":
				return sandbox.StateStopped, nil
			}
			if i.Running {
				return sandbox.StateRunning, nil
			}
			return sandbox.StateUnknown, nil
		}
	}
	return sandbox.StateAbsent, nil
}

func (b *Backend) guest(ctx context.Context, name, input string, argv []string, tty bool) (backend.Output, error) {
	command := b.guestCommand(name, argv, tty)
	return backend.RunStdin(ctx, input, command[0], command[1:]...)
}

func (b *Backend) guestCommand(name string, argv []string, tty bool) []string {
	switch b.Kind {
	case "incus":
		flag := "-T"
		if tty {
			flag = "-t"
		}
		return append(append([]string{b.binary()}, b.args("exec", b.name(name), flag, "--")...), argv...)
	case "tart":
		args := []string{b.binary(), "exec", "-i"}
		if tty {
			args = append(args, "-t")
		}
		return append(append(args, b.name(name), "--"), argv...)
	case "multipass":
		return append([]string{b.binary(), "exec", b.name(name), "--"}, argv...)
	default:
		flag := "-T"
		if tty {
			flag = "-t"
		}
		args := []string{b.binary(), flag, "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "ConnectTimeout=10"}
		if b.SSHKey != "" {
			args = append(args, "-i", b.SSHKey)
		}
		host := b.Host
		if b.SSHUser != "" {
			host = b.SSHUser + "@" + host
		}
		return append(args, host, backend.ShellArgv(argv))
	}
}

func (b *Backend) script(ctx context.Context, name, input string) (backend.Output, error) {
	argv := []string{"sudo", "-n", "sh", "-s"}
	if b.Kind == "incus" {
		argv = []string{"sh", "-s"}
	}
	return b.guest(ctx, name, input, argv, false)
}

func (b *Backend) owned(ctx context.Context, name string, id backend.Identity) error {
	if b.Kind == "tart" {
		data, err := os.ReadFile(filepath.Join(b.tartHome(), "vms", b.name(name), "ere-owner"))
		if err != nil {
			return err
		}
		if string(data) != id.Owner {
			return fmt.Errorf("refusing foreign Tart VM")
		}
		return nil
	}
	if b.Kind == "incus" {
		out, err := b.run(ctx, "", "config", "get", b.name(name), "user.ere.owner")
		if err != nil {
			return err
		}
		if strings.TrimSpace(out) != id.Owner {
			return fmt.Errorf("refusing foreign Incus instance")
		}
		return nil
	}
	if b.Kind == "multipass" {
		raw, err := b.run(ctx, "", "list", "--snapshots", "--format", "json")
		if err != nil {
			return err
		}
		var snapshots struct {
			Info map[string]map[string]json.RawMessage `json:"info"`
		}
		if err = json.Unmarshal([]byte(raw), &snapshots); err != nil {
			return err
		}
		if _, ok := snapshots.Info[b.name(name)]["ere-owner-"+id.Owner]; !ok {
			return fmt.Errorf("refusing Multipass instance without ownership snapshot")
		}
		current, err := b.native(ctx, name)
		if err != nil {
			return err
		}
		if current != sandbox.StateRunning {
			return nil
		}
	}
	out, err := b.script(ctx, name, "test \"$(cat /var/lib/ere-"+id.Owner+"/owner 2>/dev/null)\" = "+backend.Quote(id.Owner))
	if err != nil {
		return err
	}
	if out.ExitCode != 0 {
		return fmt.Errorf("refusing foreign or replaced %s workload", b.Kind)
	}
	return nil
}

func (b *Backend) Status(ctx context.Context, ref sandbox.Ref) (sandbox.Status, error) {
	result := sandbox.Status{Name: ref.Name, Backend: b.Kind, State: sandbox.StateAbsent}
	spec, id, err := b.load(ref.Name)
	if os.IsNotExist(err) {
		native, nerr := b.native(ctx, ref.Name)
		if nerr != nil {
			return result, nerr
		}
		if b.Kind != "ssh" && native != sandbox.StateAbsent {
			return result, fmt.Errorf("existing machine has no Ere ownership record")
		}
		return result, nil
	}
	if err != nil {
		return result, err
	}
	s, err := b.native(ctx, ref.Name)
	if err != nil {
		return result, err
	}
	result.State = s
	if s == sandbox.StateAbsent {
		return result, nil
	}
	if err = b.owned(ctx, ref.Name, id); err != nil {
		return result, err
	}
	if b.Kind == "ssh" {
		out, err := b.script(ctx, ref.Name, "systemctl is-active --quiet ere-"+id.Owner)
		if err != nil {
			return result, err
		}
		if out.ExitCode != 0 {
			result.State = sandbox.StateStopped
		}
	}
	if id.Digest != sandbox.Digest(spec) {
		return result, fmt.Errorf("creation is incomplete; inspect the owned machine before removal and retry")
	}
	result.Digest = sandbox.Digest(spec)
	result.Managed = spec.Workload != nil
	result.ResourceID = id.Owner
	result.Image = spec.Image
	return result, nil
}

func (b *Backend) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Status, error) {
	if _, err := b.Validate(ctx, spec); err != nil {
		return sandbox.Status{}, err
	}
	status, err := b.Status(ctx, sandbox.Ref{Name: spec.Name})
	if err != nil {
		return status, err
	}
	if status.State != sandbox.StateAbsent {
		if status.Digest != sandbox.Digest(spec) {
			return status, fmt.Errorf("configuration changed; explicit replacement required")
		}
		return status, nil
	}
	id, err := b.id(spec.Name)
	if err != nil {
		return status, err
	}
	if err = b.save(spec, id); err != nil {
		return status, err
	}
	switch b.Kind {
	case "incus":
		args := []string{"init", spec.Image, b.name(spec.Name), "-c", "user.ere.owner=" + id.Owner}
		if b.VMType == "vm" {
			args = append(args, "--vm")
		}
		if spec.CPUs > 0 {
			args = append(args, "-c", "limits.cpu="+strconv.Itoa(spec.CPUs))
		}
		if spec.MemoryMB > 0 {
			args = append(args, "-c", "limits.memory="+strconv.Itoa(spec.MemoryMB)+"MiB")
		}
		if spec.DiskGB > 0 {
			args = append(args, "-d", "root,size="+strconv.Itoa(spec.DiskGB)+"GiB")
		}
		_, err = b.run(ctx, "", args...)
	case "tart":
		_, err = b.run(ctx, "", "clone", spec.Image, b.name(spec.Name))
		if err == nil {
			err = os.WriteFile(filepath.Join(b.tartHome(), "vms", b.name(spec.Name), "ere-owner"), []byte(id.Owner), 0o600)
		}
		if err == nil {
			args := []string{"set", b.name(spec.Name)}
			if spec.CPUs > 0 {
				args = append(args, "--cpu", strconv.Itoa(spec.CPUs))
			}
			if spec.MemoryMB > 0 {
				args = append(args, "--memory", strconv.Itoa(spec.MemoryMB))
			}
			if spec.DiskGB > 0 {
				args = append(args, "--disk-size", strconv.Itoa(spec.DiskGB))
			}
			if len(args) > 2 {
				_, err = b.run(ctx, "", args...)
			}
		}
	case "multipass":
		args := []string{"launch", spec.Image, "--name", b.name(spec.Name)}
		if spec.CPUs > 0 {
			args = append(args, "--cpus", strconv.Itoa(spec.CPUs))
		}
		if spec.MemoryMB > 0 {
			args = append(args, "--memory", strconv.Itoa(spec.MemoryMB)+"M")
		}
		if spec.DiskGB > 0 {
			args = append(args, "--disk", strconv.Itoa(spec.DiskGB)+"G")
		}
		_, err = b.run(ctx, "", args...)
	}
	if err != nil {
		return status, err
	}
	if b.Kind == "ssh" || b.Kind == "multipass" {
		dir := "/var/lib/ere-" + id.Owner
		out, gerr := b.script(ctx, spec.Name, "set -eu; umask 077; mkdir -p "+backend.Quote(dir)+"; test ! -e "+backend.Quote(dir+"/owner")+"; printf %s "+backend.Quote(id.Owner)+" > "+backend.Quote(dir+"/owner"))
		if gerr != nil {
			return status, gerr
		}
		if out.ExitCode != 0 {
			return status, fmt.Errorf("create ownership marker: %s", out.Stderr)
		}
	}
	if b.Kind == "multipass" {
		if _, err = b.run(ctx, "", "stop", b.name(spec.Name)); err != nil {
			return status, err
		}
		if _, err = b.run(ctx, "", "snapshot", b.name(spec.Name), "--name", "ere-owner-"+id.Owner); err != nil {
			return status, fmt.Errorf("ownership snapshot requires a snapshot-capable Multipass driver: %w", err)
		}
	}
	if err = b.save(spec, id); err != nil {
		return status, err
	}
	id.Digest = sandbox.Digest(spec)
	if err = id.Save(); err != nil {
		return status, err
	}
	return b.Status(ctx, sandbox.Ref{Name: spec.Name})
}

func (b *Backend) Start(ctx context.Context, ref sandbox.Ref) (sandbox.Status, error) {
	spec, id, err := b.load(ref.Name)
	if err != nil {
		return sandbox.Status{}, err
	}
	s, err := b.native(ctx, ref.Name)
	if err != nil {
		return sandbox.Status{}, err
	}
	if err = b.owned(ctx, ref.Name, id); err != nil {
		return sandbox.Status{}, err
	}
	if s != sandbox.StateRunning {
		switch b.Kind {
		case "tart":
			err = b.startTart(ctx, ref.Name, id)
		case "ssh":
		default:
			_, err = b.run(ctx, "", "start", b.name(ref.Name))
		}
		if err != nil {
			return sandbox.Status{}, err
		}
	}
	if spec.Workload != nil {
		wait, cancel := context.WithTimeout(ctx, 5*time.Minute)
		defer cancel()
		for {
			out, gerr := b.script(wait, ref.Name, "true")
			if gerr == nil && out.ExitCode == 0 {
				break
			}
			select {
			case <-wait.Done():
				return sandbox.Status{}, fmt.Errorf("guest execution unavailable: %w", wait.Err())
			case <-time.After(time.Second):
			}
		}
		if err = b.owned(ctx, ref.Name, id); err != nil {
			return sandbox.Status{}, err
		}
		script := b.workload(*spec.Workload, id)
		out, gerr := b.script(ctx, ref.Name, script)
		if gerr != nil {
			return sandbox.Status{}, gerr
		}
		if out.ExitCode != 0 {
			return sandbox.Status{}, fmt.Errorf("install workload: %s", out.Stderr)
		}
	}
	return b.Status(ctx, ref)
}

func (b *Backend) Exec(ctx context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
	_, id, err := b.load(req.Name)
	if err != nil {
		return sandbox.ExecResult{}, err
	}
	if err = b.owned(ctx, req.Name, id); err != nil {
		return sandbox.ExecResult{}, err
	}
	script, err := backend.ExecScript(req)
	if err != nil {
		return sandbox.ExecResult{}, err
	}
	out, err := b.script(ctx, req.Name, script)
	return sandbox.ExecResult{ExitCode: out.ExitCode, Stdout: out.Stdout, Stderr: out.Stderr}, err
}

func (b *Backend) Connect(ctx context.Context, req sandbox.ConnectionRequest) (sandbox.Connection, error) {
	_, id, err := b.load(req.Name)
	if err != nil {
		return sandbox.Connection{}, err
	}
	if err = b.owned(ctx, req.Name, id); err != nil {
		return sandbox.Connection{}, err
	}
	argv := req.Argv
	if len(argv) == 0 {
		argv = []string{"sh", "-l"}
	}
	script := "exec " + backend.ShellArgv(argv)
	if req.Workdir != "" {
		script = "cd " + backend.Quote(req.Workdir) + " && " + script
	}
	argv = []string{"sh", "-c", script}
	if b.Kind != "incus" {
		argv = append([]string{"sudo", "-n"}, argv...)
	}
	return sandbox.Connection{Argv: b.guestCommand(req.Name, argv, true)}, nil
}

func (b *Backend) Logs(ctx context.Context, req sandbox.LogRequest) (sandbox.Logs, error) {
	_, id, err := b.load(req.Name)
	if err != nil {
		return sandbox.Logs{}, err
	}
	n := req.Lines
	if n <= 0 {
		n = 200
	}
	argv := []string{"journalctl", "-u", "ere-" + id.Owner, "-n", strconv.Itoa(n), "--no-pager"}
	if b.Kind == "tart" {
		argv = []string{"tail", "-n", strconv.Itoa(n), "/var/lib/ere-" + id.Owner + "/output.log"}
	}
	out, err := b.Exec(ctx, sandbox.ExecRequest{Name: req.Name, Argv: argv})
	if err == nil && out.ExitCode != 0 {
		err = fmt.Errorf("logs: %s", out.Stderr)
	}
	return sandbox.Logs{Lines: strings.Split(strings.TrimSpace(out.Stdout), "\n")}, err
}

func (b *Backend) Stop(ctx context.Context, ref sandbox.Ref) (sandbox.Status, error) {
	spec, id, err := b.load(ref.Name)
	if err != nil {
		return sandbox.Status{}, err
	}
	status, err := b.Status(ctx, ref)
	if err != nil {
		return status, err
	}
	if status.State == sandbox.StateStopped || status.State == sandbox.StateAbsent {
		return status, nil
	}
	switch b.Kind {
	case "ssh":
		if spec.Workload != nil {
			out, gerr := b.script(ctx, ref.Name, "systemctl stop ere-"+id.Owner)
			err = gerr
			if err == nil && out.ExitCode != 0 {
				err = fmt.Errorf("stop workload: %s", out.Stderr)
			}
		}
	case "tart":
		if _, err = backend.Check(ctx, "launchctl", "disable", fmt.Sprintf("gui/%d/com.ere.vm.%s", os.Getuid(), id.Owner)); err != nil {
			return status, err
		}
		_, err = b.run(ctx, "", "stop", b.name(ref.Name))
		if err != nil {
			return status, err
		}
		wait, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		for {
			state, nerr := b.native(wait, ref.Name)
			if nerr != nil {
				return status, nerr
			}
			if state == sandbox.StateStopped {
				break
			}
			select {
			case <-wait.Done():
				return status, wait.Err()
			case <-time.After(time.Second):
			}
		}
		_, err = backend.Check(ctx, "launchctl", "bootout", fmt.Sprintf("gui/%d/com.ere.vm.%s", os.Getuid(), id.Owner))
	default:
		_, err = b.run(ctx, "", "stop", b.name(ref.Name))
	}
	if err != nil {
		return status, err
	}
	return b.Status(ctx, ref)
}

func (b *Backend) Destroy(ctx context.Context, ref sandbox.Ref) (sandbox.Status, error) {
	spec, id, err := b.load(ref.Name)
	if err != nil {
		return sandbox.Status{}, err
	}
	native, err := b.native(ctx, ref.Name)
	if err != nil {
		return sandbox.Status{}, err
	}
	if err = b.owned(ctx, ref.Name, id); err != nil {
		return sandbox.Status{}, err
	}
	if native == sandbox.StateRunning {
		if _, err = b.Stop(ctx, ref); err != nil {
			return sandbox.Status{}, err
		}
	}
	if b.Kind == "tart" {
		domain := fmt.Sprintf("gui/%d/com.ere.vm.%s", os.Getuid(), id.Owner)
		job, err := backend.Run(ctx, "launchctl", "print", domain)
		if err != nil {
			return sandbox.Status{}, err
		}
		if job.ExitCode == 0 {
			if _, err = backend.Check(ctx, "launchctl", "bootout", domain); err != nil {
				return sandbox.Status{}, err
			}
		}
	}
	switch b.Kind {
	case "ssh":
		out, gerr := b.script(ctx, ref.Name, "set -eu; if test -f /etc/systemd/system/ere-"+id.Owner+".service; then systemctl disable ere-"+id.Owner+"; fi; rm -f /etc/systemd/system/ere-"+id.Owner+".service /var/lib/ere-"+id.Owner+"/env /var/lib/ere-"+id.Owner+"/start /var/lib/ere-"+id.Owner+"/owner; systemctl daemon-reload")
		err = gerr
		if err == nil && out.ExitCode != 0 {
			err = fmt.Errorf("remove workload: %s", out.Stderr)
		}
	case "multipass":
		_, err = b.run(ctx, "", "delete", b.name(ref.Name), "--purge")
	default:
		_, err = b.run(ctx, "", "delete", b.name(ref.Name))
	}
	if err != nil {
		return sandbox.Status{}, err
	}
	if err = os.Remove(specPath(id)); err != nil {
		return sandbox.Status{}, err
	}
	return sandbox.Status{Name: spec.Name, Backend: b.Kind, State: sandbox.StateAbsent}, nil
}

func (b *Backend) List(ctx context.Context) (sandbox.List, error) {
	id, err := b.id("index")
	if err != nil {
		return sandbox.List{}, err
	}
	files, err := filepath.Glob(filepath.Join(filepath.Dir(id.Path), "*-spec.json"))
	if err != nil {
		return sandbox.List{}, err
	}
	result := sandbox.List{Sandboxes: []sandbox.Status{}}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return result, err
		}
		var s sandbox.Spec
		if err = json.Unmarshal(data, &s); err != nil {
			return result, err
		}
		status, err := b.Status(ctx, sandbox.Ref{Name: s.Name})
		if err != nil {
			return result, err
		}
		result.Sandboxes = append(result.Sandboxes, status)
	}
	return result, nil
}

func (b *Backend) tartHome() string {
	if v := os.Getenv("TART_HOME"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".tart")
}

func xmlText(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func (b *Backend) startTart(ctx context.Context, name string, id backend.Identity) error {
	binary, err := exec.LookPath(b.binary())
	if err != nil {
		return err
	}
	binary, err = filepath.Abs(binary)
	if err != nil {
		return err
	}
	domain := fmt.Sprintf("gui/%d/com.ere.vm.%s", os.Getuid(), id.Owner)
	if _, err = backend.Check(ctx, "launchctl", "enable", domain); err != nil {
		return err
	}
	existing, err := backend.Run(ctx, "launchctl", "print", domain)
	if err != nil {
		return err
	}
	if existing.ExitCode == 0 {
		_, err = backend.Check(ctx, "launchctl", "kickstart", domain)
		return err
	}
	path := filepath.Join(filepath.Dir(id.Path), id.Owner+".plist")
	plist := `<?xml version="1.0"?><plist version="1.0"><dict><key>Label</key><string>com.ere.vm.` + id.Owner + `</string><key>ProgramArguments</key><array><string>` + xmlText(binary) + `</string><string>run</string><string>` + xmlText(b.name(name)) + `</string><string>--no-graphics</string></array><key>EnvironmentVariables</key><dict><key>TART_HOME</key><string>` + xmlText(b.tartHome()) + `</string><key>TART_NO_AUTO_PRUNE</key><string>1</string></dict><key>RunAtLoad</key><true/><key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict></dict></plist>`
	if err = store.Write(path, []byte(plist)); err != nil {
		return err
	}
	_, err = backend.Check(ctx, "launchctl", "bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), path)
	return err
}

func (b *Backend) workload(work sandbox.Workload, id backend.Identity) string {
	dir := "/var/lib/ere-" + id.Owner
	start := "set -a\n. " + backend.Quote(dir+"/env") + "\nset +a\n" + backend.WorkloadScriptAt(work, dir)
	script := "set -eu\numask 077\nmkdir -p " + backend.Quote(dir) + "\nprintf %s " + backend.Quote(backend.EnvFile(work.Env)) + " > " + backend.Quote(dir+"/env") + "\nprintf %s " + backend.Quote(start) + " > " + backend.Quote(dir+"/start") + "\n"
	if b.Kind == "tart" {
		label := "com.ere.workload." + id.Owner
		plist := `<?xml version="1.0"?><plist version="1.0"><dict><key>Label</key><string>` + label + `</string><key>ProgramArguments</key><array><string>/bin/sh</string><string>` + xmlText(dir+"/start") + `</string></array><key>RunAtLoad</key><true/><key>KeepAlive</key><true/><key>StandardOutPath</key><string>` + xmlText(dir+"/output.log") + `</string><key>StandardErrorPath</key><string>` + xmlText(dir+"/output.log") + `</string></dict></plist>`
		path := "/Library/LaunchDaemons/" + label + ".plist"
		return script + "printf %s " + backend.Quote(plist) + " > " + backend.Quote(path) + "\nchmod 644 " + backend.Quote(path) + "\nif ! launchctl print system/" + label + " >/dev/null 2>&1; then launchctl bootstrap system " + backend.Quote(path) + "; fi\n"
	}
	unit := "[Unit]\nAfter=network-online.target\n[Service]\nExecStart=/bin/sh " + dir + "/start\nRestart=always\nRestartSec=3\n[Install]\nWantedBy=multi-user.target\n"
	return script + "printf %s " + backend.Quote(unit) + " > /etc/systemd/system/ere-" + id.Owner + ".service\nsystemctl daemon-reload\nsystemctl enable --now ere-" + id.Owner + "\n"
}
