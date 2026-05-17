#!/usr/bin/env bash
# Eidetic Works daemon — uninstaller.
#
# Usage:
#   curl -fsSL https://eidetic.works/uninstall.sh | sh
#
# Stops the daemon, unregisters the launchd / systemd-user service, removes
# the binary. Leaves ~/.eidetic/ (engrams.db + state.json) untouched by
# default — pass --purge-data to also remove it.
#
# Flags:
#   --purge-data   Remove ~/.eidetic/ (irreversible — all engrams deleted)
#   --prefix PATH  Binary install prefix (default: /usr/local)
set -euo pipefail

PREFIX="${EIDETIC_PREFIX:-/usr/local}"
PURGE_DATA=0
LAUNCHD_LABEL="works.eidetic.eideticd"
SYSTEMD_UNIT="eideticd.service"

err() { echo "uninstall: $*" >&2; exit 1; }
log() { echo "uninstall: $*"; }

parse_args() {
  for arg in "$@"; do
    case "$arg" in
      --purge-data) PURGE_DATA=1 ;;
      --prefix=*)   PREFIX="${arg#--prefix=}" ;;
      --prefix)     err "--prefix requires a value (--prefix=/usr/local)" ;;
      -h|--help)
        echo "Usage: uninstall.sh [--purge-data] [--prefix=PATH]"
        echo "  --purge-data  Also delete ~/.eidetic/ (all engrams, irreversible)"
        echo "  --prefix=PATH Binary prefix (default: /usr/local)"
        exit 0 ;;
      *) err "unknown flag: $arg" ;;
    esac
  done
}

stop_macos() {
  local plist="${HOME}/Library/LaunchAgents/${LAUNCHD_LABEL}.plist"
  if launchctl list "$LAUNCHD_LABEL" &>/dev/null; then
    log "stopping launchd service $LAUNCHD_LABEL"
    launchctl bootout "gui/$(id -u)/${LAUNCHD_LABEL}" 2>/dev/null || true
  else
    log "launchd service not loaded (already stopped or never installed)"
  fi
  if [ -f "$plist" ]; then
    rm -f "$plist"
    log "removed $plist"
  fi
}

stop_linux() {
  local unit="${HOME}/.config/systemd/user/${SYSTEMD_UNIT}"
  if systemctl --user is-active --quiet "${SYSTEMD_UNIT}" 2>/dev/null; then
    log "stopping systemd-user service ${SYSTEMD_UNIT}"
    systemctl --user disable --now "${SYSTEMD_UNIT}" 2>/dev/null || true
    systemctl --user daemon-reload
  else
    log "systemd-user service not active (already stopped or never installed)"
  fi
  if [ -f "$unit" ]; then
    rm -f "$unit"
    log "removed $unit"
    systemctl --user daemon-reload 2>/dev/null || true
  fi
}

remove_binary() {
  local bin="${PREFIX}/bin/eideticd"
  if [ -f "$bin" ]; then
    rm -f "$bin"
    log "removed $bin"
  else
    log "binary not found at $bin (already removed?)"
  fi
}

remove_socket() {
  local sock="/tmp/eidetic-daemon.sock"
  [ -S "$sock" ] && rm -f "$sock" && log "removed stale socket $sock" || true
}

purge_data() {
  local data="${EIDETIC_DATA_DIR:-${HOME}/.eidetic}"
  if [ -d "$data" ]; then
    log "WARNING: removing $data — this deletes all engrams permanently"
    rm -rf "$data"
    log "removed $data"
  else
    log "data directory $data not found (nothing to purge)"
  fi
}

main() {
  parse_args "$@"

  case "$(uname -s)" in
    Darwin) stop_macos ;;
    Linux)  stop_linux ;;
    *)      log "unsupported OS for service stop; stopping any running daemon manually" ;;
  esac

  # Kill any lingering process not managed by service manager.
  pkill -x eideticd 2>/dev/null && log "killed stray eideticd process" || true

  remove_socket
  remove_binary

  if [ "$PURGE_DATA" -eq 1 ]; then
    purge_data
  else
    local data="${EIDETIC_DATA_DIR:-${HOME}/.eidetic}"
    log "engram data retained at $data (pass --purge-data to remove)"
  fi

  log "uninstall complete"
}

main "$@"
