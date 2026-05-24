#!/usr/bin/env sh
# Strips Cursor / Anysphere trailers from COMMIT_EDITMSG (Cursor Agent may inject them).
# Invoked from .husky/prepare-commit-msg, .husky/commit-msg, and .githooks/*.
# See .cursor/rules/no-cursor-commit-attribution.mdc
set -eu
file="${1:-}"
[ -n "$file" ] && [ -f "$file" ] || exit 0

script_dir=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
sed_file="${script_dir}/git-msg-strip-cursor-coauthor.sed"
if [ ! -f "$sed_file" ]; then
  echo "strip-cursor-commit-trailers: missing ${sed_file}" >&2
  exit 1
fi

_tmp="${file}.nocursor.$$"
sed -f "$sed_file" "$file" > "$_tmp"
mv "$_tmp" "$file"
