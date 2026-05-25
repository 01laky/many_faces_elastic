#!/usr/bin/env bash
# Ensure VERSION matches latest CHANGELOG release section.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
version="$(tr -d '[:space:]' <"$ROOT/VERSION")"
python3 - "$ROOT/CHANGELOG.md" "$version" <<'PY'
import re, sys
from pathlib import Path
text = Path(sys.argv[1]).read_text(encoding="utf-8")
expected = sys.argv[2]
if "## [Unreleased]" not in text: raise SystemExit("missing [Unreleased]")
after = text.split("## [Unreleased]", 1)[1]
m = re.search(r"^## \[(\d+\.\d+\.\d+)\]", after, re.M)
if not m: raise SystemExit("no release section after [Unreleased]")
latest = m.group(1)
if latest != expected: raise SystemExit(f"VERSION {expected} != CHANGELOG {latest}")
print(f"verify-version-files: ok (VERSION={expected})")
PY
