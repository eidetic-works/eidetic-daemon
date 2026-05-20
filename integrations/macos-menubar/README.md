# eidetic-daemon — macOS Menubar Integrations

Two delivery paths for showing eidetic-daemon status in the macOS menubar.
Both are zero-network — they talk to `/tmp/eidetic-daemon.sock` only.

| | SwiftBar plugin | Native AppKit app |
|---|---|---|
| File | `eidetic-status.5m.swift` | `EideticMenubar/EideticMenubar.swift` |
| Status | **PRIMARY — ship now** | **Scaffold only** (sources, no `.xcodeproj`) |
| User installs | drop into `~/Library/Application Support/SwiftBar/Plugins/` | `xcodebuild` + install `.app` |
| Refresh cadence | every 5 min (filename suffix `.5m`) | every 5 min (`Timer.scheduledTimer`) |
| Dependencies | SwiftBar + macOS-default `curl` | none beyond Xcode for building |
| Signing | inherited from SwiftBar | deferred to Lokesh for App Store path |

---

## Path 1: SwiftBar plugin (primary)

### Install

1. Install SwiftBar:
   ```
   brew install --cask swiftbar
   ```
   Open SwiftBar once and point its Plugins folder at
   `~/Library/Application Support/SwiftBar/Plugins/` (the default).

2. Drop the plugin in:
   ```
   cp eidetic-status.5m.swift ~/Library/Application\ Support/SwiftBar/Plugins/
   chmod +x ~/Library/Application\ Support/SwiftBar/Plugins/eidetic-status.5m.swift
   ```

3. SwiftBar will auto-discover it. The filename suffix `.5m` means "refresh
   every 5 minutes". Change to `.30s` / `.1h` etc. if you want a different
   cadence.

### What you see

Title bar: `🧠 300K` (engram count, abbreviated).
Click for dropdown:

- Engrams (total + per-surface breakdown, e.g. `claude_code: 299935`)
- DB size + path
- Uptime, daemon version, query p95 latency
- Last sync time (parsed from `~/.eidetic/sync-state.json` if present)
- Update available? indicator (from `update_available` field in `/metrics`)
- Open dashboard → https://eidetic.works/dashboard
- GitHub repo
- Quit eideticd (`launchctl bootout gui/$UID/works.eidetic.eideticd`)

### Degraded mode

If the daemon is not reachable on `/tmp/eidetic-daemon.sock`, the title
shows `🧠 ⚠` and the dropdown offers a one-click `eideticd -install`.

### Privacy

The plugin connects only to the local UDS at `/tmp/eidetic-daemon.sock`.
It never reaches off-machine. The "Open dashboard" / "GitHub" entries
open a browser tab — those are user-initiated, not automatic.

### Verify locally

```
swiftc -parse integrations/macos-menubar/eidetic-status.5m.swift
swift integrations/macos-menubar/eidetic-status.5m.swift   # prints the menu rendering
```

---

## Path 2: Native AppKit app (scaffold)

### Status

`EideticMenubar/EideticMenubar.swift` and `Info.plist` are checked in.
**No `.xcodeproj` is checked in** — the Xcode project / Package.swift
plumbing is too much to scaffold blindly. The intent is:

1. Lokesh creates `EideticMenubar.xcodeproj` (or `Package.swift`) at his
   keyboard with proper signing identity.
2. Drops `EideticMenubar.swift` in as the sole source file.
3. Drops `Info.plist` in as the bundle plist (`LSUIElement=true` already
   set → no Dock icon).
4. Builds + signs + notarizes for distribution:
   ```
   xcodebuild -project EideticMenubar.xcodeproj -configuration Release
   # then `xcrun notarytool submit` for App Store / outside distribution
   ```

### Why scaffold, not full project

- An `.xcodeproj` is ~200 lines of XML with embedded UUIDs that don't
  meaningfully verify in CI (you'd need a real `xcodebuild` run on a
  signed Mac to know it works).
- The Swift source is wire-equivalent to the SwiftBar plugin — same
  metrics fields, same menu items, same launchctl quit command.
- Lokesh handles the signing identity + provisioning profile, which
  cannot be safely automated.

### Mac menubar gotchas worth documenting

1. **`LSUIElement=true` is mandatory** for menubar-only apps. Without
   it, the app shows a Dock icon and a useless empty main menu.
2. **`setActivationPolicy(.accessory)`** is the runtime equivalent of
   `LSUIElement=true`; setting both is belt-and-suspenders, and the
   plist value wins at launch time.
3. **AppKit cannot be sandboxed and talk to `/tmp/eidetic-daemon.sock`
   simultaneously** unless you add a temporary-exception entitlement.
   For App Store distribution, the daemon socket needs to move to
   `~/Library/Group Containers/<group-id>/eidetic-daemon.sock` and
   both daemon + app need matching App Groups. This is a deferred
   refactor for the App Store path.
4. **`Process` + curl over UDS bridges the sandbox cleanly** but only
   in development (no entitlements). The scaffold uses this pattern;
   App Store builds will need a sandbox-friendly UDS client (raw
   `connect()` + write/read, ~80 LOC).

---

## Compounding with the existing dashboard

Two ways these menubar surfaces extend `https://eidetic.works/dashboard`:

1. **Always-on glance vs. browser-tab dive.** The dashboard answers
   "what's my engram trend, what topics, what tools" — heavy reads on
   demand. The menubar answers "is the daemon up, did sync just run,
   should I update" — passive ambient check that doesn't need a tab.
   Together: dashboard for sessions, menubar for the other 23 hours.

2. **Update channel discovery without polling the web.** The daemon's
   `/metrics` already polls GitHub releases once / 24h and sets
   `update_available`. The menubar surfaces that as a glanceable
   indicator (`⬆︎` in the title, orange "Update available" row), so
   the user discovers a new release without opening the dashboard or
   GitHub. This is the single most actionable signal for distribution
   — most users won't visit the dashboard until something visibly
   nudges them.
