#!/usr/bin/env bash
# import-claude.sh — seed your engram store from a Claude.ai export.
#
# Step 1: in Claude.ai, Settings → Privacy → "Export data" → wait for email
#         → download .zip → extract `conversations.json`.
# Step 2: ./scripts/import-claude.sh /path/to/conversations.json
#
# Claude.ai's export shape is different from ChatGPT's: each conversation
# has a top-level array of `chat_messages` with `text` + `sender`. We join
# them into one transcript per conversation, store as one engram in
# `claude_web` surface (distinct from `claude_code` which is the CLI tool).
#
# Privacy (per ADR-020): runs entirely locally. POST /engrams/batch via UDS.

set -euo pipefail

FILE="${1:-}"
SOCK="${EIDETIC_SOCK:-/tmp/eidetic-daemon.sock}"
DRY_RUN=0
SURFACE="claude_web"

while [ $# -gt 0 ]; do
    case "$1" in
        --dry-run)  DRY_RUN=1; shift ;;
        --sock)     SOCK="$2"; shift 2 ;;
        --surface)  SURFACE="$2"; shift 2 ;;
        --help|-h)
            sed -n '2,15p' "$0" | sed 's/^# \?//'
            exit 0
            ;;
        *)
            if [ -z "${FILE_SET:-}" ]; then
                FILE="$1"
                FILE_SET=1
            fi
            shift
            ;;
    esac
done

if [ -z "$FILE" ] || [ ! -f "$FILE" ]; then
    echo "usage: $0 [--dry-run] [--surface NAME] /path/to/conversations.json" >&2
    exit 1
fi
if ! command -v jq >/dev/null 2>&1; then
    echo "error: jq required (brew install jq)" >&2; exit 1
fi
if [ ! -S "$SOCK" ] && [ $DRY_RUN -eq 0 ]; then
    echo "error: daemon socket not found at $SOCK — is eideticd running?" >&2
    exit 1
fi

# Claude.ai shape: top-level array; each item has chat_messages[].
# Some exports have `text`, others `content[0].text` for tool-call messages.
# Skip messages where text resolves to empty.
ENGRAMS_JSON=$(jq -c '
  [
    .[] | select(.chat_messages != null and (.chat_messages | length) > 0) | {
      surface: "'"$SURFACE"'",
      ts: ((.created_at // .updated_at // "1970-01-01T00:00:00Z") | fromdateiso8601 * 1000000000),
      payload: (
        [ .chat_messages[] |
          (.text // (.content[0].text // ""))
            as $body |
          select($body != "") |
          (.sender + ": " + $body)
        ] | join("\n\n---\n\n")
      ),
      meta: ({
        name: (.name // "untitled"),
        uuid: (.uuid // ""),
        created_at: .created_at,
        source: "claude-ai-export"
      } | tostring)
    } | select(.payload != "")
  ]
' "$FILE")

COUNT=$(echo "$ENGRAMS_JSON" | jq 'length')
TOTAL_BYTES=$(echo "$ENGRAMS_JSON" | jq '[.[] | .payload | length] | add // 0')

echo "Will import: $COUNT conversations from Claude.ai, ~$(($TOTAL_BYTES / 1024)) KB total payload"

if [ $DRY_RUN -eq 1 ]; then
    echo "(dry-run — nothing sent to daemon)"
    echo "First conversation preview:"
    echo "$ENGRAMS_JSON" | jq '.[0] | { surface, ts, name: (.meta | fromjson | .name), payload_preview: (.payload[0:300]) }'
    exit 0
fi

TOTAL_BATCHES=$(( (COUNT + 99) / 100 ))
echo "Inserting in $TOTAL_BATCHES batches of 100..."

for i in $(seq 0 $((TOTAL_BATCHES - 1))); do
    BATCH=$(echo "$ENGRAMS_JSON" | jq -c ".[$((i * 100)):$((i * 100 + 100))]")
    RESPONSE=$(echo "$BATCH" | curl -s -X POST --unix-socket "$SOCK" \
        -H 'Content-Type: application/json' \
        --data-binary @- \
        http://localhost/engrams/batch)
    INSERTED=$(echo "$RESPONSE" | jq -r '.inserted // 0')
    printf "  batch %d/%d: inserted=%d\n" $((i + 1)) $TOTAL_BATCHES "$INSERTED"
done

echo
echo "Done. Verify with:"
echo "  eideticd --stats"
echo "  curl --unix-socket $SOCK 'http://localhost/engrams/count?surface=$SURFACE'"
echo
echo "Try recall (these and your Claude Code engrams are now in one store):"
echo "  eideticd --ask 'something I discussed with Claude'"
