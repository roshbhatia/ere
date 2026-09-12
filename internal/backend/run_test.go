package backend

import (
	"strings"
	"testing"
)

func TestEnvFileQuotesAndSorts(t *testing.T) {
	rendered := EnvFile(map[string]string{
		"B_KEY": "plain",
		"A_KEY": "has 'quote' and $dollar",
	})
	want := "A_KEY='has '\\''quote'\\'' and $dollar'\nB_KEY='plain'\n"
	if rendered != want {
		t.Fatalf("EnvFile()\n got: %q\nwant: %q", rendered, want)
	}
}

func TestWrapCommandKeepsArgvSeparate(t *testing.T) {
	argv := WrapCommand([]string{"amp", "--no-tui", "--runner-id", "a b"}, "")
	if argv[0] != "sh" || argv[1] != "-c" {
		t.Fatalf("expected a shell wrapper, got %v", argv)
	}
	if argv[3] != "lifier" {
		t.Fatalf("expected $0 to be lifier, got %q", argv[3])
	}
	// A runner id with a space must survive as one argument, not be re-split by
	// the shell that sources the environment.
	if argv[len(argv)-1] != "a b" {
		t.Fatalf("argument was re-split: %v", argv)
	}
	if !strings.Contains(argv[2], EnvPath) {
		t.Fatalf("wrapper does not source the environment file: %q", argv[2])
	}
}

func TestWrapDetachedRedirectsToLog(t *testing.T) {
	argv := WrapDetached([]string{"amp"}, "/tmp/lifier.log")
	if !strings.Contains(argv[2], ">> /tmp/lifier.log") {
		t.Fatalf("detached command does not log: %q", argv[2])
	}
	if !strings.Contains(argv[2], "nohup") {
		t.Fatalf("detached command would die with its session: %q", argv[2])
	}
}
