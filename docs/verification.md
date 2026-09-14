# Verification

Local verification ran on 14 September 2026, on an Apple M4 Pro with macOS 26.5.2.
Dependencies came from the repository Nix shell.

## Automated checks

- Go tests with the race detector, `go vet`, and `staticcheck`.
- Go, shell, and Nix formatting; ShellCheck through `nix flake check`.
- Nix package build and evaluation of all three declared platforms.
- Amp plugin behavior tests and strict types against the installed Amp plugin API.
- MCP stdio initialization, notifications, discovery, and error handling.
- Ownership rejection, UID replacement, environment isolation, and durable allocation guards.

The installed Amp CLI was `0.0.1789315238-g490ac9`.
The controller account had an active ChatGPT subscription connection.
Remote tests used Amp's `high` mode. Model-provider authentication stayed in Amp.
No subscription login files were copied into runners.

## Live provider checks

| Provider | Evidence |
|---|---|
| Docker | Remote Amp task read a retained named-volume marker; stop/start retained storage; native restart recovered a killed Amp process |
| Lima | Remote Amp task wrote and read a workspace marker; stop/start retained the guest disk; systemd recovered a killed Amp process |
| Kubernetes Pod | Remote Amp tasks completed; Pod replacement and stop/start retained the PVC; container runtime stop triggered a Kubernetes restart |
| Kubernetes KubeVirt | Remote Amp task read both retained markers after stop/start and compute replacement; systemd recovered a killed Amp process |

Docker used the repository's `lifier/amp:latest` image.
The local Kubernetes fixture used Lima 2.2.0, Ubuntu 24.04, kernel 6.8.0-139-generic, K3s v1.36.4+k3s1, KubeVirt v1.8.4, and CDI v1.66.1.
The KubeVirt guest used an ARM64 Ubuntu 24.04 cloud image imported into a boot PVC, plus a separate workspace PVC.

An earlier Ubuntu 26.04 fixture with kernel 7.0.0-28-generic stalled during nested guest disk discovery.
The committed fixture uses Ubuntu 24.04. This result does not certify every host kernel or nested virtualization platform.
Persistent boot testing exposed a changing guest MAC address and a systemd ordering cycle.
The provider now uses a stable MAC, avoids ordering the workload after cloud-final, and reapplies guest workload files during startup.

## Amp automation

The installed Amp CLI loaded 11 plugin tools, a palette command, and the bundled `lifier:runner-workflow` skill.
A live operator task recovered an earlier allocation, ran a new remote Pod task, observed completion, and released both allocations.
Compute and storage remained available after release.
A further KubeVirt task read both retained markers through the plugin.

The installed plugin API rejects runner-executor creation from plugin tools because it cannot exclude recursive tools there.
Native thread state handles also require connected threads.
The implementation therefore uses supported Amp CLI submission and fresh thread exports.
It records unknown activity before launch and adds a unique allocation label for interrupted-submission recovery.

Exported thread JSON is version-sensitive. The parser requires idle activity to match a completed final assistant message.
A queued user turn, an unrecognized shape, or a failed export cannot authorize release.
These allocations cover plugin-managed work. Direct Amp assignments require separate operational coordination.

## Reproduce

Use a dedicated profile named `verify-*` with `/workspace` as its mount path:

```sh
nix develop
task build
task runner:verify -- /absolute/path/to/test-config.yaml verify-worker
amp --executor runner:your-test-runner-id --mode high -x 'Write a marker in /workspace and read it back'
```

The script creates or starts compute, verifies the runtime, writes a marker, stops and starts compute, and checks retention.
It retains compute and storage for inspection. It does not delete PVCs or guest disks.
The separate low-level smoke script covers a bind-mounted Docker or Lima sandbox:

```sh
hack/smoke.sh docker
```

For the nested cluster, start [the Lima fixture](../examples/lima/kubernetes.yaml), then run `task kubevirt:bootstrap` with its kubeconfig and context.
The bootstrap script installs pinned upstream KubeVirt and CDI manifests only in that explicitly selected cluster.

## Upstream conventions

Research covered [official-plugins](https://github.com/ampcode/official-plugins/tree/4e8f89eb5032132b3ec908c4dc26aaccd3b10bbc),
[amp-contrib](https://github.com/ampcode/amp-contrib/tree/2ba7041a9583a02987adf595aac58d1f9abfc183),
and [amp.nvim](https://github.com/ampcode/amp.nvim/tree/01ede44322220da5dc0b73ad8ace328a5ec1f5bf).
The plugin follows Amp's typed entrypoint, tool titles, transcript groups, palette commands, skill frontmatter, and directory layout.
The public repositories did not establish a shared runner-label namespace. Lifier's thread labels are local project conventions.

See the current [plugin documentation](https://ampcode.com/docs/customize/plugins), [skills](https://ampcode.com/docs/customize/skills),
[MCP configuration](https://ampcode.com/docs/customize/mcp), and [subscription routing](https://ampcode.com/docs/the-dial).
