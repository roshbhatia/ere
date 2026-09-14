package kubernetes

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/roshbhatia/ere/internal/backend"
	"github.com/roshbhatia/ere/internal/sandbox"
)

func (b *Backend) Connect(ctx context.Context, req sandbox.ConnectionRequest) (sandbox.Connection, error) {
	parent, id, err := b.owned(ctx, req.Name)
	if err != nil {
		return sandbox.Connection{}, err
	}
	if parent == nil {
		return sandbox.Connection{}, fmt.Errorf("runner is absent")
	}
	kind, name := "pod", resource(req.Name)+"-0"
	if b.KubeVirt {
		kind, name = "vmi", resource(req.Name)
	}
	child, err := b.get(ctx, kind, name)
	if err != nil {
		return sandbox.Connection{}, err
	}
	valid := false
	if child != nil {
		for _, owner := range child.Metadata.Owners {
			if owner.UID == parent.Metadata.UID {
				valid = true
			}
		}
	}
	if !valid {
		return sandbox.Connection{}, fmt.Errorf("child does not belong to stored runner")
	}
	argv := req.Argv
	if len(argv) == 0 {
		argv = []string{"sh", "-l"}
	}
	script := "exec " + backend.ShellArgv(argv)
	if req.Workdir != "" {
		script = "cd " + backend.Quote(req.Workdir) + " && " + script
	}
	if !b.KubeVirt {
		binary := b.Binary
		if binary == "" {
			binary = "kubectl"
		}
		return sandbox.Connection{Argv: append([]string{binary}, b.args("exec", "-it", name, "-c", "runner", "--", "sh", "-c", script)...)}, nil
	}
	proxy := []string{"virtctl"}
	if b.Kubeconfig != "" {
		proxy = append(proxy, "--kubeconfig", b.Kubeconfig)
	}
	proxy = append(proxy, "--context", b.Context, "--namespace", b.Namespace, "port-forward", "--stdio", "vm/"+name, "22")
	user := b.SSHUser
	if user == "" {
		user = "ere"
	}
	known := filepath.Join(filepath.Dir(id.Path), id.Owner+"-known-hosts")
	return sandbox.Connection{Argv: []string{"ssh", "-t", "-i", b.SSHKey, "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile=" + known, "-o", "HostKeyAlias=" + id.Owner, "-o", "ProxyCommand=" + backend.ShellArgv(proxy), user + "@" + name, "sudo -n sh -c " + backend.Quote(script)}}, nil
}
