package cli

import (
	"fmt"
	"os/exec"

	"github.com/roshbhatia/ere/internal/sandbox"
	"github.com/roshbhatia/go-utils/terminal"
	"github.com/spf13/cobra"
)

func newShellCmd(opts *options) *cobra.Command {
	var printOnly bool
	cmd := &cobra.Command{Use: "shell [runner] [-- command...]", Short: "Open an interactive guest shell", RunE: func(cmd *cobra.Command, args []string) error {
		e, err := opts.engine(false)
		if err != nil {
			return err
		}
		names := args
		var argv []string
		if dash := cmd.ArgsLenAtDash(); dash >= 0 {
			names = args[:dash]
			argv = args[dash:]
		}
		if len(names) > 1 {
			return fmt.Errorf("one runner is required")
		}
		name, err := selectRunner(cmd, e, names)
		if err != nil {
			return err
		}
		r, ok := e.Config.Runner(name)
		if !ok {
			return fmt.Errorf("unknown runner %q", name)
		}
		client, err := e.Registry.Client(r.Backend)
		if err != nil {
			return err
		}
		connection, err := client.Connect(cmd.Context(), sandbox.ConnectionRequest{Name: r.Name, Workdir: r.MountPath, Argv: argv})
		if err != nil {
			return err
		}
		if printOnly {
			return writeJSON(cmd.OutOrStdout(), connection)
		}
		if !terminal.IsTTY(cmd.InOrStdin()) || !terminal.IsTTY(cmd.OutOrStdout()) {
			return fmt.Errorf("shell requires a terminal; use ere exec for scripts")
		}
		if len(connection.Argv) == 0 {
			return fmt.Errorf("provider returned an empty connection")
		}
		return attach(cmd, exec.CommandContext(cmd.Context(), connection.Argv[0], connection.Argv[1:]...))
	}}
	cmd.Flags().BoolVar(&printOnly, "json", false, "print validated connection arguments without opening a shell")
	return cmd
}
