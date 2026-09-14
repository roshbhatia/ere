# Private-runner events

Ere accepts signed JSON webhooks and queues tasks for private runners.
It also supports delayed tasks and recurring schedules.
Amp's plugin webhook API runs on Amp-managed orbs; it does not provide a receiver for private runners.
See [event-driven orbs](https://ampcode.com/docs/orbs/event-driven).

```sh
ere events enqueue dev 'Review recent changes' --id daily-review --after 10m --every 24h
ere events work
ere events list
ere events cancel EVENT-ID
```

A recurrence starts after the previous task completes. This is an interval schedule, not a wall-clock cron expression.
`work --once` performs one iteration for an external scheduler. The continuous worker checks every five seconds.
The default queue is `$XDG_STATE_HOME/ere/events/<config-path-hash>.json`, with `~/.local/state` as the fallback state root.
Queues are scoped to configuration paths. Each event also records its runner ID and provider; changed routing blocks execution.
Use `--queue /absolute/path/events.json` to isolate workflows. Keep the queue on a local filesystem with working file locks and atomic rename.

Queue writes use go-utils validated atomic storage. File locks release when a worker exits or crashes.
One worker submits at a time. Completed tasks release allocations after a fresh idle check, while compute remains available.
Delivery IDs deduplicate repeated requests. Reusing an ID with different content fails.
The queue rejects new work when 100 events remain unfinished.

Interrupted submissions become `unknown` and do not retry automatically.
Inspect the thread labels in Amp using `ere-allocation-<allocation-id>` from the event, inspect the thread, then bind it explicitly:

```sh
ere events recover EVENT-ID T-THREAD-ID VERIFIED-ALLOCATION-ID
```

Recovery validates the runner identity. The final argument confirms the allocation label you checked; Amp export does not expose labels.
Pre-submission failures retry after 30 seconds. Running tasks with failed observation retain their allocations.
Review unknown events before continuing automation. The queue retains prompts, event payloads, and completed records; protect and maintain its state directory.

## Webhooks

Create a prompt file with instructions for the selected runner. Payloads cannot select the runner or replace this prompt.

```sh
ere events serve --runner dev --secret env://ERE_WEBHOOK_SECRET --prompt-file ./event-prompt.txt
ere events work
```

The listener defaults to `127.0.0.1:8787`. Put it behind your existing authenticated TLS ingress for external senders.
Run the receiver and worker as separate processes with the same configuration and queue.
The receiver accepts POST requests with JSON bodies up to 1 MiB.
Supply `X-Hub-Signature-256: sha256=<hex HMAC-SHA256 of body>` and `X-GitHub-Delivery` or `Idempotency-Key`.
GitHub-compatible signatures authenticate the exact body bytes. The receiver returns 202 after durable enqueue, before execution.

Run these processes through your declared Nix systemd or launchd configuration.
Give the worker the required provider executables, host Amp credentials, and the environment used by credential references.
The CLI does not install a background service or modify ingress configuration.
