package runner_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roshbhatia/ere/internal/config"
	"github.com/roshbhatia/ere/internal/runner"
)

func TestThreadSubmissionIsDurableAndIdempotent(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	binary := filepath.Join(dir, "amp")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + "'" + log + "'" + "\nprintf '%s\\n' 'https://ampcode.com/threads/T-1234abcd'\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	r := baseRunner()
	r.Env = map[string]string{"AMP_API_KEY": "fixture"}
	h := newHarness(t, r, config.Amp{Binary: "amp", ClientBinary: binary})
	opts := runner.ThreadOptions{Runner: "alpha", ID: "same-task", Prompt: "work", Mode: "custom", Title: "title", Features: []string{"fast"}}
	first, err := h.engine.NewThread(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if first.ThreadID != "T-1234abcd" || !first.Submitted {
		t.Fatalf("allocation: %+v", first)
	}
	again, err := h.engine.NewThread(context.Background(), opts)
	if err != nil || again.ID != first.ID {
		t.Fatalf("retry: %+v %v", again, err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "--executor") != 1 || !strings.Contains(string(data), "-x work") || strings.Contains(string(data), "-ox") {
		t.Fatalf("calls: %s", data)
	}
	opts.Prompt = "different"
	if _, err = h.engine.NewThread(context.Background(), opts); err == nil {
		t.Fatal("conflicting task accepted")
	}
}

func TestUnknownSubmissionNeverRetriesAutomatically(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	binary := filepath.Join(t.TempDir(), "amp")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\necho transport-failed >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	r := baseRunner()
	r.Env = map[string]string{"AMP_API_KEY": "fixture"}
	h := newHarness(t, r, config.Amp{Binary: "amp", ClientBinary: binary})
	opts := runner.ThreadOptions{Runner: "alpha", ID: "uncertain", Prompt: "work"}
	a, err := h.engine.NewThread(context.Background(), opts)
	if err == nil || a.State != "unknown" {
		t.Fatalf("submission: %+v %v", a, err)
	}
	_, err = h.engine.NewThread(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "recovery") {
		t.Fatalf("unsafe retry: %v", err)
	}
}

func TestThreadSnapshotRejectsStaleIdle(t *testing.T) {
	for _, data := range []string{
		`{"meta":{"lastKnownAgentState":{"state":"idle","messageID":"old"}},"messages":[{"role":"user","protocolMessageID":"new"}]}`,
		`{"meta":{"lastKnownAgentState":{"state":"idle","messageID":null}},"messages":[{"role":"assistant","protocolMessageID":null,"state":{"type":"complete","stopReason":"end_turn"}}]}`,
	} {
		s, err := runner.ParseThread([]byte(data))
		if err != nil || s.State != "unknown" {
			t.Fatalf("snapshot: %+v %v", s, err)
		}
	}
	good := `{"meta":{"lastKnownAgentState":{"state":"idle","messageID":"final"}},"messages":[{"role":"assistant","protocolMessageID":"final","state":{"type":"complete","stopReason":"end_turn"},"content":"done"}]}`
	s, err := runner.ParseThread([]byte(good))
	if err != nil || s.State != "idle" {
		t.Fatalf("final: %+v %v", s, err)
	}
}
