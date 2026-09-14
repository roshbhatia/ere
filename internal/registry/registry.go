// Package registry resolves a backend name to a provider/v1 manifest. The
// backends ere ships are manifests too: their command is this binary's own
// `backend` subcommand, so nothing in the core calls a backend in process.
package registry

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/roshbhatia/ere/internal/sandbox"
	"github.com/roshbhatia/go-utils/paths"
	"github.com/roshbhatia/go-utils/provider"
)

// Builtin names, one per shipped backend.
const (
	Docker   = "docker"
	Lima     = "lima"
	Pod      = "kubernetes-pod"
	KubeVirt = "kubernetes-kubevirt"
	VZ       = "vz"
	QEMU     = "qemu"
)

// Entry is one resolvable backend and where its manifest came from.
type Entry struct {
	Manifest provider.Manifest
	Source   string
}

// Registry holds every backend visible on this host.
type Registry struct {
	entries map[string]Entry
	order   []string
}

// ProviderDir is where external backend manifests are discovered.
func ProviderDir(override string) string {
	if override != "" {
		return paths.ExpandHome(override)
	}
	return filepath.Join(paths.ConfigHome(), "ere", "providers")
}

// Load builds the registry: the shipped backends first, then any manifest in
// dir. A discovered manifest that reuses a shipped name replaces it, which is
// how a host swaps docker for podman or points a name at another machine.
func Load(dir string) (*Registry, error) {
	registry := &Registry{entries: map[string]Entry{}}
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate ere binary: %w", err)
	}
	for _, manifest := range Builtins(self) {
		registry.add(Entry{Manifest: manifest, Source: "built-in"})
	}
	loaded, err := provider.Discover(dir)
	if err != nil {
		return nil, err
	}
	for _, item := range loaded {
		if item.Manifest.Kind != "" && item.Manifest.Kind != sandbox.Kind {
			continue
		}
		if _, ok := item.Manifest.Actions[sandbox.Capability]; !ok {
			return nil, fmt.Errorf("provider %s declares no %q action", item.Path, sandbox.Capability)
		}
		registry.add(Entry{Manifest: item.Manifest, Source: item.Path})
	}
	return registry, nil
}

func (r *Registry) add(entry Entry) {
	if _, exists := r.entries[entry.Manifest.Name]; !exists {
		r.order = append(r.order, entry.Manifest.Name)
	}
	r.entries[entry.Manifest.Name] = entry
}

// Names lists every backend, sorted.
func (r *Registry) Names() []string {
	names := append([]string(nil), r.order...)
	sort.Strings(names)
	return names
}

// Entries lists every backend with its source, sorted by name.
func (r *Registry) Entries() []Entry {
	entries := make([]Entry, 0, len(r.entries))
	for _, name := range r.Names() {
		entries = append(entries, r.entries[name])
	}
	return entries
}

// Client returns a caller for one backend.
func (r *Registry) Client(name string) (*sandbox.Client, error) {
	entry, ok := r.entries[name]
	if !ok {
		return nil, fmt.Errorf("unknown backend %q; available: %s", name, strings.Join(r.Names(), ", "))
	}
	return sandbox.NewClient(entry.Manifest), nil
}

// Builtins describes the shipped backends as manifests bound to self.
func Builtins(self string) []provider.Manifest {
	return []provider.Manifest{
		builtin(self, Lima, "Linux VM managed by Lima", []string{"limactl"}),
		builtin(self, Pod, "Kubernetes StatefulSet runner", []string{"kubectl"}),
		builtin(self, KubeVirt, "KubeVirt virtual machine runner", []string{"kubectl", "virtctl", "ssh"}),
		builtin(self, Docker,
			"Container sandbox on the local Docker daemon",
			[]string{"docker"}),
		builtin(self, VZ,
			"Linux virtual machine on Apple's Virtualization.framework, driven by lima",
			[]string{"limactl"}),
		builtin(self, QEMU,
			"Linux virtual machine on QEMU, KVM-accelerated where available, driven by lima",
			[]string{"limactl"}),
	}
}

func builtin(self, name, description string, commands []string) provider.Manifest {
	return provider.Manifest{
		Version:     provider.Version,
		Kind:        sandbox.Kind,
		Name:        name,
		Description: description,
		Command:     []string{self, "backend", name},
		Actions: map[string]provider.Action{
			sandbox.Capability: {Description: description},
		},
		Requires: provider.Requirements{Commands: commands},
	}
}

func (r *Registry) Configure(name string, args []string) error {
	entry, ok := r.entries[name]
	if !ok {
		return fmt.Errorf("unknown provider %q", name)
	}
	entry.Manifest.Command = append(entry.Manifest.Command, args...)
	r.entries[name] = entry
	return nil
}
