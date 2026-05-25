#!/usr/bin/env bash
# Apply gofmt (tab indentation) to all Go packages in this module.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

go fmt ./...

echo "✅ many_faces_elastic format complete"
