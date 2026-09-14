package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/roshbhatia/ere/internal/backend"
	"github.com/roshbhatia/ere/internal/sandbox"
)

func (b *Backend) vm(ctx context.Context, spec sandbox.Spec, id backend.Identity, workspace Object) (Object, error) {
	name := resource(spec.Name)
	user := b.SSHUser
	if user == "" {
		user = "ere"
	}
	public, err := os.ReadFile(b.SSHKey + ".pub")
	if err != nil {
		return nil, err
	}
	hostPath := filepath.Join(filepath.Dir(id.Path), id.Owner+"-host-key")
	if _, err := os.Stat(hostPath); os.IsNotExist(err) {
		if _, err := backend.Check(ctx, "ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-f", hostPath); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	hostPrivate, err := os.ReadFile(hostPath)
	if err != nil {
		return nil, err
	}
	hostPublic, err := os.ReadFile(hostPath + ".pub")
	if err != nil {
		return nil, err
	}
	known := filepath.Join(filepath.Dir(id.Path), id.Owner+"-known-hosts")
	if err := os.WriteFile(known, []byte(id.Owner+" "+strings.TrimSpace(string(hostPublic))+"\n"), 0o600); err != nil {
		return nil, err
	}
	mount := spec.MountPath
	if mount == "" {
		mount = "/workspace"
	}
	work := sandbox.Workload{Argv: []string{"sleep", "infinity"}, Workdir: mount}
	if spec.Workload != nil {
		work = *spec.Workload
	}
	unit := "[Unit]\nAfter=network-online.target\nWants=network-online.target\nRequiresMountsFor=" + mount + "\n[Service]\nUser=root\nType=simple\nExecStart=/bin/sh /var/lib/ere/start\nRestart=always\nRestartSec=3\nKillMode=control-group\n[Install]\nWantedBy=multi-user.target\n"
	script := "set -eu\nmountpoint -q " + backend.Quote(mount) + "\nset -a\n. /var/lib/ere/env\nset +a\n" + backend.WorkloadScript(work)
	cloud := Object{"users": []Object{{"name": user, "sudo": "ALL=(ALL) NOPASSWD:ALL", "shell": "/bin/bash", "ssh_authorized_keys": []string{strings.TrimSpace(string(public))}}}, "ssh_keys": Object{"ed25519_private": string(hostPrivate), "ed25519_public": string(hostPublic)}, "ssh_pwauth": false, "write_files": []Object{{"path": "/var/lib/ere/start", "permissions": "0700", "content": script}, {"path": "/var/lib/ere/env", "permissions": "0600", "content": backend.EnvFile(work.Env)}, {"path": "/etc/systemd/system/ere-workload.service", "permissions": "0644", "content": unit}}, "fs_setup": []Object{{"label": "ere-workspace", "filesystem": "ext4", "device": "/dev/disk/by-id/virtio-workspace", "overwrite": false}}, "mounts": [][]string{{"LABEL=ere-workspace", mount, "ext4", "defaults", "0", "2"}}, "runcmd": [][]string{{"systemctl", "daemon-reload"}, {"systemctl", "enable", "ere-workload"}, {"systemctl", "start", "--no-block", "ere-workload"}}}
	data, err := json.Marshal(cloud)
	if err != nil {
		return nil, err
	}
	if err := b.secret(ctx, spec, id, name+"-cloudinit", map[string]string{"userdata": "#cloud-config\n" + string(data)}); err != nil {
		return nil, err
	}
	root := Object{"name": "root", "containerDisk": Object{"image": spec.Image}}
	if spec.BootVolume != "" {
		found, err := b.get(ctx, "pvc", spec.BootVolume)
		if err != nil {
			return nil, err
		}
		if found == nil {
			return nil, fmt.Errorf("boot PVC %s is absent", spec.BootVolume)
		}
		root = Object{"name": "root", "persistentVolumeClaim": Object{"claimName": spec.BootVolume}}
	}
	cpu := spec.CPUs
	if cpu == 0 {
		cpu = 1
	}
	memory := spec.MemoryMB
	if memory == 0 {
		memory = 2048
	}
	domain := Object{"cpu": Object{"cores": cpu, "model": "host-passthrough"}, "resources": Object{"requests": Object{"memory": strconv.Itoa(memory) + "Mi"}}, "devices": Object{"disks": []Object{{"name": "root", "disk": Object{"bus": "virtio"}}, {"name": "workspace", "serial": "workspace", "disk": Object{"bus": "virtio"}}, {"name": "cloudinit", "disk": Object{"bus": "virtio"}}}, "interfaces": []Object{{"name": "default", "macAddress": macAddress(id.Owner), "masquerade": Object{}}}}}
	arch := architecture(spec)
	if arch == "arm64" {
		domain["machine"] = Object{"type": "virt"}
		domain["firmware"] = Object{"bootloader": Object{"efi": Object{"secureBoot": false}}}
	}
	return Object{"apiVersion": "kubevirt.io/v1", "kind": "VirtualMachine", "metadata": b.meta(spec, id, name), "spec": Object{"runStrategy": "Halted", "template": Object{"metadata": Object{"labels": b.labels(spec, id)}, "spec": Object{"nodeSelector": Object{"kubernetes.io/arch": arch}, "domain": domain, "volumes": []Object{root, workspace, {"name": "cloudinit", "cloudInitNoCloud": Object{"secretRef": Object{"name": name + "-cloudinit"}}}}, "networks": []Object{{"name": "default", "pod": Object{}}}}}}}, nil
}

func macAddress(owner string) string {
	digest := sandbox.Digest(owner)
	return "02:" + digest[0:2] + ":" + digest[2:4] + ":" + digest[4:6] + ":" + digest[6:8] + ":" + digest[8:10]
}
