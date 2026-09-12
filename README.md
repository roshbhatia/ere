# lifier

Run [Amp](https://ampcode.com) runners in disposable sandboxes.

`amp --no-tui --runner-id <id>` turns a machine into a runner: it serves threads
you create on ampcode.com against the directory it started in. Amp is free when
the compute is yours, so the question stops being *what does it cost* and starts
being *what is the agent allowed to touch*.

lifier answers that. It keeps a declared fleet of runners alive, each in a
sandbox, and it treats a container, a local virtual machine, and a machine
across the network as the same shape.

```bash
lifier up                # every declared runner is created, started, serving
lifier ls                # what is running, and whether the agent is up
lifier logs sysinit      # the agent's output
lifier down              # stop the sandboxes, keep their disks
```

## Backends

| Backend  | Isolation                              | Where it runs |
| -------- | -------------------------------------- | ------------- |
| `docker` | Container, shared kernel                | anywhere Docker runs |
| `vz`     | Linux VM on Virtualization.framework    | macOS |
| `qemu`   | Linux VM on QEMU, KVM-accelerated       | Linux, and macOS without vz |

`vz` and `qemu` are one driver: [lima](https://lima-vm.io) already abstracts the
hypervisor, and KVM is an accelerator for QEMU rather than a separate backend.

```
$ lifier doctor
BACKEND  AVAILABLE  DETAIL
docker   true       server 29.2.1
qemu     true       limactl version 2.2.0, vmType qemu
vz       true       limactl version 2.2.0, vmType vz
```

## Every backend is a provider

A backend is an executable that speaks [provider/v1](https://github.com/roshbhatia/provider-spec):
one JSON request frame on standard input, progress events and one result frame
on standard output. The backends lifier ships are reached the same way an
external one is, through `lifier backend <name>`.

That is the whole extension point. A manifest in
`$XDG_CONFIG_HOME/lifier/providers/` adds a backend, and a manifest that reuses
a shipped name replaces it. Because the manifest owns the argv, a remote host
needs no code in lifier:

```yaml
version: provider/v1
kind: sandbox
name: arrakis
description: Container sandbox on arrakis over SSH
command: [ssh, -o, BatchMode=yes, arrakis, lifier, backend, docker]
actions:
  sandbox:
    description: Create, start, exec, and destroy containers on arrakis
```

`backend: arrakis` in a runner now places that runner on the other machine.

Develop a backend against the contract directly, with no agent and no
credential:

```bash
lifier sandbox create docker scratch --image debian:bookworm-slim --workspace ~/src/thing
lifier sandbox start docker scratch
lifier sandbox exec docker scratch -- uname -sm
lifier sandbox destroy docker scratch
```

`hack/smoke.sh <backend>` runs that sequence as a check.

## Configuration

`$XDG_CONFIG_HOME/lifier/lifier.yaml`, or `--config`. `lifier config schema`
prints the JSON Schema; `lifier config show` prints the effective values.

```yaml
defaultBackend: docker

amp:
  apiKeySecret: "op://Personal/ampcode/credential"

defaults:
  image: lifier/amp:latest
  cpus: 4
  memoryMB: 8192

runners:
  - name: sysinit
    workspace: ~/src/sysinit
    runnerId: workstation-sysinit

  - name: sysinit-vm
    backend: vz
    workspace: ~/src/sysinit
    runnerId: workstation-sysinit-vm
    secrets:
      OPENROUTER_API_KEY: "op://Personal/openrouter/api-key"
```

A runner id must be a valid hostname; lifier rejects one that is not rather
than letting Amp fail after the sandbox boots.

## Secrets

lifier holds no credential. `apiKeySecret` and every entry under `secrets` is a
reference resolved at launch:

- `op://vault/item/field` reads the 1Password CLI
- `env://NAME` reads this process's environment
- `file://path` reads a file
- anything else is a literal

A resolved value reaches the sandbox over stdin as a `0600` file the launched
command sources. It is never an argument, never a container label, and never
written to lifier's own config.

## Bring your own model

Amp registers a model router against your account, not against a sandbox:

```bash
op read "op://Personal/openrouter/api-key" \
  | amp config model-providers add-router openrouter --api-key-file - --active
```

Every runner then inherits it. The sandbox needs only `AMP_API_KEY`.

## Install

```bash
nix build            # or: task build
task image:build     # the default docker sandbox image
```

## Layout

```
cmd/lifier/          entry point
internal/
  cli/               cobra tree, including the hidden `backend` provider entry
  config/            declarative fleet, typed YAML and JSON Schema
  runner/            reconciles declared runners against backend state
  sandbox/           the wire contract: payload types, client, and serve loop
  registry/          backend manifests, built-in and discovered
  backend/docker/    container backend
  backend/lima/      vz and qemu backends
  secret/            op://, env://, and file:// reference resolution
  amp/               the only package that knows how a runner is launched
images/amp/          the default sandbox image
examples/            a config and an external backend manifest
```
