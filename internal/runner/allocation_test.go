package runner

import (
	"context"
	"testing"

	"github.com/roshbhatia/lifier/internal/config"
)

func TestChangingRunnerIDCannotBypassBusyProfile(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	engine := &Engine{}
	old := config.Runner{Name: "worker", RunnerID: "old", Backend: "docker"}
	lock, err := engine.lock(context.Background(), old)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Acquire(old.Name, old.RunnerID, old.Backend, "one"); err != nil {
		t.Fatal(err)
	}
	lock.Close()
	changed := old
	changed.RunnerID = "new"
	if lock, err := engine.lock(context.Background(), changed); err == nil {
		lock.Close()
		t.Fatal("changed runner ID bypassed active allocation")
	}
	lock, err = engine.lock(context.Background(), old)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock.Update("one", "", "released"); err != nil {
		t.Fatal(err)
	}
	lock.Close()
	lock, err = engine.lock(context.Background(), changed)
	if err != nil {
		t.Fatal(err)
	}
	lock.Close()
}
