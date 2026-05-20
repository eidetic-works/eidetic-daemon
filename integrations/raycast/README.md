# Eidetic Engrams — Raycast Extension

Browse, search, and recall engrams captured by `eidetic-daemon` (Claude Code,
Cursor, and any other surface plugged into the daemon) without leaving Raycast.

## Commands

| Command | Description |
| --- | --- |
| `Recent Engrams` | List the latest 50 engrams across all surfaces. Filter by surface in the dropdown; press Enter to open the full engram. |
| `Search Engrams` | Server-side debounced full-text search via the daemon's FTS5 index. |
| `Recall (Ask Question)` | Submit a natural-language question, daemon assembles instructions + top-N engrams as Markdown. |
| `Daemon Stats` | View daemon version, engram totals, per-surface breakdown, DB size, last sync, and update availability. |

## Preferences

| Preference | Default | Notes |
| --- | --- | --- |
| `Daemon Socket Path` | `/tmp/eidetic-daemon.sock` | Override only if you run the daemon with a custom `--socket` flag. |
| `Surface Filter` | `(empty)` | Optional surface name used by Recent + Search + Recall. Empty = all surfaces. |
| `Search Debounce (ms)` | `250` | Wait between keystrokes before firing `GET /search`. |
| `Request Timeout (ms)` | `5000` | Per-request timeout for daemon HTTP calls. |

## Install (local dev)

```bash
cd integrations/raycast
npm install
npm run dev      # opens Raycast in dev mode, hot-reloads as you edit
```

Once `npm run dev` is running:

1. Open Raycast (`⌥ Space` by default).
2. Type `eidetic` — the four commands should appear under the **Eidetic Engrams** group.
3. Pick any command; results populate from the local daemon's UDS socket.

## Build

```bash
npm run build    # full production build via ray build
npm run lint     # @raycast/eslint-config
```

## Publish (Raycast Store)

The Raycast publish flow opens a PR against the
[raycast/extensions](https://github.com/raycast/extensions) monorepo:

```bash
npx @raycast/api@latest publish
# or:
npm run publish
```

The CLI will:

1. Build the extension.
2. Fork `raycast/extensions` on your behalf (interactive auth).
3. Open a PR with the extension under `extensions/eidetic-engrams/`.

We do **not** auto-publish. Run `publish` only when the daemon UDS contract is
stable and the screenshots / metadata are ready for the store.

## How it talks to the daemon

`src/lib/daemon.ts` uses Node's built-in `http` with a `socketPath` option —
identical transport shape to `integrations/vscode/src/daemonClient.ts`. No
third-party HTTP client. Raycast extensions on macOS already include Node, so
UDS works out of the box (no TCP fallback needed).

If the daemon is unreachable (`ENOENT` / `ECONNREFUSED` / timeout), every
command shows a Failure toast with a `Retry` primary action — see
`src/lib/errors.ts`.

## Layout

```
integrations/raycast/
├── package.json
├── tsconfig.json
├── .eslintrc.json
├── .prettierrc
├── .gitignore
├── README.md
├── assets/
│   └── command-icon.png        # placeholder — replace before store publish
└── src/
    ├── recent.tsx              # List of last 50 engrams
    ├── search.tsx              # FTS5 search with debounce
    ├── recall.tsx              # Form → /ask → Detail
    ├── stats.tsx               # /metrics dashboard
    └── lib/
        ├── daemon.ts           # UDS HTTP client (mirrors VS Code daemonClient.ts)
        ├── types.ts            # Engram / AskResponse / MetricsResponse
        ├── format.ts           # relative-time, byte/uptime, payload truncation
        ├── errors.ts           # daemon-unreachable toast helper
        └── EngramDetail.tsx    # shared Detail view for an engram
```

## Known TODOs before store-publish

- Replace `assets/command-icon.png` placeholder with a 512×512 icon (light + dark variants supported).
- Add per-command screenshots under `metadata/` for the store listing.
- Wire `package.json` `author` / `publisher` to the Raycast publisher slug Lokesh registers.
