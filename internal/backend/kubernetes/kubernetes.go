package kubernetes

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/roshbhatia/ere/internal/backend"
	"github.com/roshbhatia/ere/internal/sandbox"
)

type Object map[string]interface{}

type Backend struct {
	Binary, Context, Namespace, Kubeconfig, SSHKey, SSHUser string
	KubeVirt                                                bool
}

func (b *Backend) Name() string {
	if b.KubeVirt {
		return "kubernetes-kubevirt"
	}
	return "kubernetes-pod"
}

func (b *Backend) kind() string {
	if b.KubeVirt {
		return "virtualmachine"
	}
	return "statefulset"
}
func resource(name string) string { return "ere-" + name }
func (b *Backend) scope() string {
	return b.Name() + "|" + b.Kubeconfig + "|" + b.Context + "|" + b.Namespace
}

func (b *Backend) args(args ...string) []string {
	out := []string{"--context", b.Context, "--namespace", b.Namespace}
	if b.Kubeconfig != "" {
		out = append(out, "--kubeconfig", b.Kubeconfig)
	}
	return append(out, args...)
}

func (b *Backend) run(ctx context.Context, input string, args ...string) (string, error) {
	if b.Context == "" || b.Namespace == "" {
		return "", fmt.Errorf("%s requires explicit context and namespace", b.Name())
	}
	binary := b.Binary
	if binary == "" {
		binary = "kubectl"
	}
	if len(args) > 0 && args[0] == "delete" {
		args = append(args, "--interactive=false")
	}
	out, err := backend.RunStdin(ctx, input, binary, b.args(args...)...)
	if err != nil {
		return "", err
	}
	if out.ExitCode != 0 {
		return "", fmt.Errorf("kubectl %s: %s", args[0], strings.TrimSpace(out.Stderr))
	}
	return out.Stdout, nil
}

func (b *Backend) apply(ctx context.Context, obj Object) error {
	data, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	_, err = b.run(ctx, string(data), "apply", "--server-side", "--field-manager=ere", "-f", "-")
	return err
}

type metadata struct {
	Name            string            `json:"name"`
	UID             string            `json:"uid"`
	ResourceVersion string            `json:"resourceVersion"`
	Labels          map[string]string `json:"labels"`
	Annotations     map[string]string `json:"annotations"`
	Owners          []struct {
		UID string `json:"uid"`
	} `json:"ownerReferences"`
}
type record struct {
	Data     map[string]string `json:"data"`
	Metadata metadata          `json:"metadata"`
	Spec     struct {
		Replicas         int    `json:"replicas"`
		RunStrategy      string `json:"runStrategy"`
		StorageClassName string `json:"storageClassName"`
		Resources        struct {
			Requests struct {
				Storage string `json:"storage"`
			} `json:"requests"`
		} `json:"resources"`
	} `json:"spec"`
	Status struct {
		ReadyReplicas int    `json:"readyReplicas"`
		Ready         bool   `json:"ready"`
		Phase         string `json:"phase"`
	} `json:"status"`
}

func (b *Backend) get(ctx context.Context, kind, name string) (*record, error) {
	out, err := b.run(ctx, "", "get", kind, name, "--ignore-not-found", "-o", "json")
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(out) == "" {
		return nil, nil
	}
	var result record
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		return nil, fmt.Errorf("decode %s: %w", kind, err)
	}
	return &result, nil
}

func (b *Backend) owned(ctx context.Context, name string) (*record, backend.Identity, error) {
	id, err := backend.LoadIdentity(b.scope(), name)
	if err != nil {
		return nil, id, err
	}
	r, err := b.get(ctx, b.kind(), resource(name))
	if err != nil || r == nil {
		return r, id, err
	}
	if r.Metadata.Labels["ere.owner"] != id.Owner || (id.UID != "" && id.UID != r.Metadata.UID) {
		return nil, id, fmt.Errorf("refusing foreign %s %s", b.kind(), resource(name))
	}
	if id.UID == "" {
		id.UID = r.Metadata.UID
		err = id.Save()
	}
	return r, id, err
}

