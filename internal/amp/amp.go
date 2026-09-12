// Package amp knows how an Amp runner is launched. It is the only package that
// mentions Amp: a sandbox backend sees a command and an environment, nothing
// more.
package amp

import (
	"github.com/roshbhatia/lifier/internal/config"
)

// EnvAPIKey is the variable the Amp CLI reads its credential from.
const EnvAPIKey = "AMP_API_KEY"

// EnvURL overrides the Amp server.
const EnvURL = "AMP_URL"

// RunnerArgv builds the command that turns a sandbox into an Amp runner.
// --no-tui is what makes the process serve remotely created threads for its
// working directory.
func RunnerArgv(cfg config.Amp, runner config.Runner) []string {
	binary := cfg.Binary
	if binary == "" {
		binary = "amp"
	}
	argv := []string{binary, "--no-tui", "--runner-id", runner.RunnerID}
	if runner.RemoteControlTerminal {
		argv = append(argv, "--remote-control-terminal")
	}
	return append(argv, cfg.Args...)
}

// Env returns the Amp-specific variables for a sandbox, merged over the
// runner's own environment and resolved secrets.
func Env(cfg config.Amp, apiKey string, extra map[string]string) map[string]string {
	env := make(map[string]string, len(extra)+2)
	for key, value := range extra {
		env[key] = value
	}
	if apiKey != "" {
		env[EnvAPIKey] = apiKey
	}
	if cfg.URL != "" {
		env[EnvURL] = cfg.URL
	}
	return env
}
