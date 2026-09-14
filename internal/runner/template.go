package runner

import (
	"context"
	"fmt"

	"github.com/roshbhatia/ere/internal/sandbox"
)

func (e *Engine) BuildTemplate(ctx context.Context, profile, name string) error {
	r, ok := e.Config.Runner(profile)
	if !ok {
		return fmt.Errorf("unknown profile %q", profile)
	}
	r.Name = name
	r.RunnerID = "ere-template-" + sandbox.Digest(r.Backend + "|" + name)[:32]
	lease, err := e.lock(ctx, r)
	if err != nil {
		return err
	}
	defer lease.Close()
	if err = lease.CanStop(); err != nil {
		return err
	}
	r.Workspace = ""
	r.Storage = sandbox.Storage{Kind: "guest-disk"}
	r.BootVolume = ""
	spec, err := e.spec(r)
	if err != nil {
		return err
	}
	spec.Labels = map[string]string{"ere.template": "building"}
	client, err := e.client(r)
	if err != nil {
		return err
	}
	kind := r.Backend
	if p := e.Config.Providers[kind]; p.Kind != "" {
		kind = p.Kind
	}
	if kind != "lima" {
		return fmt.Errorf("prepared template build currently requires lima")
	}
	if _, err = client.Validate(ctx, spec); err != nil {
		return err
	}
	status, err := client.Status(ctx, sandbox.Ref{Name: name})
	if err != nil {
		return err
	}
	if status.State != sandbox.StateAbsent {
		return fmt.Errorf("template name already exists")
	}
	if _, err = client.Create(ctx, spec); err != nil {
		return err
	}
	if _, err = client.Start(ctx, sandbox.Ref{Name: name}); err != nil {
		return err
	}
	for _, script := range r.Provision {
		out, err := client.Exec(ctx, sandbox.ExecRequest{Name: name, Argv: []string{"sh", "-c", script}})
		if err != nil {
			return err
		}
		if out.ExitCode != 0 {
			return fmt.Errorf("template provisioning failed: %s", out.Stderr)
		}
	}
	out, err := client.Exec(ctx, sandbox.ExecRequest{Name: name, Argv: []string{"sh", "-c", "set -eu; test ! -e /var/lib/ere/env; test ! -e /etc/systemd/system/ere-workload.service; truncate -s 0 /etc/machine-id; rm -f /var/lib/dbus/machine-id; rm -f /etc/ssh/ssh_host_*"}})
	if err != nil {
		return err
	}
	if out.ExitCode != 0 {
		return fmt.Errorf("template preparation: %s", out.Stderr)
	}
	if _, err = client.Stop(ctx, sandbox.Ref{Name: name}); err != nil {
		return err
	}
	_, err = client.SealTemplate(ctx, sandbox.Ref{Name: name})
	return err
}

func (e *Engine) CloneTemplate(ctx context.Context, source, target string) error {
	r, ok := e.Config.Runner(target)
	if !ok {
		return fmt.Errorf("unknown target %q", target)
	}
	s, err := e.lock(ctx, r)
	if err != nil {
		return err
	}
	defer s.Close()
	if err = s.CanStop(); err != nil {
		return err
	}
	env, err := e.environment(ctx, r)
	if err != nil {
		return err
	}
	spec, err := e.managedSpec(r, env)
	if err != nil {
		return err
	}
	client, err := e.client(r)
	if err != nil {
		return err
	}
	_, err = client.Clone(ctx, sandbox.CloneRequest{Source: source, Spec: spec})
	return err
}
