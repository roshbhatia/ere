package docker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roshbhatia/lifier/internal/sandbox"
)

func TestStatusDistinguishesMissingContainerFromDaemonFailure(t *testing.T) {
	for _, tc := range []struct {
		message string
		absent  bool
	}{
		{"error: no such object: lifier-test", true},
		{"Error: No such container: lifier-test", true},
		{"Cannot connect to the Docker daemon", false},
	} {
		t.Run(tc.message, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "docker")
			if err := os.WriteFile(binary, []byte("#!/bin/sh\nprintf '%s\\n' '"+tc.message+"' >&2\nexit 1\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			status, err := New(binary).Status(context.Background(), sandbox.Ref{Name: "test"})
			if tc.absent {
				if err != nil || status.State != sandbox.StateAbsent {
					t.Fatalf("status=%+v err=%v", status, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("lost daemon error: %v", err)
			}
		})
	}
}

func TestExecRejectsForeignContainer(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "docker")
	script := "#!/bin/sh\nif [ \"$1\" != inspect ]; then exit 88; fi\nprintf '%s' '[{\"Id\":\"foreign\",\"Config\":{\"Labels\":{}},\"State\":{\"Status\":\"running\"}}]'\n"
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := New(binary).Exec(context.Background(), sandbox.ExecRequest{Name: "test", Argv: []string{"true"}})
	if err == nil || !strings.Contains(err.Error(), "foreign") {
		t.Fatalf("foreign container executed: %v", err)
	}
}
