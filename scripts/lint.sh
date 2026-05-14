#!/usr/bin/env bash
# Lint many_faces_elastic — go vet (matches standalone Go CI).
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

echo "🔍 Linting many_faces_elastic (go vet)..."
echo ""

go vet ./...

echo ""
echo "✅ many_faces_elastic lint passed"
