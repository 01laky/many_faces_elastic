#!/usr/bin/env bash
# Bump repo version (VERSION + CHANGELOG). Usage: ./scripts/bump-version.sh patch|minor|major
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION_FILE="$ROOT/VERSION"
CHANGELOG="$ROOT/CHANGELOG.md"
GITHUB_REPO="${GITHUB_REPO:-$(git -C "$ROOT" remote get-url origin 2>/dev/null | sed -E 's#.*github.com[:/]([^/]+/[^/.]+)(\.git)?$#\1#' || echo "REPO_UNKNOWN")}"
if [[ ! -f "$VERSION_FILE" || ! -f "$CHANGELOG" ]]; then echo "Missing VERSION or CHANGELOG.md" >&2; exit 1; fi
current="$(tr -d '[:space:]' <"$VERSION_FILE")"
[[ "$current" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "Invalid VERSION: $current" >&2; exit 1; }
IFS='.' read -r major minor patch <<<"$current"
case "${1:-}" in
  patch) patch=$((patch+1)); next="${major}.${minor}.${patch}" ;;
  minor) minor=$((minor+1)); patch=0; next="${major}.${minor}.${patch}" ;;
  major) major=$((major+1)); minor=0; patch=0; next="${major}.${minor}.${patch}" ;;
  '') echo "Usage: $0 patch|minor|major|X.Y.Z" >&2; exit 1 ;;
  *) [[ "$1" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] && next="$1" || { echo "Unknown: $1" >&2; exit 1; } ;;
esac
printf '%s\n' "$next" >"$VERSION_FILE"
python3 - "$CHANGELOG" "$current" "$next" "$GITHUB_REPO" <<'PY'
import re, sys
from pathlib import Path
path, prev, new, repo = Path(sys.argv[1]), sys.argv[2], sys.argv[3], sys.argv[4]
text = path.read_text(encoding="utf-8")
marker = "## [Unreleased]"
if marker not in text: raise SystemExit("missing [Unreleased]")
after = text.split(marker, 1)[1]
sep = "\n---\n"
if sep not in after: raise SystemExit("missing --- after [Unreleased]")
body, _ = after.split(sep, 1)
sections = {"Added": "", "Changed": "", "Fixed": ""}; key = None
for line in body.strip("\n").splitlines():
    if line.startswith("### "):
        k = line[4:].strip(); key = k if k in sections else None; continue
    if key: sections[key] += line + "\n"
def block(n):
    b = sections[n].strip()
    return f"### {n}\n\n{b}\n\n" if b else ""
blocks = block("Added") + block("Changed") + block("Fixed")
if not blocks.strip(): blocks = "### Added\n\n"
new_release = f"## [{new}]\n\n{blocks}".rstrip() + "\n"
insert = f"## [Unreleased]\n\n### Added\n\n### Changed\n\n### Fixed\n\n---\n\n{new_release}\n"
text = text.replace(marker, insert, 1)
ul = f"[Unreleased]: https://github.com/{repo}/compare/v{new}...HEAD"
nl = f"[{new}]: https://github.com/{repo}/compare/v{prev}...v{new}"
text = re.sub(r"^\[Unreleased\]:.*$", ul, text, count=1, flags=re.M) if re.search(r"^\[Unreleased\]:", text, re.M) else text.rstrip() + "\n" + ul + "\n"
if f"[{new}]:" not in text:
    pp = rf"(\[{re.escape(prev)}\]:)"
    text = re.sub(pp, nl + "\n\\1", text, count=1) if re.search(pp, text) else text.rstrip() + "\n" + nl + "\n"
path.write_text(text, encoding="utf-8")
PY
echo "Version: $current -> $next"