func (b *Backend) Probe(ctx context.Context) (sandbox.Probe, error) {
	p := sandbox.Probe{Backend: b.Name(), Operations: append(sandbox.Operations(), sandbox.OpValidate), Contract: sandbox.ContractVersion, StorageModes: []string{"pvc", "ephemeral"}, Supervision: "StatefulSet", Retention: "PVCs retained; ephemeral storage is lost"}
	if b.KubeVirt {
		p.Supervision = "VirtualMachine and systemd"
		p.Retention = "workspace and boot PVCs retained; container-disk roots are ephemeral"
		p.StorageModes = []string{"pvc"}
	}
	if _, err := b.run(ctx, "", "get", "namespace", b.Namespace); err != nil {
		p.Detail = err.Error()
		return p, nil
	}
	if _, err := b.run(ctx, "", "get", b.kind(), "--chunk-size=1"); err != nil {
		p.Detail = err.Error()
		return p, nil
	}
	for _, verb := range []string{"get", "list", "create", "patch", "delete"} {
		out, err := b.run(ctx, "", "auth", "can-i", verb, b.kind())
		if err != nil || strings.TrimSpace(out) != "yes" {
			p.Detail = "missing " + verb + " permission on " + b.kind()
			return p, nil
		}
	}
	p.Available = true
	p.Detail = b.Context + "/" + b.Namespace
	return p, nil
}

func (b *Backend) validate(spec sandbox.Spec) error {
	if err := sandbox.Validate(spec); err != nil {
		return err
	}
	if b.Context == "" || b.Namespace == "" {
		return fmt.Errorf("explicit context and namespace are required")
	}
	if spec.Workspace != "" {
		return fmt.Errorf("kubernetes cannot mount controller-local workspace paths; configure storage.kind=pvc or ephemeral")
	}
	if spec.Storage.Kind != "pvc" && spec.Storage.Kind != "ephemeral" {
		return fmt.Errorf("kubernetes requires explicit pvc or ephemeral storage")
	}
	if spec.Image == "" && (!b.KubeVirt || spec.BootVolume == "") {
		return fmt.Errorf("image is required")
	}
	if spec.Architecture != "" && spec.Architecture != "amd64" && spec.Architecture != "arm64" {
		return fmt.Errorf("architecture must be amd64 or arm64")
	}
	if !b.KubeVirt && spec.BootVolume != "" {
		return fmt.Errorf("bootVolume is only supported by KubeVirt")
	}
	if spec.DiskGB != 0 {
		return fmt.Errorf("configure storage.sizeGB instead of diskGB")
	}
	if spec.Storage.Source != "" && (spec.Storage.Class != "" || spec.Storage.SizeGB != 0) {
		return fmt.Errorf("existing PVC source cannot specify class or size; manage it separately")
	}
	if spec.Storage.Kind == "ephemeral" && (spec.Storage.Source != "" || spec.Storage.Class != "" || spec.Storage.SizeGB != 0) {
		return fmt.Errorf("ephemeral storage cannot specify a PVC source, class, or size")
	}
	if b.KubeVirt {
		if spec.Architecture == "" {
			return fmt.Errorf("KubeVirt requires an explicit guest architecture")
		}
		if spec.ReadOnly {
			return fmt.Errorf("KubeVirt workspace must be writable")
		}
		if spec.Image != "" && spec.BootVolume != "" {
			return fmt.Errorf("choose either image or bootVolume")
		}
		if spec.Storage.Kind != "pvc" {
			return fmt.Errorf("KubeVirt requires persistent workspace storage")
		}
		if b.SSHKey == "" {
			return fmt.Errorf("KubeVirt requires sshKey for guest transport")
		}
		if _, err := os.Stat(b.SSHKey); err != nil {
			return fmt.Errorf("sshKey: %w", err)
		}
		if _, err := os.ReadFile(b.SSHKey + ".pub"); err != nil {
			return fmt.Errorf("SSH public key: %w", err)
		}
	}
	return nil
}

