# LinkedIn Post — eidetic-daemon Day 8 launch

**Target:** Engineers, founders building with Claude Code / Cursor. LinkedIn audience = more senior/professional than Twitter.
**Tone:** founder-direct, numbers-led, no hype. Same voice as corpus samples.
**Status:** DRAFT — ready to post

---

Every time you close a Claude Code or Cursor session, your work history disappears.

Not archived. Not searchable. Just gone.

I shipped something to fix that.

**eidetic-daemon** — a Go binary that runs silently in the background, watching your session files. When you type a message in Claude Code or Cursor, it commits that engram to a local SQLite database within 50 milliseconds. No cloud, no configuration, no moving parts.

Two weeks of dogfood on my own machine:

- 141,502 engrams captured across sessions
- P95 retrieval: **0.27ms** on that live dataset
- SLO was 100ms — we're **370× inside it**

Local-only. MIT licensed. Pure Go binary — no CGO, starts at login via launchd.

```
curl -fsSL https://eidetic.works/install.sh | sh
```

The use case I kept hitting: start a new session, ask "what was that benchmark number I measured last week?" — then spend 10 minutes hunting through conversation transcripts. Now:

```
curl --unix-socket /tmp/eidetic-daemon.sock \
  'http://localhost/search?q=benchmark&limit=3'
```

0.27ms later, there it is.

FTS5 full-text search across everything you've typed in every session. Boolean operators. Phrase queries. The same query power as SQLite's full-text engine — because it is.

Works with Claude Code, Cursor, and Cowork. Day 8 of a 90-day sprint. Next up: Cloudflare sync so your engrams follow you across machines.

GitHub: **eidetic-works/eidetic-daemon** (link in comments)

If context loss between sessions is slowing you down, worth 10 seconds to try.

---

## Comments to add

Comment 1 (link):
> https://github.com/eidetic-works/eidetic-daemon
> One-line install: `curl -fsSL https://eidetic.works/install.sh | sh`

Comment 2 (if engagement picks up):
> The single number that mattered: P95 0.27ms on 141K real engrams.
> 
> That's not synthetic. That's two weeks of actual Claude Code sessions on one machine.
> 
> SLO was 100ms. We cleared it by 370×.

---

## Hashtags (add to post or first comment)

#ClaudeCode #DeveloperTools #OpenSource #GoLang #BuildingInPublic

---

## Character count note

Main post: ~310 words. LinkedIn shows first ~210 chars before "see more" — hook must land there. Current first line: "Every time you close a Claude Code or Cursor session, your work history disappears." = 87 chars. Second line adds the punch. Hook is clean.
