// Package backend holds helpers shared by the sandbox backends lifier ships.
package backend

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
)

// Output is one finished command.
type Output struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Run executes a command and returns its output. A non-zero exit is an Output,
// not an error: the callers here decide whether an exit code means failure.
func Run(ctx context.Context, name string, args ...string) (Output, error) {
	command := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	out := Output{Stdout: stdout.String(), Stderr: stderr.String()}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		out.ExitCode = exitErr.ExitCode()
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("run %s: %w", name, err)
	}
	return out, nil
}

// Check runs a command and turns a non-zero exit into an error carrying the
// stderr the tool printed.
func Check(ctx context.Context, name string, args ...string) (string, error) {
	out, err := Run(ctx, name, args...)
	if err != nil {
		return "", err
	}
	if out.ExitCode != 0 {
		detail := strings.TrimSpace(out.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(out.Stdout)
		}
		return "", fmt.Errorf("%s %s exited %d: %s", name, strings.Join(args, " "), out.ExitCode, detail)
	}
	return out.Stdout, nil
}

// Available reports whether a command resolves on PATH.
func Available(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// RunStdin executes a command with data on its standard input.
func RunStdin(ctx context.Context, stdin string, name string, args ...string) (Output, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	out := Output{Stdout: stdout.String(), Stderr: stderr.String()}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		out.ExitCode = exitErr.ExitCode()
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("run %s: %w", name, err)
	}
	return out, nil
}

// EnvFile renders an environment map as a POSIX shell fragment. Values are
// single-quoted so a secret never needs escaping at the call site, and the
// fragment reaches the sandbox over stdin rather than through argv, where
// another process on the host could read it.
func EnvFile(env map[string]string) string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	for _, key := range keys {
		builder.WriteString(key)
		builder.WriteString("='")
		builder.WriteString(strings.ReplaceAll(env[key], "'", `'\''`))
		builder.WriteString("'\n")
	}
	return builder.String()
}

// EnvPath is where a rendered environment lands inside a sandbox.
const EnvPath = "/tmp/.lifier-env"

// WrapCommand builds the argv that sources the environment file and then
// replaces the shell with the requested command.
func WrapCommand(argv []string, logFile string) []string {
	script := "set -a; [ -f " + EnvPath + " ] && . " + EnvPath + "; set +a; exec \"$@\""
	if logFile != "" {
		script = "set -a; [ -f " + EnvPath + " ] && . " + EnvPath + "; set +a; exec \"$@\" >> " + logFile + " 2>&1"
	}
	return append([]string{"sh", "-c", script, "lifier"}, argv...)
}

// WrapDetached builds the argv for a backend that has no detach flag of its
// own. nohup keeps the process alive after the transport session closes.
func WrapDetached(argv []string, logFile string) []string {
	if logFile == "" {
		logFile = "/dev/null"
	}
	script := "set -a; [ -f " + EnvPath + " ] && . " + EnvPath + "; set +a; " +
		"nohup \"$@\" >> " + logFile + " 2>&1 < /dev/null & echo $!"
	return append([]string{"sh", "-c", script, "lifier"}, argv...)
}
