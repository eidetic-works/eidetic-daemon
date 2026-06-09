#!/usr/bin/env bash
# Eidetic Works bootstrap priming for Claude Code SessionStart.
# Reads plan files from sibling mcp-server-nucleus repo (claude/ultraplan-setup-T3E6q branch)
# so cc-main has full plan context when opening sessions in eidetic-daemon.
# ADR-021: polyrepo persistence-layer hook replication pattern.

set -euo pipefail

PROJECT_DIR="${CLAUDE_PROJECT_DIR:-$(git rev-parse --show-toplevel 2>/dev/null || pwd)}"

# Sibling repo: ai-mvp-backend is ../../../ai-mvp-backend relative to eidetic-daemon root
# /Users/example/work-eidetic-daemon/work/ → ../../ → /Users/example/ → ai-mvp-backend
PLAN_REPO="$(cd "$PROJECT_DIR/../.." && pwd)/ai-mvp-backend"
PLAN_BRANCH="claude/ultraplan-setup-T3E6q"

W4="2026-06-08"
W8="2026-07-08"
W12="2026-08-08"
TODAY=$(date +%Y-%m-%d)

TMP=$(mktemp)
trap 'rm -f "$TMP"' EXIT

{
  echo "# Eidetic Works — auto-priming (eidetic-daemon SessionStart hook, ADR-021)"
  echo
  echo "**Today:** $TODAY  |  **W4 gate:** $W4  |  **W8 gate:** $W8  |  **W12 gate:** $W12"
  echo "**Repo:** eidetic-daemon (plan files sourced from $PLAN_REPO [$PLAN_BRANCH])"
  echo

  if ! git -C "$PLAN_REPO" cat-file -e "${PLAN_BRANCH}:BOOTSTRAP.md" 2>/dev/null; then
    echo "[WARN] Cannot read plan files from $PLAN_REPO. Check that the repo exists and branch $PLAN_BRANCH is fetched."
    echo "Run: git -C $PLAN_REPO fetch origin $PLAN_BRANCH"
  else
    echo "--- BOOTSTRAP.md ---"
    git -C "$PLAN_REPO" show "${PLAN_BRANCH}:BOOTSTRAP.md"
    echo

    echo "--- STATUS.md ---"
    git -C "$PLAN_REPO" show "${PLAN_BRANCH}:STATUS.md"
    echo

    echo "--- DECISIONS.md (recent — last 6KB) ---"
    git -C "$PLAN_REPO" show "${PLAN_BRANCH}:DECISIONS.md" | tail -c 6000
    echo
  fi

  echo
  echo "**First action:** print the canonical first-action banner from BOOTSTRAP.md ## First action of every session, then begin today's STATUS.md target list."
} > "$TMP"

if command -v jq >/dev/null 2>&1; then
  jq -Rs '{hookSpecificOutput: {hookEventName: "SessionStart", additionalContext: .}}' < "$TMP"
else
  cat "$TMP"
fi
