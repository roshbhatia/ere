// Package cli wires lifier's command tree. Commands parse input and print
// results; the reconciliation lives in internal/runner.
package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/roshbhatia/lifier/internal/config"
	"github.com/roshbhatia/lifier/internal/registry"
	"github.com/roshbhatia/lifier/internal/runner"
	"github.com/roshbhatia/lifier/internal/secret"
	"github.com/spf13/cobra"
)

type options struct {
	configPath string
	opBinary   string
}

// NewRootCmd builds the lifier command tree.
func NewRootCmd(version string) *cobra.Command {
	opts := &options{}

	root := &cobra.Command{
		Use:   "lifier",
		Short: "Run Amp runners in disposable sandboxes",
		Long: "lifier keeps a declared fleet of Amp runners alive in sandboxes.\n" +
			"Each sandbox backend is a provider/v1 executable, so a container, a\n" +
			"virtual machine, and a machine across the network are the same shape.",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&opts.configPath, "config", "", "path to the lifier config file")
	root.PersistentFlags().StringVar(&opts.opBinary, "op", "op", "1Password CLI used to resolve op:// secrets")

	root.AddCommand(
		newUpCmd(opts),
		newAPICmd(opts),
		newPlanCmd(opts),
		newReconcileCmd(opts),
		newMCPCmd(opts),
		newDownCmd(opts),
		newRemoveCmd(opts),
		newListCmd(opts),
		newLogsCmd(opts),
		newExecCmd(opts),
		newDoctorCmd(opts),
		newBackendsCmd(opts),
		newConfigCmd(opts),
		newSandboxCmd(opts),
		newBackendCmd(),
	)
	return root
}

func (o *options) engine(progress bool) (*runner.Engine, error) {
	cfg, err := config.Load(o.configPath)
	if err != nil {
		return nil, err
	}
	reg, err := registry.Load(registry.ProviderDir(cfg.ProviderDir))
	if err != nil {
		return nil, err
	}
	for name, settings := range cfg.Providers {
		if err := reg.Configure(name, settings.Args()); err != nil {
			return nil, err
		}
	}
	var out *os.File
	if progress {
		out = os.Stderr
	}
	if out == nil {
		return runner.New(cfg, reg, secret.New(o.opBinary), nil), nil
	}
	return runner.New(cfg, reg, secret.New(o.opBinary), out), nil
}

func newUpCmd(opts *options) *cobra.Command {
	var restart bool
	cmd := &cobra.Command{
		Use:   "up [runner...]",
		Short: "Create, start, and serve the selected runners",
		RunE: func(cmd *cobra.Command, args []string) error {
			engine, err := opts.engine(true)
			if err != nil {
				return err
			}
			return engine.Up(cmd.Context(), args, restart)
		},
	}
	cmd.Flags().BoolVar(&restart, "restart", false, "restart the Amp process even when it is already serving")
	return cmd
}

func newDownCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "down [runner...]",
		Short: "Stop the sandboxes of the selected runners, keeping their disks",
		RunE: func(cmd *cobra.Command, args []string) error {
			engine, err := opts.engine(true)
			if err != nil {
				return err
			}
			return engine.Down(cmd.Context(), args)
		},
	}
}

func newRemoveCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:     "rm [runner...]",
		Aliases: []string{"destroy"},
		Short:   "Destroy the sandboxes of the selected runners",
		RunE: func(cmd *cobra.Command, args []string) error {
			engine, err := opts.engine(true)
			if err != nil {
				return err
			}
			return engine.Remove(cmd.Context(), args)
		},
	}
}

func newListCmd(opts *options) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "ls [runner...]",
		Aliases: []string{"list", "status"},
		Short:   "Show each runner's sandbox state and whether the amp process is up",
		RunE: func(cmd *cobra.Command, args []string) error {
			engine, err := opts.engine(false)
			if err != nil {
				return err
			}
			rows, err := engine.Status(cmd.Context(), args)
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), rows)
			}
			table := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(table, "RUNNER\tRUNNER ID\tBACKEND\tSANDBOX\tAMP\tDETAIL")
			for _, row := range rows {
				fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\n",
					row.Runner, row.RunnerID, row.Backend, row.State, row.Agent, row.Detail)
			}
			return table.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON")
	return cmd
}

func newLogsCmd(opts *options) *cobra.Command {
	var lines int
	cmd := &cobra.Command{
		Use:   "logs <runner>",
		Short: "Print a bounded tail of a runner's Amp output",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			engine, err := opts.engine(false)
			if err != nil {
				return err
			}
			out, err := engine.Logs(cmd.Context(), args[0], lines)
			if err != nil {
				return err
			}
			for _, line := range out {
				fmt.Fprintln(cmd.OutOrStdout(), line)
			}
			return nil
		},
	}
	cmd.Flags().IntVarP(&lines, "lines", "n", 200, "how many trailing lines to print")
	return cmd
}

func newExecCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "exec <runner> -- <command>...",
		Short: "Run one command inside a runner's sandbox",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			engine, err := opts.engine(false)
			if err != nil {
				return err
			}
			code, err := engine.Exec(cmd.Context(), args[0], args[1:], cmd.OutOrStdout(), cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			if code != 0 {
				return &ExitError{Code: code}
			}
			return nil
		},
	}
}

func newDoctorCmd(opts *options) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Probe every registered backend on this host",
		RunE: func(cmd *cobra.Command, args []string) error {
			engine, err := opts.engine(false)
			if err != nil {
				return err
			}
			probes, err := engine.Doctor(cmd.Context())
			if err != nil {
				return err
			}
			if asJSON {
				return writeJSON(cmd.OutOrStdout(), probes)
			}
			table := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(table, "BACKEND\tAVAILABLE\tDETAIL")
			for _, probe := range probes {
				fmt.Fprintf(table, "%s\t%t\t%s\n", probe.Backend, probe.Available, probe.Detail)
			}
			return table.Flush()
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON")
	return cmd
}

func newBackendsCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "backends",
		Short: "List the backend manifests this host resolves",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(opts.configPath)
			if err != nil {
				return err
			}
			reg, err := registry.Load(registry.ProviderDir(cfg.ProviderDir))
			if err != nil {
				return err
			}
			table := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(table, "BACKEND\tSOURCE\tCOMMAND\tDESCRIPTION")
			for _, entry := range reg.Entries() {
				fmt.Fprintf(table, "%s\t%s\t%s\t%s\n",
					entry.Manifest.Name, entry.Source,
					shellJoin(entry.Manifest.Command), entry.Manifest.Description)
			}
			return table.Flush()
		},
	}
}

func newConfigCmd(opts *options) *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Inspect the lifier configuration"}
	cmd.AddCommand(&cobra.Command{
		Use:   "path",
		Short: "Print the config file lifier would read",
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := config.Path(opts.configPath)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), path)
			return nil
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "schema",
		Short: "Print the JSON Schema for the config file",
		RunE: func(cmd *cobra.Command, args []string) error {
			schema, err := config.Schema()
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(schema)
			return err
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Print the effective configuration after defaults and overrides",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(opts.configPath)
			if err != nil {
				return err
			}
			return writeJSON(cmd.OutOrStdout(), cfg)
		},
	})
	return cmd
}

// ExitError carries a sandboxed command's exit code out to main.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("command exited %d", e.Code) }

func writeJSON(out interface{ Write([]byte) (int, error) }, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func shellJoin(argv []string) string {
	joined := ""
	for i, part := range argv {
		if i > 0 {
			joined += " "
		}
		joined += part
	}
	return joined
}
