package runner

import (
	"context"
	"fmt"
	"strings"

	"github.com/roshbhatia/lifier/internal/config"
	"github.com/roshbhatia/lifier/internal/state"
)

type runnerLock struct {
	*state.Store
	profile *state.Store
}

func (l *runnerLock) Close() error {
	err := l.Store.Close()
	profileErr := l.profile.Close()
	if err != nil {
		return err
	}
	return profileErr
}

func (e *Engine) lock(ctx context.Context, r config.Runner) (*runnerLock, error) {
	profile, err := state.Open(ctx, "profile:"+strings.ToLower(r.Name))
	if err != nil {
		return nil, err
	}
	id := strings.ToLower(r.RunnerID)
	if profile.Record.RunnerID != "" && profile.Record.RunnerID != id {
		previous, err := state.Open(ctx, profile.Record.RunnerID)
		if err != nil {
			profile.Close()
			return nil, err
		}
		busy := previous.CanStop()
		previous.Close()
		if busy != nil {
			profile.Close()
			return nil, fmt.Errorf("profile still belongs to runner %s: %w", profile.Record.RunnerID, busy)
		}
	}
	store, err := state.Open(ctx, id)
	if err != nil {
		profile.Close()
		return nil, err
	}
	if profile.Record.RunnerID != id {
		profile.Record.RunnerID = id
		if err := profile.Save(); err != nil {
			store.Close()
			profile.Close()
			return nil, err
		}
	}
	return &runnerLock{Store: store, profile: profile}, nil
}

func (e *Engine) Allocation(ctx context.Context, name, operation, id, thread, activity string) (interface{}, error) {
	r, ok := e.Config.Runner(name)
	if !ok {
		return nil, fmt.Errorf("unknown runner %q", name)
	}
	store, err := e.lock(ctx, r)
	if err != nil {
		return nil, err
	}
	defer store.Close()
	switch operation {
	case "acquire":
		return store.Acquire(r.Name, r.RunnerID, r.Backend, id)
	case "update":
		return store.Update(id, thread, activity)
	case "list":
		return store.Record, nil
	case "drain":
		store.Record.Draining = true
		return store.Record, store.Save()
	case "resume":
		store.Record.Draining = false
		return store.Record, store.Save()
	default:
		return nil, fmt.Errorf("unknown allocation operation %q", operation)
	}
}
