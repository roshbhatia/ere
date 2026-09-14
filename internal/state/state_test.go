package state

import (
	"context"
	"testing"
	"time"
)

func TestAllocationRetainsUnknownWorkAndRejectsTerminalReuse(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store, err := Open(context.Background(), "runner")
	if err != nil {
		t.Fatal(err)
	}
	a, err := store.Acquire("one", "runner", "docker", "request-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Acquire("one", "runner", "docker", "request-2"); err == nil {
		t.Fatal("concurrent allocation admitted")
	}
	if _, err := store.Update(a.ID, "T-test", "unknown"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Update(a.ID, "", "released"); err == nil {
		t.Fatal("unknown work released")
	}
	if err := store.CanStop(); err == nil {
		t.Fatal("busy runner can stop")
	}
	if _, err := store.Update(a.ID, "T-other", "idle"); err == nil {
		t.Fatal("thread binding overwritten")
	}
	if _, err := store.Update(a.ID, "T-test", "idle"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Update(a.ID, "", "released"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Acquire("one", "runner", "docker", a.ID); err == nil {
		t.Fatal("terminal request reused")
	}
	store.Close()
	reopened, err := Open(context.Background(), "runner")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.Record.Allocations[0].ThreadID != "T-test" {
		t.Fatal("thread association was not durable")
	}
}

func TestLockRespectsCancellation(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	store, err := Open(context.Background(), "runner")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := Open(ctx, "runner"); err == nil {
		t.Fatal("second writer acquired held lock")
	}
}
