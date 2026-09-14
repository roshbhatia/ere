package lima

import (
	"context"
	"fmt"

	"github.com/roshbhatia/ere/internal/backend"
	"github.com/roshbhatia/ere/internal/sandbox"
)

func (b *Backend) Connect(ctx context.Context, req sandbox.ConnectionRequest) (sandbox.Connection, error) {
	s, err := b.Status(ctx, sandbox.Ref{Name: req.Name})
	if err != nil {
		return sandbox.Connection{}, err
	}
	if s.State != sandbox.StateRunning {
		return sandbox.Connection{}, fmt.Errorf("runner is not running")
	}
	argv := req.Argv
	if len(argv) == 0 {
		argv = []string{"sh", "-l"}
	}
	script := "exec " + backend.ShellArgv(argv)
	if req.Workdir != "" {
		script = "cd " + backend.Quote(req.Workdir) + " && " + script
	}
	args := []string{b.Binary, "shell", "--tty=true", "--workdir", "/", instance(req.Name), "--", "sudo", "-n", "sh", "-c", script}
	return sandbox.Connection{Argv: args}, nil
}
