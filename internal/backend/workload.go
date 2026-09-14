package backend

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/roshbhatia/lifier/internal/sandbox"
)

func Quote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'" }

func ShellArgv(argv []string) string {
	parts := make([]string, len(argv))
	for i, arg := range argv {
		parts[i] = Quote(arg)
	}
	return strings.Join(parts, " ")
}

func ExecScript(req sandbox.ExecRequest) (string, error) {
	if len(req.Argv) == 0 {
		return "", fmt.Errorf("exec requires a command")
	}
	valid := regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	for key := range req.Env {
		if !valid.MatchString(key) {
			return "", fmt.Errorf("invalid environment name %q", key)
		}
	}
	script := "set -eu\nset -a\n" + EnvFile(req.Env) + "set +a\n"
	if req.Workdir != "" {
		script += "cd " + Quote(req.Workdir) + "\n"
	}
	if req.Detach {
		log := req.LogFile
		if log == "" {
			log = "/dev/null"
		}
		script += "nohup " + ShellArgv(req.Argv) + " >> " + Quote(log) + " 2>&1 < /dev/null &\necho $!\n"
	} else {
		script += "exec " + ShellArgv(req.Argv)
		if req.LogFile != "" {
			script += " >> " + Quote(req.LogFile) + " 2>&1"
		}
		script += "\n"
	}
	return script, nil
}

func WorkloadScript(work sandbox.Workload) string {
	script := "set -eu\numask 077\nmkdir -p /var/lib/lifier\n"
	if work.Workdir != "" {
		script += "mkdir -p " + Quote(work.Workdir) + "\ncd " + Quote(work.Workdir) + "\n"
	}
	digest := sandbox.Digest(work.Provision)
	script += "if [ \"$(cat /var/lib/lifier/bootstrap.digest 2>/dev/null || true)\" != " + Quote(digest) + " ]; then\n"
	for _, command := range work.Provision {
		script += "sh -lec " + Quote(command) + "\n"
	}
	script += "printf '%s' " + Quote(digest) + " > /var/lib/lifier/bootstrap.digest\nfi\nexec " + ShellArgv(work.Argv) + "\n"
	return script
}
