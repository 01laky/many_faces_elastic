# Strip Cursor / Anysphere co-author and marketing trailers from COMMIT_EDITMSG.
# Used by scripts/strip-cursor-commit-trailers.sh and .githooks/* (all many_faces_* repos).
/^Co-authored-by:[[:space:]]*Cursor[[:space:]]*$/d
/^Co-authored-by:[[:space:]]*cursoragent[[:space:]]*$/d
/^Co-authored-by:.*[Cc]ursor/d
/^Co-authored-by:.*cursoragent/d
/^Co-authored-by:.*@cursor\.com/d
/^Co-authored-by:.*[Aa]nysphere/d
/^Signed-off-by:.*[Cc]ursor/d
/^Signed-off-by:.*cursoragent/d
/^Made with Cursor/d
