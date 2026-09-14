package runner_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/roshbhatia/ere/internal/config"
	"github.com/roshbhatia/ere/internal/registry"
	"github.com/roshbhatia/ere/internal/runner"
	"github.com/roshbhatia/ere/internal/sandbox"
	"github.com/roshbhatia/ere/internal/secret"
)

// recording is a backend that keeps its state on disk, because every operation
// reaches it in a separate process.
type recording struct {
	statePath string
	execPath  string
}

type persisted struct {
	States   map[string]sandbox.State `json:"states"`
	Launched string                   `json:"launched"`
}

func (r *recording) load() persisted {
	state := persisted{States: map[string]sandbox.State{}}
	data, err := os.ReadFile(r.statePath)
	if err != nil {
		return state
	}
	_ = json.Unmarshal(data, &state)
	if state.States == nil {
		state.States = map[string]sandbox.State{}
	}
	return state
}

func (r *recording) save(state persisted) {
	data, _ := json.Marshal(state)
	_ = os.WriteFile(r.statePath, data, 0o600)
}

func (r *recording) record(line string) {
	file, err := os.OpenFile(r.execPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer func() { _ = file.Close() }()
	_, _ = fmt.Fprintln(file, line)
}

func (r *recording) Name() string { return "recording" }

func (r *recording) Probe(context.Context) (sandbox.Probe, error) {
	return sandbox.Probe{Backend: "recording", Available: true, Operations: sandbox.Operations()}, nil
}

func (r *recording) Create(_ context.Context, spec sandbox.Spec) (sandbox.Status, error) {
	state := r.load()
	state.States[spec.Name] = sandbox.StateStopped
	r.save(state)
	r.record("create " + spec.Name + " image=" + spec.Image + " workspace=" + spec.Workspace)
	return sandbox.Status{Name: spec.Name, Backend: "recording", State: sandbox.StateStopped}, nil
}

func (r *recording) Start(_ context.Context, ref sandbox.Ref) (sandbox.Status, error) {
	state := r.load()
	state.States[ref.Name] = sandbox.StateRunning
	r.save(state)
	r.record("start " + ref.Name)
	return sandbox.Status{Name: ref.Name, Backend: "recording", State: sandbox.StateRunning}, nil
}

func (r *recording) Exec(_ context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
	joined := strings.Join(req.Argv, " ")
	keys := make([]string, 0, len(req.Env))
	for key := range req.Env {
		keys = append(keys, key)
	}
	// Keys only: a test log is not a place for a resolved credential.
	r.record(fmt.Sprintf("exec detach=%t workdir=%s envKeys=%d argv=%s", req.Detach, req.Workdir, len(keys), joined))

	state := r.load()
	if strings.Contains(joined, "ps -A") {
		return sandbox.ExecResult{Stdout: state.Launched}, nil
	}
	if req.Detach && os.Getenv("ERE_TEST_EXIT_ON_LAUNCH") == "" {
		state.Launched = joined
		r.save(state)
	}
	if hasKey(req.Env, "AMP_API_KEY") {
		r.record("exec carried AMP_API_KEY")
	}
	return sandbox.ExecResult{}, nil
}

func hasKey(env map[string]string, key string) bool {
	_, ok := env[key]
	return ok
}

func (r *recording) Status(_ context.Context, ref sandbox.Ref) (sandbox.Status, error) {
	state := r.load()
	value, ok := state.States[ref.Name]
	if !ok {
		value = sandbox.StateAbsent
	}
	return sandbox.Status{Name: ref.Name, Backend: "recording", State: value}, nil
}

func (r *recording) List(context.Context) (sandbox.List, error) {
	return sandbox.List{}, nil
}

func (r *recording) Logs(context.Context, sandbox.LogRequest) (sandbox.Logs, error) {
	return sandbox.Logs{Lines: []string{"log line"}}, nil
}

func (r *recording) Stop(_ context.Context, ref sandbox.Ref) (sandbox.Status, error) {
	state := r.load()
	state.States[ref.Name] = sandbox.StateStopped
	state.Launched = ""
	r.save(state)
	r.record("stop " + ref.Name)
	return sandbox.Status{Name: ref.Name, Backend: "recording", State: sandbox.StateStopped}, nil
}

func (r *recording) Destroy(_ context.Context, ref sandbox.Ref) (sandbox.Status, error) {
	state := r.load()
	delete(state.States, ref.Name)
	r.save(state)
	r.record("destroy " + ref.Name)
	return sandbox.Status{Name: ref.Name, Backend: "recording", State: sandbox.StateAbsent}, nil
}

// TestHelperBackend is not a test. It is the backend executable the engine
// invokes, so Up is exercised across the same process boundary a real backend
// sits behind.
func TestHelperBackend(t *testing.T) {
	if os.Getenv("ERE_TEST_STATE") == "" {
		t.Skip("helper process")
	}
	backend := &recording{
		statePath: os.Getenv("ERE_TEST_STATE"),
		execPath:  os.Getenv("ERE_TEST_EXEC"),
	}
	if err := sandbox.Serve(context.Background(), backend, os.Stdin, os.Stdout); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}

type harness struct {
	engine   *runner.Engine
	execPath string
}

func newHarness(t *testing.T, runnerEntry config.Runner, amp config.Amp) *harness {
	t.Helper()
	runner.SettleDelay = 10 * time.Millisecond
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.json")
	execPath := filepath.Join(dir, "exec.log")

	providerDir := filepath.Join(dir, "providers")
	if err := os.MkdirAll(providerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf(`version: provider/v1
kind: sandbox
name: recording
description: test backend
command: [%q, "-test.run=^TestHelperBackend$"]
actions:
  sandbox:
    description: test backend
    env:
      ERE_TEST_STATE: %q
      ERE_TEST_EXEC: %q
`, os.Args[0], statePath, execPath)
	if err := os.WriteFile(filepath.Join(providerDir, "recording.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	reg, err := registry.Load(providerDir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{DefaultBackend: "recording", Amp: amp, Runners: []config.Runner{runnerEntry}}
	return &harness{
		engine:   runner.New(cfg, reg, secret.New("op"), nil),
		execPath: execPath,
	}
}

func (h *harness) log(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(h.execPath)
	if err != nil {
		return ""
	}
	return string(data)
}

func baseRunner() config.Runner {
	return config.Runner{
		Name:      "alpha",
		Backend:   "recording",
		Workspace: ".",
		MountPath: "/workspace",
		Image:     "test-image",
		RunnerID:  "alpha",
	}
}

func TestUpCreatesStartsAndServes(t *testing.T) {
	h := newHarness(t, baseRunner(), config.Amp{Binary: "amp", APIKeySecret: "test-key"})
	if err := h.engine.Up(context.Background(), nil, false); err != nil {
		t.Fatal(err)
	}
	log := h.log(t)
	for _, want := range []string{
		"create alpha image=test-image",
		"start alpha",
		"exec detach=true workdir=/workspace",
		"amp --no-tui --runner-id alpha",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("Up did not %q; log:\n%s", want, log)
		}
	}
}

func TestUpReportsAnAgentThatExitsImmediately(t *testing.T) {
	entry := baseRunner()
	entry.Name = "exits"
	entry.RunnerID = "exits"
	h := newHarness(t, entry, config.Amp{Binary: "amp", APIKeySecret: "test-key"})
	t.Setenv("ERE_TEST_EXIT_ON_LAUNCH", "1")

	err := h.engine.Up(context.Background(), nil, false)
	if err == nil {
		t.Fatal("expected a silent early exit to be reported")
	}
	if !strings.Contains(err.Error(), "exited within") {
		t.Fatalf("error does not name the failure: %v", err)
	}
}

func TestUpIsIdempotent(t *testing.T) {
	h := newHarness(t, baseRunner(), config.Amp{Binary: "amp", APIKeySecret: "test-key"})
	ctx := context.Background()
	if err := h.engine.Up(ctx, nil, false); err != nil {
		t.Fatal(err)
	}
	before := strings.Count(h.log(t), "detach=true")
	if err := h.engine.Up(ctx, nil, false); err != nil {
		t.Fatal(err)
	}
	if after := strings.Count(h.log(t), "detach=true"); after != before {
		t.Fatalf("a second Up launched another agent: %d then %d", before, after)
	}
}

func TestUpRestartRelaunchesTheAgent(t *testing.T) {
	h := newHarness(t, baseRunner(), config.Amp{Binary: "amp", APIKeySecret: "test-key"})
	ctx := context.Background()
	if err := h.engine.Up(ctx, nil, false); err != nil {
		t.Fatal(err)
	}
	if err := h.engine.Up(ctx, nil, true); err != nil {
		t.Fatal(err)
	}
	if count := strings.Count(h.log(t), "detach=true"); count != 2 {
		t.Fatalf("expected a relaunch, saw %d detached execs", count)
	}
	if !strings.Contains(h.log(t), "pkill") {
		t.Fatalf("restart did not stop the previous agent:\n%s", h.log(t))
	}
}

func TestUpResolvesSecretsIntoTheLaunchEnvironment(t *testing.T) {
	t.Setenv("ERE_TEST_KEY", "resolved-key")
	entry := baseRunner()
	entry.Secrets = map[string]string{"OPENROUTER_API_KEY": "env://ERE_TEST_KEY"}
	h := newHarness(t, entry, config.Amp{Binary: "amp", APIKeySecret: "env://ERE_TEST_KEY"})

	if err := h.engine.Up(context.Background(), nil, false); err != nil {
		t.Fatal(err)
	}
	log := h.log(t)
	if !strings.Contains(log, "exec carried AMP_API_KEY") {
		t.Fatalf("the credential never reached the sandbox:\n%s", log)
	}
	if strings.Contains(log, "resolved-key") {
		t.Fatalf("a resolved secret leaked into an argument:\n%s", log)
	}
}

func TestUpRunsProvisionBeforeTheAgent(t *testing.T) {
	entry := baseRunner()
	entry.Provision = []string{"exit 1"}
	h := newHarness(t, entry, config.Amp{Binary: "amp", APIKeySecret: "test-key"})
	if err := h.engine.Up(context.Background(), nil, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(h.log(t), "sh -lc exit 1") {
		t.Fatalf("provision did not run:\n%s", h.log(t))
	}
}

func TestStatusReportsWhetherTheAgentServes(t *testing.T) {
	h := newHarness(t, baseRunner(), config.Amp{Binary: "amp", APIKeySecret: "test-key"})
	ctx := context.Background()
	rows, err := h.engine.Status(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].State != sandbox.StateAbsent {
		t.Fatalf("state = %q, want absent before Up", rows[0].State)
	}
	if err := h.engine.Up(ctx, nil, false); err != nil {
		t.Fatal(err)
	}
	rows, err = h.engine.Status(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Agent != "running" {
		t.Fatalf("agent = %q, want running", rows[0].Agent)
	}
}

func TestUpRefusesToLaunchWithoutACredential(t *testing.T) {
	h := newHarness(t, baseRunner(), config.Amp{Binary: "amp"})
	err := h.engine.Up(context.Background(), nil, false)
	if err == nil {
		t.Fatal("expected a missing credential to fail before the agent is launched")
	}
	if !strings.Contains(err.Error(), "AMP_API_KEY") {
		t.Fatalf("error does not say what is missing: %v", err)
	}
	if strings.Contains(h.log(t), "detach=true") {
		t.Fatalf("the agent was launched anyway:\n%s", h.log(t))
	}
}

func TestUnknownRunnerNamesWhatIsDeclared(t *testing.T) {
	h := newHarness(t, baseRunner(), config.Amp{})
	err := h.engine.Up(context.Background(), []string{"beta"}, false)
	if err == nil {
		t.Fatal("expected an unknown runner to fail")
	}
	if !strings.Contains(err.Error(), "alpha") {
		t.Fatalf("error does not list the declared runners: %v", err)
	}
}
