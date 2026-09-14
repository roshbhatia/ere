#!/usr/bin/env bash
# End-to-end check against a real backend: create a throwaway sandbox, run a
# command in it, destroy it. It never launches the agent, so it needs no
# credential.
#
# Usage: hack/smoke.sh [backend]
set -euo pipefail

BACKEND="${1:-docker}"
NAME="${LIFIER_SMOKE_NAME:-smoke-$$}"
CREATED=false
BIN="${LIFIER_BIN:-./lifier}"
IMAGE="${LIFIER_SMOKE_IMAGE:-debian:bookworm-slim}"
# Docker Desktop shares the home directory, not macOS's private temp root, so a
# mktemp workspace would mount empty.
WORKDIR="$(mktemp -d "${HOME}/.lifier-smoke.XXXXXX")"

cleanup() {
  if "$CREATED"; then
    "$BIN" sandbox destroy "$BACKEND" "$NAME" > /dev/null
  fi
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
CREATED=true

echo "==> start"
"$BIN" sandbox start "$BACKEND" "$NAME"

echo "==> exec"
guest_check="$(
  cat << 'SCRIPT'
uname -sm
test "$(cat /workspace/marker.txt)" = marker
echo guest > /workspace/guest.txt
SCRIPT
)"
"${BIN}" sandbox exec "${BACKEND}" "${NAME}" -- sh -ec "${guest_check}"
test "$(cat "$WORKDIR/guest.txt")" = guest

echo "==> stop and restart"
"$BIN" sandbox stop "$BACKEND" "$NAME"
"$BIN" sandbox start "$BACKEND" "$NAME"
"$BIN" sandbox exec "$BACKEND" "$NAME" -- cat /workspace/guest.txt

echo "==> list"
"$BIN" sandbox ls "$BACKEND"

echo "==> smoke passed on $BACKEND"
