package lima

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roshbhatia/ere/internal/sandbox"
)

func TestTemplateDoesNotInheritHostHomeMount(t *testing.T) {
	b := New("", "vz", "")
	if b.Base != "template://_images/ubuntu-lts" {
		t.Fatalf("base must contain only an image, got %q", b.Base)
	}
	empty := b.template(sandbox.Spec{})
	if !strings.Contains(empty, "mounts: []") {
		t.Fatalf("empty workspace must disable mounts: %s", empty)
	}
	mounted := b.template(sandbox.Spec{Workspace: "/tmp/project", ReadOnly: true})
	for _, want := range []string{`location: "/tmp/project"`, `mountPoint: "/workspace"`, "writable: false"} {
		if !strings.Contains(mounted, want) {
			t.Errorf("missing %q in %s", want, mounted)
		}
	}
}

func TestListRejectsMalformedJSON(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "limactl")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf 'not-json'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := New(binary, "vz", "").List(context.Background()); err == nil {
		t.Fatal("malformed output must not appear as an empty fleet")
	}
}

func TestListFiltersVMType(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "limactl")
	script := `#!/bin/sh
printf '%s\n' '{"name":"ere-native","vmType":"vz","status":"Running"}' '{"name":"ere-emulated","vmType":"qemu","status":"Stopped"}'
`
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	list, err := New(binary, "vz", "").List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Sandboxes) != 1 || list.Sandboxes[0].Name != "native" {
		t.Fatalf("unexpected VZ fleet: %+v", list)
	}
}
