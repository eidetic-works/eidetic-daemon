# Eidetic Engrams — VS Code extension

Browse, search, and recall engrams captured by
[`eidetic-daemon`](https://github.com/eidetic-works/eidetic-daemon) from your
Claude Code and Cursor sessions, without leaving VS Code.

## Features

- **Recent Engrams** sidebar — tree view of the last 50 engrams grouped by
  surface, auto-refreshed every 60 s.
- **Search** (`Eidetic: Search Engrams`) — FTS5-backed quickpick over the
  daemon's `/search` endpoint; pick a hit to open the full engram in a panel.
- **Recall** (`Eidetic: Recall (Ask Question)`) — natural-language question
  hitting `/ask`, rendered with citations + the daemon's grounding instructions.
- **Status bar** — total engram count, refreshed every 5 min; click to recall.
- **Settings** — socket path, request timeout, optional surface filter.

## Requirements

- `eidetic-daemon` v0.0.45 or newer running locally.
  - macOS / Linux: UDS at `/tmp/eidetic-daemon.sock` (override via `eidetic.socketPath`).
  - Windows: TCP on `127.0.0.1:9876` (override via `eidetic.tcpHost` / `eidetic.tcpPort`).

## Install (development)

```bash
cd integrations/vscode
npm install
npm run compile
```

Then in VS Code: **Run > Start Debugging** with the `Run Extension` launch
config, or package a `.vsix` with `npx @vscode/vsce package`.

## Settings

| Key | Default | Notes |
| --- | --- | --- |
| `eidetic.socketPath` | `/tmp/eidetic-daemon.sock` | UDS path (mac/linux). |
| `eidetic.tcpHost` | `127.0.0.1` | TCP host (windows fallback). |
| `eidetic.tcpPort` | `9876` | TCP port (windows fallback). |
| `eidetic.timeoutMs` | `5000` | Per-request HTTP timeout. |
| `eidetic.surfaceFilter` | `""` | If non-empty, restricts `/search` and `/recent` to one surface. |
| `eidetic.recentPollMs` | `60000` | Recent-engrams tree refresh interval. |
| `eidetic.metricsPollMs` | `300000` | Status-bar refresh interval. |

## Development

```bash
npm run watch         # esbuild in watch mode
npm run check-types   # tsc --noEmit
npm run build-tests   # compile mocha smoke tests
npm test              # run daemonClient smoke tests
npm run package       # production bundle ready for `vsce package`
```

The smoke tests boot an in-process mock HTTP server, so no daemon is required.

## Deferred

- Marketplace publisher cert / `LICENSE` propagation.
- Custom activity-bar icon SVG (currently uses VS Code's built-in `$(database)`).
- Extension-host integration tests via `@vscode/test-electron`.
- Telemetry on command usage.
