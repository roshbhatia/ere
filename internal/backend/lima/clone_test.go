package lima

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloneKeepsResolvedImageAndChangesMounts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lima.yaml")
	source := "vmType: vz\nimages:\n  - location: /cache/disk.qcow2\n    arch: aarch64\nmounts:\n  - location: /source\n    mountPoint: /workspace\n"
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := cloneMounts(path, "base: template://ubuntu\nmounts:\n  - location: /target\n    mountPoint: /workspace\nmountType: virtiofs\n")
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, want := range []string{"/cache/disk.qcow2", "aarch64", "/target", "virtiofs"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %s: %s", want, text)
		}
	}
	for _, bad := range []string{"/source", "base:"} {
		if strings.Contains(text, bad) {
			t.Fatalf("retained %s: %s", bad, text)
		}
	}
}
