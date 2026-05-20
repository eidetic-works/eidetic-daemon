#!/usr/bin/env bash
# weekly-digest.sh — pull /digest from the local daemon, render as plain-text,
# optionally email or write to /tmp.
#
# Usage:
#   ./weekly-digest.sh                  # prints to stdout
#   ./weekly-digest.sh --email you@x    # pipes via `mail` (requires mail/mailx)
#   ./weekly-digest.sh --tee            # writes to /tmp/eidetic-weekly-digest.txt + stdout
#   ./weekly-digest.sh --window 30d     # different window (24h | 7d | 30d)
#
# Cron-friendly: install via `crontab -e`:
#   0 9 * * 1   /path/to/eideticd/scripts/weekly-digest.sh --tee >/dev/null
#
# Privacy: makes one local UDS request to /digest. Nothing leaves your machine
# unless you pipe the output to an external mailer. Per ADR-020.

set -euo pipefail

WINDOW="7d"
EMAIL=""
TEE=0
SOCK="/tmp/eidetic-daemon.sock"

while [ $# -gt 0 ]; do
    case "$1" in
        --window) WINDOW="$2"; shift 2 ;;
        --email)  EMAIL="$2"; shift 2 ;;
        --tee)    TEE=1; shift ;;
        --sock)   SOCK="$2"; shift 2 ;;
        --help|-h)
            sed -n '2,16p' "$0" | sed 's/^# \?//'
            exit 0
            ;;
        *) echo "unknown flag: $1" >&2; exit 1 ;;
    esac
done

case "$WINDOW" in
    24h|7d|30d) ;;
    *) echo "error: --window must be 24h | 7d | 30d (got: $WINDOW)" >&2; exit 1 ;;
esac

if ! command -v curl >/dev/null 2>&1; then
    echo "error: curl required" >&2
    exit 1
fi
if ! command -v jq >/dev/null 2>&1; then
    echo "error: jq required (brew install jq / apt install jq)" >&2
    exit 1
fi
if [ ! -S "$SOCK" ]; then
    echo "error: daemon socket not found at $SOCK — is eideticd running?" >&2
    exit 1
fi

# Fetch
JSON=$(curl -s --unix-socket "$SOCK" "http://localhost/digest?window=$WINDOW")
if [ -z "$JSON" ]; then
    echo "error: empty response from /digest" >&2
    exit 1
fi

# Render as plain-text
render() {
    local total surfaces top_terms top_hours samples
    total=$(jq -r '.total_engrams' <<< "$JSON")
    if [ "$total" = "0" ]; then
        echo "No engrams in the last $WINDOW. Nothing to recap."
        return
    fi

    echo "eidetic-daemon — $WINDOW recap"
    echo "============================================="
    echo
    echo "Total engrams: $total"
    echo
    echo "By surface:"
    jq -r '.by_surface | to_entries | sort_by(-.value) | .[] | "  \(.key | gsub(" "; "_"; "g")) \(.value)"' <<< "$JSON" | column -t
    echo
    echo "Most-active hours (UTC):"
    jq -r '.top_hours[]? | "  \(.hour):00 — \(.count) engrams"' <<< "$JSON"
    echo
    echo "Top terms:"
    jq -r '.top_terms[]? | "  \(.term) (\(.count))"' <<< "$JSON" | head -10
    echo
    echo "Sampled engrams:"
    jq -r '.sample_engrams[]? | "  [\(.surface)] \(.payload[0:120] | gsub("\n"; " "))..."' <<< "$JSON" | head -10
    echo
    echo "Generated $(date -u +%Y-%m-%dT%H:%M:%SZ)"
    echo "Source: $SOCK/digest?window=$WINDOW"
}

OUT=$(render)

if [ -n "$EMAIL" ]; then
    if command -v mail >/dev/null 2>&1; then
        echo "$OUT" | mail -s "eidetic weekly recap ($WINDOW)" "$EMAIL"
        echo "sent to $EMAIL"
    else
        echo "error: --email requires mail/mailx in PATH; falling back to stdout" >&2
        echo "$OUT"
    fi
elif [ "$TEE" = "1" ]; then
    OUT_FILE="/tmp/eidetic-weekly-digest.txt"
    echo "$OUT" | tee "$OUT_FILE"
    echo "(also written to $OUT_FILE)"
else
    echo "$OUT"
fi
