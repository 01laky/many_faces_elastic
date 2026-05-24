#!/usr/bin/env bash
# Point this repo at .githooks when Husky is not managing hooks (Go/Java/Python workers).
set -euo pipefail
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"
chmod +x scripts/strip-cursor-commit-trailers.sh 2>/dev/null || true
chmod +x .githooks/prepare-commit-msg .githooks/commit-msg 2>/dev/null || true
if [ -d .husky ] && [ -f .husky/commit-msg ]; then
  chmod +x .husky/prepare-commit-msg 2>/dev/null || true
  echo "hooks: husky (.husky/) — run yarn install if hooks missing"
  exit 0
fi
git config core.hooksPath .githooks
echo "hooks: core.hooksPath=.githooks (Cursor co-author strip enabled)"
