# Show HN: eidetic-daemon v0.0.38 — local-first Claude Code memory + AI recall

**Title (HN — keep under 80 chars):**
> Show HN: A local daemon that remembers every Claude Code session, with AI recall

**URL:** https://eidetic.works

**Body (HN allows ~2000 chars on Show HN posts; this is ~1900):**

I built a daemon that captures every JSONL written by Claude Code (and Cursor) sessions, stores them in a local SQLite with FTS5, and exposes them via MCP + HTTP API. 277K engrams from my own sessions over 8 months.

The pitch is "stop losing context between AI sessions" — you can ask Claude in a new session "what did I figure out about this last week?" and the MCP tool retrieves the relevant past engrams from your local store. Engrams never leave your machine.

What's new since the last Show HN attempt:

- **`nucleus_ask` MCP tool** — natural-language recall. Stop-word strip + FTS5 + RAG-formatted output for the host LLM. Your engrams stay local; the "AI" is the LLM in your session.
- **`/ask` HTTP endpoint** — same RAG semantics for non-MCP clients.
- **Web dashboard** at /dashboard — paste a bridge URL + token, browse + search your engrams in the browser. Pure static HTML, no backend.
- **Cloud sync** (opt-in, BYOR2 or managed Pro tier) — drop a `sync.json`, daemon hot-reloads, starts uploading. Restore on a new machine in 60 seconds: `eideticd --restore`.
- **Health check** — `eideticd --check` validates sync config + pings the Worker. Exit 1 if broken.
- **`--backups`** — ring buffer of last 10 cloud uploads. Trust but verify.

Stack: Go (~3K LOC) for the daemon, Python wrapper for MCP, Astro for landing/dashboard, Cloudflare Workers + R2 for sync, KV for per-user API keys. modernc.org/sqlite so the binary is statically linked + no CGO cross-compile pain.

Install: `brew tap eidetic-works/nucleus && brew install eideticd && eideticd -install` (registers launchd/systemd-user automatically). Or `irm https://eidetic.works/install.sh | sh`. Windows: `irm https://eidetic.works/install.ps1 | iex`.

Source: https://github.com/eidetic-works/eidetic-daemon (MIT). Pro tier ($29/mo): https://eideticworks.gumroad.com/l/eidetic-pro.

Happy to answer questions about the architecture, the cross-compile gotchas (modernc vs CGO), or the local-first-with-optional-cloud design.

---

## Notes for posting

- Best time: Tuesday or Wednesday, 8-9am PT (HN's morning surge)
- Don't post on Friday or weekend — Show HN dies fast there
- After posting, monitor first 30 min — respond to every comment immediately, that drives ranking
- Have a 1-line reply for each likely question:
  - "Why not LangChain memory?" → "LangChain memory is per-agent runtime state. eidetic is OS-level cross-tool capture — Claude Code session 1 → Cursor session 2 sees the same engrams."
  - "Why SQLite + FTS5 not vectors?" → "FTS5 is 0.27ms P95 on 10K rows, zero embedding cost, works offline. Vector store is on the roadmap for v0.1."
  - "How is this different from [tool]?" → "Local-first single binary, captures from any tool that writes JSONL, no cloud dependency by default."
