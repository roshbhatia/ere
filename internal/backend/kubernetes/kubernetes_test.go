package kubernetes

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/roshbhatia/ere/internal/backend"
	"github.com/roshbhatia/ere/internal/sandbox"
)

func fake(t *testing.T, response string) *Backend {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	dir := t.TempDir()
	script := "#!/bin/sh\ncase \" $* \" in *' get '*) printf '%s' " + backend.Quote(response) + ";; *) cat >/dev/null;; esac\n"
	path := filepath.Join(dir, "kubectl")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return &Backend{Binary: path, Context: "isolated", Namespace: "runners"}
}

func TestForeignStatefulSetIsNeverAdopted(t *testing.T) {
	b := fake(t, `{"metadata":{"name":"ere-worker","uid":"foreign","labels":{"ere.owner":"someone-else"}}}`)
	if _, err := b.Status(context.Background(), sandbox.Ref{Name: "worker"}); err == nil || !strings.Contains(err.Error(), "foreign") {
		t.Fatalf("foreign ownership accepted: %v", err)
	}
}

func TestPodManifestRetainsWorkspaceAndOmitsAccountToken(t *testing.T) {
	b := fake(t, "")
	spec := sandbox.Spec{Name: "worker", Image: "runner:1", Storage: sandbox.Storage{Kind: "pvc"}, Workload: &sandbox.Workload{Argv: []string{"sleep", "infinity"}, Env: map[string]string{"TEST_SECRET": "sensitive-value"}}}
	obj, err := b.pod(context.Background(), spec, backend.Identity{Owner: "owner"}, Object{"name": "workspace", "persistentVolumeClaim": Object{"claimName": "work"}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{`"replicas":0`, `"automountServiceAccountToken":false`, `"whenDeleted":"Retain"`, `"secretRef"`, `"ere.owner":"owner"`} {
		if !strings.Contains(string(data), required) {
			t.Fatalf("missing %s", required)
		}
	}
	if strings.Contains(string(data), "sensitive-value") {
		t.Fatal("secret in workload declaration")
	}
}

func TestRejectsLocalMountAndMissingScope(t *testing.T) {
	b := fake(t, "")
	for _, spec := range []sandbox.Spec{{Name: "worker", Image: "ubuntu", Workspace: "/host/path", Storage: sandbox.Storage{Kind: "pvc"}}, {Name: "worker", Image: "ubuntu"}} {
		if _, err := b.Create(context.Background(), spec); err == nil {
			t.Fatal("unsupported storage accepted")
		}
	}
	b.Context = ""
	if _, err := b.run(context.Background(), "", "get", "pods"); err == nil {
		t.Fatal("ambient context accepted")
	}
}

func TestRejectsReplacedNativeUID(t *testing.T) {
	b := fake(t, "")
	id, err := backend.LoadIdentity(b.scope(), "worker")
	if err != nil {
		t.Fatal(err)
	}
	id.UID = "old"
	if err := id.Save(); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(Object{"metadata": Object{"name": "ere-worker", "uid": "new", "labels": Object{"ere.owner": id.Owner}}})
	script := "#!/bin/sh\nprintf '%s' " + backend.Quote(string(data)) + "\n"
	if err := os.WriteFile(b.Binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Stop(context.Background(), sandbox.Ref{Name: "worker"}); err == nil {
		t.Fatal("replaced resource accepted")
	}
}

func TestVMKeepsNetworkIdentityAndStoresKeysOutsideManifest(t *testing.T) {
	b := fake(t, "")
	b.KubeVirt = true
	dir := t.TempDir()
	captured := filepath.Join(dir, "secret.json")
	script := "#!/bin/sh\ncase \" $* \" in *' get '*) exit 0;; *) cat > " + backend.Quote(captured) + ";; esac\n"
	if err := os.WriteFile(b.Binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	b.SSHKey = filepath.Join(dir, "ssh-key")
	id := backend.Identity{Owner: "test-owner", Path: filepath.Join(dir, "identity.json")}
	for name, value := range map[string]string{b.SSHKey + ".pub": "ssh-ed25519 guest-key", filepath.Join(dir, "test-owner-host-key"): "private-host-key", filepath.Join(dir, "test-owner-host-key.pub"): "ssh-ed25519 host-key"} {
		if err := os.WriteFile(name, []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	spec := sandbox.Spec{Name: "worker", Image: "cloud-image", Architecture: "arm64", Workload: &sandbox.Workload{Argv: []string{"amp", "--no-tui"}}}
	obj, err := b.vm(context.Background(), spec, id, Object{"name": "workspace", "persistentVolumeClaim": Object{"claimName": "work"}})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"macAddress":"` + macAddress(id.Owner) + `"`, `"runStrategy":"Halted"`, `"secureBoot":false`, `"kubernetes.io/arch":"arm64"`, `"secretRef"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("missing %s", want)
		}
	}
	if strings.Contains(string(data), "private-host-key") {
		t.Fatal("private host key leaked into VM declaration")
	}
	secretData, err := os.ReadFile(captured)
	if err != nil {
		t.Fatal(err)
	}
	var secret struct {
		StringData map[string]string `json:"stringData"`
	}
	if err := json.Unmarshal(secretData, &secret); err != nil {
		t.Fatal(err)
	}
	cloud := secret.StringData["userdata"]
	if strings.Contains(cloud, "After=network-online.target cloud-final.service") {
		t.Fatal("workload creates a boot ordering cycle")
	}
	for _, required := range []string{"RequiresMountsFor=/workspace", "mountpoint -q", "0600"} {
		if !strings.Contains(cloud, required) {
			t.Fatalf("missing guest bootstrap requirement %s", required)
		}
	}
	if macAddress(id.Owner) == macAddress("different-owner") {
		t.Fatal("different VMs share network identity")
	}
}
