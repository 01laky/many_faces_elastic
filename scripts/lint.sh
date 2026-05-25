#!/usr/bin/env bash
# Lint many_faces_elastic — gofmt check + go vet (matches standalone Go CI).
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

echo "🔍 Linting many_faces_elastic (gofmt + go vet)..."
echo ""

unformatted="$(gofmt -l .)"
if [ -n "$unformatted" ]; then
	echo "gofmt required on:" >&2
	echo "$unformatted" >&2
	echo "Run: ./scripts/format.sh" >&2
	exit 1
fi

go vet ./...

echo ""
echo "✅ many_faces_elastic lint passed"
