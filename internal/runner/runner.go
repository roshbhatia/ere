// Package runner reconciles the declared runners against what the backends
// actually hold. It owns the only knowledge that spans the two: an Amp runner
// is a detached process inside a sandbox, and the sandbox outlives it.
package runner

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/roshbhatia/ere/internal/amp"
	"github.com/roshbhatia/ere/internal/config"
	"github.com/roshbhatia/ere/internal/registry"
	"github.com/roshbhatia/ere/internal/sandbox"
	"github.com/roshbhatia/ere/internal/secret"
	"github.com/roshbhatia/go-utils/paths"
	"github.com/roshbhatia/go-utils/provider"
)

// LogPath is where a runner's output lands inside its sandbox.
const LogPath = "/tmp/ere.log"

// Engine drives the declared fleet.
type Engine struct {
	Config   config.Config
	Registry *registry.Registry
	Secrets  *secret.Resolver
	Progress io.Writer
}

// New binds an engine to a loaded config and registry.
func New(cfg config.Config, reg *registry.Registry, secrets *secret.Resolver, progress io.Writer) *Engine {
	return &Engine{Config: cfg, Registry: reg, Secrets: secrets, Progress: progress}
}

func (e *Engine) report(format string, args ...any) {
	if e.Progress == nil {
		return
	}
	fmt.Fprintf(e.Progress, format+"\n", args...)
}

func (e *Engine) client(runner config.Runner) (*sandbox.Client, error) {
	client, err := e.Registry.Client(runner.Backend)
	if err != nil {
		return nil, err
	}
	client.OnEvent = func(event provider.Event) {
		if event.Message != "" {
			e.report("  %s: %s", runner.Name, event.Message)
		}
	}
	return client, nil
}

func (e *Engine) selected(names []string) ([]config.Runner, error) {
	if len(names) == 0 {
		if len(e.Config.Runners) == 0 {
			return nil, fmt.Errorf("no runners declared; add one to %s.yaml", config.Name)
		}
		return e.Config.Runners, nil
	}
	runners := make([]config.Runner, 0, len(names))
	for _, name := range names {
		runner, ok := e.Config.Runner(name)
		if !ok {
			return nil, fmt.Errorf("unknown runner %q; declared: %s", name, strings.Join(e.Config.Names(), ", "))
		}
		runners = append(runners, runner)
	}
	return runners, nil
}

func (e *Engine) spec(runner config.Runner) (sandbox.Spec, error) {
	workspace := runner.Workspace
	if workspace != "" && runner.Storage.Kind != "volume" && runner.Storage.Kind != "pvc" {
		expanded, err := filepath.Abs(paths.ExpandHome(workspace))
		if err != nil {
			return sandbox.Spec{}, fmt.Errorf("runner %s workspace: %w", runner.Name, err)
		}
		workspace = expanded
	}
	return sandbox.Spec{
		Name:         runner.Name,
		Storage:      runner.Storage,
		BootVolume:   runner.BootVolume,
		Architecture: runner.Architecture,
		Image:        runner.Image,
		Workspace:    workspace,
		MountPath:    runner.MountPath,
		ReadOnly:     runner.ReadOnly,
		CPUs:         runner.CPUs,
		MemoryMB:     runner.MemoryMB,
		DiskGB:       runner.DiskGB,
		Labels:       map[string]string{"ere.runner-id": runner.RunnerID},
	}, nil
}

// environment resolves the secrets a runner declares and merges them under the
// Amp variables. Values exist only for the life of this process.
func (e *Engine) environment(ctx context.Context, runner config.Runner) (map[string]string, error) {
	merged := map[string]string{}
	for key, value := range runner.Env {
		merged[key] = value
	}
	resolved, err := e.Secrets.ResolveAll(ctx, runner.Secrets)
	if err != nil {
		return nil, fmt.Errorf("runner %s: %w", runner.Name, err)
	}
	for key, value := range resolved {
		merged[key] = value
	}
	apiKey, err := e.Secrets.Resolve(ctx, e.Config.Amp.APIKeySecret)
	if err != nil {
		return nil, fmt.Errorf("runner %s amp credential: %w", runner.Name, err)
	}
	return amp.Env(e.Config.Amp, apiKey, merged), nil
}

