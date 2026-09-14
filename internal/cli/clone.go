package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/roshbhatia/ere/internal/config"
	"github.com/roshbhatia/ere/internal/sandbox"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

func newCloneCmd(opts *options) *cobra.Command {
	var workspace, fork, output, template string
	var share bool
	cmd := &cobra.Command{Use: "clone <source-profile> <new-profile>", Short: "Copy a profile with an independent workspace or prepared VM template", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		e, err := opts.engine(false)
		if err != nil {
			return err
		}
		r, ok := e.Config.Runner(args[0])
		if !ok {
			return fmt.Errorf("unknown source profile %q", args[0])
		}
		if _, ok = e.Config.Runner(args[1]); ok {
			return fmt.Errorf("target profile already exists")
		}
		if r.BootVolume != "" {
			return fmt.Errorf("clone requires an independent boot volume; shared VM boot disks are unsupported")
		}
		r.Name = args[1]
		r.RunnerID, err = newRunnerID(r.Name)
		if err != nil {
			return err
		}
		if err = sandbox.Validate(sandbox.Spec{Name: r.Name}); err != nil {
			return err
		}
		modes := 0
		for _, enabled := range []bool{workspace != "", fork != "", share} {
			if enabled {
				modes++
			}
		}
		if modes > 1 {
			return fmt.Errorf("choose one of --workspace, --fork-workspace, or --share-workspace")
		}
		if output == "" {
			return fmt.Errorf("--output must name a new configuration file")
		}
		if _, err = os.Lstat(output); !os.IsNotExist(err) {
			return fmt.Errorf("output already exists or cannot be inspected")
		}
		switch {
		case fork != "":
			if r.Workspace == "" {
				return fmt.Errorf("workspace forks need a local Git checkout")
			}
			destination, err := filepath.Abs(fork)
			if err != nil {
				return err
			}
			process := exec.CommandContext(cmd.Context(), "git", "clone", "--no-hardlinks", "--", r.Workspace, destination)
			process.Stdout = cmd.ErrOrStderr()
			process.Stderr = cmd.ErrOrStderr()
			if err = process.Run(); err != nil {
				return err
			}
			remote, err := exec.CommandContext(cmd.Context(), "git", "-C", r.Workspace, "remote", "get-url", "origin").Output()
			if err == nil {
				if err = exec.CommandContext(cmd.Context(), "git", "-C", destination, "remote", "set-url", "origin", strings.TrimSpace(string(remote))).Run(); err != nil {
					return err
				}
			}
			r.Workspace = destination
			r.Storage = sandbox.Storage{Kind: "bind"}
			fmt.Fprintln(cmd.ErrOrStderr(), "Forked committed Git state; source changes remain in the original checkout")
		case workspace != "":
			r.Workspace, err = filepath.Abs(workspace)
			if err != nil {
				return err
			}
			r.Storage = sandbox.Storage{Kind: "bind"}
		case share:
		default:
			if r.Workspace != "" || r.Storage.Source != "" || r.BootVolume != "" {
				return fmt.Errorf("choose an independent workspace or explicitly --share-workspace")
			}
		}
		if r.BootVolume != "" && !share {
			return fmt.Errorf("bootVolume needs an independent clone; a shared boot disk is not copied")
		}
		cfg := e.Config
		cfg.Runners = []config.Runner{r}
		if err = cfg.Validate(); err != nil {
			return err
		}
		data, err := yaml.Marshal(cfg)
		if err != nil {
			return err
		}
		file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		_, err = file.Write(data)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if template != "" {
			e.Config = cfg
			if err = e.CloneTemplate(cmd.Context(), template, r.Name); err != nil {
				return fmt.Errorf("configuration retained at %s: %w", output, err)
			}
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Created %s; next: ere --config %s start %s\n", output, output, r.Name)
		return nil
	}}
	cmd.Flags().StringVar(&workspace, "workspace", "", "independent existing host directory")
	cmd.Flags().StringVar(&fork, "fork-workspace", "", "clone committed Git state into a new host directory")
	cmd.Flags().BoolVar(&share, "share-workspace", false, "explicitly share the source workspace and external storage")
	cmd.Flags().StringVar(&template, "template", "", "clone a stopped prepared template on the same provider")
	cmd.Flags().StringVar(&output, "output", "", "new configuration file; original stays unchanged")
	return cmd
}

func newTemplateCmd(opts *options) *cobra.Command {
	root := &cobra.Command{Use: "template", Short: "Build credential-free runner templates"}
	root.AddCommand(&cobra.Command{Use: "build <profile> <template-name>", Short: "Provision a new Lima template without the source workspace or credentials", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		e, err := opts.engine(true)
		if err != nil {
			return err
		}
		if err = e.BuildTemplate(cmd.Context(), args[0], args[1]); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Prepared template %s\n", args[1])
		return nil
	}})
	return root
}
