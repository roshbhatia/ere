package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"

	"github.com/roshbhatia/ere/internal/sandbox"
	"github.com/roshbhatia/ere/internal/state"
)

type ThreadOptions struct {
	Runner   string   `json:"runner"`
	ID       string   `json:"id,omitempty"`
	Prompt   string   `json:"prompt,omitempty"`
	Mode     string   `json:"mode,omitempty"`
	Title    string   `json:"title,omitempty"`
	Labels   []string `json:"labels,omitempty"`
	Features []string `json:"features,omitempty"`
}

var (
	threadIDPattern  = regexp.MustCompile(`^T-[0-9a-fA-F-]+$`)
	threadURLPattern = regexp.MustCompile(`(?m)^https?://[^\s]+/threads/(T-[0-9a-fA-F-]+)\s*$`)
)

func (e *Engine) AmpCommand(ctx context.Context, args ...string) *exec.Cmd {
	binary := e.Config.Amp.ClientBinary
	if binary == "" {
		binary = "amp"
	}
	args = append(append([]string(nil), e.Config.Amp.ClientArgs...), args...)
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = os.Environ()
	if e.Config.Amp.URL != "" {
		cmd.Env = append(cmd.Env, "AMP_URL="+e.Config.Amp.URL)
	}
	return cmd
}

