package sandbox

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/roshbhatia/go-utils/provider"
)

// Emitter lets a backend report progress before the result frame.
type Emitter interface {
	Emit(event, message string) error
}

type emitter struct {
	writer    *json.Encoder
	requestID string
}

func (e *emitter) Emit(event, message string) error {
	if e == nil || e.writer == nil {
		return nil
	}
	return e.writer.Encode(provider.Event{
		Version:   provider.Version,
		Kind:      provider.FrameEvent,
		RequestID: e.requestID,
		Event:     event,
		Message:   message,
	})
}

type ctxKey struct{}

// WithEmitter carries the progress channel to a backend without widening the
// Backend interface with a writer argument on every method.
func WithEmitter(ctx context.Context, e Emitter) context.Context {
	return context.WithValue(ctx, ctxKey{}, e)
}

// Progress reports a step to the caller, or does nothing outside a served
// invocation.
func Progress(ctx context.Context, event, message string) {
	if e, ok := ctx.Value(ctxKey{}).(Emitter); ok && e != nil {
		_ = e.Emit(event, message)
	}
}

// Serve reads one provider/v1 request frame, dispatches it to the backend, and
// writes the events and the single result frame.
func Serve(ctx context.Context, backend Backend, stdin io.Reader, stdout io.Writer) error {
	request, err := readRequest(stdin)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(stdout)
	send := &emitter{writer: encoder, requestID: request.RequestID}
	output, derr := dispatch(WithEmitter(ctx, send), backend, request)
	result := provider.Result{
		Version:   provider.Version,
		Kind:      provider.FrameResult,
		RequestID: request.RequestID,
		Status:    provider.ResultOK,
	}
	if derr != nil {
		result.Status = provider.ResultError
		result.Message = derr.Error()
	} else if output != nil {
		encoded, merr := json.Marshal(output)
		if merr != nil {
			result.Status = provider.ResultError
			result.Message = merr.Error()
		} else {
			result.Output = encoded
		}
	}
	return encoder.Encode(result)
}

func readRequest(stdin io.Reader) (provider.Request, error) {
	var request provider.Request
	scanner := bufio.NewScanner(stdin)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		if len(scanner.Bytes()) == 0 {
			continue
		}
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			return request, fmt.Errorf("decode request: %w", err)
		}
		break
	}
	if err := scanner.Err(); err != nil {
		return request, err
	}
	if request.Kind != provider.FrameRequest {
		return request, errors.New("no request frame on standard input")
	}
	if request.Capability != Capability {
		return request, fmt.Errorf("unsupported capability %q", request.Capability)
	}
	return request, nil
}

func dispatch(ctx context.Context, backend Backend, request provider.Request) (any, error) {
	switch request.Operation {
	case OpProbe:
		return backend.Probe(ctx)
	case OpCreate:
		return decodeThen(ctx, request, backend.Create)
	case OpStart:
		return decodeThen(ctx, request, backend.Start)
	case OpExec:
		return decodeThen(ctx, request, backend.Exec)
	case OpStatus:
		return decodeThen(ctx, request, backend.Status)
	case OpList:
		return backend.List(ctx)
	case OpLogs:
		return decodeThen(ctx, request, backend.Logs)
	case OpStop:
		return decodeThen(ctx, request, backend.Stop)
	case OpDestroy:
		return decodeThen(ctx, request, backend.Destroy)
	default:
		return nil, fmt.Errorf("unsupported operation %q", request.Operation)
	}
}

func decodeThen[I, O any](ctx context.Context, request provider.Request, run func(context.Context, I) (O, error)) (any, error) {
	var input I
	if len(request.Input) > 0 {
		if err := json.Unmarshal(request.Input, &input); err != nil {
			return nil, fmt.Errorf("decode %s input: %w", request.Operation, err)
		}
	}
	return run(ctx, input)
}
