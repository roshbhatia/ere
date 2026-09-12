#!/usr/bin/env bash
# End-to-end check against a real backend: create a throwaway sandbox, run a
# command in it, destroy it. It never launches the agent, so it needs no
# credential.
#
# Usage: hack/smoke.sh [backend]
set -euo pipefail

BACKEND="${1:-docker}"
NAME="${LIFIER_SMOKE_NAME:-smoke}"
BIN="${LIFIER_BIN:-./lifier}"
IMAGE="${LIFIER_SMOKE_IMAGE:-debian:bookworm-slim}"
# Docker Desktop shares the home directory, not macOS's private temp root, so a
# mktemp workspace would mount empty.
WORKDIR="$(mktemp -d "${HOME}/.lifier-smoke.XXXXXX")"

cleanup() {
  "$BIN" sandbox destroy "$BACKEND" "$NAME" > /dev/null 2>&1 || true
  rm -rf "$WORKDIR"
}
trap cleanup EXIT

echo "marker" > "$WORKDIR/marker.txt"

echo "==> doctor"
"$BIN" doctor

echo "==> create on $BACKEND"
case "$BACKEND" in
  docker) "$BIN" sandbox create "$BACKEND" "$NAME" --image "$IMAGE" --workspace "$WORKDIR" ;;
  *) "$BIN" sandbox create "$BACKEND" "$NAME" --workspace "$WORKDIR" --cpus 2 --memory-mb 2048 --disk-gb 20 ;;
esac

echo "==> start"
"$BIN" sandbox start "$BACKEND" "$NAME"

echo "==> exec"
"$BIN" sandbox exec "$BACKEND" "$NAME" -- sh -c 'uname -sm; cat /workspace/marker.txt'

echo "==> list"
"$BIN" sandbox ls "$BACKEND"

echo "==> smoke passed on $BACKEND"
