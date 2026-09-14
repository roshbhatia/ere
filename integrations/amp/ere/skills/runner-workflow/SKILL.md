---
name: runner-workflow
description: Run an Amp task on a configured ere runner, recover a retained allocation, or inspect runner lifecycle and logs.
metadata:
  version: '1'
builtin-tools:
  - runner_profiles
  - runner_plan
  - runner_ensure
  - runner_status
  - runner_logs
  - runner_allocations
  - runner_run
  - runner_poll
  - runner_release
  - runner_drain
  - runner_resume
---

Select an existing profile with `runner_profiles`. Use `runner_plan` to inspect required compute changes.
Docker uses containers. Lima uses local VMs. Kubernetes profiles use Pods or KubeVirt guests in their configured cluster.

Use `runner_run` with the requested built-in Amp mode. It acquires an allocation before preparing the runner and submitting an independent private thread through Amp CLI.
Each submission has a unique allocation label. If submission is interrupted before its thread ID returns, keep the unknown allocation and search Amp for that label.
Keep the allocation and thread IDs in the result. Inspect errors with `runner_status`, `runner_logs`, and `runner_allocations`.

A timeout does not mean the thread stopped. Use `runner_poll` to recover current activity.
Release the allocation with `runner_release` only when the task is idle and its result is recorded.
Release keeps compute and workspace. A future turn on a released thread needs a new managed allocation before work resumes.

Draining blocks new managed assignments. It does not stop threads assigned directly through Amp.
Do not infer safe shutdown from an empty managed allocation list when direct assignments are possible.
Explicit host CLI commands control compute removal; the plugin does not delete storage.

The operator plugin belongs on the controller host. Workers do not need cluster credentials, the Docker socket, or operator tools.
