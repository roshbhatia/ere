package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/roshbhatia/go-utils/provider"
)

// Client calls one backend manifest. Every operation is a fresh process: the
// contract is one request in, one result out.
type Client struct {
	Manifest provider.Manifest
	Workdir  string
	OnEvent  func(provider.Event)

	counter atomic.Uint64
}

// NewClient binds a discovered or built-in manifest to a caller.
func NewClient(manifest provider.Manifest) *Client {
	return &Client{Manifest: manifest}
}

// Name is the backend name callers address.
func (c *Client) Name() string { return c.Manifest.Name }

// A manifest that declares a timeout owns every operation. Without one, a VM
// create gets minutes and a status call does not.
func (c *Client) timeout(operation string) time.Duration {
	if declared := c.Manifest.Defaults.Timeout.Duration(); declared > 0 {
		return declared
	}
	switch operation {
	case OpCreate, OpStart:
		return 20 * time.Minute
	case OpStop, OpDestroy:
		return 5 * time.Minute
	default:
		return 2 * time.Minute
	}
}

func (c *Client) call(ctx context.Context, operation string, input, output any) error {
	var encoded json.RawMessage
	if input != nil {
		data, err := json.Marshal(input)
		if err != nil {
			return fmt.Errorf("encode %s input: %w", operation, err)
		}
		encoded = data
	}
	request := provider.Request{
		RequestID:  c.Manifest.Name + "-" + operation + "-" + strconv.FormatUint(c.counter.Add(1), 10),
		Capability: Capability,
		Operation:  operation,
		Input:      encoded,
	}
	options := provider.InvokeOptions{
		WorkingDirectory: c.Workdir,
		Timeout:          c.timeout(operation),
	}
	if c.OnEvent != nil {
		options.OnEvent = func(event provider.Event) error {
			c.OnEvent(event)
			return nil
		}
	}
	invocation, err := provider.Invoke(ctx, c.Manifest, request, options)
	if err != nil {
		return fmt.Errorf("backend %s %s: %w", c.Manifest.Name, operation, err)
	}
	switch invocation.Result.Status {
	case provider.ResultOK:
	case provider.ResultDeclined:
		return fmt.Errorf("backend %s declined %s: %s", c.Manifest.Name, operation, invocation.Result.Message)
	default:
		return fmt.Errorf("backend %s failed %s: %s", c.Manifest.Name, operation, invocation.Result.Message)
	}
	if output == nil || len(invocation.Result.Output) == 0 {
		return nil
	}
	if err := json.Unmarshal(invocation.Result.Output, output); err != nil {
		return fmt.Errorf("decode %s output: %w", operation, err)
	}
	return nil
}

func (c *Client) Probe(ctx context.Context) (Probe, error) {
	var out Probe
	err := c.call(ctx, OpProbe, nil, &out)
	if out.Backend == "" {
		out.Backend = c.Manifest.Name
	}
	return out, err
}

func (c *Client) Create(ctx context.Context, spec Spec) (Status, error) {
	var out Status
	return out, c.call(ctx, OpCreate, spec, &out)
}

func (c *Client) Start(ctx context.Context, ref Ref) (Status, error) {
	var out Status
	return out, c.call(ctx, OpStart, ref, &out)
}

func (c *Client) Exec(ctx context.Context, req ExecRequest) (ExecResult, error) {
	var out ExecResult
	return out, c.call(ctx, OpExec, req, &out)
}

func (c *Client) Status(ctx context.Context, ref Ref) (Status, error) {
	var out Status
	return out, c.call(ctx, OpStatus, ref, &out)
}

func (c *Client) List(ctx context.Context) (List, error) {
	var out List
	return out, c.call(ctx, OpList, nil, &out)
}

func (c *Client) Logs(ctx context.Context, req LogRequest) (Logs, error) {
	var out Logs
	return out, c.call(ctx, OpLogs, req, &out)
}

func (c *Client) Stop(ctx context.Context, ref Ref) (Status, error) {
	var out Status
	return out, c.call(ctx, OpStop, ref, &out)
}

func (c *Client) Destroy(ctx context.Context, ref Ref) (Status, error) {
	var out Status
	return out, c.call(ctx, OpDestroy, ref, &out)
}
