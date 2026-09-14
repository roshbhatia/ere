// Package secret resolves a configured reference into a value at launch time.
// A resolved value is held in memory and handed to a sandbox over stdin; ere
// never writes one to its own config or to a provider manifest.
package secret

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/roshbhatia/ere/internal/backend"
)

// Scheme prefixes a reference understands.
const (
	SchemeOP   = "op://"
	SchemeEnv  = "env://"
	SchemeFile = "file://"
)

// Resolver turns references into values, once per reference per process.
type Resolver struct {
	// OPBinary is the 1Password CLI. Empty means "op".
	OPBinary string

	mu     sync.Mutex
	cached map[string]string
}

// New returns a resolver using the given 1Password CLI.
func New(opBinary string) *Resolver {
	if opBinary == "" {
		opBinary = "op"
	}
	return &Resolver{OPBinary: opBinary, cached: map[string]string{}}
}

// Resolve reads one reference. A value with no known scheme is a literal, so a
// non-secret default needs no ceremony.
func (r *Resolver) Resolve(ctx context.Context, ref string) (string, error) {
	if ref == "" {
		return "", nil
	}
	r.mu.Lock()
	if value, ok := r.cached[ref]; ok {
		r.mu.Unlock()
		return value, nil
	}
	r.mu.Unlock()

	value, err := r.read(ctx, ref)
	if err != nil {
		return "", err
	}
	r.mu.Lock()
	r.cached[ref] = value
	r.mu.Unlock()
	return value, nil
}

func (r *Resolver) read(ctx context.Context, ref string) (string, error) {
	switch {
	case strings.HasPrefix(ref, SchemeOP):
		if !backend.Available(r.OPBinary) {
			return "", fmt.Errorf("resolve %s: %s is not on PATH", ref, r.OPBinary)
		}
		out, err := backend.Check(ctx, r.OPBinary, "read", "--no-newline", ref)
		if err != nil {
			return "", fmt.Errorf("resolve %s: %w", ref, err)
		}
		return out, nil
	case strings.HasPrefix(ref, SchemeEnv):
		name := strings.TrimPrefix(ref, SchemeEnv)
		value, ok := os.LookupEnv(name)
		if !ok {
			return "", fmt.Errorf("resolve %s: %s is not set", ref, name)
		}
		return value, nil
	case strings.HasPrefix(ref, SchemeFile):
		data, err := os.ReadFile(strings.TrimPrefix(ref, SchemeFile))
		if err != nil {
			return "", fmt.Errorf("resolve %s: %w", ref, err)
		}
		return strings.TrimRight(string(data), "\n"), nil
	default:
		return ref, nil
	}
}

// ResolveAll resolves every value in a map, keeping the keys.
func (r *Resolver) ResolveAll(ctx context.Context, refs map[string]string) (map[string]string, error) {
	resolved := make(map[string]string, len(refs))
	for key, ref := range refs {
		value, err := r.Resolve(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("secret %s: %w", key, err)
		}
		resolved[key] = value
	}
	return resolved, nil
}
