package sandbox_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/roshbhatia/ere/internal/sandbox"
	"github.com/roshbhatia/go-utils/provider"
)

// fake is the backend the helper process serves.
type fake struct{ failStart bool }

func (f *fake) Name() string { return "fake" }

func (f *fake) Probe(context.Context) (sandbox.Probe, error) {
	return sandbox.Probe{Backend: "fake", Available: true, Operations: sandbox.Operations()}, nil
}

func (f *fake) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Status, error) {
	sandbox.Progress(ctx, "create", "creating "+spec.Name)
	return sandbox.Status{Name: spec.Name, Backend: "fake", State: sandbox.StateStopped, Image: spec.Image}, nil
}

func (f *fake) Start(_ context.Context, ref sandbox.Ref) (sandbox.Status, error) {
	if f.failStart {
		return sandbox.Status{}, errors.New("hypervisor refused the instance")
	}
	return sandbox.Status{Name: ref.Name, Backend: "fake", State: sandbox.StateRunning}, nil
}

func (f *fake) Exec(_ context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
	return sandbox.ExecResult{ExitCode: 0, Stdout: strings.Join(req.Argv, " ")}, nil
}

func (f *fake) Status(_ context.Context, ref sandbox.Ref) (sandbox.Status, error) {
	return sandbox.Status{Name: ref.Name, Backend: "fake", State: sandbox.StateRunning}, nil
}

func (f *fake) List(context.Context) (sandbox.List, error) {
	return sandbox.List{Sandboxes: []sandbox.Status{{Name: "one", Backend: "fake"}}}, nil
}

func (f *fake) Logs(_ context.Context, _ sandbox.LogRequest) (sandbox.Logs, error) {
	return sandbox.Logs{Lines: []string{"line"}}, nil
}

func (f *fake) Stop(_ context.Context, ref sandbox.Ref) (sandbox.Status, error) {
	return sandbox.Status{Name: ref.Name, Backend: "fake", State: sandbox.StateStopped}, nil
}

func (f *fake) Destroy(_ context.Context, ref sandbox.Ref) (sandbox.Status, error) {
	return sandbox.Status{Name: ref.Name, Backend: "fake", State: sandbox.StateAbsent}, nil
}

// TestHelperBackend is not a test. It is the provider executable the round-trip
// tests invoke, so the contract is exercised across a real process boundary.
func TestHelperBackend(t *testing.T) {
	if os.Getenv("ERE_TEST_BACKEND") == "" {
		t.Skip("helper process")
	}
	backend := &fake{failStart: os.Getenv("ERE_TEST_FAIL_START") != ""}
	if err := sandbox.Serve(context.Background(), backend, os.Stdin, os.Stdout); err != nil {
		t.Fatal(err)
	}
	// The test binary would print its own PASS line after the result frame, and
	// provider/v1 rejects any frame that follows the result.
	os.Exit(0)
}

func helperManifest(t *testing.T) provider.Manifest {
	t.Helper()
	return provider.Manifest{
		Version:     provider.Version,
		Kind:        sandbox.Kind,
		Name:        "fake",
		Description: "test backend",
		Command:     []string{os.Args[0], "-test.run=^TestHelperBackend$", "-test.v=false"},
		Actions: map[string]provider.Action{
			sandbox.Capability: {
				Description: "test backend",
				Env:         map[string]string{"ERE_TEST_BACKEND": "1"},
			},
		},
	}
}

func TestRoundTripCarriesInputAndOutput(t *testing.T) {
	client := sandbox.NewClient(helperManifest(t))
	var events []string
	client.OnEvent = func(event provider.Event) { events = append(events, event.Message) }

	status, err := client.Create(context.Background(), sandbox.Spec{Name: "alpha", Image: "debian"})
	if err != nil {
		t.Fatal(err)
	}
	if status.Name != "alpha" || status.Image != "debian" {
		t.Fatalf("spec did not reach the backend: %+v", status)
	}
	if status.State != sandbox.StateStopped {
		t.Fatalf("state = %q, want stopped", status.State)
	}
	if len(events) != 1 || events[0] != "creating alpha" {
		t.Fatalf("progress events = %v", events)
	}
}

func TestRoundTripPreservesArgvAcrossTheProcessBoundary(t *testing.T) {
	client := sandbox.NewClient(helperManifest(t))
	result, err := client.Exec(context.Background(), sandbox.ExecRequest{
		Name: "alpha",
		Argv: []string{"sh", "-c", "echo 'a b'"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stdout != "sh -c echo 'a b'" {
		t.Fatalf("argv was reshaped in transit: %q", result.Stdout)
	}
}

func TestBackendErrorBecomesAnErrorResult(t *testing.T) {
	manifest := helperManifest(t)
	action := manifest.Actions[sandbox.Capability]
	action.Env["ERE_TEST_FAIL_START"] = "1"
	manifest.Actions[sandbox.Capability] = action

	client := sandbox.NewClient(manifest)
	_, err := client.Start(context.Background(), sandbox.Ref{Name: "alpha"})
	if err == nil {
		t.Fatal("expected the backend failure to reach the caller")
	}
	if !strings.Contains(err.Error(), "hypervisor refused the instance") {
		t.Fatalf("error lost the backend's reason: %v", err)
	}
}

func TestUnsupportedOperationIsRejected(t *testing.T) {
	var out strings.Builder
	request := `{"version":"provider/v1","kind":"request","requestId":"1","capability":"sandbox","operation":"teleport"}`
	if err := sandbox.Serve(context.Background(), &fake{}, strings.NewReader(request), &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"status":"error"`) {
		t.Fatalf("expected an error result, got %s", out.String())
	}
}

func TestForeignCapabilityIsRefused(t *testing.T) {
	request := `{"version":"provider/v1","kind":"request","requestId":"1","capability":"chat"}`
	err := sandbox.Serve(context.Background(), &fake{}, strings.NewReader(request), &strings.Builder{})
	if err == nil {
		t.Fatal("expected a capability mismatch to fail")
	}
}