func (b *Backend) labels(spec sandbox.Spec, id backend.Identity) map[string]string {
	labels := map[string]string{"app.kubernetes.io/name": "ere", "app.kubernetes.io/instance": resource(spec.Name), "app.kubernetes.io/component": "runner", "app.kubernetes.io/part-of": "ere", "app.kubernetes.io/managed-by": "ere", "ere.owner": id.Owner, "ere.sandbox": spec.Name, "ere.backend": b.Name()}
	for key, value := range spec.Labels {
		if _, reserved := labels[key]; !reserved {
			labels[key] = value
		}
	}
	return labels
}

func (b *Backend) meta(spec sandbox.Spec, id backend.Identity, name string) Object {
	return Object{"name": name, "namespace": b.Namespace, "labels": b.labels(spec, id), "annotations": map[string]string{"ere.digest": sandbox.Digest(spec)}}
}

func (b *Backend) ensurePVC(ctx context.Context, spec sandbox.Spec, id backend.Identity) (string, error) {
	name := spec.Storage.Source
	if name != "" {
		found, err := b.get(ctx, "pvc", name)
		if err != nil {
			return "", err
		}
		if found == nil {
			return "", fmt.Errorf("external PVC %s does not exist", name)
		}
		return name, nil
	}
	size := spec.Storage.SizeGB
	if size == 0 {
		size = 10
	}
	name = resource(spec.Name) + "-workspace"
	found, err := b.get(ctx, "pvc", name)
	if err != nil {
		return "", err
	}
	if found != nil {
		if found.Metadata.Labels["ere.owner"] != id.Owner {
			return "", fmt.Errorf("refusing foreign PVC %s", name)
		}
		if found.Spec.Resources.Requests.Storage != strconv.Itoa(size)+"Gi" || (spec.Storage.Class != "" && found.Spec.StorageClassName != spec.Storage.Class) {
			return "", fmt.Errorf("retained PVC size or class differs; use its existing settings or a separately managed storage.source")
		}
		return name, nil
	}
	claim := Object{"accessModes": []string{"ReadWriteOnce"}, "resources": Object{"requests": Object{"storage": strconv.Itoa(size) + "Gi"}}}
	if spec.Storage.Class != "" {
		claim["storageClassName"] = spec.Storage.Class
	}
	return name, b.create(ctx, Object{"apiVersion": "v1", "kind": "PersistentVolumeClaim", "metadata": b.meta(spec, id, name), "spec": claim})
}

func (b *Backend) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Status, error) {
	if err := b.validate(spec); err != nil {
		return sandbox.Status{}, err
	}
	existing, id, err := b.owned(ctx, spec.Name)
	if err != nil {
		return sandbox.Status{}, err
	}
	digest := sandbox.Digest(spec)
	if existing != nil {
		if existing.Metadata.Annotations["ere.digest"] != digest {
			return sandbox.Status{}, fmt.Errorf("configuration changed: stop and remove compute before recreating; PVCs are retained")
		}
		if b.KubeVirt {
			_, err = b.vm(ctx, spec, id, Object{})
		} else {
			_, err = b.pod(ctx, spec, id, Object{})
		}
		if err != nil {
			return sandbox.Status{}, err
		}
		return b.Status(ctx, sandbox.Ref{Name: spec.Name})
	}
	id.UID = ""
	if err := id.Save(); err != nil {
		return sandbox.Status{}, err
	}
	volume := Object{"name": "workspace", "emptyDir": Object{}}
	if spec.Storage.Kind == "pvc" {
		name, err := b.ensurePVC(ctx, spec, id)
		if err != nil {
			return sandbox.Status{}, err
		}
		volume = Object{"name": "workspace", "persistentVolumeClaim": Object{"claimName": name}}
	}
	var obj Object
	if b.KubeVirt {
		obj, err = b.vm(ctx, spec, id, volume)
	} else {
		obj, err = b.pod(ctx, spec, id, volume)
	}
	if err != nil {
		return sandbox.Status{}, err
	}
	if err := b.create(ctx, obj); err != nil {
		return sandbox.Status{}, err
	}
	return b.Status(ctx, sandbox.Ref{Name: spec.Name})
}

