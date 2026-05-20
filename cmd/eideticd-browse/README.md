# eideticd-browse

Terminal UI for `engrams.db` — for users who live in the terminal and want a
faster path to recall than the web dashboard.

Reads the same SQLite store the daemon writes to (WAL means concurrent
read-only access is safe). Built with [Bubble Tea] + [Lipgloss], pure-Go,
cross-compiles to darwin-arm64 / linux-amd64 / linux-arm64 / windows-amd64.

It is a **separate binary** from `eideticd`. The daemon stays lean — no TUI
deps in the always-on hot path.

## Install

```sh
make build                              # daemon
make build-browse                       # this binary -> bin/eideticd-browse
# or cross-compile a single target
GOOS=linux GOARCH=amd64 go build -o eideticd-browse ./cmd/eideticd-browse
```

## Usage

```sh
eideticd-browse                         # uses ~/.eidetic/engrams.db
eideticd-browse -db /path/to/engrams.db # override
eideticd-browse -version
```

Honours `$EIDETIC_DATA_DIR` exactly the way the daemon does.

On a non-TTY stdout the binary prints a one-liner pointing at `eideticd
--ask` / the web dashboard and exits 0, rather than throwing the kind of
opaque Bubble Tea error a piped invocation would normally produce.

## Views

| Tab | Name   | What it does                                                 |
| --- | ------ | ------------------------------------------------------------ |
| `1` | Recent | Last 50 engrams across the active surface filter             |
| `2` | Search | FTS5 search; debounced 250ms after the last keystroke        |
| `3` | Ask    | Natural-language question via `internal/textsearch.QuestionToFTS` (same path `eideticd --ask` uses) |

## Key bindings

| Key       | Action                                                |
| --------- | ----------------------------------------------------- |
| `1` `2` `3` | Switch view (Recent / Search / Ask)                 |
| `j` `k` / arrows | Move selection                                 |
| `enter`   | Open detail pane for the highlighted engram           |
| `esc`     | Close detail; or leave Search/Ask back to Recent      |
| `s`       | Cycle surface filter: all → claude_code → cursor → cowork |
| `/`       | Jump to Search                                        |
| `?`       | Jump to Ask                                           |
| `q`       | Quit (in Recent or in detail)                         |
| `ctrl+c`  | Quit from anywhere                                    |

Detail pane: `j`/`k` scrolls the payload; meta is pretty-printed as JSON
when it parses, raw otherwise.

## Recording a demo with asciinema

```sh
brew install asciinema agg                      # agg renders cast → gif
asciinema rec -t 'eideticd-browse demo' demo.cast
# inside the cast: launch eideticd-browse, press 1/2/3, search "engram", quit with q
agg demo.cast demo.gif                          # post to README / blog
```

`agg` keeps the lipgloss colours; the teal accent (`#5eead4`) on near-black
(`#0a0a0a`) matches the landing page palette.

## Architecture notes

- Pure-Go: uses `modernc.org/sqlite` via the existing `internal/store` package
  — no CGO, no toolchain headaches when cross-compiling.
- Read-only by design: the binary opens the store but only calls Recent /
  Retrieve / Search. There is no write path.
- TTY detection via `github.com/mattn/go-isatty` (already an indirect
  dependency of the daemon's pipeline).
- State transitions are unit-tested (`main_test.go`); the rendered output is
  not, because lipgloss output is harder to assert against than the bug
  density justifies — dogfood + asciinema is the verification path for view
  rendering.

[Bubble Tea]: https://github.com/charmbracelet/bubbletea
[Lipgloss]: https://github.com/charmbracelet/lipgloss
