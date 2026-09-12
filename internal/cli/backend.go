package cli

import (
	"fmt"
	"os"

	"github.com/roshbhatia/lifier/internal/backend/docker"
	"github.com/roshbhatia/lifier/internal/backend/lima"
	"github.com/roshbhatia/lifier/internal/registry"
	"github.com/roshbhatia/lifier/internal/sandbox"
	"github.com/spf13/cobra"
)

// newBackendCmd is the provider side of the contract. lifier's own backends are
// reached the same way an external one is: a process, one request frame in, one
// result frame out.
func newBackendCmd() *cobra.Command {
	var binary, base string

	cmd := &cobra.Command{
		Use:    "backend <name>",
		Short:  "Serve one provider/v1 sandbox request on standard input",
		Args:   cobra.ExactArgs(1),
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			impl, err := newBackend(args[0], binary, base)
			if err != nil {
				return err
			}
			return sandbox.Serve(cmd.Context(), impl, os.Stdin, os.Stdout)
		},
	}
	cmd.Flags().StringVar(&binary, "binary", "", "override the backend's command (docker, podman, limactl)")
	cmd.Flags().StringVar(&base, "base", "", "lima base template for the virtual-machine backends")
	return cmd
}

func newBackend(name, binary, base string) (sandbox.Backend, error) {
	switch name {
	case registry.Docker:
		return docker.New(binary), nil
	case registry.VZ:
		return lima.New(binary, "vz", base), nil
	case registry.QEMU:
		return lima.New(binary, "qemu", base), nil
	default:
		return nil, fmt.Errorf("unknown built-in backend %q", name)
	}
}