func (b *Backend) pod(ctx context.Context, spec sandbox.Spec, id backend.Identity, volume Object) (Object, error) {
	name := resource(spec.Name)
	mount := spec.MountPath
	if mount == "" {
		mount = "/workspace"
	}
	command := []string{"sleep", "infinity"}
	env := map[string]string{}
	if spec.Workload != nil {
		command = []string{"sh", "-c", backend.WorkloadScript(*spec.Workload)}
		env = spec.Workload.Env
	}
	if err := b.secret(ctx, spec, id, name+"-env", env); err != nil {
		return nil, err
	}
	cpu := spec.CPUs
	if cpu == 0 {
		cpu = 1
	}
	memory := spec.MemoryMB
	if memory == 0 {
		memory = 1024
	}
	container := Object{"name": "runner", "image": spec.Image, "imagePullPolicy": "IfNotPresent", "command": command, "envFrom": []Object{{"secretRef": Object{"name": name + "-env"}}}, "volumeMounts": []Object{{"name": "workspace", "mountPath": mount, "readOnly": spec.ReadOnly}}, "resources": Object{"requests": Object{"cpu": strconv.Itoa(cpu), "memory": strconv.Itoa(memory) + "Mi"}}}
	podSpec := Object{"automountServiceAccountToken": false, "terminationGracePeriodSeconds": 30, "containers": []Object{container}, "volumes": []Object{volume}}
	if spec.Architecture != "" {
		podSpec["nodeSelector"] = Object{"kubernetes.io/arch": spec.Architecture}
	}
	return Object{"apiVersion": "apps/v1", "kind": "StatefulSet", "metadata": b.meta(spec, id, name), "spec": Object{"replicas": 0, "serviceName": name, "selector": Object{"matchLabels": Object{"ere.owner": id.Owner}}, "persistentVolumeClaimRetentionPolicy": Object{"whenDeleted": "Retain", "whenScaled": "Retain"}, "template": Object{"metadata": Object{"labels": b.labels(spec, id)}, "spec": podSpec}}}, nil
}

func (b *Backend) secret(ctx context.Context, spec sandbox.Spec, id backend.Identity, name string, data map[string]string) error {
	old, err := b.get(ctx, "secret", name)
	if err != nil {
		return err
	}
	if old != nil && old.Metadata.Labels["ere.owner"] != id.Owner {
		return fmt.Errorf("refusing foreign secret %s", name)
	}
	meta := b.meta(spec, id, name)
	obj := Object{"apiVersion": "v1", "kind": "Secret", "type": "Opaque", "metadata": meta, "stringData": data}
	if old == nil {
		return b.create(ctx, obj)
	}
	meta["resourceVersion"] = old.Metadata.ResourceVersion
	meta["uid"] = old.Metadata.UID
	return b.apply(ctx, obj)
}

func (b *Backend) Status(ctx context.Context, ref sandbox.Ref) (sandbox.Status, error) {
	status := sandbox.Status{Name: ref.Name, Backend: b.Name(), State: sandbox.StateAbsent}
	r, _, err := b.owned(ctx, ref.Name)
	if err != nil || r == nil {
		return status, err
	}
	status.ResourceID = r.Metadata.UID
	status.Digest = r.Metadata.Annotations["ere.digest"]
	status.Managed = true
	status.State = sandbox.StateStarting
	if b.KubeVirt {
		if r.Spec.RunStrategy == "Halted" {
			status.State = sandbox.StateStopped
		} else if r.Status.Ready {
			status.State = sandbox.StateRunning
		}
	} else {
		if r.Spec.Replicas == 0 {
			status.State = sandbox.StateStopped
		} else if r.Status.ReadyReplicas > 0 {
			status.State = sandbox.StateRunning
		}
	}
	status.Detail = b.Context + "/" + b.Namespace
	return status, nil
}

