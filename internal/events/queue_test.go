package events

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/roshbhatia/ere/internal/config"

	"github.com/roshbhatia/ere/internal/runner"
)

func queue(t *testing.T) *Queue {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	q, err := Open(filepath.Join(t.TempDir(), "events.json"))
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestConcurrentDuplicateDeliveryAndConflicts(t *testing.T) {
	q := queue(t)
	job := Job{ID: "delivery", Task: runner.ThreadOptions{Runner: "dev", Prompt: "inspect"}}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := q.Enqueue(job); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	jobs, err := q.List()
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs: %v %v", jobs, err)
	}
	job.Task.Prompt = "different"
	if _, err = q.Enqueue(job); err == nil {
		t.Fatal("conflicting delivery accepted")
	}
}

func TestClaimAndRecurringCompletionAreIdempotent(t *testing.T) {
	q := queue(t)
	job, err := q.Enqueue(Job{ID: "timer", Task: runner.ThreadOptions{Runner: "dev", Prompt: "inspect"}, Every: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	claimed, ok, err := q.claim(time.Now())
	if err != nil || !ok || claimed.ID != job.ID {
		t.Fatalf("claim: %+v %v", claimed, err)
	}
	if _, ok, err = q.claim(time.Now()); err != nil || ok {
		t.Fatalf("duplicate claim: %v %v", ok, err)
	}
	if err = q.complete(claimed); err != nil {
		t.Fatal(err)
	}
	if err = q.complete(claimed); err != nil {
		t.Fatal(err)
	}
	jobs, err := q.List()
	if err != nil || len(jobs) != 2 || jobs[0].State != "completed" || jobs[1].State != "queued" || len(jobs[1].ID) > 200 {
		t.Fatalf("recurrence: %+v %v", jobs, err)
	}
	if err = q.Cancel(jobs[1].ID); err != nil {
		t.Fatal(err)
	}
}

func TestWebhookAuthenticatesAndDeduplicates(t *testing.T) {
	q := queue(t)
	key := []byte("test-key")
	handler := Handler(q, key, "fixed-profile", "Review this event", Route{RunnerID: "fixed", Provider: "lima"})
	body := `{"action":"opened","runner":"untrusted"}`
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(body))
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	send := func(signature string) int {
		req := httptest.NewRequest("POST", "/", strings.NewReader(body))
		req.Header.Set("X-Hub-Signature-256", signature)
		req.Header.Set("X-GitHub-Delivery", "delivery-1")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w.Code
	}
	if code := send("sha256=bad"); code != 401 {
		t.Fatalf("unsigned: %d", code)
	}
	for range 2 {
		if code := send(sig); code != 202 {
			t.Fatalf("signed: %d", code)
		}
	}
	jobs, err := q.List()
	if err != nil || len(jobs) != 1 || jobs[0].Task.Runner != "fixed-profile" {
		t.Fatalf("jobs: %+v %v", jobs, err)
	}
}

func TestCrashBeforeAllocationRequeuesWithoutSubmitting(t *testing.T) {
	q := queue(t)
	route := Route{RunnerID: "runner-dev", Provider: "lima"}
	job, err := q.Enqueue(Job{ID: "crashed", Route: route, Task: runner.ThreadOptions{Runner: "dev", Prompt: "work"}, Due: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := q.claim(time.Now().Add(2 * time.Hour)); err != nil || !ok {
		t.Fatal(err)
	}
	e := &runner.Engine{Config: config.Config{Runners: []config.Runner{{Name: "dev", RunnerID: route.RunnerID, Backend: route.Provider}}}}
	if err = q.Work(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	jobs, err := q.List()
	if err != nil || jobs[0].ID != job.ID || jobs[0].State != "queued" {
		t.Fatalf("recovery: %+v %v", jobs, err)
	}
}

func TestQueueRejectsAnotherFleet(t *testing.T) {
	q := queue(t)
	_, err := q.Enqueue(Job{ID: "wrong-fleet", Route: Route{RunnerID: "original", Provider: "lima"}, Task: runner.ThreadOptions{Runner: "dev", Prompt: "work"}})
	if err != nil {
		t.Fatal(err)
	}
	e := &runner.Engine{Config: config.Config{Runners: []config.Runner{{Name: "dev", RunnerID: "different", Backend: "lima"}}}}
	if err = q.Work(context.Background(), e); err == nil || !strings.Contains(err.Error(), "another runner") {
		t.Fatalf("routing: %v", err)
	}
	jobs, _ := q.List()
	if jobs[0].State != "queued" {
		t.Fatal("wrong fleet claimed event")
	}
}
