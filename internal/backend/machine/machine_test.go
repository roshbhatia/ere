package machine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roshbhatia/ere/internal/backend"
	"github.com/roshbhatia/ere/internal/sandbox"
)

func TestForeignMachinesCannotBeMutated(t *testing.T) {
	for _, kind := range []string{"incus", "multipass", "tart"} {
		t.Run(kind, func(t *testing.T) {
			t.Setenv("XDG_STATE_HOME", t.TempDir())
			t.Setenv("TART_HOME", t.TempDir())
			dir := t.TempDir()
			log := filepath.Join(dir, "calls")
			binary := filepath.Join(dir, kind)
			script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + backend.Quote(log) + "\ncase \"$1\" in\n list) echo '[{\"name\":\"ere-dev\",\"state\":\"stopped\"}]';;\n config) echo foreign;;\n *) exit 99;;\nesac\n"
			if kind == "multipass" {
				script = "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + backend.Quote(log) + "\ncase \"$*\" in\n *--snapshots*) echo '{\"info\":{\"ere-dev\":{\"foreign\":{}}}}';;\n list*) echo '{\"list\":[{\"name\":\"ere-dev\",\"state\":\"stopped\"}]}';;\n *) exit 99;;\nesac\n"
			}
			if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
				t.Fatal(err)
			}
			b := &Backend{Kind: kind, Binary: binary}
			id, err := b.id("dev")
			if err != nil {
				t.Fatal(err)
			}
			if err = b.save(sandbox.Spec{Name: "dev"}, id); err != nil {
				t.Fatal(err)
			}
			for _, operation := range []func(context.Context, sandbox.Ref) (sandbox.Status, error){b.Start, b.Stop, b.Destroy} {
				if _, err = operation(context.Background(), sandbox.Ref{Name: "dev"}); err == nil {
					t.Fatal("foreign resource accepted")
				}
			}
			data, _ := os.ReadFile(log)
			for _, verb := range []string{"start ", "stop ", "delete ", "exec "} {
				if strings.Contains(string(data), verb) {
					t.Fatalf("mutation: %s", data)
				}
			}
		})
	}
}

func TestMultipassStoppedOwnershipUsesSnapshot(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	b := &Backend{Kind: "multipass"}
	id, err := b.id("dev")
	if err != nil {
		t.Fatal(err)
	}
	b.Binary = filepath.Join(t.TempDir(), "multipass")
	script := "#!/bin/sh\ncase \"$*\" in\n *--snapshots*) echo '{\"info\":{\"ere-dev\":{\"ere-owner-" + id.Owner + "\":{}}}}';;\n *) echo '{\"list\":[{\"name\":\"ere-dev\",\"state\":\"stopped\"}]}';;\nesac\n"
	if err = os.WriteFile(b.Binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = b.owned(context.Background(), "dev", id); err != nil {
		t.Fatal(err)
	}
}

func TestWorkloadPathsDoNotRewriteUserProvisioning(t *testing.T) {
	b := &Backend{Kind: "ssh"}
	script := b.workload(sandbox.Workload{Argv: []string{"amp"}, Provision: []string{"echo /var/lib/ere/source"}}, backend.Identity{Owner: "test"})
	if !strings.Contains(script, "/var/lib/ere/source") || strings.Contains(script, "/var/lib/ere-test/source") {
		t.Fatal("rewrote user provisioning")
	}
}

func TestSSHConnectionQuotesHostAndCommands(t *testing.T) {
	b := &Backend{Kind: "ssh", Host: "host", SSHUser: "user"}
	args := b.guestCommand("dev", []string{"echo", "a; false"}, true)
	if args[len(args)-2] != "user@host" || args[len(args)-1] != "'echo' 'a; false'" {
		t.Fatalf("args: %q", args)
	}
}