func (b *Backend) Start(ctx context.Context, ref sandbox.Ref) (sandbox.Status, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	r, id, err := b.owned(ctx, ref.Name)
	if err != nil {
		return sandbox.Status{}, err
	}
	if r == nil {
		return sandbox.Status{}, fmt.Errorf("sandbox %s is absent", ref.Name)
	}
	patch := `{"spec":{"replicas":1}}`
	if b.KubeVirt {
		patch = `{"spec":{"runStrategy":"Always"}}`
	}
	if err := b.patchOwned(ctx, r, patch); err != nil {
		return sandbox.Status{}, err
	}
	var lastError error
	for {
		status, err := b.Status(ctx, ref)
		if err != nil {
			return status, err
		}
		if status.State == sandbox.StateRunning {
			if !b.KubeVirt {
				return status, nil
			}
			lastError = b.installGuestWorkload(ctx, ref.Name, id)
			if lastError == nil {
				return status, nil
			}
		}
		select {
		case <-ctx.Done():
			return status, fmt.Errorf("startup: %w; last guest error: %v", ctx.Err(), lastError)
		case <-time.After(2 * time.Second):
		}
	}
}

func (b *Backend) Stop(ctx context.Context, ref sandbox.Ref) (sandbox.Status, error) {
	r, _, err := b.owned(ctx, ref.Name)
	if err != nil || r == nil {
		return sandbox.Status{Name: ref.Name, Backend: b.Name(), State: sandbox.StateAbsent}, err
	}
	patch := `{"spec":{"replicas":0}}`
	child := "pod"
	childName := resource(ref.Name) + "-0"
	if b.KubeVirt {
		patch = `{"spec":{"runStrategy":"Halted"}}`
		child = "vmi"
		childName = resource(ref.Name)
	}
	if err := b.patchOwned(ctx, r, patch); err != nil {
		return sandbox.Status{}, err
	}
	if _, err := b.run(ctx, "", "wait", "--for=delete", child+"/"+childName, "--timeout=180s"); err != nil {
		return sandbox.Status{}, err
	}
	return b.Status(ctx, ref)
}

func (b *Backend) Destroy(ctx context.Context, ref sandbox.Ref) (sandbox.Status, error) {
	r, id, err := b.owned(ctx, ref.Name)
	if err != nil {
		return sandbox.Status{}, err
	}
	if r != nil {
		if _, err := b.Stop(ctx, ref); err != nil {
			return sandbox.Status{}, err
		}
		r, _, err = b.owned(ctx, ref.Name)
		if err != nil {
			return sandbox.Status{}, err
		}
		if r != nil {
			if err := b.deleteOwned(ctx, b.kind(), r); err != nil {
				return sandbox.Status{}, err
			}
		}
	}
	for _, suffix := range []string{"-env", "-cloudinit"} {
		item, err := b.get(ctx, "secret", resource(ref.Name)+suffix)
		if err != nil {
			return sandbox.Status{}, err
		}
		if item != nil {
			if item.Metadata.Labels["ere.owner"] != id.Owner {
				return sandbox.Status{}, fmt.Errorf("refusing foreign secret")
			}
			if err := b.deleteOwned(ctx, "secret", item); err != nil {
				return sandbox.Status{}, err
			}
		}
	}
	id.UID = ""
	if err := id.Save(); err != nil {
		return sandbox.Status{}, err
	}
	return sandbox.Status{Name: ref.Name, Backend: b.Name(), State: sandbox.StateAbsent, Detail: "workspace and boot PVCs retained"}, nil
}

