#!/usr/bin/env bash
# Point this repo at .githooks (strip Cursor Co-authored-by trailers on commit).
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
chmod +x .githooks/prepare-commit-msg 2>/dev/null || true
git config core.hooksPath .githooks
echo "core.hooksPath=.githooks (prepare-commit-msg strips Cursor Co-authored-by lines)."
