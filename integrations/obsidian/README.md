# Eidetic Engrams — Obsidian plugin

Capture vault notes to [`eidetic-daemon`](https://github.com/eidetic-works/eidetic-daemon)
and recall engrams from any Claude Code / Cursor / VS Code session, without
leaving Obsidian.

## Features

- **Capture on save** (toggleable) — every modified `.md` file is sent to the
  daemon as an engram with `surface=obsidian` and `meta={vault_name, file_path}`.
  Debounced (default 2 500 ms) so per-keystroke edits don't spam the daemon.
- **Command palette: `Eidetic: Recall (ask eidetic-daemon)`** — prompts for a
  natural-language question, calls `GET /ask`, and inserts the matching
  engrams as a markdown callout block at the cursor with daemon-emitted
  grounding instructions and per-engram citations.
- **Status bar dot** — green when `/healthz` is reachable, red when not,
  grey while polling. Polls every 60 s by default; click to recheck now.

## Install (development)

This plugin is not yet on the Obsidian community directory. Load it unpacked:

```bash
cd integrations/obsidian
npm install
npm run build   # produces main.js at the plugin root
```

Then copy `main.js`, `manifest.json`, and `styles.css` (or symlink the entire
plugin folder) into your vault at:

```
<vault>/.obsidian/plugins/eidetic-obsidian/
```

In Obsidian: **Settings → Community plugins → Installed plugins → reload**,
then enable **Eidetic Engrams**.

Submission to the community-plugin directory is deferred until v0.1.0.

## Settings

| Key | Default | Notes |
| --- | --- | --- |
| `daemonUrl` | `uds:/tmp/eidetic-daemon.sock` | Use `http://127.0.0.1:9876` on Windows, mobile, or remote bridges. |
| `bearerToken` | _(empty)_ | Required when daemon is exposed over a Cloudflare tunnel. |
| `captureOnModify` | `true` | When false, only the Recall command runs. |
| `debounceMs` | `2500` | Wait after last edit before sending. |
| `healthPollMs` | `60000` | Status-bar refresh interval. |
| `timeoutMs` | `5000` | Per-request HTTP timeout. |

## Mobile

UDS sockets aren't available on iOS / Android. Set `daemonUrl` to a remote
HTTP URL (typically a Cloudflare tunnel pointing at `eideticd -bridge :8421`)
and supply the matching Bearer token. If the URL is left as the default
UDS path on mobile, capture is silently disabled and a one-time Notice
informs the user.

## Privacy

- The plugin contacts **only** the URL you configure in settings. No
  analytics, no third-party endpoints, no telemetry.
- Notes are sent to the daemon as raw markdown. If you don't want a note
  captured, either toggle `captureOnModify` off globally, or store the note
  in a folder excluded from your daemon's surface configuration.
- The daemon, in turn, stores engrams locally in SQLite under
  `~/.eidetic/engrams.db`. Cloud sync (R2) is opt-in per ADR-020.

## Development

```bash
npm run dev          # esbuild in watch mode
npm run check-types  # tsc --noEmit
npm run build        # production bundle (also runs tsc --noEmit)
```

The bundle output (`main.js`) is the file Obsidian loads. `manifest.json` and
`styles.css` sit alongside it and are read directly by the plugin loader.

## Compatibility

- Obsidian ≥ 1.0.0 (`minAppVersion` in manifest)
- `eidetic-daemon` ≥ 0.0.45 (uses `/healthz`, `/ask`, `/engrams`)
- Desktop (macOS, Linux, Windows) and Mobile (with HTTP bridge)
