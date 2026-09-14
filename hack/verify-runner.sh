#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 2 || $2 != verify-* ]]; then
  echo 'usage: hack/verify-runner.sh CONFIG verify-PROFILE' >&2
  echo 'Uses a dedicated test runner. Stops and starts compute; retains storage.' >&2
  exit 2
fi
config="$1"
runner="$2"
ere_binary="${ERE_BINARY:-./ere}"
marker="ere-verify-$(date +%s)-${RANDOM}"
args=(--config "${config}")

"${ere_binary}" "${args[@]}" up "${runner}"
write_marker="$(
  cat << 'SCRIPT'
printf %s "$1" > /workspace/.ere-verify
SCRIPT
)"
"${ere_binary}" "${args[@]}" exec "${runner}" -- sh -ec "${write_marker}" sh "${marker}"
"${ere_binary}" "${args[@]}" down "${runner}"
"${ere_binary}" "${args[@]}" up "${runner}"
actual="$("${ere_binary}" "${args[@]}" exec "${runner}" -- cat /workspace/.ere-verify)"
if [[ ${actual} != "${marker}" ]]; then
  echo "ERROR: workspace did not survive stop/start for ${runner}" >&2
  exit 1
fi
"${ere_binary}" "${args[@]}" exec "${runner}" -- rm /workspace/.ere-verify
"${ere_binary}" "${args[@]}" plan "${runner}"
echo "Verified runtime and workspace retention for ${runner}" >&2
