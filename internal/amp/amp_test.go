package amp

import (
	"slices"
	"testing"

	"github.com/roshbhatia/ere/internal/config"
)

func TestRunnerArgvServesRemoteThreads(t *testing.T) {
	argv := RunnerArgv(config.Amp{}, config.Runner{RunnerID: "lv426-sysinit"})
	want := []string{"amp", "--no-tui", "--runner-id", "lv426-sysinit"}
	if !slices.Equal(argv, want) {
		t.Fatalf("argv = %v, want %v", argv, want)
	}
}

func TestRunnerArgvAddsTerminalAccessAndExtraArgs(t *testing.T) {
	argv := RunnerArgv(
		config.Amp{Binary: "/opt/amp/bin/amp", Args: []string{"--log-level", "debug"}},
		config.Runner{RunnerID: "alpha", RemoteControlTerminal: true},
	)
	want := []string{
		"/opt/amp/bin/amp", "--no-tui", "--runner-id", "alpha",
		"--remote-control-terminal", "--log-level", "debug",
	}
	if !slices.Equal(argv, want) {
		t.Fatalf("argv = %v, want %v", argv, want)
	}
}

func TestEnvKeepsRunnerValuesAndAddsCredential(t *testing.T) {
	env := Env(config.Amp{URL: "https://example.test"}, "secret", map[string]string{"FOO": "bar"})
	if env["FOO"] != "bar" {
		t.Fatalf("runner value was dropped: %v", env)
	}
	if env[EnvAPIKey] != "secret" {
		t.Fatalf("credential missing: %v", env)
	}
	if env[EnvURL] != "https://example.test" {
		t.Fatalf("url missing: %v", env)
	}
}

func TestEnvOmitsAnAbsentCredential(t *testing.T) {
	env := Env(config.Amp{}, "", nil)
	if _, ok := env[EnvAPIKey]; ok {
		t.Fatal("an empty credential must not be exported")
	}
}
