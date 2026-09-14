package config

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ere.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadAppliesDefaults(t *testing.T) {
	path := write(t, `
defaultBackend: vz
defaults:
  cpus: 8
runners:
  - name: alpha
    workspace: ~/src
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	runner := cfg.Runners[0]
	if runner.Backend != "vz" {
		t.Fatalf("backend = %q, want vz", runner.Backend)
	}
	if runner.CPUs != 8 {
		t.Fatalf("cpus = %d, want 8", runner.CPUs)
	}
	if runner.RunnerID != "alpha" {
		t.Fatalf("runnerId = %q, want the runner name", runner.RunnerID)
	}
	if runner.MountPath != "/workspace" {
		t.Fatalf("mountPath = %q, want /workspace", runner.MountPath)
	}
}

func TestLoadRejectsRunnerIDThatIsNotAHostname(t *testing.T) {
	path := write(t, `
runners:
  - name: alpha
    runnerId: "not a hostname"
`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected a validation error for an invalid runner id")
	}
}

func TestLoadRejectsDuplicateRunners(t *testing.T) {
	path := write(t, `
runners:
  - name: alpha
  - name: alpha
`)
	if _, err := Load(path); err == nil {
		t.Fatal("expected a validation error for a duplicate runner")
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := write(t, "runnerz: []\n")
	if _, err := Load(path); err == nil {
		t.Fatal("expected a decode error for an unknown field")
	}
}

func TestSchemaIsGenerated(t *testing.T) {
	schema, err := Schema()
	if err != nil {
		t.Fatal(err)
	}
	if len(schema) == 0 {
		t.Fatal("schema is empty")
	}
}

func TestNamedProviderDefaultsUseBaseKind(t *testing.T) {
	path := write(t, `providers:
  remote-container:
    kind: docker
  host:
    kind: ssh
    host: build-host
runners:
  - name: container
    backend: remote-container
  - name: host
    backend: host
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Runners[0].DiskGB != 0 {
		t.Fatal("docker alias inherited VM disk")
	}
	r := cfg.Runners[1]
	if r.CPUs != 0 || r.MemoryMB != 0 || r.DiskGB != 0 {
		t.Fatal("SSH inherited managed host resources")
	}
}
