# Runner workflows

Start with `ere init`. It writes `ere.yaml` in the current directory and refuses to overwrite an existing file.
The CLI reads this project file before the normal XDG configuration. `--config` and `ERE_CONFIG` take precedence.
Set `AMP_API_KEY` for the generated credential reference. The host Amp CLI must also be signed in.

```sh
ere init --provider lima
ere plan dev
ere doctor dev
ere start dev
```

`start` prepares the runner, creates a private remote thread, and opens the native Amp TUI.
Without a prompt, it sends a short readiness request. This initial turn uses Amp credits.
Use `ere start dev -- 'Review this repository'` to supply the first task instead.
Leaving the TUI retains compute and the allocation. Amp can ask you to acknowledge that another client owns execution; press Enter.

Omit the runner or thread argument to use the picker. Use arrows or `j`/`k`, `/` to filter, Enter to select, and Escape to cancel.
Scripts must specify an argument. Install shell completion with `ere completion zsh`, `bash`, or `fish` using your shell configuration.
The picker uses go-utils navigation, keymaps, cell sizing, and terminal detection.

```sh
ere run dev --mode high --title 'Review parser' --label review --id parser-review -- 'Review parser changes'
ere run dev --stdin --json < prompt.txt
ere threads
ere threads poll T-THREAD-ID
ere continue T-THREAD-ID
ere threads release T-THREAD-ID
ere shell dev
ere inspect dev --json
```

Modes include installed custom Amp modes. `--features` passes thread features to Amp.
`run --json` returns the allocation and thread URL. Prompts supplied on stdin are limited to 1 MiB.
`amp.clientBinary` selects the host executable; `amp.binary` selects the guest executable.
`amp.clientArgs` supplies host options such as `--settings-file /absolute/path/settings.json`.

An idempotency key identifies one task. A retry with different content fails.
An interrupted submission retains an unknown allocation because the remote request might have succeeded.
Search Amp for its `ere-allocation-<id>` label before recovery. Do not submit a second copy blindly.
Release requires a fresh export with an idle final assistant message matching Amp's current activity record.

`down` and `rm` require a runner name or `--all`. Allocations must be released before either operation.
Removal reports the provider's storage retention rules. `shell --json` prints native connection arguments for terminal launchers.

## Copies and templates

Profile copies receive a distinct runner ID. They write a new configuration file and preserve the source configuration.
Workspace forks copy committed Git state without hardlinks. They preserve the upstream origin and leave uncommitted changes in the source.

```sh
ere clone dev review --fork-workspace ../project-review --output review.yaml
ere --config review.yaml start review
```

Use `--workspace` for an independent existing directory, or explicitly choose `--share-workspace`.
Ere refuses shared VM boot volumes. It does not infer a filesystem snapshot from a profile copy.

Lima supports prepared disk templates:

```sh
ere template build dev dev-template
ere clone dev review --template dev-template --workspace ../project-review --output review.yaml
```

Template builds create a new VM without the source workspace, runtime secrets, or runner workload.
They run the profile's provisioning, reset Linux machine identity and host SSH keys, then stop and seal the template.
Only a sealed, stopped template can be cloned. Source and target resources and images must match.
Clone preserves Lima's resolved configuration and changes its mounts before first boot.
Provisioning must install software without embedding personal credentials. Ere cannot identify arbitrary credentials inside user scripts or images.

Native template cloning currently supports Lima. Other profiles support configuration copies and Git workspace forks.
For Kubernetes, independent PVC or KubeVirt boot-volume clones require storage support and separate provisioning.

## Amp clients

Use the thread URL from the CLI in the web app, macOS app, or iOS app under the same Amp account.
Workspace Cross-Client Access policy can block remote creation and messaging.
Files require a connected runner. Changes require a Git or Jujutsu workspace.
Set `amp.remoteControlTerminal: true` to enable Amp's web terminal, then restart the runner when allocations allow it.
Runner portals, desktop streaming, and multiplayer are not supported by Amp.
See [runners](https://ampcode.com/docs/cli/runners), [cross-client access](https://ampcode.com/docs/cli/remote-control),
and [native apps](https://ampcode.com/docs/macos-and-ios).

The Ere Amp plugin adds runner selection, status, logs, drain, resume, start, and continuation commands.
Its task tool uses the durable CLI submission path. Native start preserves the current plugin agent mode.
`ere plugin install .amp/plugins/ere` installs the bundled plugin and refuses existing directories.
Use `--role worker` for runner installations. Keep the plugin in operator mode on the controlling client and worker mode inside runners.
Use the existing [Amp integration instructions](../README.md#amp-plugin-and-mcp) for installation and role configuration.
