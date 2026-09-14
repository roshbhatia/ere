package cli

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	ampplugin "github.com/roshbhatia/ere/integrations/amp"
	"github.com/roshbhatia/ere/internal/config"
	"github.com/spf13/cobra"
)

func newPluginCmd(opts *options) *cobra.Command {
	root := &cobra.Command{Use: "plugin", Short: "Install the bundled Amp plugin"}
	var role string
	install := &cobra.Command{Use: "install <directory>", Short: "Install into a new Amp plugin directory without overwriting files", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if role != "operator" && role != "worker" {
			return fmt.Errorf("role must be operator or worker")
		}
		target, err := filepath.Abs(args[0])
		if err != nil {
			return err
		}
		configPath, err := config.Path(opts.configPath)
		if err != nil {
			return err
		}
		if configPath != "" {
			configPath, err = filepath.Abs(configPath)
			if err != nil {
				return err
			}
		}
		if err = os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err = os.Mkdir(target, 0o755); err != nil {
			return err
		}
		err = fs.WalkDir(ampplugin.Files, "ere", func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relative := strings.TrimPrefix(path, "ere")
			out := filepath.Join(target, relative)
			if d.IsDir() {
				return os.MkdirAll(out, 0o755)
			}
			data, err := ampplugin.Files.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(out, data, 0o644)
		})
		if err != nil {
			return fmt.Errorf("partial plugin retained at %s: %w", target, err)
		}
		encoded, err := json.Marshal(struct {
			Binary         string `json:"binary"`
			Config         string `json:"config"`
			Role           string `json:"role"`
			WaitTimeoutMS  int    `json:"waitTimeoutMs"`
			PollIntervalMS int    `json:"pollIntervalMs"`
		}{"ere", configPath, role, 300000, 2000})
		if err != nil {
			return err
		}
		settings := "export const settings: { binary: string; config: string; role: 'operator' | 'worker'; waitTimeoutMs: number; pollIntervalMs: number } = " + string(encoded) + "\n"
		if err = os.WriteFile(filepath.Join(target, "settings.ts"), []byte(settings), 0o644); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Installed %s plugin at %s; verify with amp plugins list\n", role, target)
		return nil
	}}
	install.Flags().StringVar(&role, "role", "operator", "operator or worker")
	root.AddCommand(install)
	return root
}
