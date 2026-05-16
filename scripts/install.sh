#!/usr/bin/env bash
# Eidetic Works daemon — one-line installer.
#
# Usage:
#   curl -fsSL https://nucleusos.dev/install.sh | sh
#
# Detects host OS + arch, downloads the matching release binary, places it
# in /usr/local/bin (or $EIDETIC_PREFIX/bin), and registers the launchd /
# systemd-user service so the daemon spawns at login.
#
# Per ADR-016 the spawn-at-startup mandate is non-negotiable: it absorbs the
# 1.75s modernc.org/sqlite cold-init behind login UI.
set -euo pipefail

REPO="${EIDETIC_REPO:-eidetic-works/eidetic-daemon}"
PREFIX="${EIDETIC_PREFIX:-/usr/local}"
VERSION="${EIDETIC_VERSION:-latest}"

err() { echo "install: $*" >&2; exit 1; }
log() { echo "install: $*"; }

detect_target() {
  local os arch
  case "$(uname -s)" in
    Darwin) os=darwin ;;
    Linux)  os=linux ;;
    *) err "unsupported OS: $(uname -s) — see ADR-016 for cross-compile coverage" ;;
  esac
  case "$(uname -m)" in
    arm64|aarch64) arch=arm64 ;;
    x86_64|amd64)  arch=amd64 ;;
    *) err "unsupported arch: $(uname -m)" ;;
  esac
  if [ "$os" = "darwin" ] && [ "$arch" = "amd64" ]; then
    err "darwin/amd64 not in W1 release matrix; use Rosetta-translated arm64 build"
  fi
  echo "${os}-${arch}"
}

download_url() {
  local target="$1"
  if [ "$VERSION" = "latest" ]; then
    echo "https://github.com/${REPO}/releases/latest/download/eideticd-${target}"
  else
    echo "https://github.com/${REPO}/releases/download/${VERSION}/eideticd-${target}"
  fi
}

install_binary() {
  local target="$1" url tmp
  url="$(download_url "$target")"
  tmp="$(mktemp -d)"
  log "fetching $url"
  if ! curl -fsSL "$url" -o "$tmp/eideticd"; then
    rm -rf "$tmp"
    err "download failed (404 or network error). If 404: the daemon repo may still be private during early-access — see https://github.com/${REPO}/releases for status, or contact hello@nucleusos.dev"
  fi
  install -d "${PREFIX}/bin"
  install -m 0755 "$tmp/eideticd" "${PREFIX}/bin/eideticd"
  rm -rf "$tmp"
  log "installed ${PREFIX}/bin/eideticd"
}

register_macos() {
  local plist="${HOME}/Library/LaunchAgents/works.eidetic.eideticd.plist"
  install -d "$(dirname "$plist")"
  cat > "$plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>works.eidetic.eideticd</string>
    <key>ProgramArguments</key>
    <array>
        <string>${PREFIX}/bin/eideticd</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/tmp/eideticd.out.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/eideticd.err.log</string>
</dict>
</plist>
PLIST
  log "registered LaunchAgent at $plist"
  launchctl bootout "gui/$(id -u)/works.eidetic.eideticd" 2>/dev/null || true
  launchctl bootstrap "gui/$(id -u)" "$plist"
  log "launchd: started works.eidetic.eideticd"
}

register_linux() {
  local unit="${HOME}/.config/systemd/user/eideticd.service"
  install -d "$(dirname "$unit")"
  cat > "$unit" <<UNIT
[Unit]
Description=Eidetic Works engram daemon
After=default.target

[Service]
Type=simple
ExecStart=${PREFIX}/bin/eideticd
Restart=always
RestartSec=5

[Install]
WantedBy=default.target
UNIT
  log "registered systemd-user unit at $unit"
  systemctl --user daemon-reload
  systemctl --user enable --now eideticd.service
  log "systemd-user: started eideticd.service"
}

main() {
  command -v curl >/dev/null || err "curl is required"

  local target
  target="$(detect_target)"
  log "target: $target"
  install_binary "$target"

  case "$(uname -s)" in
    Darwin) register_macos ;;
    Linux)  register_linux ;;
  esac

  log "smoke test"
  sleep 1
  if "${PREFIX}/bin/eideticd" -version; then
    log "OK — daemon installed and registered. UDS: /tmp/eidetic-daemon.sock (Mac) or /var/run/eidetic.sock (Linux)"
  else
    err "binary failed -version smoke; see logs"
  fi
}

main "$@"
