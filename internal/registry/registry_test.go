package registry

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/roshbhatia/lifier/internal/sandbox"
)

func TestBuiltinsDeclareTheSandboxAction(t *testing.T) {
	for _, manifest := range Builtins("/usr/local/bin/lifier") {
		if err := manifest.Validate(); err != nil {
			t.Fatalf("built-in %s is not a valid manifest: %v", manifest.Name, err)
		}
		if _, ok := manifest.Actions[sandbox.Capability]; !ok {
			t.Fatalf("built-in %s declares no %q action", manifest.Name, sandbox.Capability)
		}
		if manifest.Kind != sandbox.Kind {
			t.Fatalf("built-in %s has kind %q", manifest.Name, manifest.Kind)
		}
		want := []string{"/usr/local/bin/lifier", "backend", manifest.Name}
		if !slices.Equal(manifest.Command, want) {
			t.Fatalf("built-in %s command = %v, want %v", manifest.Name, manifest.Command, want)
		}
	}
}

func TestLoadWithoutAProviderDirectoryReturnsTheBuiltins(t *testing.T) {
	reg, err := Load(filepath.Join(t.TempDir(), "absent"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(reg.Names(), []string{Docker, KubeVirt, Pod, Lima, QEMU, VZ}) {
		t.Fatalf("names = %v", reg.Names())
	}
}

func TestDiscoveredManifestReplacesABuiltin(t *testing.T) {
	dir := t.TempDir()
	manifest := `version: provider/v1
kind: sandbox
name: docker
description: podman on this host
command: [podman-sandbox]
actions:
  sandbox:
    description: podman
`
	if err := os.WriteFile(filepath.Join(dir, "docker.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	reg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	entries := reg.Entries()
	index := slices.IndexFunc(entries, func(e Entry) bool { return e.Manifest.Name == Docker })
	if index < 0 {
		t.Fatal("docker is missing from the registry")
	}
	if entries[index].Source == "built-in" {
		t.Fatal("the discovered manifest did not replace the built-in")
	}
	if !slices.Equal(entries[index].Manifest.Command, []string{"podman-sandbox"}) {
		t.Fatalf("command = %v", entries[index].Manifest.Command)
	}
	if len(reg.Names()) != 6 {
		t.Fatalf("an override must not add a backend: %v", reg.Names())
	}
}

func TestManifestWithoutTheSandboxActionIsRejected(t *testing.T) {
	dir := t.TempDir()
	manifest := `version: provider/v1
kind: sandbox
name: broken
description: declares the wrong action
command: [true]
actions:
  chat:
    description: not a sandbox
`
	if err := os.WriteFile(filepath.Join(dir, "broken.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("expected a manifest without the sandbox action to fail")
	}
}

func TestUnknownBackendListsWhatExists(t *testing.T) {
	reg, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	_, err = reg.Client("firecracker")
	if err == nil {
		t.Fatal("expected an unknown backend to fail")
	}
}