func (b *Backend) List(ctx context.Context) (sandbox.List, error) {
	out, err := b.run(ctx, "", "get", b.kind(), "-l", "app.kubernetes.io/managed-by=ere,ere.backend="+b.Name(), "-o", "json")
	if err != nil {
		return sandbox.List{}, err
	}
	var list struct {
		Items []record `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		return sandbox.List{}, err
	}
	result := sandbox.List{Sandboxes: []sandbox.Status{}}
	for _, r := range list.Items {
		status, err := b.Status(ctx, sandbox.Ref{Name: r.Metadata.Labels["ere.sandbox"]})
		if err != nil {
			return result, err
		}
		result.Sandboxes = append(result.Sandboxes, status)
	}
	return result, nil
}

func (b *Backend) Exec(ctx context.Context, req sandbox.ExecRequest) (sandbox.ExecResult, error) {
	parent, id, err := b.owned(ctx, req.Name)
	if err != nil {
		return sandbox.ExecResult{}, err
	}
	if parent == nil {
		return sandbox.ExecResult{}, fmt.Errorf("sandbox is absent")
	}
	script, err := backend.ExecScript(req)
	if err != nil {
		return sandbox.ExecResult{}, err
	}
	if b.KubeVirt {
		child, err := b.get(ctx, "vmi", resource(req.Name))
		if err != nil {
			return sandbox.ExecResult{}, err
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
			return sandbox.ExecResult{}, fmt.Errorf("VMI does not belong to stored VM")
		}
		return b.guestExec(ctx, req.Name, id, script)
	}
	child, err := b.get(ctx, "pod", resource(req.Name)+"-0")
	if err != nil {
		return sandbox.ExecResult{}, err
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
		return sandbox.ExecResult{}, fmt.Errorf("runner Pod does not belong to the stored StatefulSet")
	}
	binary := b.Binary
	if binary == "" {
		binary = "kubectl"
	}
	out, err := backend.RunStdin(ctx, script, binary, b.args("exec", "-i", resource(req.Name)+"-0", "-c", "runner", "--", "sh", "-s")...)
	return sandbox.ExecResult{ExitCode: out.ExitCode, Stdout: out.Stdout, Stderr: out.Stderr}, err
}

func (b *Backend) Logs(ctx context.Context, req sandbox.LogRequest) (sandbox.Logs, error) {
	lines := req.Lines
	if lines <= 0 {
		lines = 200
	}
	if b.KubeVirt {
		out, err := b.Exec(ctx, sandbox.ExecRequest{Name: req.Name, Argv: []string{"sudo", "journalctl", "-u", "ere-workload", "-n", strconv.Itoa(lines), "--no-pager"}})
		if err != nil {
			return sandbox.Logs{}, err
		}
		if out.ExitCode != 0 {
			return sandbox.Logs{}, fmt.Errorf("guest logs: %s", out.Stderr)
		}
		return sandbox.Logs{Lines: strings.Split(strings.TrimRight(out.Stdout, "\n"), "\n")}, nil
	}
	parent, _, err := b.owned(ctx, req.Name)
	if err != nil {
		return sandbox.Logs{}, err
	}
	child, err := b.get(ctx, "pod", resource(req.Name)+"-0")
	if err != nil {
		return sandbox.Logs{}, err
	}
	valid := false
	if parent != nil && child != nil {
		for _, owner := range child.Metadata.Owners {
			if owner.UID == parent.Metadata.UID {
				valid = true
			}
		}
	}
	if !valid {
		return sandbox.Logs{}, fmt.Errorf("runner Pod does not belong to stored StatefulSet")
	}
	out, err := b.run(ctx, "", "logs", resource(req.Name)+"-0", "-c", "runner", "--tail="+strconv.Itoa(lines), "--timestamps")
	return sandbox.Logs{Lines: strings.Split(strings.TrimRight(out, "\n"), "\n")}, err
}

func (b *Backend) guestExec(ctx context.Context, name string, id backend.Identity, script string) (sandbox.ExecResult, error) {
	user := b.SSHUser
	if user == "" {
		user = "ere"
	}
	proxy := []string{"virtctl"}
	if b.Kubeconfig != "" {
		proxy = append(proxy, "--kubeconfig", b.Kubeconfig)
	}
	proxy = append(proxy, "--context", b.Context, "--namespace", b.Namespace, "port-forward", "--stdio", "vm/"+resource(name), "22")
	known := filepath.Join(filepath.Dir(id.Path), id.Owner+"-known-hosts")
	out, err := backend.RunStdin(ctx, script, "ssh", "-T", "-i", b.SSHKey, "-o", "IdentitiesOnly=yes", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10", "-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile="+known, "-o", "HostKeyAlias="+id.Owner, "-o", "ProxyCommand="+backend.ShellArgv(proxy), user+"@"+resource(name), "sudo -n sh -s")
	return sandbox.ExecResult{ExitCode: out.ExitCode, Stdout: out.Stdout, Stderr: out.Stderr}, err
}

func (b *Backend) patchOwned(ctx context.Context, r *record, patch string) error {
	var obj Object
	if err := json.Unmarshal([]byte(patch), &obj); err != nil {
		return err
	}
	obj["metadata"] = Object{"resourceVersion": r.Metadata.ResourceVersion, "uid": r.Metadata.UID}
	data, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	_, err = b.run(ctx, "", "patch", b.kind(), r.Metadata.Name, "--type=merge", "-p", string(data))
	return err
}

func (b *Backend) deleteOwned(ctx context.Context, kind string, r *record) error {
	path := "/api/v1/namespaces/" + b.Namespace + "/secrets/" + r.Metadata.Name
	if kind == "statefulset" {
		path = "/apis/apps/v1/namespaces/" + b.Namespace + "/statefulsets/" + r.Metadata.Name
	}
	if kind == "virtualmachine" {
		path = "/apis/kubevirt.io/v1/namespaces/" + b.Namespace + "/virtualmachines/" + r.Metadata.Name
	}
	data, err := json.Marshal(Object{"apiVersion": "v1", "kind": "DeleteOptions", "propagationPolicy": "Foreground", "preconditions": Object{"uid": r.Metadata.UID, "resourceVersion": r.Metadata.ResourceVersion}})
	if err != nil {
		return err
	}
	_, err = b.run(ctx, string(data), "delete", "--raw", path, "-f", "-")
	if err != nil {
		return err
	}
	_, err = b.run(ctx, "", "wait", "--for=delete", kind+"/"+r.Metadata.Name, "--timeout=180s")
	return err
}

func architecture(spec sandbox.Spec) string {
	if spec.Architecture != "" {
		return spec.Architecture
	}
	return runtime.GOARCH
}

func (b *Backend) Validate(ctx context.Context, spec sandbox.Spec) (sandbox.Plan, error) {
	err := b.validate(spec)
	return sandbox.Plan{Name: spec.Name, Backend: b.Name(), Digest: sandbox.Digest(spec), Retention: "PVCs retained"}, err
}

func (b *Backend) create(ctx context.Context, obj Object) error {
	data, err := json.Marshal(obj)
	if err != nil {
		return err
	}
	_, err = b.run(ctx, string(data), "create", "-f", "-")
	return err
}

func (b *Backend) installGuestWorkload(ctx context.Context, name string, id backend.Identity) error {
	secret, err := b.get(ctx, "secret", resource(name)+"-cloudinit")
	if err != nil {
		return err
	}
	if secret == nil || secret.Metadata.Labels["ere.owner"] != id.Owner {
		return fmt.Errorf("workload secret is absent or foreign")
	}
	data, err := base64.StdEncoding.DecodeString(secret.Data["userdata"])
	if err != nil {
		return fmt.Errorf("decode workload secret: %w", err)
	}
	var cloud struct {
		Files []struct {
			Path    string
			Content string
		} `json:"write_files"`
	}
	if err := json.Unmarshal([]byte(strings.TrimPrefix(string(data), "#cloud-config\n")), &cloud); err != nil {
		return fmt.Errorf("decode guest workload: %w", err)
	}
	script := "set -eu\numask 077\nmkdir -p /var/lib/ere\n"
	expected := map[string]bool{"/var/lib/ere/start": false, "/var/lib/ere/env": false, "/etc/systemd/system/ere-workload.service": false}
	for _, file := range cloud.Files {
		if _, ok := expected[file.Path]; !ok {
			return fmt.Errorf("unexpected workload file path")
		}
		expected[file.Path] = true
		script += "printf %s " + backend.Quote(file.Content) + " > " + backend.Quote(file.Path) + "\n"
	}
	for _, found := range expected {
		if !found {
			return fmt.Errorf("incomplete workload secret")
		}
	}
	script += "systemctl daemon-reload\nsystemctl enable --now ere-workload\n"
	out, err := b.guestExec(ctx, name, id, script)
	if err != nil {
		return err
	}
	if out.ExitCode != 0 {
		return fmt.Errorf("install guest workload: %s", out.Stderr)
	}
	return nil
}
