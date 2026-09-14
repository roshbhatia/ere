package events

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/roshbhatia/ere/internal/runner"
	"github.com/roshbhatia/ere/internal/sandbox"
	"github.com/roshbhatia/ere/internal/state"
	"github.com/roshbhatia/go-utils/paths"
	"github.com/roshbhatia/go-utils/store"
)

type (
	Route struct {
		RunnerID string `json:"runnerId"`
		Provider string `json:"provider"`
	}
	Job struct {
		Route      Route                `json:"route"`
		ID         string               `json:"id"`
		Task       runner.ThreadOptions `json:"task"`
		Due        time.Time            `json:"due"`
		Every      time.Duration        `json:"every,omitempty"`
		State      string               `json:"state"`
		Allocation state.Allocation     `json:"allocation"`
		Error      string               `json:"error,omitempty"`
		Digest     string               `json:"digest"`
	}
)
type Queue struct{ store store.Store }

func Open(path string) (*Queue, error) {
	if path == "" {
		path = filepath.Join(paths.StateHome(), "ere", "events.json")
	}
	var err error
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return &Queue{store: store.Store{Path: path, Validate: store.JSONValidator[[]Job](nil), Initial: func() ([]byte, error) { return []byte("[]"), nil }}}, nil
}

func (q *Queue) change(fn func(*[]Job) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	lease, err := state.Open(ctx, "events-write|"+q.store.Path)
	if err != nil {
		return err
	}
	defer lease.Close()
	data, err := q.store.Read()
	if err != nil {
		return err
	}
	var jobs []Job
	if err = json.Unmarshal(data, &jobs); err != nil {
		return err
	}
	if err = fn(&jobs); err != nil {
		return err
	}
	data, err = json.Marshal(jobs)
	if err != nil {
		return err
	}
	return q.store.Publish(data)
}

func (q *Queue) List() ([]Job, error) {
	data, err := q.store.Read()
	if err != nil {
		return nil, err
	}
	var jobs []Job
	err = json.Unmarshal(data, &jobs)
	return jobs, err
}

func (q *Queue) Enqueue(job Job) (Job, error) {
	if job.ID == "" || len(job.ID) > 200 || job.Task.Runner == "" || job.Task.Prompt == "" {
		return job, fmt.Errorf("event needs an ID, runner, and prompt")
	}
	if job.Every < 0 {
		return job, fmt.Errorf("repeat interval cannot be negative")
	}
	job.Digest = sandbox.Digest(struct {
		Task  runner.ThreadOptions
		Every time.Duration
		Route Route
	}{job.Task, job.Every, job.Route})
	job.Task.ID = "event-" + sandbox.Digest(job.ID)
	job.State = "queued"
	err := q.change(func(jobs *[]Job) error {
		pending := 0
		for _, existing := range *jobs {
			if existing.ID == job.ID {
				if existing.Digest != job.Digest {
					return fmt.Errorf("event ID already has different content")
				}
				job = existing
				return nil
			}
			if existing.State != "completed" && existing.State != "cancelled" {
				pending++
			}
		}
		if pending >= 100 {
			return fmt.Errorf("queue has 100 unfinished events")
		}
		*jobs = append(*jobs, job)
		return nil
	})
	return job, err
}

func (q *Queue) Update(job Job) error {
	return q.change(func(jobs *[]Job) error {
		for i := range *jobs {
			if (*jobs)[i].ID == job.ID {
				(*jobs)[i] = job
				return nil
			}
		}
		return fmt.Errorf("event is absent")
	})
}

func (q *Queue) Cancel(id string) error {
	return q.change(func(jobs *[]Job) error {
		for i := range *jobs {
			j := &(*jobs)[i]
			if j.ID == id {
				if j.State != "queued" {
					return fmt.Errorf("only queued events can be cancelled; running threads retain their allocation")
				}
				j.State = "cancelled"
				return nil
			}
		}
		return fmt.Errorf("event is absent")
	})
}

func (q *Queue) claim(now time.Time) (Job, bool, error) {
	var job Job
	found := false
	err := q.change(func(jobs *[]Job) error {
		for i := range *jobs {
			j := &(*jobs)[i]
			if j.State == "queued" && !j.Due.After(now) {
				j.State = "submitting"
				job = *j
				found = true
				break
			}
		}
		return nil
	})
	return job, found, err
}