// Up brings every selected runner to running: the sandbox exists, it is
// started, provisioning has run, and the Amp runner process is alive.
func (e *Engine) Up(ctx context.Context, names []string, restart bool) error {
	runners, err := e.selected(names)
	if err != nil {
		return err
	}
	for _, runner := range runners {
		if err := e.up(ctx, runner, restart); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) up(ctx context.Context, runner config.Runner, restart bool) error {
	store, err := e.lock(ctx, runner)
	if err != nil {
		return err
	}
	defer store.Close()
	if restart {
		if err := store.CanStop(); err != nil {
			return err
		}
	}
	client, err := e.client(runner)
	if err != nil {
		return err
	}
	env, err := e.environment(ctx, runner)
	if err != nil {
		return err
	}
	if env[amp.EnvAPIKey] == "" {
		return fmt.Errorf(
			"runner %s has no Amp credential: set amp.apiKeySecret, or %s under the runner's env or secrets. "+
				"Without one amp opens an interactive login prompt inside the sandbox and waits there",
			runner.Name, amp.EnvAPIKey)
	}
	ref := sandbox.Ref{Name: runner.Name}
	status, err := client.Status(ctx, ref)
	if err != nil {
		return err
	}
	probe, err := client.Probe(ctx)
	if err != nil {
		return err
	}
	if !probe.Available && probe.Contract == sandbox.ContractVersion {
		return fmt.Errorf("provider %s: %s", runner.Backend, probe.Detail)
	}
	if status.Managed || (status.State == sandbox.StateAbsent && probe.Contract == sandbox.ContractVersion) {
		return e.upManaged(ctx, client, runner, env, restart)
	}
	if status.State == sandbox.StateAbsent {
		spec, err := e.spec(runner)
		if err != nil {
			return err
		}
		e.report("%s: creating sandbox on %s", runner.Name, runner.Backend)
		if status, err = client.Create(ctx, spec); err != nil {
			return err
		}
	}
	if status.State != sandbox.StateRunning {
		e.report("%s: starting sandbox", runner.Name)
		if _, err = client.Start(ctx, ref); err != nil {
			return err
		}
	}

	workdir := runner.MountPath
	if workdir == "" {
		workdir = "/workspace"
	}

	if !restart {
		running, err := e.agentRunning(ctx, client, runner)
		if err != nil {
			return err
		}
		if running {
			e.report("%s: amp runner %s is already up", runner.Name, runner.RunnerID)
			return nil
		}
	}
	for _, command := range runner.Provision {
		e.report("%s: provision %s", runner.Name, command)
		result, err := client.Exec(ctx, sandbox.ExecRequest{
			Name:    runner.Name,
			Argv:    []string{"sh", "-lc", command},
			Workdir: workdir,
			Env:     env,
		})
		if err != nil {
			return err
		}
		if result.ExitCode != 0 {
			return fmt.Errorf("runner %s provision %q exited %d: %s",
				runner.Name, command, result.ExitCode, strings.TrimSpace(result.Stderr))
		}
	}

	running, err := e.agentRunning(ctx, client, runner)
	if err != nil {
		return err
	}
	if running && !restart {
		e.report("%s: amp runner %s is already up", runner.Name, runner.RunnerID)
		return nil
	}
	if running {
		if err := e.stopAgent(ctx, client, runner); err != nil {
			return err
		}
	}

	argv := amp.RunnerArgv(e.Config.Amp, runner)
	e.report("%s: starting %s", runner.Name, strings.Join(argv, " "))
	result, err := client.Exec(ctx, sandbox.ExecRequest{
		Name:    runner.Name,
		Argv:    argv,
		Workdir: workdir,
		Env:     env,
		Detach:  true,
		LogFile: LogPath,
	})
	if err != nil {
		return err
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("runner %s failed to launch amp (exit %d): %s",
			runner.Name, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return e.confirmLaunch(ctx, client, runner)
}

// SettleDelay is how long a launched agent is given before ere checks that
// it is still there.
var SettleDelay = 3 * time.Second

// confirmLaunch turns a silent early exit into a reported failure. amp exits in
// under a second on a bad credential, and a detached exec cannot tell us.
func (e *Engine) confirmLaunch(ctx context.Context, client *sandbox.Client, runner config.Runner) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(SettleDelay):
	}
	running, err := e.agentRunning(ctx, client, runner)
	if err != nil {
		return err
	}
	if running {
		e.report("%s: amp is up as runner id %s", runner.Name, runner.RunnerID)
		return nil
	}
	logs, logErr := client.Logs(ctx, sandbox.LogRequest{Name: runner.Name, Lines: 10})
	detail := ""
	if logErr == nil && len(logs.Lines) > 0 {
		detail = ": " + strings.Join(logs.Lines, "; ")
	}
	return fmt.Errorf("runner %s: amp exited within %s of launch%s", runner.Name, SettleDelay, detail)
}

// agentRunning asks the sandbox for its process table rather than trusting a
// pid file, which a sandbox restart would leave stale.
func (e *Engine) agentRunning(ctx context.Context, client *sandbox.Client, runner config.Runner) (bool, error) {
	result, err := client.Exec(ctx, sandbox.ExecRequest{
		Name: runner.Name,
		Argv: []string{"sh", "-c", "ps -A -o args= 2>/dev/null || ps ax 2>/dev/null || true"},
	})
	if err != nil {
		return false, err
	}
	if result.ExitCode != 0 {
		return false, fmt.Errorf("read process table: %s", result.Stderr)
	}
	for _, line := range strings.Split(result.Stdout, "\n") {
		fields := strings.Fields(line)
		idMatch, noTUI := false, false
		for i, field := range fields {
			if field == "--no-tui" {
				noTUI = true
			}
			if field == "--runner-id" && i+1 < len(fields) && fields[i+1] == runner.RunnerID {
				idMatch = true
			}
		}
		if idMatch && noTUI {
			return true, nil
		}
	}
	return false, nil
}

func (e *Engine) stopAgent(ctx context.Context, client *sandbox.Client, runner config.Runner) error {
	e.report("%s: stopping the running amp runner", runner.Name)
	out, err := client.Exec(ctx, sandbox.ExecRequest{
		Name: runner.Name,
		Argv: []string{"sh", "-c", "pkill -f -- '[a]mp .*--no-tui .*--runner-id " + runner.RunnerID + "( |$)'; code=$?; [ \"$code\" -eq 0 ] || [ \"$code\" -eq 1 ]"},
	})
	if err != nil {
		return err
	}
	if out.ExitCode != 0 {
		return fmt.Errorf("stop amp: %s", out.Stderr)
	}
	return nil
}

// Down stops the sandbox of every selected runner, keeping its disk.
func (e *Engine) Down(ctx context.Context, names []string) error {
	return e.each(ctx, names, func(ctx context.Context, client *sandbox.Client, runner config.Runner) error {
		store, err := e.lock(ctx, runner)
		if err != nil {
			return err
		}
		defer store.Close()
		if err := store.CanStop(); err != nil {
			return err
		}
		e.report("%s: stopping sandbox", runner.Name)
		_, err = client.Stop(ctx, sandbox.Ref{Name: runner.Name})
		return err
	})
}

// Remove destroys the sandbox of every selected runner.
func (e *Engine) Remove(ctx context.Context, names []string) error {
	return e.each(ctx, names, func(ctx context.Context, client *sandbox.Client, runner config.Runner) error {
		store, err := e.lock(ctx, runner)
		if err != nil {
			return err
		}
		defer store.Close()
		if err := store.CanStop(); err != nil {
			return err
		}
		e.report("%s: destroying sandbox", runner.Name)
		_, err = client.Destroy(ctx, sandbox.Ref{Name: runner.Name})
		return err
	})
}

func (e *Engine) each(
	ctx context.Context,
	names []string,
	do func(context.Context, *sandbox.Client, config.Runner) error,
) error {
	runners, err := e.selected(names)
	if err != nil {
		return err
	}
	for _, runner := range runners {
		client, err := e.client(runner)
		if err != nil {
			return err
		}
		if err := do(ctx, client, runner); err != nil {
			return err
		}
	}
	return nil
}

// Row is one line of `ere ls`.
type Row struct {
	Runner   string        `json:"runner"`
	RunnerID string        `json:"runnerId"`
	Backend  string        `json:"backend"`
	State    sandbox.State `json:"state"`
	Agent    string        `json:"agent"`
	Detail   string        `json:"detail,omitempty"`
}

// Status reports each selected runner and whether its Amp process is serving.
func (e *Engine) Status(ctx context.Context, names []string) ([]Row, error) {
	runners, err := e.selected(names)
	if err != nil {
		return nil, err
	}
	rows := make([]Row, 0, len(runners))
	for _, runner := range runners {
		client, err := e.client(runner)
		if err != nil {
			return nil, err
		}
		client.OnEvent = nil
		status, err := client.Status(ctx, sandbox.Ref{Name: runner.Name})
		if err != nil {
			return nil, err
		}
		row := Row{
			Runner:   runner.Name,
			RunnerID: runner.RunnerID,
			Backend:  runner.Backend,
			State:    status.State,
			Agent:    "-",
			Detail:   status.Detail,
		}
		if status.State == sandbox.StateRunning {
			// This is the sandbox's process table, so it reports that amp is up,
			// not that ampcode.com has accepted the runner.
			running, err := e.agentRunning(ctx, client, runner)
			if err != nil {
				return nil, err
			}
			row.Agent = "stopped"
			if running {
				row.Agent = "running"
			}
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// Logs returns a bounded tail of one runner's Amp output.
func (e *Engine) Logs(ctx context.Context, name string, lines int) ([]string, error) {
	runner, ok := e.Config.Runner(name)
	if !ok {
		return nil, fmt.Errorf("unknown runner %q", name)
	}
	client, err := e.client(runner)
	if err != nil {
		return nil, err
	}
	client.OnEvent = nil
	logs, err := client.Logs(ctx, sandbox.LogRequest{Name: name, Lines: lines})
	if err != nil {
		return nil, err
	}
	return logs.Lines, nil
}

// Exec runs one command inside a runner's sandbox and returns its exit code.
func (e *Engine) Exec(ctx context.Context, name string, argv []string, out, errOut io.Writer) (int, error) {
	runner, ok := e.Config.Runner(name)
	if !ok {
		return 1, fmt.Errorf("unknown runner %q", name)
	}
	client, err := e.client(runner)
	if err != nil {
		return 1, err
	}
	client.OnEvent = nil
	env, err := e.environment(ctx, runner)
	if err != nil {
		return 1, err
	}
	result, err := client.Exec(ctx, sandbox.ExecRequest{
		Name:    name,
		Argv:    argv,
		Workdir: runner.MountPath,
		Env:     env,
	})
	if err != nil {
		return 1, err
	}
	fmt.Fprint(out, result.Stdout)
	fmt.Fprint(errOut, result.Stderr)
	return result.ExitCode, nil
}

// Doctor probes every registered backend.
func (e *Engine) Doctor(ctx context.Context) ([]sandbox.Probe, error) {
	names := e.Registry.Names()
	probes := make([]sandbox.Probe, 0, len(names))
	for _, name := range names {
		client, err := e.Registry.Client(name)
		if err != nil {
			return nil, err
		}
		probe, err := client.Probe(ctx)
		if err != nil {
			probe = sandbox.Probe{Backend: name, Available: false, Detail: err.Error()}
		}
		probes = append(probes, probe)
	}
	return probes, nil
}
