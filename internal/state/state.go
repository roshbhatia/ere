package state

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/roshbhatia/lifier/internal/sandbox"
)

type Allocation struct {
	ID        string    `json:"id"`
	Runner    string    `json:"runner"`
	RunnerID  string    `json:"runnerId"`
	Provider  string    `json:"provider"`
	ThreadID  string    `json:"threadId,omitempty"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}
type Record struct {
	RunnerID    string       `json:"runnerId,omitempty"`
	Draining    bool         `json:"draining"`
	Allocations []Allocation `json:"allocations"`
}
type Store struct {
	file   *os.File
	path   string
	Record Record
}

func Open(ctx context.Context, key string) (*Store, error) {
	root := os.Getenv("XDG_STATE_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		root = filepath.Join(home, ".local", "state")
	}
	dir := filepath.Join(root, "lifier", "allocations")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, sandbox.Digest(key))
	file, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK {
			file.Close()
			return nil, err
		}
		select {
		case <-ctx.Done():
			file.Close()
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	store := &Store{file: file, path: path + ".json", Record: Record{Allocations: []Allocation{}}}
	data, err := os.ReadFile(store.path)
	if err == nil {
		err = json.Unmarshal(data, &store.Record)
	} else if os.IsNotExist(err) {
		err = nil
	}
	if err != nil {
		store.Close()
		return nil, err
	}
	return store, nil
}
func (s *Store) Close() error { return s.file.Close() }
func (s *Store) Save() error {
	data, err := json.Marshal(s.Record)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(s.path), ".state-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), s.path)
}

func (s *Store) Acquire(runner, runnerID, provider, key string) (Allocation, error) {
	if s.Record.Draining {
		return Allocation{}, fmt.Errorf("runner is draining")
	}
	for _, a := range s.Record.Allocations {
		if a.ID == key && key != "" {
			if a.State == "released" {
				return Allocation{}, fmt.Errorf("released allocation ID cannot be reused")
			}
			if a.RunnerID != runnerID || a.Provider != provider {
				return Allocation{}, fmt.Errorf("allocation identity differs")
			}
			return a, nil
		}
		if a.State != "released" {
			return Allocation{}, fmt.Errorf("runner has allocation %s in state %s", a.ID, a.State)
		}
	}
	if key == "" {
		bytes := make([]byte, 16)
		if _, err := rand.Read(bytes); err != nil {
			return Allocation{}, err
		}
		key = hex.EncodeToString(bytes)
	}
	now := time.Now().UTC()
	a := Allocation{ID: key, Runner: runner, RunnerID: runnerID, Provider: provider, State: "allocated", CreatedAt: now, UpdatedAt: now}
	s.Record.Allocations = append(s.Record.Allocations, a)
	return a, s.Save()
}

func (s *Store) Update(id, thread, activity string) (Allocation, error) {
	for i := range s.Record.Allocations {
		a := s.Record.Allocations[i]
		if a.ID != id {
			continue
		}
		if a.State == "released" {
			return a, fmt.Errorf("allocation is released")
		}
		if thread != "" {
			if a.ThreadID != "" && a.ThreadID != thread {
				return a, fmt.Errorf("allocation already bound to another thread")
			}
			a.ThreadID = thread
		}
		switch activity {
		case "allocated", "running", "idle", "awaiting-approval", "error", "unknown":
			a.State = activity
		case "released":
			if a.State != "idle" && (a.State != "allocated" || a.ThreadID != "") {
				return a, fmt.Errorf("allocation must be idle before release")
			}
			a.State = activity
		default:
			return a, fmt.Errorf("invalid activity %q", activity)
		}
		a.UpdatedAt = time.Now().UTC()
		s.Record.Allocations[i] = a
		return a, s.Save()
	}
	return Allocation{}, fmt.Errorf("unknown allocation %s", id)
}

func (s *Store) CanStop() error {
	for _, a := range s.Record.Allocations {
		if a.State != "released" {
			return fmt.Errorf("runner has unreleased allocation %s (%s)", a.ID, a.State)
		}
	}
	return nil
}