func (q *Queue) Work(ctx context.Context, e *runner.Engine) error {
	// Only one worker may recover or submit events from this queue.
	lease, err := state.Open(ctx, "events-worker|"+q.store.Path)
	if err != nil {
		return err
	}
	defer lease.Close()
	jobs, err := q.List()
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if j.State != "completed" && j.State != "cancelled" {
			r, ok := e.Config.Runner(j.Task.Runner)
			if !ok || r.RunnerID != j.Route.RunnerID || r.Backend != j.Route.Provider {
				return fmt.Errorf("event %s belongs to another runner configuration", j.ID)
			}
		}
		if j.State == "submitting" {
			allocations, err := e.Threads(ctx)
			if err != nil {
				return err
			}
			exists := false
			for _, a := range allocations {
				if a.ID == j.Task.ID && a.Runner == j.Task.Runner {
					exists = true
				}
			}
			if !exists {
				j.State = "queued"
				j.Error = ""
				if err = q.Update(j); err != nil {
					return err
				}
				continue
			}
			j.State = "unknown"
			j.Error = "worker interrupted during submission; inspect allocation and recover thread before retrying"
			if err = q.Update(j); err != nil {
				return err
			}
		}
		if j.State == "running" || j.State == "queued" {
			r, ok := e.Config.Runner(j.Task.Runner)
			if !ok || r.RunnerID != j.Route.RunnerID || r.Backend != j.Route.Provider {
				return fmt.Errorf("event %s belongs to another runner configuration", j.ID)
			}
		}
		if j.State == "running" {
			allocations, err := e.Threads(ctx)
			if err != nil {
				return err
			}
			released := false
			for _, a := range allocations {
				if a.ID == j.Allocation.ID && a.Runner == j.Allocation.Runner && a.ThreadID == j.Allocation.ThreadID && a.State == "released" {
					released = true
				}
			}
			if released {
				if err = q.complete(j); err != nil {
					return err
				}
				continue
			}
			snapshot, err := e.PollThread(ctx, j.Allocation, false)
			if err != nil {
				j.Error = err.Error()
				if err = q.Update(j); err != nil {
					return err
				}
				continue
			}
			if snapshot.State == "idle" {
				if _, err = e.PollThread(ctx, j.Allocation, true); err != nil {
					continue
				}
				j.State = "completed"
				j.Error = ""
				if err = q.complete(j); err != nil {
					return err
				}
			}
		}
	}
	job, found, err := q.claim(time.Now())
	if err != nil || !found {
		return err
	}
	r, ok := e.Config.Runner(job.Task.Runner)
	if !ok || r.RunnerID != job.Route.RunnerID || r.Backend != job.Route.Provider {
		job.State = "queued"
		if err = q.Update(job); err != nil {
			return err
		}
		return fmt.Errorf("event %s belongs to another runner configuration", job.ID)
	}
	a, err := e.NewThread(ctx, job.Task)
	job.Allocation = a
	if err != nil {
		job.State = "unknown"
		job.Error = err.Error()
		if a.ID == "" || a.State == "allocated" && a.ThreadID == "" {
			job.State = "queued"
			job.Due = time.Now().Add(30 * time.Second)
		}
	} else {
		job.State = "running"
		job.Error = ""
	}
	return q.Update(job)
}

func (q *Queue) complete(job Job) error {
	return q.change(func(jobs *[]Job) error {
		for i := range *jobs {
			if (*jobs)[i].ID == job.ID {
				if (*jobs)[i].State == "completed" {
					return nil
				}
				(*jobs)[i].State = "completed"
				(*jobs)[i].Allocation.State = "released"
				(*jobs)[i].Error = ""
				if job.Every > 0 {
					next := job
					next.ID = "repeat-" + sandbox.Digest(job.ID)
					next.Task.ID = "event-" + sandbox.Digest(next.ID)
					next.Due = time.Now().Add(job.Every)
					next.State = "queued"
					next.Error = ""
					next.Allocation = state.Allocation{}
					*jobs = append(*jobs, next)
				}
				return nil
			}
		}
		return fmt.Errorf("event is absent")
	})
}

func (q *Queue) Recover(ctx context.Context, e *runner.Engine, id, threadID, verifiedAllocation string) error {
	jobs, err := q.List()
	if err != nil {
		return err
	}
	for _, j := range jobs {
		if j.ID == id {
			if j.State != "unknown" {
				return fmt.Errorf("only unknown submissions need recovery")
			}
			if threadID == "" {
				return fmt.Errorf("thread ID is required")
			}
			allocations, err := e.Threads(ctx)
			if err != nil {
				return err
			}
			for _, a := range allocations {
				if a.ID == j.Task.ID && a.Runner == j.Task.Runner {
					if verifiedAllocation != a.ID {
						return fmt.Errorf("verify the allocation label on the thread before recovery")
					}
					if a.ThreadID != "" && a.ThreadID != threadID {
						return fmt.Errorf("allocation already belongs to another thread")
					}
					a.ThreadID = threadID
					if _, err = e.PollThread(ctx, a, false); err != nil {
						return err
					}
					j.Allocation = a
					j.State = "running"
					j.Error = ""
					return q.Update(j)
				}
			}
			return fmt.Errorf("no allocation exists; inspect the event before cancelling or retrying")
		}
	}
	return fmt.Errorf("event is absent")
}
