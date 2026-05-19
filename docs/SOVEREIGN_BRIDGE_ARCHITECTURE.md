# Sovereign + Bridge Architecture

*Written 2026-05-19. Covers: Sovereign platform brand decision, Cloudflare tunnel wiring, eidetic Bridge dual-listener design, port allocation, and the Windows capture path fix shipped same session.*

---

## 1. Sovereign — brand and platform positioning

### Decision

**Sovereign is an Eidetic Works platform product, not a NucleusOS sub-product.**

| Dimension | Decision | Rationale |
|---|---|---|
| Parent brand | Eidetic Works (LLC) | Sovereign = "local-first AI you own" — shares the privacy-first ethos of Eidetic. NucleusOS is a developer product brand; Sovereign targets consumers. |
| Relationship to NucleusOS | Peer product, not sub-product | NucleusOS is an AI orchestration OS for builders. Sovereign is a personal AI platform (voice, memory, mobile). Different audiences, different GTM. |
| Tunnel hostname | `sovereign.eidetic.works` | Not `sovereign.nucleusos.dev` — that would leak the NucleusOS brand to Sovereign beta users before any official bundle. `eidetic.works` is the clean parent. |
| `sovereign` keyword reservation | Not reserved for future use | Sovereign is the name. The ethos (local-first, sovereign AI) IS the product. Not a prefix for unrelated things. |

### What Sovereign is

- **Mac daemon layer**: always-on local services (voice XTTS, future engram bridge, sensor hooks)
- **iPhone companion**: iOS app connecting to the Mac daemon via the Cloudflare tunnel
- **The protocol**: iPhone ↔ `sovereign.eidetic.works` ↔ Cloudflare tunnel ↔ Mac localhost daemon(s)
- **Future**: potentially integrates with ChatGPT/Claude mobile apps as an external tool endpoint

Sovereign is the *platform ethos*: your AI runs on your hardware, you control it. Eidetic Works is the LLC. NucleusOS is a separate developer-facing product.

---

## 2. Cloudflare tunnel wiring

### Named tunnel

```
Tunnel ID:  812b058f-3422-421c-ba1b-7a641c5b8bfe
Name:       sovereign
```

### Config (`~/.cloudflared/config.yml`)

```yaml
tunnel: 812b058f-3422-421c-ba1b-7a641c5b8bfe
credentials-file: /Users/lokeshgarg/.cloudflared/812b058f-3422-421c-ba1b-7a641c5b8bfe.json

ingress:
  - hostname: sovereign.eidetic.works
    service: http://localhost:8420        # XTTS voice daemon

  - hostname: telemetry.nucleusos.dev
    service: http://localhost:4318        # OpenTelemetry collector

  - hostname: tg.nucleusos.dev
    service: http://localhost:5001        # Telegram bot

  - service: http_status:404
```

### DNS CNAME

`sovereign.eidetic.works CNAME <tunnel-id>.cfargotunnel.com` — already exists (confirmed via `cloudflared tunnel route dns`, which exits 0 when CNAME is present).

### Port allocation (Sovereign tunnel)

| Port | Service | Owner |
|---|---|---|
| 8420 | XTTS voice daemon (Coqui TTS, MPS, 3 voice profiles) | `claude_code_voice` / Sovereign |
| 4318 | OTel collector | NucleusOS telemetry |
| 5001 | Telegram bot | NucleusOS tg.nucleusos.dev |

**Port 8420 is XTTS-only. eidetic daemon must never bind this port.**

---

## 3. eidetic Bridge dual-listener (v0.0.31)

### Problem

Before v0.0.31, `eideticd` supported exactly one listener: either UDS (default, local MCP) or TCP (opt-in via `EIDETIC_TCP=1`). These were mutually exclusive — you could serve MCP clients OR expose a remote endpoint, not both.

### Solution

`-bridge <addr>` starts a second `api.Server` instance on a TCP address, sharing the same `*store.Store`. The primary listener (UDS or TCP) is unchanged.

```
┌─────────────────────────────────────────────┐
│                  eideticd                   │
│                                             │
│  Primary listener            Bridge listener│
│  /tmp/eidetic-daemon.sock    :8421 (TCP)    │
│  (UDS, 0600, no auth)        (auth + CORS)  │
│           │                       │         │
│           └──────┐  ┌─────────────┘         │
│                  ▼  ▼                       │
│              store.Store                    │
│              ~/.eidetic/engrams.db          │
└─────────────────────────────────────────────┘
         │                    │
    MCP clients          Cloudflare tunnel
    (Claude Code,        (iPhone app,
     Cursor, Cline)       Claude.ai web)
```

### Auth model

| Listener | Auth | Token location | Notes |
|---|---|---|---|
| UDS (primary) | None (UDS trust boundary = 0600 socket) | N/A | W1 security model |
| Bridge (TCP) | Always-on Bearer | `~/.eidetic/bridge-token` (0600) | Rotates every restart |

### CORS

Bridge listener has `Access-Control-Allow-Origin: *`. This is intentional: the Bearer token is the auth boundary. CORS is required for browser-based AI clients (Claude.ai, ChatGPT web) that run origin checks.

### CORS middleware (code)

`internal/api/server.go`:

