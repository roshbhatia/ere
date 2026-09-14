package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/roshbhatia/ere/internal/config"
	"github.com/roshbhatia/ere/internal/registry"
	"github.com/roshbhatia/ere/internal/sandbox"
	"github.com/roshbhatia/go-utils/provider"
	"github.com/spf13/cobra"
)

// newSandboxCmd exposes the backend contract without the agent on top. It is
// how a backend is developed and how `hack/smoke.sh` proves one works before
// any credential is involved.
func newSandboxCmd(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sandbox",
		Short: "Drive a backend directly, without launching an agent",
	}
	cmd.AddCommand(
		newSandboxCreateCmd(opts),
		newSandboxLifecycleCmd(opts, "start", "Start an existing sandbox"),
		newSandboxLifecycleCmd(opts, "stop", "Stop a sandbox, keeping its disk"),
		newSandboxLifecycleCmd(opts, "destroy", "Destroy a sandbox"),
		newSandboxStatusCmd(opts),
		newSandboxListCmd(opts),
		newSandboxExecCmd(opts),
	)
	return cmd
}

func (o *options) backendClient(name string) (*sandbox.Client, error) {
	cfg, err := config.Load(o.configPath)
	if err != nil {
		return nil, err
	}
	reg, err := registry.Load(registry.ProviderDir(cfg.ProviderDir))
	if err != nil {
		return nil, err
	}
	for provider, settings := range cfg.Providers {
		if err := reg.Configure(provider, settings.Args()); err != nil {
			return nil, err
		}
	}
	return reg.Client(name)
}

func newSandboxCreateCmd(opts *options) *cobra.Command {
	spec := sandbox.Spec{}
	cmd := &cobra.Command{
		Use:   "create <backend> <name>",
		Short: "Create a sandbox on one backend",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := opts.backendClient(args[0])
			if err != nil {
				return err
			}
			client.OnEvent = eventPrinter(cmd)
			spec.Name = args[1]
			status, err := client.Create(cmd.Context(), spec)
			if err != nil {
				return err
			}
			return writeJSON(cmd.OutOrStdout(), status)
		},
	}
	cmd.Flags().StringVar(&spec.Image, "image", "", "container image or lima base template")
	cmd.Flags().StringVar(&spec.Workspace, "workspace", "", "host directory to mount")
	cmd.Flags().StringVar(&spec.MountPath, "mount-path", "/workspace", "where the workspace appears inside")
	cmd.Flags().StringVar(&spec.Storage.Kind, "storage-kind", "", "bind, volume, guest-disk, pvc, or ephemeral")
	cmd.Flags().StringVar(&spec.Storage.Source, "storage-source", "", "existing volume or PVC")
	cmd.Flags().StringVar(&spec.Architecture, "architecture", "", "guest or container architecture: amd64 or arm64")
	cmd.Flags().StringVar(&spec.Storage.Class, "storage-class", "", "workspace PVC storage class")
	cmd.Flags().IntVar(&spec.Storage.SizeGB, "storage-size-gb", 0, "workspace PVC size in GiB")
	cmd.Flags().StringVar(&spec.BootVolume, "boot-volume", "", "existing VM boot PVC")
	cmd.Flags().BoolVar(&spec.ReadOnly, "read-only", false, "mount the workspace read-only")
	cmd.Flags().IntVar(&spec.CPUs, "cpus", 0, "virtual CPUs")
	cmd.Flags().IntVar(&spec.MemoryMB, "memory-mb", 0, "memory in MiB")
	cmd.Flags().IntVar(&spec.DiskGB, "disk-gb", 0, "disk in GiB (virtual machines only)")
	return cmd
}

func newSandboxLifecycleCmd(opts *options, verb, short string) *cobra.Command {
	return &cobra.Command{
		Use:   verb + " <backend> <name>",
		Short: short,
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := opts.backendClient(args[0])
			if err != nil {
				return err
			}
			client.OnEvent = eventPrinter(cmd)
			ref := sandbox.Ref{Name: args[1]}
			var status sandbox.Status
			switch verb {
			case "start":
				status, err = client.Start(cmd.Context(), ref)
			case "stop":
				status, err = client.Stop(cmd.Context(), ref)
			default:
				status, err = client.Destroy(cmd.Context(), ref)
			}
			if err != nil {
				return err
			}
			return writeJSON(cmd.OutOrStdout(), status)
		},
	}
}

func newSandboxStatusCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "status <backend> <name>",
		Short: "Print one sandbox's state",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := opts.backendClient(args[0])
			if err != nil {
				return err
			}
			status, err := client.Status(cmd.Context(), sandbox.Ref{Name: args[1]})
			if err != nil {
				return err
			}
			return writeJSON(cmd.OutOrStdout(), status)
		},
	}
}

func newSandboxListCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "ls <backend>",
		Short: "List every sandbox one backend owns",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := opts.backendClient(args[0])
			if err != nil {
				return err
			}
			list, err := client.List(cmd.Context())
			if err != nil {
				return err
			}
			table := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(table, "NAME\tBACKEND\tSTATE\tIMAGE\tDETAIL")
			for _, item := range list.Sandboxes {
				fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\n",
					item.Name, item.Backend, item.State, item.Image, item.Detail)
			}
			return table.Flush()
		},
	}
}

func newSandboxExecCmd(opts *options) *cobra.Command {
	var workdir string
	cmd := &cobra.Command{
		Use:   "exec <backend> <name> -- <command>...",
		Short: "Run one command in a sandbox, with no agent environment",
		Args:  cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := opts.backendClient(args[0])
			if err != nil {
				return err
			}
			result, err := client.Exec(cmd.Context(), sandbox.ExecRequest{
				Name:    args[1],
				Argv:    args[2:],
				Workdir: workdir,
			})
			if err != nil {
				return err
			}
			fmt.Fprint(cmd.OutOrStdout(), result.Stdout)
			fmt.Fprint(cmd.ErrOrStderr(), result.Stderr)
			if result.ExitCode != 0 {
				return &ExitError{Code: result.ExitCode}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&workdir, "workdir", "", "working directory inside the sandbox")
	return cmd
}

func eventPrinter(cmd *cobra.Command) func(provider.Event) {
	return func(event provider.Event) {
		if event.Message != "" {
			fmt.Fprintln(cmd.ErrOrStderr(), event.Message)
		}
	}
}