func (e *Engine) ampOutput(ctx context.Context, args ...string) ([]byte, error) {
	cmd := e.AmpCommand(ctx, args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("amp %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

func (e *Engine) ThreadURL(id string) string {
	base := e.Config.Amp.URL
	if base == "" {
		base = "https://ampcode.com"
	}
	return strings.TrimRight(base, "/") + "/threads/" + url.PathEscape(id)
}

func (e *Engine) Threads(ctx context.Context) ([]state.Allocation, error) {
	var result []state.Allocation
	for _, r := range e.Config.Runners {
		s, err := e.lock(ctx, r)
		if err != nil {
			return nil, err
		}
		result = append(result, s.Record.Allocations...)
		s.Close()
	}
	sort.Slice(result, func(i, j int) bool { return result[i].UpdatedAt.After(result[j].UpdatedAt) })
	return result, nil
}

func (e *Engine) NewThread(ctx context.Context, opts ThreadOptions) (state.Allocation, error) {
	r, ok := e.Config.Runner(opts.Runner)
	if !ok {
		return state.Allocation{}, fmt.Errorf("unknown runner %q", opts.Runner)
	}
	raw, err := e.Allocation(ctx, r.Name, "acquire", opts.ID, "", "")
	if err != nil {
		return state.Allocation{}, err
	}
	a, ok := raw.(state.Allocation)
	if !ok {
		return a, fmt.Errorf("invalid allocation result")
	}
	intentOptions := opts
	intentOptions.ID = ""
	intent := sandbox.Digest(intentOptions)
	if a.Intent != "" && a.Intent != intent {
		return a, fmt.Errorf("allocation ID already has different task content")
	}
	if a.Submitted {
		return a, nil
	}
	if a.ThreadID != "" {
		return a, fmt.Errorf("thread %s exists but submission completion is unknown; inspect it before continuing", a.ThreadID)
	}
	if a.State != "allocated" {
		return a, fmt.Errorf("allocation %s needs recovery; search Amp label ere-allocation-%s before retrying", a.ID, a.ID)
	}
	if err = e.Up(ctx, []string{r.Name}, false); err != nil {
		return a, err
	}
	// Claim submission under the allocation lock before any remote side effect.
	s, err := e.lock(ctx, r)
	if err != nil {
		return a, err
	}
	for _, current := range s.Record.Allocations {
		if current.ID == a.ID && current.State != "allocated" {
			s.Close()
			return current, fmt.Errorf("allocation %s was already submitted", a.ID)
		}
	}
	for i := range s.Record.Allocations {
		if s.Record.Allocations[i].ID == a.ID {
			s.Record.Allocations[i].Intent = intent
		}
	}
	_, err = s.Update(a.ID, "", "unknown")
	s.Close()
	if err != nil {
		return a, err
	}
	a.State = "unknown"
	prompt := opts.Prompt
	if prompt == "" {
		prompt = "Do not use tools or change files. Reply only: Runner ready."
	}
	args := []string{"--executor", "runner:" + r.RunnerID, "--visibility", "private", "--label", "ere", "--label", "ere-" + r.Backend, "--label", "ere-allocation-" + a.ID, "--plugin-ready-timeout", "30", "--no-archive-after-execute"}
	if opts.Mode != "" {
		args = append(args, "--mode", opts.Mode)
	}
	if opts.Title != "" {
		args = append(args, "--title", opts.Title)
	}
	for _, v := range opts.Labels {
		args = append(args, "--label", v)
	}
	for _, v := range opts.Features {
		args = append(args, "--features", v)
	}
	args = append(args, "-x", prompt)
	out, err := e.ampOutput(ctx, args...)
	if err != nil {
		return a, fmt.Errorf("allocation %s retained for recovery: %w", a.ID, err)
	}
	match := threadURLPattern.FindSubmatch(out)
	if len(match) != 2 {
		return a, fmt.Errorf("allocation %s retained: Amp returned no thread URL", a.ID)
	}
	a.ThreadID = string(match[1])
	if _, err = e.Allocation(ctx, r.Name, "update", a.ID, a.ThreadID, "unknown"); err != nil {
		return a, err
	}
	s, err = e.lock(ctx, r)
	if err != nil {
		return a, err
	}
	for i := range s.Record.Allocations {
		if s.Record.Allocations[i].ID == a.ID {
			s.Record.Allocations[i].Submitted = true
			a = s.Record.Allocations[i]
		}
	}
	err = s.Save()
	s.Close()
	return a, err
}

func (e *Engine) ContinueThread(ctx context.Context, id string) (state.Allocation, error) {
	if !threadIDPattern.MatchString(id) {
		return state.Allocation{}, fmt.Errorf("invalid thread ID %q", id)
	}
	all, err := e.Threads(ctx)
	if err != nil {
		return state.Allocation{}, err
	}
	for _, a := range all {
		if a.ThreadID != id {
			continue
		}
		if a.State == "released" {
			raw, err := e.Allocation(ctx, a.Runner, "acquire", "", "", "")
			if err != nil {
				return a, err
			}
			var ok bool
			a, ok = raw.(state.Allocation)
			if !ok {
				return a, fmt.Errorf("invalid allocation result")
			}
		}
		if _, err = e.Allocation(ctx, a.Runner, "update", a.ID, id, "unknown"); err != nil {
			return a, err
		}
		a.ThreadID = id
		if err = e.Up(ctx, []string{a.Runner}, false); err != nil {
			return a, err
		}
		return a, nil
	}
	return state.Allocation{}, fmt.Errorf("thread %s is not tracked by configured Ere runners", id)
}

type ThreadSnapshot struct {
	State    string          `json:"state"`
	Response json.RawMessage `json:"response,omitempty"`
}

func ParseThread(data []byte) (ThreadSnapshot, error) {
	var thread struct {
		Meta struct {
			Activity struct {
				State     string          `json:"state"`
				MessageID json.RawMessage `json:"messageID"`
			} `json:"lastKnownAgentState"`
		} `json:"meta"`
		Messages []struct {
			Role    string          `json:"role"`
			ID      json.RawMessage `json:"protocolMessageID"`
			Content json.RawMessage `json:"content"`
			State   struct {
				Type       string `json:"type"`
				StopReason string `json:"stopReason"`
			} `json:"state"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(data, &thread); err != nil {
		return ThreadSnapshot{}, err
	}
	a := thread.Meta.Activity
	switch a.State {
	case "running", "awaiting-approval", "error":
		return ThreadSnapshot{State: a.State}, nil
	}
	if len(thread.Messages) > 0 {
		m := thread.Messages[len(thread.Messages)-1]
		if a.State == "idle" && m.Role == "assistant" && m.State.Type == "complete" && m.State.StopReason == "end_turn" && len(a.MessageID) > 0 && string(a.MessageID) != "null" && string(a.MessageID) == string(m.ID) {
			return ThreadSnapshot{State: "idle", Response: m.Content}, nil
		}
	}
	return ThreadSnapshot{State: "unknown"}, nil
}

func (e *Engine) PollThread(ctx context.Context, a state.Allocation, release bool) (ThreadSnapshot, error) {
	if !threadIDPattern.MatchString(a.ThreadID) {
		return ThreadSnapshot{}, fmt.Errorf("allocation %s has no valid thread; recover its ID before release", a.ID)
	}
	out, err := e.ampOutput(ctx, "threads", "export", a.ThreadID)
	if err != nil {
		return ThreadSnapshot{}, err
	}
	var identity struct {
		ID  string `json:"id"`
		Env struct {
			Initial struct {
				RunnerID string `json:"runnerID"`
			} `json:"initial"`
		} `json:"env"`
	}
	if err = json.Unmarshal(out, &identity); err != nil {
		return ThreadSnapshot{}, err
	}
	if identity.ID != a.ThreadID || !strings.EqualFold(identity.Env.Initial.RunnerID, a.RunnerID) {
		return ThreadSnapshot{}, fmt.Errorf("thread identity does not match the allocated runner")
	}
	snapshot, err := ParseThread(out)
	if err != nil {
		return snapshot, err
	}
	if _, err = e.Allocation(ctx, a.Runner, "update", a.ID, a.ThreadID, snapshot.State); err != nil {
		return snapshot, err
	}
	if release {
		if snapshot.State != "idle" {
			return snapshot, fmt.Errorf("thread is %s; allocation retained", snapshot.State)
		}
		_, err = e.Allocation(ctx, a.Runner, "update", a.ID, a.ThreadID, "released")
	}
	return snapshot, err
}
