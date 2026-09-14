#!/usr/bin/env bash
set -euo pipefail

plugin_types_dir="$(mktemp -d)"
trap 'rm -rf "${plugin_types_dir:?}"' EXIT
amp plugins show-docs > "${plugin_types_dir}/docs.txt"
python3 - "${plugin_types_dir}" << 'PY'
from pathlib import Path
import sys
root = Path(sys.argv[1])
text = (root / 'docs.txt').read_text()
start = text.index("declare module '@ampcode/plugin' {")
end = text.rfind('\n}') + 2
if end <= start:
    raise SystemExit('Amp documentation has no complete plugin declaration')
(root / 'plugin.d.ts').write_text(text[start:end])
PY
tsc --noEmit --strict --allowImportingTsExtensions --moduleResolution bundler \
  --module esnext --target es2022 --lib es2022,dom \
  integrations/amp/runtime.d.ts "${plugin_types_dir}/plugin.d.ts" integrations/amp/lifier/index.ts
