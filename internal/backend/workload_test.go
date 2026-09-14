package backend

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/roshbhatia/ere/internal/sandbox"
)

func TestExecEnvironmentsAreIsolatedAndLiteral(t *testing.T) {
	var wg sync.WaitGroup
	for _, value := range []string{"plain", "quote' $(exit 9)\nsecond line"} {
		wg.Add(1)
		go func(value string) {
			defer wg.Done()
			script, err := ExecScript(sandbox.ExecRequest{Argv: []string{"sh", "-c", "printf %s \"$ERE_VALUE\""}, Env: map[string]string{"ERE_VALUE": value}})
			if err != nil {
				t.Error(err)
				return
			}
			out, err := RunStdin(context.Background(), script, "sh", "-s")
			if err != nil || out.ExitCode != 0 || out.Stdout != value {
				t.Errorf("isolated environment failed: %+v %v", out, err)
			}
		}(value)
	}
	wg.Wait()
	script, err := ExecScript(sandbox.ExecRequest{Argv: []string{"sh", "-c", "printf %s \"${ERE_VALUE-unset}\""}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := RunStdin(context.Background(), script, "sh", "-s")
	if err != nil || out.Stdout != "unset" {
		t.Fatalf("previous environment leaked: %+v %v", out, err)
	}
}

func TestExecRejectsEnvironmentCode(t *testing.T) {
	_, err := ExecScript(sandbox.ExecRequest{Argv: []string{"true"}, Env: map[string]string{"A;false": "x"}})
	if err == nil {
		t.Fatal("invalid key accepted")
	}
}

func TestWorkloadBootstrapHasDigestAndLiteralArgv(t *testing.T) {
	script := WorkloadScript(sandbox.Workload{Argv: []string{"echo", "a b; false"}, Provision: []string{"exit 3"}})
	if !strings.Contains(script, "bootstrap.digest") || !strings.Contains(script, "'a b; false'") {
		t.Fatal(script)
	}
}
