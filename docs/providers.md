# Provider contract

Lifier separates compute, workload, and allocation state.
A running container or VM does not prove that Amp registered remotely.
A completed Amp turn does not release its allocation or remove its workspace.

## Provider operations

Built-in managed providers advertise `sandbox/v2` through the existing `provider/v1` process framing.
External providers can keep the original contract. The engine preserves their detached execution path.

| Operation | Provider responsibility |
|---|---|
| `probe` | Report availability, contract version, operations, storage modes, supervision, and retention |
| `validate` | Reject unsupported specifications before compute creation; return the configuration digest |
| `create` | Create stopped compute, install declared workload state, and record ownership; accept an identical existing specification |
| `start` | Start native compute and its supervisor |
| `exec` | Execute literal arguments with a private, per-call environment inside the owned sandbox |
| `status` | Report native state, resource identity, digest, and whether the workload is managed |
| `list` | Discover provider resources in the selected scope |
| `logs` | Read the native workload log or guest systemd journal |
| `stop` | Stop compute and retain durable storage |
| `destroy` | Remove owned compute; retain named Docker volumes, host binds, and Kubernetes PVCs |

`up` checks for the configured Amp process after provider startup.
Prove remote readiness by submitting a task through `amp --executor runner:<id>`.
Cloud-init completion, VM readiness, and Pod readiness cannot replace that check.

## Ownership and scope

Docker selects a daemon context. Managed containers have a private owner token and a recorded container ID.
Lima selects a hypervisor. Managed instances have a private owner marker in the instance directory and a controller specification.
Kubernetes requires a context and namespace. Mutations check owner tokens, native UIDs, and resource versions.
Pod and VMI execution also checks the child owner reference.

Kubernetes resources use the standard `app.kubernetes.io/*` labels.
The `lifier.owner`, `lifier.backend`, `lifier.sandbox`, and `lifier.runner-id` labels describe ownership and routing.
Labels alone do not authorize adoption. Losing controller state requires explicit recovery.

Only the controller needs Docker, Lima, or cluster administration access.
Guest execution uses the workload user: root for managed Lima and KubeVirt guests, and the image user for containers.
KubeVirt connects through SSH with a pinned host key and passwordless sudo.
Its MAC address stays stable across VM instances so persistent guest network configuration remains valid.

## Storage and changes

| Provider | Supported workspace storage | Removal behavior |
|---|---|---|
| Docker | Bind or named volume | Retains workspace; deletes container root |
| Lima | Bind or guest disk | Retains bind; deletes guest disk |
| Kubernetes Pod | PVC or explicitly ephemeral | Retains PVC; deletes ephemeral storage |
| Kubernetes KubeVirt | PVC | Retains workspace PVC and external boot PVC |

Use `diskGB` for Lima guest disks and `storage.sizeGB` for newly created PVCs.
Existing PVC sources are external resources. Lifier neither resizes nor deletes them.
A changed managed specification returns a replacement requirement. The engine does not silently replace running compute.
Check the provider's retention policy before explicit replacement.

Provisioning runs before Amp and records a digest in the root filesystem.
A fresh root repeats provisioning. Persistent roots preserve the marker across restarts.
Guest startup reapplies the declared private files and enables the service through SSH or Lima transport.
It does not depend on cloud-init running again.
Docker and Kubernetes supervise containers. Lima and KubeVirt supervise Amp through systemd.
No controller connection is needed for process recovery.

## Allocation lifecycle

`acquire` reserves one runner identity. Repeated requests with the same live allocation ID are idempotent.
The controller records the thread ID, activity, and timestamps in private local state.
Running, approval, error, and unknown states remain reserved.
Only idle work or an unbound allocation can be released. Released IDs cannot be reused.

The Amp plugin records unknown activity before submitting work.
It inspects a fresh `amp threads export` response and matches idle activity to the final assistant message.
A stale idle state followed by a queued user message remains unknown.
Unsupported export shapes or transport failures cannot authorize release.

Drain blocks new allocations. Stop, removal, and explicit restart reject unreleased allocations.
Reconciliation keeps declared runtimes up; it never expires workspaces.
The allocation lock covers the runner ID and profile name in one state directory.
Changing either cannot bypass an existing active allocation. It is not a distributed lease.
Direct Amp assignments and low-level `sandbox` commands bypass allocation tracking.
Do not use those commands to remove a runner with managed work.

See [Amp plugins](https://ampcode.com/docs/customize/plugins), [runners](https://ampcode.com/docs/cli/runners),
and [Kubernetes labels](https://kubernetes.io/docs/concepts/overview/working-with-objects/common-labels/) for upstream conventions.
