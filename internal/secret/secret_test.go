package secret

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLiteralValuePassesThrough(t *testing.T) {
	value, err := New("op").Resolve(context.Background(), "plain-value")
	if err != nil {
		t.Fatal(err)
	}
	if value != "plain-value" {
		t.Fatalf("value = %q", value)
	}
}

func TestEnvReferenceReadsTheEnvironment(t *testing.T) {
	t.Setenv("ERE_TEST_SECRET", "from-env")
	value, err := New("op").Resolve(context.Background(), "env://ERE_TEST_SECRET")
	if err != nil {
		t.Fatal(err)
	}
	if value != "from-env" {
		t.Fatalf("value = %q", value)
	}
}

func TestMissingEnvReferenceFails(t *testing.T) {
	if _, err := New("op").Resolve(context.Background(), "env://ERE_TEST_ABSENT"); err == nil {
		t.Fatal("expected an unset variable to fail rather than resolve empty")
	}
}

func TestFileReferenceStripsTheTrailingNewline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(path, []byte("value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := New("op").Resolve(context.Background(), "file://"+path)
	if err != nil {
		t.Fatal(err)
	}
	if value != "value" {
		t.Fatalf("value = %q", value)
	}
}

func TestResolveAllKeepsKeysAndNamesAFailingOne(t *testing.T) {
	t.Setenv("ERE_TEST_SECRET", "ok")
	resolver := New("op")
	resolved, err := resolver.ResolveAll(context.Background(), map[string]string{
		"A": "env://ERE_TEST_SECRET",
		"B": "literal",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved["A"] != "ok" || resolved["B"] != "literal" {
		t.Fatalf("resolved = %v", resolved)
	}
	_, err = resolver.ResolveAll(context.Background(), map[string]string{"C": "env://ERE_TEST_ABSENT"})
	if err == nil {
		t.Fatal("expected a failure")
	}
	if !strings.Contains(err.Error(), "secret C") {
		t.Fatalf("error does not name the failing key: %v", err)
	}
}
