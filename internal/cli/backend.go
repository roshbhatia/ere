package cli

import (
	"fmt"
	"os"

	"github.com/roshbhatia/ere/internal/backend/docker"
	"github.com/roshbhatia/ere/internal/backend/kubernetes"
	"github.com/roshbhatia/ere/internal/backend/lima"
	"github.com/roshbhatia/ere/internal/backend/machine"
	"github.com/roshbhatia/ere/internal/registry"
	"github.com/roshbhatia/ere/internal/sandbox"
	"github.com/spf13/cobra"
)

// newBackendCmd is the provider side of the contract. ere's own backends are
// reached the same way an external one is: a process, one request frame in, one
// result frame out.
func newBackendCmd() *cobra.Command {
	var binary, base, vmType, host, project, remote string
	var kube kubernetes.Backend

	cmd := &cobra.Command{
		Use:    "backend <name>",
		Short:  "Serve one provider/v1 sandbox request on standard input",
		Args:   cobra.ExactArgs(1),
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			var impl sandbox.Backend
			var err error
			switch args[0] {
			case registry.Pod, registry.KubeVirt:
				kube.Binary = binary
				kube.KubeVirt = args[0] == registry.KubeVirt
				impl = &kube
			case "incus", "tart", "multipass", "ssh":
				user := kube.SSHUser
				if !cmd.Flags().Changed("ssh-user") {
					user = ""
				}
				impl = &machine.Backend{Kind: args[0], Binary: binary, Host: host, Project: project, Remote: remote, VMType: vmType, SSHKey: kube.SSHKey, SSHUser: user}
			case registry.Lima:
				impl = lima.NewManaged(binary, vmType, base)
			default:
				impl, err = newBackend(args[0], binary, base)
			}
			if err != nil {
				return err
			}
			if dockerBackend, ok := impl.(*docker.Backend); ok {
				dockerBackend.Context = kube.Context
			}
			return sandbox.Serve(cmd.Context(), impl, os.Stdin, os.Stdout)
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "SSH host")
	cmd.Flags().StringVar(&project, "project", "", "Incus project")
	cmd.Flags().StringVar(&remote, "remote", "", "Incus remote")
	cmd.Flags().StringVar(&binary, "binary", "", "override the backend's command (docker, podman, limactl)")
	cmd.Flags().StringVar(&base, "base", "", "lima base template for the virtual-machine backends")
	cmd.Flags().StringVar(&vmType, "vm-type", "", "Lima hypervisor")
	cmd.Flags().StringVar(&kube.Context, "context", "", "explicit Docker or Kubernetes context")
	cmd.Flags().StringVar(&kube.Namespace, "namespace", "", "Kubernetes namespace")
	cmd.Flags().StringVar(&kube.Kubeconfig, "kubeconfig", "", "Kubernetes config file")
	cmd.Flags().StringVar(&kube.SSHKey, "ssh-key", "", "KubeVirt guest SSH identity")
	cmd.Flags().StringVar(&kube.SSHUser, "ssh-user", "ere", "KubeVirt guest user")
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
