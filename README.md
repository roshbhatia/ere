# lifier

Run [Amp runners](https://ampcode.com/docs/cli/runners) on Docker, Lima, Kubernetes Pods, or Kubernetes KubeVirt VMs.
Lifier owns compute and workspace lifecycle. Amp owns threads and model-provider authentication, including its ChatGPT subscription integration.

## Providers

| Provider | Compute | Supervision | Workspace retention |
|---|---|---|---|
| `docker` | Container on the selected Docker context | Container entrypoint and restart policy | Bind mounts and named volumes survive removal |
| `lima` | Local Linux VM; `vmType: auto`, `vz`, or `qemu` | systemd | Guest disks survive stop; host mounts survive removal |
| `kubernetes-pod` | StatefulSet with one runner Pod | Kubernetes restarts and replaces containers | PVC survives Pod replacement and runner removal |
| `kubernetes-kubevirt` | KubeVirt VirtualMachine | VM run strategy and guest systemd | Workspace and boot PVCs survive compute removal |

`vz` and `qemu` remain available for existing configurations and runner IDs.
Existing runners created with detached Amp processes retain that behavior until explicitly replaced.
A Lima-hosted Kubernetes cluster is a development fixture. The Kubernetes providers connect to any compatible cluster with an explicit context and namespace.

## Build and configure

```sh
nix develop
task build
export PATH="$PWD:$PATH"
lifier backends
```

Copy [examples/lifier.yaml](examples/lifier.yaml) to `~/.config/lifier/config.yaml`, or pass `--config` explicitly.
Run named profiles while configuring the examples; bare `up` selects every declared runner.
The example image registry, kubeconfig, context, and SSH key are placeholders to replace.

Configure your model provider through Amp itself. Lifier does not copy ChatGPT login files or implement a separate subscription adapter.
An Amp API credential authenticates the runner connection; it is separate from model-provider billing.
Reference it with `amp.apiKeySecret: env://AMP_API_KEY`, `op://...`, or a runner secret reference.
Do not put credentials in provider command arguments, labels, or committed files.
Link the subscription once on your Amp account:

```sh
amp config model-providers add-chatgpt-subscription
amp config model-providers list
```

Subscription billing follows Amp model routing. It does not cover every model or mode.
See [Amp model providers](https://ampcode.com/docs/the-dial) and [runners](https://ampcode.com/docs/cli/runners).

```sh
lifier --config examples/lifier.yaml plan docker-example
lifier --config examples/lifier.yaml up docker-example
lifier --config examples/lifier.yaml ls --json
amp --executor runner:lifier-docker-example --mode high -x 'Inspect this workspace'
```

`up` validates credentials before creating compute. New managed runners use native supervision.
A running process does not prove remote registration; submit an Amp task to prove the complete connection.

## Docker

```sh
task image:build
lifier --config examples/lifier.yaml up docker-example
```

A Docker bind path belongs to the daemon host. For remote daemons, configure a daemon-host path or a named volume:

```yaml
providers:
  docker:
    context: remote-dev
runners:
  - name: remote-worker
    backend: docker
    image: your-registry/amp:version
    storage:
      kind: volume
      source: remote-worker-workspace
```

The named volume is retained. Lifier does not upload a local directory into a remote daemon automatically.
Credentials are installed through stdin in a private container file; command execution uses an independent stdin environment for each invocation.

## Lima

```sh
lifier --config examples/lifier.yaml up lima-example
lifier --config examples/lifier.yaml exec lima-example -- uname -a
```

The canonical provider selects VZ on macOS and QEMU on Linux. Templates are configurable through `image`.
Use a host workspace mount or `storage: {kind: guest-disk}` for a workspace inside the VM.
VM removal deletes guest disks. Stop the runner to retain those disks.
The default Linux guest must provide systemd and passwordless sudo for the Lima user.

The [Amp Lima template](examples/lima/amp.yaml) installs Amp, Git, and CA certificates.
It exposes `amp` through `/usr/local/bin/amp` for guest shells and leaves host directories unmounted.
Use its absolute host path as a Lima runner's `image`; Lifier supplies the workspace mount and manages the runner process.
For example: `image: /absolute/path/to/lifier/examples/lima/amp.yaml`.
The existing example profile also exposes Amp through `/usr/local/bin/amp` during provisioning.

To create a standalone VM with the [Lima template commands](https://lima-vm.io/docs/templates/):

```sh
limactl start --tty=false --name amp-shell examples/lima/amp.yaml
limactl shell amp-shell amp --version
limactl shell amp-shell
```

The standalone template installs the CLI. Configure authentication and start a runner separately, or use Lifier to manage both.
A shell user has separate Amp credentials from Lifier's managed workload.
An existing managed runner already runs Amp; opening its shell does not require another `amp --no-tui` process.

## Kubernetes Pod

Prepare a namespace and an image containing Amp, a POSIX shell, and the tools your tasks need.
Push the [Docker image](images/amp/Dockerfile) to your registry and set the profile's `image`.
The provider supports PVC storage or explicitly ephemeral storage. It rejects local workspace paths.

```sh
kubectl --context your-context create namespace lifier
lifier --config examples/lifier.yaml plan pod-example
lifier --config examples/lifier.yaml up pod-example
```

An empty PVC source creates an owned workspace claim. `storage.source` names an existing external PVC.
Runner removal retains both kinds of PVC. Lifier never deletes external claims.
The Pod does not mount a Kubernetes service-account token.
A replacement Pod restores the declared workload and uses the same workspace claim.

## Kubernetes KubeVirt

Install KubeVirt in the target cluster and configure a guest SSH key.
Use an existing boot PVC or a container-disk image. Container disks have ephemeral root filesystems.
For persistent boot storage, import a cloud image with CDI, for example [boot-volume.yaml](examples/kubernetes/boot-volume.yaml).
Change its architecture-specific image URL when targeting AMD64.

```sh
ssh-keygen -t ed25519 -f ~/.ssh/lifier
kubectl --context your-context apply -f examples/kubernetes/boot-volume.yaml
lifier --config examples/lifier.yaml up kubevirt-example
```

The cloud image must support cloud-init and systemd. Set `architecture` to match the guest image and eligible cluster nodes.
Lifier configures the guest user, pins an SSH host key, mounts the workspace disk, and installs the supervised workload.
Guest execution uses SSH through `virtctl port-forward`; it does not execute commands in the launcher container.
Existing boot images used with another cloud-init identity need preparation before reuse.

For local development on an Apple M3 or newer, [the Lima fixture](examples/lima/kubernetes.yaml) enables nested virtualization and installs pinned K3s.
Its kubeconfig remains separate from the host's default context:

```sh
limactl start --tty=false --name lifier-provider-test examples/lima/kubernetes.yaml
kubectl --kubeconfig ~/.lima/lifier-provider-test/copied-from-guest/kubeconfig.yaml get nodes
```

Install pinned KubeVirt and CDI explicitly in that test cluster:

```sh
task kubevirt:bootstrap -- ~/.lima/lifier-provider-test/copied-from-guest/kubeconfig.yaml default
```

The repository Nix shell supplies `kubectl` and `virtctl`. The fixture uses host API port 16443.
The fixture does not install KubeVirt or CDI implicitly during runner creation.

See [the provider contract](docs/providers.md) for operations, ownership, storage, and allocation state.

## Lifecycle and automation

```sh
lifier plan worker
lifier up worker
lifier logs worker -n 100
lifier down worker
lifier rm worker
```

`plan` identifies creates, starts, and replacements. Changed managed configurations require explicit compute replacement.
Check retention before removing a runner: container root filesystems and Lima guest disks are not retained by `rm`.
`down` retains durable disks; explicitly ephemeral Pod storage is lost when the Pod is removed.
Provisioning uses a digest in the root filesystem. It reruns after a fresh root filesystem or a changed provision declaration.

`lifier reconcile worker` repeats reconciliation until interrupted. Run it under a host service manager when continuous reconciliation is needed.
Native supervision continues when the CLI exits. The controller never expires or deletes workspaces automatically.

Automation uses durable allocations, keyed by Amp runner identity. File locks serialize local controller mutations.
`runner_drain` blocks new managed allocations. `down`, `rm`, and `up --restart` reject unreleased allocations.
Approval waits, timeouts, and unknown activity retain the allocation. A released allocation ID cannot be reused.
Direct assignments through Amp bypass managed allocations. An empty allocation list does not prove no direct task is running.
Only one controller state directory should manage a runner; this is not a distributed locking service.

```sh
lifier api runner_profiles
lifier api runner_acquire --input '{"runner":"worker","id":"request-123"}'
lifier api runner_allocations --input '{"runner":"worker"}'
lifier api runner_drain --input '{"runner":"worker"}'
```

## Amp plugin and MCP

The [Amp plugin](integrations/amp/lifier) exposes tools, a palette command, and `lifier:runner-workflow`.
Install the directory into `.amp/plugins/lifier` at the operator project's Git root, or the operator host's Amp system plugin directory.
Edit its `settings.ts` to select the lifier binary and configuration. Keep it outside worker images.

```sh
mkdir -p .amp/plugins
cp -R integrations/amp/lifier .amp/plugins/lifier
amp plugins list
amp skills list
```

Use `runner_run` to allocate a profile, prepare its runtime, create a private native Amp thread, attach labels, and submit work.
Select the built-in mode explicitly. The installed Amp API rejects runner-executor creation from plugin tools. The plugin uses Amp CLI execution with an allocation label instead.
It marks the allocation unknown before submission, then records the returned thread ID. An interrupted submission remains allocated for recovery.
`runner_poll` recovers activity after a timeout. `runner_release` reads a fresh thread export and matches idle activity to the final assistant message before releasing the allocation.
It retains compute and storage. Finishing a turn never destroys the runner.
Thread labels are `lifier`, `lifier-<provider>`, and `lifier-allocation-<id>`; these are project conventions, not reserved Amp labels.

For another client, `lifier mcp` serves the same engine over stdio. Use [examples/mcp.json](examples/mcp.json) for Amp's MCP configuration shape.
The MCP server has no separate scheduler or lifecycle implementation. It accepts configured runner names, not arbitrary shell commands.

Kubernetes resources carry `app.kubernetes.io/*` labels plus lifier ownership metadata.
Stored native UIDs and owner-reference checks establish ownership; display labels alone do not.
Controller state and guest credentials live in private files under `$XDG_STATE_HOME/lifier`, defaulting to `~/.local/state/lifier`.
Preserve this state while resources exist. Losing it requires explicit resource recovery, not automatic adoption by name.

## Verification

```sh
task check
task lint
task plugin:test
nix build
nix flake check
```

`task plugin:types` checks against the installed Amp API without vendoring upstream declarations.
The live smoke tests require their matching daemon or cluster and create only named test resources.
See [verification notes](docs/verification.md) for tested versions, lifecycle evidence, and limitations.
External providers still use `provider/v1` framing; managed built-ins advertise `sandbox/v2` capabilities and validation.
