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