```go
if opts.CORS {
    inner := handler
    handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Access-Control-Allow-Origin", "*")
        w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
        w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
        if r.Method == http.MethodOptions {
            w.WriteHeader(http.StatusNoContent)
            return
        }
        inner.ServeHTTP(w, r)
    })
}
```

### Activating the bridge

The `-bridge` flag exists but is NOT activated in the launchd plist by default. To enable:

```xml
<!-- ~/Library/LaunchAgents/works.eidetic.eideticd.plist -->
<key>ProgramArguments</key>
<array>
    <string>/path/to/eideticd</string>
    <string>-bridge</string>
    <string>:8421</string>   <!-- NOT :8420 — that's XTTS -->
</array>
```

Then reload launchd:
```bash
launchctl unload ~/Library/LaunchAgents/works.eidetic.eideticd.plist
launchctl load ~/Library/LaunchAgents/works.eidetic.eideticd.plist
```

To use with the Cloudflare tunnel, add a new ingress rule in `~/.cloudflared/config.yml`:
```yaml
- hostname: engrams.eidetic.works      # proposed; not wired yet
  service: http://localhost:8421
```

Then `cloudflared tunnel route dns sovereign engrams.eidetic.works`.

---

## 4. Port conflict incident (2026-05-19)

### What happened

During the v0.0.31 bridge build, the launchd plist was updated to include `-bridge :8420`. Port 8420 is owned by the XTTS voice daemon. When eideticd started with `-bridge :8420`, it bound that port with Bearer auth, blocking XTTS from receiving connections.

`cc_voice` detected this and reported: *"The eideticd binary was squatting on port 8420 with Bearer auth — that's from another project. You may want to prevent it from auto-starting again."*

### Fix

1. Reverted plist to no `-bridge` flag
2. Killed all bridge-enabled eideticd processes (`pkill -9 eideticd`)
3. Reloaded launchd service
4. Verified: `lsof -i :8420` → empty; `/tmp/eidetic-daemon.sock` → `{"status":"ok"}`
5. Sent relay to cc_voice confirming port 8420 is clear

### Port allocation rule going forward

| Port | Service | Inviolable |
|---|---|---|
| 8420 | XTTS (Sovereign voice) | Yes — XTTS owns this permanently |
| 8421 | eidetic Bridge (if activated) | Proposed default |
| 9876 | eidetic TCP mode (legacy, opt-in) | Existing |

---

## 5. Windows capture path fix (v0.0.30)

### Problem

`DefaultSurfaces()` in `internal/capture/watcher.go` built the Claude Code root path using `filepath.Join(home, ".claude", "projects")`. This is correct on macOS/Linux but wrong on Windows.

Windows platform paths:
- Claude Code: `%APPDATA%\Claude\projects` (Electron, uses AppData/Roaming)
- Cursor: `%APPDATA%\Cursor\User\workspaceStorage`

### Solution

Split platform-specific path logic into three build-tagged files:

```
internal/capture/
  paths_darwin.go     //go:build darwin
  paths_linux.go      //go:build linux   (was paths_other.go with !darwin tag)
  paths_windows.go    //go:build windows
```

Each file exports:
```go
func claudeRoot(home string) string { ... }
func cursorRoot(home string) string { ... }
```

`DefaultSurfaces()` calls `claudeRoot(home)` and `cursorRoot(home)` — platform-agnostic.

### Windows implementation

`paths_windows.go`:
```go
//go:build windows
package capture

import (
    "os"
    "path/filepath"
)

func claudeRoot(home string) string {
    appdata := os.Getenv("APPDATA")
    if appdata == "" {
        appdata = filepath.Join(home, "AppData", "Roaming")
    }
    return filepath.Join(appdata, "Claude", "projects")
}

func cursorRoot(home string) string {
    appdata := os.Getenv("APPDATA")
    if appdata == "" {
        appdata = filepath.Join(home, "AppData", "Roaming")
    }
    return filepath.Join(appdata, "Cursor", "User", "workspaceStorage")
}
```

APPDATA fallback (`filepath.Join(home, "AppData", "Roaming")`) handles edge cases where the env var is stripped (e.g., service contexts, WSL boundary leaks).

### Cross-compile verification

All four targets cross-compile cleanly from darwin-arm64 host:
```bash
GOOS=windows GOARCH=amd64 go build ./...  # OK
GOOS=linux   GOARCH=amd64 go build ./...  # OK
GOOS=linux   GOARCH=arm64 go build ./...  # OK
GOOS=darwin  GOARCH=arm64 go build ./...  # OK
```

No CGO — `modernc.org/sqlite` pure Go, zero C toolchain requirement.

---

## 6. Current daemon state (2026-05-19)

```
Version:    v0.0.31
Binary:     /Users/lokeshgarg/work-eidetic-daemon/work/bin/eideticd
Socket:     /tmp/eidetic-daemon.sock (UDS, 0600)
DB:         ~/.eidetic/engrams.db
Launchd:    works.eidetic.eideticd (KeepAlive=true, RunAtLoad=true)
Bridge:     NOT active (flag exists, plist does not pass -bridge)
Port 8420:  CLEAR (XTTS owns it)
```

Health check:
```bash
curl -s --unix-socket /tmp/eidetic-daemon.sock http://localhost/healthz
# → {"status":"ok"}

curl -s --unix-socket /tmp/eidetic-daemon.sock http://localhost/metrics | jq .version
# → "v0.0.31"
```
