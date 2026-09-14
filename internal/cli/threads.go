package cli

import (
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"

	"github.com/roshbhatia/go-utils/terminal"

	"github.com/roshbhatia/ere/internal/runner"
	"github.com/roshbhatia/ere/internal/state"
	"github.com/roshbhatia/go-utils/cell"
	"github.com/spf13/cobra"
)

func selectRunner(cmd *cobra.Command, e *runner.Engine, args []string) (string, error) {
	if len(args) > 0 {
		return args[0], nil
	}
	var items []choice
	for _, r := range e.Config.Runners {
		items = append(items, choice{r.Name, r.Name + "  " + r.Backend + "  " + r.Workspace})
	}
	return pick(cmd, "Runner", items)
}

func attach(cmd *cobra.Command, process *exec.Cmd) error {
	process.Stdin = cmd.InOrStdin()
	process.Stdout = cmd.OutOrStdout()
	process.Stderr = cmd.ErrOrStderr()
	if err := process.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return &ExitError{Code: exit.ExitCode()}
		}
		return err
	}
	return nil
}

func newThreadCmd(opts *options, interactive bool) *cobra.Command {
	var input runner.ThreadOptions
	var asJSON, stdin bool
	verb := "run"
	short := "Submit a task and print its Amp thread URL"
	if interactive {
		verb = "start"
		short = "Prepare a runner and open a native Amp TUI thread"
	}
	cmd := &cobra.Command{Use: verb + " [runner] [-- prompt]", Short: short, Args: cobra.ArbitraryArgs, RunE: func(cmd *cobra.Command, args []string) error {
		e, err := opts.engine(true)
		if err != nil {
			return err
		}
		positional := args
		if dash := cmd.ArgsLenAtDash(); dash >= 0 {
			positional = args[:dash]
			input.Prompt = strings.Join(args[dash:], " ")
		}
		if len(positional) > 1 {
			return fmt.Errorf("put the prompt after --")
		}
		input.Runner, err = selectRunner(cmd, e, positional)
		if err != nil {
			return err
		}
		if stdin {
			data, err := io.ReadAll(io.LimitReader(cmd.InOrStdin(), 1024*1024+1))
			if err != nil {
				return err
			}
			if len(data) > 1024*1024 {
				return fmt.Errorf("prompt exceeds 1 MiB")
			}
			input.Prompt = string(data)
		}
		if !interactive && strings.TrimSpace(input.Prompt) == "" {
			return fmt.Errorf("provide a prompt after -- or use --stdin")
		}
		if interactive && !asJSON && (!terminal.IsTTY(cmd.InOrStdin()) || !terminal.IsTTY(cmd.OutOrStdout())) {
			return fmt.Errorf("start needs a terminal; use --json for headless creation")
		}
		a, err := e.NewThread(cmd.Context(), input)
		if err != nil {
			return err
		}
		if interactive && !asJSON {
			return attach(cmd, e.AmpCommand(cmd.Context(), "threads", "continue", a.ThreadID))
		}
		if asJSON {
			return writeJSON(cmd.OutOrStdout(), struct {
				Allocation state.Allocation `json:"allocation"`
				URL        string           `json:"url"`
			}{a, e.ThreadURL(a.ThreadID)})
		}
		fmt.Fprintln(cmd.OutOrStdout(), e.ThreadURL(a.ThreadID))
		return nil
	}}
	cmd.Flags().StringVarP(&input.Mode, "mode", "m", "", "Amp mode, including installed plugin modes")
	cmd.Flags().StringVar(&input.Title, "title", "", "thread title")
	cmd.Flags().StringSliceVarP(&input.Labels, "label", "l", nil, "thread labels")
	cmd.Flags().StringSliceVar(&input.Features, "features", nil, "Amp thread features")
	cmd.Flags().StringVar(&input.ID, "id", "", "idempotency key; interrupted submissions require recovery")
	cmd.Flags().BoolVar(&stdin, "stdin", false, "read the prompt from stdin")
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit allocation and URL as JSON")
	return cmd
}

func trackedThread(cmd *cobra.Command, e *runner.Engine, args []string) (state.Allocation, error) {
	all, err := e.Threads(cmd.Context())
	if err != nil {
		return state.Allocation{}, err
	}
	id := ""
	if len(args) > 0 {
		id = args[0]
	}
	if id == "" {
		var items []choice
		seen := map[string]bool{}
		for _, a := range all {
			if a.ThreadID != "" && !seen[a.ThreadID] {
				items = append(items, choice{a.ThreadID, a.Runner + "  " + a.ThreadID + "  " + a.State})
				seen[a.ThreadID] = true
			}
		}
		id, err = pick(cmd, "Thread", items)
		if err != nil {
			return state.Allocation{}, err
		}
	}
	for _, a := range all {
		if a.ThreadID == id || a.ID == id {
			return a, nil
		}
	}
	return state.Allocation{}, fmt.Errorf("unknown Ere thread or allocation %q", id)
}

func newContinueCmd(opts *options) *cobra.Command {
	return &cobra.Command{Use: "continue [thread-id]", Short: "Continue a tracked thread with an allocation", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		e, err := opts.engine(true)
		if err != nil {
			return err
		}
		a, err := trackedThread(cmd, e, args)
		if err != nil {
			return err
		}
		if !terminal.IsTTY(cmd.InOrStdin()) || !terminal.IsTTY(cmd.OutOrStdout()) {
			return fmt.Errorf("continue needs a terminal")
		}
		a, err = e.ContinueThread(cmd.Context(), a.ThreadID)
		if err != nil {
			return err
		}
		return attach(cmd, e.AmpCommand(cmd.Context(), "threads", "continue", a.ThreadID))
	}}
}

func newThreadsCmd(opts *options) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "threads", Short: "List tracked Amp threads", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		e, err := opts.engine(false)
		if err != nil {
			return err
		}
		all, err := e.Threads(cmd.Context())
		if err != nil {
			return err
		}
		if asJSON {
			return writeJSON(cmd.OutOrStdout(), all)
		}
		rows := [][]string{{"RUNNER", "THREAD", "STATE", "ALLOCATION"}}
		for _, a := range all {
			rows = append(rows, []string{a.Runner, a.ThreadID, a.State, a.ID})
		}
		return cell.Table(cmd.OutOrStdout(), rows)
	}}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON")
	for _, verb := range []string{"poll", "release", "open", "url"} {
		cmd.AddCommand(&cobra.Command{Use: verb + " [thread-id]", Short: verb + " a tracked thread", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
			e, err := opts.engine(false)
			if err != nil {
				return err
			}
			a, err := trackedThread(cmd, e, args)
			if err != nil {
				return err
			}
			if verb == "open" {
				binary := "xdg-open"
				if runtime.GOOS == "darwin" {
					binary = "open"
				}
				return attach(cmd, exec.CommandContext(cmd.Context(), binary, e.ThreadURL(a.ThreadID)))
			}
			if verb == "url" {
				fmt.Fprintln(cmd.OutOrStdout(), e.ThreadURL(a.ThreadID))
				return nil
			}
			snapshot, err := e.PollThread(cmd.Context(), a, verb == "release")
			if err != nil {
				return err
			}
			return writeJSON(cmd.OutOrStdout(), snapshot)
		}})
	}
	return cmd
}
