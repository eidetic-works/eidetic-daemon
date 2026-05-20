# Integrating nucleus_ask — recipes for your AI workflow

`nucleus_ask` is the killer eidetic-daemon feature: ask Claude / Cursor / Cline a natural-language question about your past work and the host LLM reads matching engrams from your local store. Engrams never leave your machine.

This guide is 5 copy-paste recipes for the most common integrations. Setup time per recipe: 30-90 seconds.

---

## Prerequisites

1. **eidetic-daemon installed** + running. Verify: `eideticd --stats` shows non-zero engrams.
2. **eidetic-mcp 0.0.5+** installed: `pip install --upgrade eidetic-mcp` (verify: `pip show eidetic-mcp | grep Version` ≥ 0.0.5)
3. **The daemon's UDS socket reachable** at `/tmp/eidetic-daemon.sock` (macOS/Linux default) — verify with `curl --unix-socket /tmp/eidetic-daemon.sock http://localhost/healthz`

If any of these fail, see `eideticd --check` for a diagnostic.

---

## Recipe 1 — Claude Code: nucleus_ask as a slash command

**Goal:** type `/recall <question>` in Claude Code and get an answer grounded in your past sessions.

```sh
# 1. Add the MCP server to Claude Code (one-time)
claude mcp add eidetic -- python -m eidetic_mcp.server

# 2. Verify it appears
claude mcp list
# → eidetic    python -m eidetic_mcp.server    Ready
```

In any Claude Code session: just ask. Claude will pick `nucleus_ask` itself when the question is past-tense ("what did we decide about...", "did I write anything about...", etc.).

If you want to force it, prefix:
```
Use nucleus_ask to find: what was that Postgres tuning trick I learned last week?
```

**What happens:** Claude calls `nucleus_ask(question="...")`, eidetic-mcp extracts keywords + FTS-retrieves top-10 engrams from your local store, returns them wrapped in answer-scaffolding. Claude reads the engrams, cites surface + timestamp, synthesizes the answer.

---

## Recipe 2 — Cursor: same MCP, different config path

Cursor uses the same MCP protocol. Add the server in Cursor's settings:

```json
// ~/.cursor/config/mcp_servers.json
{
  "mcpServers": {
    "eidetic": {
      "command": "python",
      "args": ["-m", "eidetic_mcp.server"]
    }
  }
}
```

Restart Cursor. The `nucleus_ask` tool will appear in any Cursor agent session.

**Tip:** Cursor's chat is more conservative about tool calls. Phrase your question with explicit recall intent: "Recall any earlier work on X" or "Find what I wrote about Y last month".

---

## Recipe 3 — Cline (VS Code): MCP add via settings.json

```json
// VS Code settings.json (Cline section)
"cline.mcpServers": {
  "eidetic": {
    "command": "python",
    "args": ["-m", "eidetic_mcp.server"]
  }
}
```

Reload VS Code. Cline picks up the server automatically.

---

## Recipe 4 — CLI / shell scripts: hit /ask directly

For automation outside an AI host (cron jobs, git hooks, shell aliases), call the daemon's HTTP endpoint:

```sh
# Quick recall from terminal
ask() {
  local question="$*"
  local q=$(printf %s "$question" | jq -sRr @uri)
  curl --unix-socket /tmp/eidetic-daemon.sock \
    "http://localhost/ask?question=$q&limit=5" \
  | jq -r '.engrams[] | "[\(.surface) \(.ts | tonumber / 1000000 | floor / 1000 | strftime("%Y-%m-%d"))] \(.payload[0:200])"'
}

# Usage
ask what was that Postgres trick I learned
```

Returns top-5 matching engrams in `[surface YYYY-MM-DD] <payload preview>` format. Wire into git hooks, shell prompts, anywhere you want past-context-aware automation.

---

## Recipe 5 — Web dashboard: browse + search in your browser

If you don't want to wire MCP, the web dashboard at https://eidetic.works/dashboard gives you the same retrieval power in the browser:

1. Start the daemon's bridge listener (one-time):
   ```sh
   # Add this to your launchd plist OR run manually
   eideticd -bridge :8421
   ```
2. Open https://eidetic.works/dashboard
3. Paste `http://127.0.0.1:8421` and the bearer token from `~/.eidetic/bridge-token`

The dashboard renders your engram store with full-text search, surface filters, and clickable expand. No data leaves your machine — the page only talks to your daemon.

For remote access (so you can browse from your phone), expose the bridge via Cloudflare Tunnel:
```sh
cloudflared tunnel --url http://localhost:8421
# → https://random-name.trycloudflare.com — paste that into the dashboard
```

---

## Verification — is nucleus_ask actually retrieving the right things?

```sh
# Check what FTS query nucleus_ask generates for your phrasing
curl --unix-socket /tmp/eidetic-daemon.sock \
  "http://localhost/ask?question=what+was+that+Postgres+trick" \
| jq '.fts_query'
# → "postgres OR trick"

# See the raw matches
curl --unix-socket /tmp/eidetic-daemon.sock \
  "http://localhost/ask?question=what+was+that+Postgres+trick" \
| jq '.engrams[].payload[0:200]'
```

If the FTS query is too narrow or your phrasing is too generic, you'll see empty results. The fix is usually to use a more specific keyword in your question.

---

## Troubleshooting

| Symptom | Fix |
|---|---|
| `nucleus_ask` tool doesn't appear in Claude Code | `pip install --upgrade eidetic-mcp` (need ≥ 0.0.5) + `claude mcp restart` |
| Tool returns "no engrams matched" for queries you know should hit | Try `eideticd --stats` — confirm you actually have data; try different keywords; the FTS5 tokenizer is lowercase + word-boundary split |
| MCP server fails to start | Check `python -m eidetic_mcp.server --help` runs standalone; check daemon's UDS socket exists; check `eidetic_mcp` package version |
| Web dashboard says "unauthorized" | `cat ~/.eidetic/bridge-token` — the file rotates each daemon restart; copy fresh and reconnect |

---

## What nucleus_ask is NOT

- **Not embeddings-based.** It's FTS5 keyword retrieval — fast, offline, no GPU cost, no external API. We may add vector search in v0.1; FTS5 will remain the default for sub-millisecond response.
- **Not external-AI-call.** No prompt ever leaves your machine. The "AI" is the host LLM (Claude Code etc.); we just hand it relevant engrams and answer-scaffolding.
- **Not cross-user.** Each user's engrams are private to their device. Team subscribers can opt-in to cross-seat shared engrams (`team_id` in sync.json) — that's the one exception, and it's still opt-in.

Questions? hi@eidetic.works — Pro subscribers get 24h response, Team gets 12h.
