# X/Twitter Thread — eidetic-daemon Day 8

**Tweet 1 (main):**
Context loss between Claude Code sessions was killing my productivity.

I built a background daemon to fix it.

278,561 engrams captured. P95 retrieval: 0.27ms.

Here's what I shipped: 🧵

**Tweet 2:**
`eideticd` watches your Claude Code session files with fsnotify.

Every message you type → SQLite row within 50ms.
Nothing leaves your machine. Pure-Go binary, MIT.

```
curl -fsSL https://eidetic.works/install.sh | sh
```

**Tweet 3:**
The number I'm most proud of:

SLO: 100ms P95 retrieval
Actual: 0.27ms

That's 370× inside the target.

On 278K real engrams, not a synthetic fixture.

**Tweet 4:**
MCP bridge so your AI assistant queries your own history:

```
pip install eidetic-mcp
claude mcp add eidetic -- python -m eidetic_mcp.server
```

Then: "What was I debugging last Tuesday?" → engrams in <1ms.

**Tweet 5:**
Architecture notes for the curious:

- modernc.org/sqlite (pure-Go, not CGO mattn) → cross-compile clean
- WAL mode + single-writer pool → zero lock contention
- FTS5 full-text index → boolean + phrase queries on everything you've typed

**Tweet 6:**
Day 9 of a 90-day sprint. Free and open-source forever.

Pro tier just launched: managed cloud sync so your engrams follow you across machines. $29/mo, first 50 keep that price.

→ https://eidetic.works

GitHub: https://github.com/eidetic-works/eidetic-daemon

#ClaudeCode #GoLang #OpenSource #BuildingInPublic
