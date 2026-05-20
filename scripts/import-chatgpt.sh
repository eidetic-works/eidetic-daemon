#!/usr/bin/env bash
# import-chatgpt.sh — seed your engram store from a ChatGPT export.
#
# Step 1: in ChatGPT, Settings → Data Controls → Export → wait for email →
#         download the .zip and extract `conversations.json`.
# Step 2: ./scripts/import-chatgpt.sh /path/to/conversations.json
#
# Each ChatGPT conversation becomes ONE engram in the `chatgpt` surface;
# the engram payload contains the full transcript (joined messages with
# author tags). Metadata captures the original conversation title +
# create_time so you can filter by date later.
#
# Idempotent-ish: re-running over the same file inserts duplicates today
# (no de-dupe on ChatGPT conversation_id). Plan: pipe through `--dry-run`
# first to preview counts.
#
# Privacy (per ADR-020): runs entirely locally. Uses POST /engrams/batch
# via UDS — nothing leaves your machine.

set -euo pipefail

FILE="${1:-}"
SOCK="${EIDETIC_SOCK:-/tmp/eidetic-daemon.sock}"
DRY_RUN=0
SURFACE="chatgpt"

while [ $# -gt 0 ]; do
    case "$1" in
        --dry-run)  DRY_RUN=1; shift ;;
        --sock)     SOCK="$2"; shift 2 ;;
        --surface)  SURFACE="$2"; shift 2 ;;
        --help|-h)
            sed -n '2,17p' "$0" | sed 's/^# \?//'
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
    echo "(use --dry-run to preview without daemon)" >&2
    exit 1
fi

# Transform: each conversation → one engram. Build a transcript by joining
# mapping[*].message.content.parts (when present) with author tags.
ENGRAMS_JSON=$(jq -c '
  [
    .[] | select(.mapping != null) | {
      surface: "'"$SURFACE"'",
      ts: ((.create_time // (now)) * 1000000000 | floor),
      payload: (
        [ .mapping[] | select(.message != null) |
          select(.message.content.parts != null) |
          select(.message.content.parts | length > 0) |
          select(.message.content.parts[0] | type == "string") |
          (.message.author.role + ": " + (.message.content.parts | join("\n")))
        ] | join("\n\n---\n\n")
      ),
      meta: ({
        title: (.title // "untitled"),
        conversation_id: (.conversation_id // .id // ""),
        create_time: .create_time,
        source: "chatgpt-export"
      } | tostring)
    } | select(.payload != "")
  ]
' "$FILE")

COUNT=$(echo "$ENGRAMS_JSON" | jq 'length')
TOTAL_BYTES=$(echo "$ENGRAMS_JSON" | jq '[.[] | .payload | length] | add // 0')

echo "Will import: $COUNT conversations, ~$(($TOTAL_BYTES / 1024)) KB total payload"

if [ $DRY_RUN -eq 1 ]; then
    echo "(dry-run — nothing sent to daemon)"
    echo "First conversation preview:"
    echo "$ENGRAMS_JSON" | jq '.[0] | { surface, ts, title: (.meta | fromjson | .title), payload_preview: (.payload[0:300]) }'
    exit 0
fi

# Batch in chunks of 100 to avoid POST /engrams/batch's 32 MiB body cap.
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
echo "Try recall:"
echo "  eideticd --ask 'something I asked ChatGPT recently'"
