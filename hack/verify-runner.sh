#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 || $2 != verify-* ]]; then
  echo 'usage: hack/verify-runner.sh CONFIG verify-PROFILE' >&2
  echo 'Uses a dedicated test runner. Stops and starts compute; retains storage.' >&2
  exit 2
fi
config="$1"
runner="$2"
lifier_binary="${LIFIER_BINARY:-./lifier}"
marker="lifier-verify-$(date +%s)-${RANDOM}"
args=(--config "${config}")

"${lifier_binary}" "${args[@]}" up "${runner}"
write_marker="$(
  cat << 'SCRIPT'
printf %s "$1" > /workspace/.lifier-verify
SCRIPT
)"
"${lifier_binary}" "${args[@]}" exec "${runner}" -- sh -ec "${write_marker}" sh "${marker}"
"${lifier_binary}" "${args[@]}" down "${runner}"
"${lifier_binary}" "${args[@]}" up "${runner}"
actual="$("${lifier_binary}" "${args[@]}" exec "${runner}" -- cat /workspace/.lifier-verify)"
if [[ ${actual} != "${marker}" ]]; then
  echo "ERROR: workspace did not survive stop/start for ${runner}" >&2
  exit 1
fi
"${lifier_binary}" "${args[@]}" exec "${runner}" -- rm /workspace/.lifier-verify
"${lifier_binary}" "${args[@]}" plan "${runner}"
echo "Verified runtime and workspace retention for ${runner}" >&2
