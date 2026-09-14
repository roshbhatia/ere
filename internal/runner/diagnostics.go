package runner

import (
	"context"
	"fmt"
	"strings"

	"github.com/roshbhatia/ere/internal/sandbox"
)

type Diagnostic struct {
	Check  string `json:"check"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

func (e *Engine) Diagnose(ctx context.Context, name string) ([]Diagnostic, error) {
	r, ok := e.Config.Runner(name)
	if !ok {
		return nil, fmt.Errorf("unknown runner %q", name)
	}
	result := []Diagnostic{}
	add := func(check, detail string, err error) {
		status := "ok"
		if err != nil {
			status = "error"
			detail = err.Error()
		}
		result = append(result, Diagnostic{check, status, detail})
	}
	if out, err := e.ampOutput(ctx, "--version"); err != nil {
		add("controller Amp", "", err)
	} else {
		add("controller Amp", strings.TrimSpace(string(out)), nil)
	}
	env, err := e.environment(ctx, r)
	if err == nil && env["AMP_API_KEY"] == "" {
		err = fmt.Errorf("set amp.apiKeySecret to an Amp access-token reference")
	}
	add("runner credential", "configured; authentication not tested", err)
	client, err := e.client(r)
	if err != nil {
		return result, err
	}
	p, err := client.Probe(ctx)
	if err == nil && !p.Available {
		err = fmt.Errorf("%s", p.Detail)
	}
	add("provider", p.Detail, err)
	if err != nil {
		return result, nil
	}
	s, err := client.Status(ctx, sandbox.Ref{Name: r.Name})
	add("compute", string(s.State), err)
	if err == nil && s.State == sandbox.StateRunning {
		out, err := client.Exec(ctx, sandbox.ExecRequest{Name: r.Name, Argv: []string{"sh", "-c", "test -d \"$1\" && cd \"$1\" && test -r . && command -v \"$2\" && \"$2\" --version", "sh", r.MountPath, e.Config.Amp.Binary}})
		if err == nil && out.ExitCode != 0 {
			err = fmt.Errorf("guest workspace or Amp CLI is unavailable: %s", out.Stderr)
		}
		add("workspace and guest Amp", strings.TrimSpace(out.Stdout), err)
		running, err := e.agentRunning(ctx, client, r)
		if err == nil && !running {
			err = fmt.Errorf("amp process is stopped; inspect ere logs %s", name)
		}
		add("Amp process", "running", err)
	}
	result = append(result, Diagnostic{"cross-client access", "unverified", "check the Amp account, workspace Cross-Client Access policy, and runner connection"})
	result = append(result, Diagnostic{"web terminal", "info", fmt.Sprintf("remoteControlTerminal=%t", r.RemoteControlTerminal)})
	return result, nil
}
