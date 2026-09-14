# lifier

Run [Amp runners](https://ampcode.com/docs/cli/runners) in Docker containers or local [Lima](https://lima-vm.io) virtual machines.
Use Lima with K3s when the runner needs Kubernetes.

Amp supports a linked ChatGPT subscription with your own compute.
[Self-hosted runners require no Amp monthly plan](https://ampcode.com/news/free-agent).
Model usage follows your selected provider and its limits.

## Start here

Enter the repository's development shell, then build and check the CLI:

```bash
nix develop
task build
task check
task lint
export PATH="$PWD:$PATH"
lifier doctor
```

Connect your ChatGPT subscription once, on the host:

```bash
amp login
amp config model-providers add-chatgpt-subscription
amp config model-providers list
```

Skip the connection command if the list already shows your active subscription.
The connection belongs to your Amp account, so runners use the same connection.
The sandbox needs an Amp API key, not your ChatGPT password or browser tokens.
See [Amp's subscription setup](https://ampcode.com/docs/the-dial).

Set `AMP_API_KEY` through your secret manager or a hidden terminal prompt:

```bash
read -rs AMP_API_KEY
export AMP_API_KEY
```

Paste your Amp API key and press Enter.
The examples resolve `env://AMP_API_KEY` at launch.
You can instead set `amp.apiKeySecret` to an `op://vault/item/field` reference.

## Docker

Start your local Docker daemon, then run:

```bash
task image:build
lifier --config examples/lifier.yaml up docker-example
lifier --config examples/lifier.yaml ls
lifier --config examples/lifier.yaml logs docker-example
lifier --config examples/lifier.yaml exec docker-example -- amp --version
```

The example mounts the current directory at `/workspace`.
Run these commands from the repository root, or change `workspace` to your project's absolute path.
Select `lifier-docker` in Amp's location picker to create a thread on this runner.

## Lima

On macOS, `vz` uses Virtualization.framework.
On Linux, change the example's backend to `qemu`; KVM accelerates it when available.

```bash
lifier --config examples/lifier.yaml up lima-example
lifier --config examples/lifier.yaml exec lima-example -- amp --version
lifier --config examples/lifier.yaml logs lima-example
```

The example installs Amp at `/opt/amp/bin/amp` inside the VM.
Only the declared workspace is mounted, and that mount is writable unless `readOnly: true` is set.
Select `lifier-lima` in Amp's location picker, or send a task from the CLI:

```bash
amp --executor runner:lifier-lima --mode high -x "List the files in the current directory."
```

## Kubernetes through Lima

The `k8s` provider uses [Lima's K3s template](https://github.com/lima-vm/lima/blob/master/templates/k3s.yaml).
Amp runs inside the VM and can manage its local Kubernetes cluster with `kubectl`.
This example does not deploy Amp as a Kubernetes Pod.

Install the provider manifest:

```bash
mkdir -p "${XDG_CONFIG_HOME:-$HOME/.config}/lifier/providers"
cp examples/providers/k8s.yaml "${XDG_CONFIG_HOME:-$HOME/.config}/lifier/providers/k8s.yaml"
```

On Linux, change `vz` to `qemu` in that manifest.
Keep `lifier` on `PATH` so the provider can start it.

```bash
lifier --config examples/lifier.yaml up k8s-example
lifier --config examples/lifier.yaml exec k8s-example -- kubectl get nodes
lifier --config examples/lifier.yaml exec k8s-example -- kubectl get pods -A
```

Select `lifier-k8s` in Amp's location picker.
Use `lifier exec` for cluster commands to keep them scoped to this VM.

## Lifecycle and configuration

`up` creates and starts the sandbox, provisions it, and starts Amp.
`up --restart` restarts Amp; `down` stops the sandbox and preserves its disk.
`rm` destroys the sandbox and its disk.
`ls` checks the Amp process; it does not prove that Amp's server accepted the connection.
Check the location picker before assigning work.
Stop a runner when you finish:

```bash
lifier --config examples/lifier.yaml down lima-example
```

Configuration defaults to `$XDG_CONFIG_HOME/lifier/lifier.yaml`.
Use `--config` to select another file.
`lifier config schema` prints the JSON Schema; `lifier config show` prints effective values.
Runner IDs must be valid hostnames.

Secret references support `op://`, `env://`, and `file://`; other strings are literal values.
Resolved secrets enter the sandbox over stdin in a private environment file.
Do not put credentials in the examples or commit them.

## Providers

Each backend is an executable that implements [provider/v1](https://github.com/roshbhatia/provider-spec).
It reads one JSON request and writes progress events followed by one result.
Built-in providers use `lifier backend docker`, `lifier backend vz`, or `lifier backend qemu`.
Manifests in `$XDG_CONFIG_HOME/lifier/providers/` can add or replace providers.
The [Kubernetes manifest](examples/providers/k8s.yaml) shows how to select a Lima base template.

## Verification

Run sandbox checks without an Amp credential:

```bash
hack/smoke.sh docker
hack/smoke.sh vz
hack/smoke.sh k8s
```

Each check creates a disposable sandbox, verifies the workspace mount, restarts it, and deletes it.
Use `qemu` instead of `vz` on Linux.
The Kubernetes check requires the provider manifest above.

```bash
nix build
nix flake check
```

## Layout

- `cmd/lifier`: CLI entry point.
- `internal/runner`: fleet lifecycle and Amp launch checks.
- `internal/backend`: Docker and Lima implementations.
- `internal/sandbox`: provider contract, client, and serve loop.
- `internal/config`, `internal/registry`, `internal/secret`: configuration and provider resolution.
- `images/amp`: Docker image.
- `examples`: Docker, Lima, and Kubernetes configurations.
