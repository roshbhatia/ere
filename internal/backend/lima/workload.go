package lima

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/roshbhatia/ere/internal/backend"
	"github.com/roshbhatia/ere/internal/sandbox"
)

func NewManaged(binary, vmType, base string) *Backend {
	if vmType == "auto" {
		vmType = ""
	}
	b := New(binary, vmType, base)
	b.Managed = true
	return b
}

func (b *Backend) specPath(name string) (string, error) {
	id, err := backend.LoadIdentity("lima|"+b.VMType, name)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(id.Path), id.Owner+"-spec.json"), nil
}

func (b *Backend) saveSpec(spec sandbox.Spec) error {
	path, err := b.specPath(spec.Name)
	if err != nil {
		return err
	}
	data, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func (b *Backend) loadSpec(name string) (sandbox.Spec, error) {
	var spec sandbox.Spec
	path, err := b.specPath(name)
	if err != nil {
		return spec, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return spec, fmt.Errorf("unmanaged Lima instance: %w", err)
	}
	err = json.Unmarshal(data, &spec)
	return spec, err
}

func (b *Backend) installWorkload(ctx context.Context, name string) error {
	spec, err := b.loadSpec(name)
	if err != nil {
		return err
	}
	if spec.Workload == nil {
		return nil
	}
	work := *spec.Workload
	unit := "[Unit]\nAfter=network-online.target\nWants=network-online.target\n[Service]\nUser=root\nType=simple\nExecStart=/bin/sh /var/lib/ere/start\nRestart=always\nRestartSec=3\nKillMode=control-group\n[Install]\nWantedBy=multi-user.target\n"
	start := "set -a\n. /var/lib/ere/env\nset +a\n" + backend.WorkloadScript(work)
	script := "set -eu\numask 077\nmkdir -p /var/lib/ere\nprintf %s " + backend.Quote(backend.EnvFile(work.Env)) + " > /var/lib/ere/env\nprintf %s " + backend.Quote(start) + " > /var/lib/ere/start\nprintf %s " + backend.Quote(unit) + " > /etc/systemd/system/ere-workload.service\nsystemctl daemon-reload\nsystemctl enable --now ere-workload\n"
	out, err := backend.RunStdin(ctx, script, b.Binary, "shell", "--tty=false", "--workdir", "/", instance(name), "--", "sudo", "sh", "-s")
	if err != nil {
		return err
	}
	if out.ExitCode != 0 {
		return fmt.Errorf("install guest workload: %s", out.Stderr)
	}
	return nil
}
