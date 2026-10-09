#!/usr/bin/env bash
# Keeps Wit.ai keys out of the repo; without WITAI_KEYS the Wit.ai engine option stays hidden.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
out="$repo_root/internal/stt/witai/keys_embedded.go"

if [ -z "${WITAI_KEYS:-}" ]; then
  echo "WITAI_KEYS not set; skipping Wit.ai key embed"
  exit 0
fi

python3 - "$out" <<'PY'
import json
import os
import sys

with open(sys.argv[1], "w") as f:
    f.write("package witai\n\n")
    f.write("func init() {\n")
    # json.dumps escaping is a valid Go string literal: same quote/backslash/unicode rules.
    f.write("\trawKeys = %s\n" % json.dumps(os.environ["WITAI_KEYS"]))
    f.write("}\n")
PY

echo "Embedded Wit.ai keys into $out"
