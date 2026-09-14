package docker

import (
	"context"
	"fmt"

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
	args := []string{"exec", "-it"}
	if req.Workdir != "" {
		args = append(args, "--workdir", req.Workdir)
	}
	args = append(args, s.ResourceID)
	args = append(args, argv...)
	return sandbox.Connection{Argv: append([]string{b.Binary}, b.args(args)...)}, nil
}
