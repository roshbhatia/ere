# Provider connections

These recipes use named connections. A connection inherits a base provider with `kind` and adds arguments.
Aliases cannot inherit other aliases. `ere backends` shows resolved commands, including configured aliases.
Declare native provider executables through your Nix configuration. Ere does not install provider daemons.

| Recipe | Prerequisites | Workspace and removal |
| --- | --- | --- |
| [Remote Docker](remote-docker.yaml) | Docker context `build-host`; Amp image and `ere-workspace` volume on that daemon | Named volume survives removal |
| [Podman](podman.yaml) | Podman machine or Linux runtime; Amp image and named volume | Named volume survives removal |
| [Incus](incus.yaml) | Trusted `lab` remote, `agents` project, Ubuntu image source, VM support | VM disk survives stop; removal deletes it |
| [SSH](ssh.yaml) | Existing Linux host, trusted host key, passwordless sudo | Dedicated workload removed; host and workspace retained |
| [Tart](tart.yaml) | Apple Silicon macOS host; guest agent and passwordless sudo in the image | Disk survives stop; removal deletes it |
| [Multipass](multipass.yaml) | Ubuntu images; snapshot-capable driver | Ownership snapshot required; removal purges only the named VM |

Edit connection names, hostnames, paths, images, and resource sizes before use.
Use `ere --config examples/connections/FILE.yaml plan NAME`, then `doctor NAME` and `up NAME`.
Recipes are configuration examples, not evidence that your runtime meets their prerequisites.
Incus, SSH, Tart, Multipass, and Podman still need validation against your native installation.

SSH uses strict host-key checking and never shuts down or deletes the host.
Its workload, logs, and credentials use a separate directory and service name for each Ere ownership record.
Interactive shells run with the same root privileges as the workload.
Use an explicit, distinct workspace path for each SSH profile.

Tart uses host launchd supervision and guest launchd for Amp.
Its workspace must be under `/Users/Shared/`; macOS does not permit creating `/workspace` on the system volume.
Tart automatic image pruning is disabled for creation and supervised execution.
Use a prepared image without personal credentials. A runner image is not a safe reusable template.

Multipass creates an ownership snapshot before installing runner credentials.
Keep this snapshot: Ere checks it before stopped-instance operations and checks the guest marker while running.
A missing snapshot or ownership marker blocks mutation. Inspect a partially created VM with native tooling before recovery.

Provider creation failures preserve existing resources and local intent. Ere refuses unverified adoption.
This can require native inspection after an interrupted first creation; it does not imply the partial VM is ready.
