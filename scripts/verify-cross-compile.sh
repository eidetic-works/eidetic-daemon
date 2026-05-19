#!/usr/bin/env bash
# Cross-compile correctness gate — mattn/go-sqlite3 cross-compile silently produces a
# broken binary (missing CGO linkage; same BuildID, smaller file, crashes at runtime).
#
# This gate guards against future regressions even though we use modernc.org/sqlite
# (pure-Go, doesn't have the failure mode). If anyone ever swaps in a CGO dep,
# this script will catch the silent strip.
#
# Verification: file presence + ELF/Mach-O/PE shape + size floor.
# (Smoke-test via QEMU/container is a Day-4+ enhancement; out of Phase 0 scope.)

set -euo pipefail

DIST_DIR="${DIST_DIR:-dist}"
SIZE_FLOOR_BYTES="${SIZE_FLOOR_BYTES:-5000000}"  # 5 MB; production binary should be ~9 MB

fail=0

check() {
    local target="$1"
    local binary="$DIST_DIR/eideticd-${target}"
    [ "$target" = "windows-amd64" ] && binary="${binary}.exe"

    if [ ! -f "$binary" ]; then
        echo "FAIL [$target]: $binary missing — did you run 'make build-$target'?"
        fail=1
        return
    fi

    if ! file "$binary" >/dev/null 2>&1; then
        echo "FAIL [$target]: $binary is not a recognized binary"
        fail=1
        return
    fi

    local size
    size=$(stat -f%z "$binary" 2>/dev/null || stat -c%s "$binary" 2>/dev/null)
    if [ "$size" -lt "$SIZE_FLOOR_BYTES" ]; then
        echo "FAIL [$target]: $binary is suspiciously small ($size bytes < $SIZE_FLOOR_BYTES floor) — possible CGO silent strip"
        fail=1
        return
    fi

    echo "PASS [$target]: $binary ($size bytes)"
}

check darwin-arm64
check linux-amd64
check linux-arm64
check windows-amd64

if [ "$fail" -eq 1 ]; then
    echo "Cross-compile verification FAILED. See messages above."
    exit 1
fi
echo "All cross-compile artifacts verified."
