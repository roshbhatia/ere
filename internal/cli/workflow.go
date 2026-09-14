package cli

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/roshbhatia/ere/internal/config"
	"github.com/roshbhatia/go-utils/cell"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
)

const installAmp = `set -eu
export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y curl ca-certificates git
installer=$(mktemp)
trap 'rm -f "$installer"' EXIT
curl -fsSL https://ampcode.com/install.sh -o "$installer"
AMP_HOME=/opt/amp bash "$installer"
chmod 755 /opt/amp /opt/amp/bin /opt/amp/bin/amp
ln -sfn /opt/amp/bin/amp /usr/local/bin/amp`

func newInitCmd() *cobra.Command {
	var provider, name, output string
	cmd := &cobra.Command{Use: "init", Short: "Write a minimal project runner configuration", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		cfg := config.New()
		cfg.Amp.APIKeySecret = "env://AMP_API_KEY"
		id, err := newRunnerID(name)
		if err != nil {
			return err
		}
		r := config.Runner{Name: name, RunnerID: id, Backend: provider, Workspace: cwd, MountPath: "/workspace", CPUs: 2, MemoryMB: 4096, DiskGB: 30}
		switch provider {
		case "lima":
			r.Provision = []string{installAmp}
			r.Image = "template://_images/ubuntu-lts"
		case "docker":
			r.DiskGB = 0
			r.Image = "ere/amp:latest"
		default:
			return fmt.Errorf("init supports lima or docker; use the provider recipes for %s", provider)
		}
		cfg.Runners = []config.Runner{r}
		if err = cfg.Validate(); err != nil {
			return err
		}
		data, err := yaml.Marshal(cfg)
		if err != nil {
			return err
		}
		if output == "-" {
			_, err = cmd.OutOrStdout().Write(data)
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
		absolute, err := filepath.Abs(output)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Created %s\nNext: ere --config %s start %s\n", absolute, absolute, name)
		return nil
	}}
	cmd.Flags().StringVar(&provider, "provider", "lima", "lima or docker")
	cmd.Flags().StringVar(&name, "name", "dev", "runner profile name")
	cmd.Flags().StringVar(&output, "output", "ere.yaml", "new file, or - for stdout; existing files are never overwritten")
	return cmd
}

func newInspectCmd(opts *options) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{Use: "inspect [runner]", Short: "Inspect runtime, workspace, capabilities, and allocations", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		e, err := opts.engine(false)
		if err != nil {
			return err
		}
		name, err := selectRunner(cmd, e, args)
		if err != nil {
			return err
		}
		r, ok := e.Config.Runner(name)
		if !ok {
			return fmt.Errorf("unknown runner %q", name)
		}
		status, err := e.Status(cmd.Context(), []string{name})
		if err != nil {
			return err
		}
		client, err := e.Registry.Client(r.Backend)
		if err != nil {
			return err
		}
		probe, err := client.Probe(cmd.Context())
		if err != nil {
			return err
		}
		allocations, err := e.Allocation(cmd.Context(), name, "list", "", "", "")
		if err != nil {
			return err
		}
		if asJSON {
			return writeJSON(cmd.OutOrStdout(), map[string]interface{}{"runtime": status, "allocations": allocations, "capabilities": probe, "workspace": r.Workspace, "storage": r.Storage, "remoteConnectivity": "unverified", "terminal": r.RemoteControlTerminal})
		}
		rows := [][]string{{"PROPERTY", "VALUE"}, {"Runner", r.Name}, {"Amp runner ID", r.RunnerID}, {"Provider", r.Backend}, {"Workspace", r.Workspace}, {"Guest path", r.MountPath}, {"Storage", r.Storage.Kind + " " + r.Storage.Source}, {"Retention", probe.Retention}, {"Remote connection", "unverified; use ere run to prove it"}, {"Terminal access", fmt.Sprint(r.RemoteControlTerminal)}}
		for _, s := range status {
			rows = append(rows, []string{"Compute", string(s.State)}, []string{"Amp process", s.Agent})
		}
		if err = cell.Table(cmd.OutOrStdout(), rows); err != nil {
			return err
		}
		return writeJSON(cmd.OutOrStdout(), allocations)
	}}
	cmd.Flags().BoolVar(&asJSON, "json", false, "emit JSON")
	return cmd
}

func runnerCompletion(opts *options) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
		cfg, err := config.Load(opts.configPath)
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		var values []string
		for _, r := range cfg.Runners {
			if strings.HasPrefix(r.Name, prefix) {
				values = append(values, r.Name+"\t"+r.Backend)
			}
		}
		return values, cobra.ShellCompDirectiveNoFileComp
	}
}

func newRunnerID(name string) (string, error) {
	bytes := make([]byte, 6)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	if len(name) > 40 {
		name = name[:40]
	}
	return "ere-" + name + "-" + hex.EncodeToString(bytes), nil
}
