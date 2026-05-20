# Eidetic Engrams — JetBrains plugin

Browse, search, and recall engrams captured by
[`eidetic-daemon`](https://github.com/eidetic-works/eidetic-daemon) from your
Claude Code, Cursor, and JetBrains AI sessions, without leaving the IDE.

Compatible with any IntelliJ Platform IDE on 2024.1+: IDEA, PyCharm, GoLand,
WebStorm, Rider, PhpStorm, RubyMine, CLion, etc.

## Features

- **Eidetic tool window** (right sidebar) — three tabs:
  - **Recent** — last 50 engrams across surfaces, refresh button.
  - **Surfaces** — surface → engram count, descending.
  - **Search** — inline FTS5 query box.
- **Recall** (`Tools → Eidetic: Recall…`) — natural-language question hitting
  `/ask`, rendered with citations + the daemon's grounding instructions.
- **Search** (`Tools → Eidetic: Search Engrams…`) — popup with FTS5 hits;
  pick a hit to view the full payload.
- **Settings** — `Settings → Tools → Eidetic Engrams`:
  socket path, TCP host/port, request timeout, surface filter, refresh interval.

## Requirements

- `eidetic-daemon` v0.0.45 or newer running locally.
  - macOS / Linux: UDS at `/tmp/eidetic-daemon.sock`
  - Windows: TCP on `127.0.0.1:9876` (or set `EIDETIC_TCP=1` on mac/linux to
    force TCP).
- An IntelliJ Platform IDE on build 241 (2024.1) or newer.
- Java 17 toolchain for the build.

## Build

```bash
cd integrations/jetbrains
./gradlew buildPlugin
```

The distributable `.zip` lands at
`build/distributions/eidetic-jetbrains-<version>.zip`.

First-time builds download the IntelliJ Platform SDK (~1 GB) into the Gradle
cache; subsequent builds are fast.

## Install (development)

1. `./gradlew buildPlugin`
2. In your IDE: **Settings → Plugins → ⚙ → Install Plugin from Disk…**
3. Pick `build/distributions/eidetic-jetbrains-<version>.zip`.
4. Restart the IDE.

Alternatively, run the plugin in a sandbox IDE without installing:

```bash
./gradlew runIde
```

This launches a fresh IntelliJ instance with the plugin pre-loaded.

## Settings

| Setting | Default | Notes |
| --- | --- | --- |
| UDS socket path | `/tmp/eidetic-daemon.sock` | macOS / Linux transport. |
| TCP host | `127.0.0.1` | Windows fallback (or `forceTcp = true`). |
| TCP port | `9876` | Windows fallback. |
| Request timeout (ms) | `5000` | Per-request HTTP timeout. |
| Surface filter | `""` | Empty = all surfaces. |
| Recent refresh (ms) | `60000` | Tool-window auto-refresh interval. |
| Force TCP | `false` | Override OS default (mac/linux to TCP). |

## Architecture notes

- `DaemonClient` (application service) does UDS over a raw `SocketChannel` —
  Java's `HttpClient` doesn't expose a custom-socket hook, so we emit
  HTTP/1.1 by hand and parse the response. The wire shape is identical to
  Node's `http.request({ socketPath })`, so the daemon mux accepts it
  unchanged.
- TCP path uses `java.net.http.HttpClient` directly.
- All HTTP runs on `ApplicationManager.executeOnPooledThread`; UI marshalling
  back to EDT goes through `invokeLater(..., ModalityState.any())`.
- Settings persist to `eidetic.xml` in the IDE config directory via
  `PersistentStateComponent`.

## Deferred

- Background polling for the tool window (currently manual refresh button).
- Native engram-detail editor tab (currently `Messages.showInfoMessage`).
- Marketplace publishing / signing (`signPlugin` / `publishPlugin` blocks).
- Status-bar widget mirroring the VS Code extension's `$(database) N engrams`.
- Status-line update notification when `metrics.update_available` is true.
- Telemetry on action usage.
