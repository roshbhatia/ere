package runner

import (
	"context"
	"fmt"
	"time"

	"github.com/roshbhatia/lifier/internal/amp"
	"github.com/roshbhatia/lifier/internal/config"
	"github.com/roshbhatia/lifier/internal/sandbox"
)

func (e *Engine) managedSpec(r config.Runner, env map[string]string) (sandbox.Spec, error) {
	spec, err := e.spec(r)
	if err != nil {
		return spec, err
	}
	mount := r.MountPath
	if mount == "" {
		mount = "/workspace"
	}
	spec.Workload = &sandbox.Workload{Argv: amp.RunnerArgv(e.Config.Amp, r), Env: env, Workdir: mount, Provision: r.Provision}
	return spec, sandbox.Validate(spec)
}

func (e *Engine) upManaged(ctx context.Context, client *sandbox.Client, r config.Runner, env map[string]string, restart bool) error {
	spec, err := e.managedSpec(r, env)
	if err != nil {
		return err
	}
	if _, err := client.Validate(ctx, spec); err != nil {
		return err
	}
	status, err := client.Create(ctx, spec)
	if err != nil {
		return err
	}
	if restart && status.State == sandbox.StateRunning {
		if _, err := client.Stop(ctx, sandbox.Ref{Name: r.Name}); err != nil {
			return err
		}
	}
	e.report("%s: ensuring supervised workload on %s", r.Name, r.Backend)
	if _, err := client.Start(ctx, sandbox.Ref{Name: r.Name}); err != nil {
		return err
	}
	wait, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	for {
		running, err := e.agentRunning(wait, client, r)
		if err == nil && running {
			e.report("%s: supervised Amp process is running; remote registration is not inferred", r.Name)
			return nil
		}
		select {
		case <-wait.Done():
			return fmt.Errorf("runner %s did not reach runtime readiness: %w; last probe error: %v; inspect lifier logs", r.Name, wait.Err(), err)
		case <-time.After(2 * time.Second):
		}
	}
}

func (e *Engine) Plan(ctx context.Context, names []string) ([]sandbox.Plan, error) {
	runners, err := e.selected(names)
	if err != nil {
		return nil, err
	}
	plans := make([]sandbox.Plan, 0, len(runners))
	for _, r := range runners {
		client, err := e.client(r)
		if err != nil {
			return nil, err
		}
		probe, err := client.Probe(ctx)
		if err != nil {
			return nil, err
		}
		if !probe.Available {
			return nil, fmt.Errorf("provider %s: %s", r.Backend, probe.Detail)
		}
		env, err := e.environment(ctx, r)
		if err != nil {
			return nil, err
		}
		spec, err := e.managedSpec(r, env)
		if err != nil {
			return nil, err
		}
		if probe.Contract == sandbox.ContractVersion {
			if _, err := client.Validate(ctx, spec); err != nil {
				return nil, err
			}
		}
		status, err := client.Status(ctx, sandbox.Ref{Name: r.Name})
		if err != nil {
			return nil, err
		}
		action := "none"
		if status.State == sandbox.StateAbsent {
			action = "create"
		} else if status.Digest != "" && status.Digest != sandbox.Digest(spec) {
			action = "replace"
		} else if status.State != sandbox.StateRunning {
			action = "start"
		}
		plans = append(plans, sandbox.Plan{Name: r.Name, Backend: r.Backend, Action: action, Digest: sandbox.Digest(spec), Retention: probe.Retention})
	}
	return plans, nil
}
