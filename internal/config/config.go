// Package config is lifier's declarative surface: the runners a host should
// hold, the backend each one uses, and where their secrets come from.
package config

import (
	"fmt"
	"regexp"
	"strings"

	goconfig "github.com/roshbhatia/go-utils/config"
)

// Name is the config directory and file stem under XDG config home.
const Name = "lifier"

// EnvPrefix scopes environment overrides.
const EnvPrefix = "LIFIER"

// Config is one host's declared runner fleet.
type Config struct {
	DefaultBackend string   `json:"defaultBackend,omitempty" yaml:"defaultBackend,omitempty"`
	ProviderDir    string   `json:"providerDir,omitempty"    yaml:"providerDir,omitempty"`
	Amp            Amp      `json:"amp,omitempty"            yaml:"amp,omitempty"`
	Defaults       Defaults `json:"defaults,omitempty"       yaml:"defaults,omitempty"`
	Runners        []Runner `json:"runners,omitempty"        yaml:"runners,omitempty"`
}

// Amp describes how the agent is launched inside every sandbox. lifier never
// holds an Amp credential itself: it names a secret reference and resolves it
// at launch.
type Amp struct {
	Binary                string   `json:"binary,omitempty"                yaml:"binary,omitempty"`
	URL                   string   `json:"url,omitempty"                   yaml:"url,omitempty"`
	APIKeySecret          string   `json:"apiKeySecret,omitempty"          yaml:"apiKeySecret,omitempty"`
	Args                  []string `json:"args,omitempty"                  yaml:"args,omitempty"`
	RemoteControlTerminal bool     `json:"remoteControlTerminal,omitempty" yaml:"remoteControlTerminal,omitempty"`
}

// Defaults fill in what a runner entry leaves out.
type Defaults struct {
	Backend   string   `json:"backend,omitempty"   yaml:"backend,omitempty"`
	Image     string   `json:"image,omitempty"     yaml:"image,omitempty"`
	MountPath string   `json:"mountPath,omitempty" yaml:"mountPath,omitempty"`
	CPUs      int      `json:"cpus,omitempty"      yaml:"cpus,omitempty"`
	MemoryMB  int      `json:"memoryMB,omitempty"  yaml:"memoryMB,omitempty"`
	DiskGB    int      `json:"diskGB,omitempty"    yaml:"diskGB,omitempty"`
	Provision []string `json:"provision,omitempty" yaml:"provision,omitempty"`
}

// Runner is one sandbox holding one Amp runner.
type Runner struct {
	Name                  string            `json:"name"                            yaml:"name"`
	Backend               string            `json:"backend,omitempty"               yaml:"backend,omitempty"`
	Workspace             string            `json:"workspace,omitempty"             yaml:"workspace,omitempty"`
	MountPath             string            `json:"mountPath,omitempty"             yaml:"mountPath,omitempty"`
	ReadOnly              bool              `json:"readOnly,omitempty"              yaml:"readOnly,omitempty"`
	Image                 string            `json:"image,omitempty"                 yaml:"image,omitempty"`
	CPUs                  int               `json:"cpus,omitempty"                  yaml:"cpus,omitempty"`
	MemoryMB              int               `json:"memoryMB,omitempty"              yaml:"memoryMB,omitempty"`
	DiskGB                int               `json:"diskGB,omitempty"                yaml:"diskGB,omitempty"`
	Env                   map[string]string `json:"env,omitempty"                   yaml:"env,omitempty"`
	Secrets               map[string]string `json:"secrets,omitempty"               yaml:"secrets,omitempty"`
	Provision             []string          `json:"provision,omitempty"             yaml:"provision,omitempty"`
	RunnerID              string            `json:"runnerId,omitempty"              yaml:"runnerId,omitempty"`
	RemoteControlTerminal bool              `json:"remoteControlTerminal,omitempty" yaml:"remoteControlTerminal,omitempty"`
}

// Defaults returns the configuration used when no file exists.
func New() Config {
	return Config{
		DefaultBackend: "docker",
		Amp:            Amp{Binary: "amp"},
		Defaults:       Defaults{MountPath: "/workspace", CPUs: 4, MemoryMB: 8192, DiskGB: 60},
	}
}

// Load reads the config file and environment overrides over the defaults.
func Load(path string) (Config, error) {
	cfg, err := goconfig.Load(New(), goconfig.Options{Name: Name, EnvPrefix: EnvPrefix, Path: path})
	if err != nil {
		return cfg, err
	}
	cfg.applyDefaults()
	return cfg, cfg.Validate()
}

// Path reports which file Load would read.
func Path(path string) (string, error) {
	return goconfig.Path(goconfig.Options{Name: Name, EnvPrefix: EnvPrefix, Path: path})
}

// Schema returns the JSON Schema for the config file.
func Schema() ([]byte, error) { return goconfig.Schema[Config]("lifier configuration") }

func (c *Config) applyDefaults() {
	if c.Defaults.Backend == "" {
		c.Defaults.Backend = c.DefaultBackend
	}
	if c.Amp.Binary == "" {
		c.Amp.Binary = "amp"
	}
	for i := range c.Runners {
		runner := &c.Runners[i]
		if runner.Backend == "" {
			runner.Backend = c.Defaults.Backend
		}
		if runner.Image == "" {
			runner.Image = c.Defaults.Image
		}
		if runner.MountPath == "" {
			runner.MountPath = c.Defaults.MountPath
		}
		if runner.CPUs == 0 {
			runner.CPUs = c.Defaults.CPUs
		}
		if runner.MemoryMB == 0 {
			runner.MemoryMB = c.Defaults.MemoryMB
		}
		if runner.DiskGB == 0 {
			runner.DiskGB = c.Defaults.DiskGB
		}
		if len(runner.Provision) == 0 {
			runner.Provision = c.Defaults.Provision
		}
		if runner.RunnerID == "" {
			runner.RunnerID = runner.Name
		}
		if !runner.RemoteControlTerminal {
			runner.RemoteControlTerminal = c.Amp.RemoteControlTerminal
		}
	}
}

// hostname is Amp's stated constraint on a runner id.
var hostname = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

// Validate rejects a config that would fail late, inside a sandbox.
func (c Config) Validate() error {
	seen := make(map[string]bool, len(c.Runners))
	for _, runner := range c.Runners {
		if strings.TrimSpace(runner.Name) == "" {
			return fmt.Errorf("every runner needs a name")
		}
		if seen[runner.Name] {
			return fmt.Errorf("duplicate runner %q", runner.Name)
		}
		seen[runner.Name] = true
		if !hostname.MatchString(runner.RunnerID) {
			return fmt.Errorf("runner %q has runnerId %q, which is not a valid hostname", runner.Name, runner.RunnerID)
		}
		if runner.Backend == "" {
			return fmt.Errorf("runner %q has no backend and no default is set", runner.Name)
		}
	}
	return nil
}

// Runner finds one declared runner by name.
func (c Config) Runner(name string) (Runner, bool) {
	for _, runner := range c.Runners {
		if runner.Name == name {
			return runner, true
		}
	}
	return Runner{}, false
}

// Names lists every declared runner in file order.
func (c Config) Names() []string {
	names := make([]string, 0, len(c.Runners))
	for _, runner := range c.Runners {
		names = append(names, runner.Name)
	}
	return names
}
