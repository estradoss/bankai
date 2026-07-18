#!/usr/bin/env bash
# PostToolUse hook: regenerate the progress artifact whenever CLAUDE.md is edited.
# Reads the tool-call JSON on stdin; only acts when the edited path is CLAUDE.md.
set -euo pipefail
payload="$(cat)"
path="$(printf '%s' "$payload" | python3 -c 'import sys,json;print(json.load(sys.stdin).get("tool_input",{}).get("file_path",""))' 2>/dev/null || true)"
case "$path" in
  */CLAUDE.md|CLAUDE.md)
    python3 "$(dirname "$0")/gen_progress.py" >/dev/null 2>&1 || true
    ;;
esac
exit 0
