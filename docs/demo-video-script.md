# Demo Video Script — eidetic-daemon

**Target length:** 90 seconds  
**Format:** Terminal recording (asciinema) + voice narration  
**Setup:** Install already complete; daemon running; ~140K real engrams loaded  
**Start recording:** `asciinema rec demo.cast --cols 110 --rows 30 --title "eidetic-daemon demo"`

---

## Narration + terminal flow

---

**[0:00 – 0:10] Hook (voice only, dark terminal in background)**

> "Every time you close a Claude Code or Cursor session, your work history disappears.
> This daemon captures it — in real time, locally, under 50 milliseconds — and gives it back in under a millisecond."

---

**[0:10 – 0:25] Install**

*Type:*
```sh
curl -fsSL https://eidetic.works/install.sh | sh
```

*Expected output scrolls:*
```
install: target: darwin-arm64
install: installed /usr/local/bin/eideticd
install: registered LaunchAgent
install: started works.eidetic.eideticd
install: OK
```

> "One line. Pure Go binary. No CGO. Starts at login via launchd."

---

**[0:25 – 0:40] Capture in flight**

*Open Claude Code in background (alt-tab), type a question, come back.*

*Or, in the terminal:*
```sh
echo '{"role":"user","payload":"What did the last benchmark say?"}' \
  >> ~/.claude/projects/demo-session/session.jsonl
```

*Then:*
```sh
sqlite3 ~/.eidetic/engrams.db \
  "SELECT id, surface FROM engrams ORDER BY ts DESC LIMIT 1"
```

*Output:*
```
141503|claude_code
```

> "Fifty milliseconds after that write — it's committed. Not a log. A queryable row."

---

**[0:40 – 0:55] Retrieve**

```sh
curl -s --unix-socket /tmp/eidetic-daemon.sock \
  'http://localhost/engrams?surface=claude_code&limit=3' | jq '.[].payload'
```

*Output (3 payload strings, truncated):*
```
"What did the last benchmark say?"
"...prior message..."
"...prior message..."
```

> "Local Unix socket. No network hop. That round-trip is 0.27 milliseconds P95 on 140K rows."

---

**[0:55 – 1:10] Search**

```sh
curl -s --unix-socket /tmp/eidetic-daemon.sock \
  'http://localhost/search?q=benchmark&limit=3' | jq '.[].payload'
```

*Output:*
```
"What did the last benchmark say?"
"P95 retrieval: 0.27ms..."
"...benchmark result..."
```

> "FTS5 search over everything you've ever typed across every surface. Phrase queries, boolean operators — the same query power as SQLite's full-text engine."

---

**[1:10 – 1:20] Real numbers**

```sh
curl -s --unix-socket /tmp/eidetic-daemon.sock http://localhost/metrics | \
  jq '{engram_total, query_latency_p95_us}'
```

*Output:*
```json
{
  "engram_total": 141502,
  "query_latency_p95_us": 0.27
}
```

> "141,502 real Claude Code engrams. P95 at 0.27 microseconds. The SLO is 100 milliseconds — headroom is 370x."

---

**[1:20 – 1:30] CTA**

*Terminal cleared, cursor blinking.*

> "MIT licensed. One-line install. Works with Cursor, Claude Code, Cowork.
> Link in the thread. Star it if it's useful."

---

## Twitter thread follow-up copy

Post as a reply to the original announcement tweet:

---

**Tweet 1 (video embed):**
> Here's the 90-second demo. Install → work captured → searchable instantly.
> [video]

**Tweet 2:**
> The single number that matters: P95 retrieval = 0.27ms on 140K real engrams.
> 
> SLO was 100ms. We cleared it by 370x.
> 
> `curl -fsSL https://eidetic.works/install.sh | sh`

**Tweet 3 (if engagement is high):**
> What's next:
> - Cloudflare sync (W2) — engrams follow you across machines
> - Pro tier — team-level capture + shared search
> - Training pipeline — your engrams feed your local model
>
> DM or reply with what you'd actually use this for.

---

## Recording tips

- Use `asciinema rec` for terminal-native recording (no compression artifacts on text)
- Export to GIF for Twitter embed: `agg demo.cast demo.gif --cols 110 --rows 30`
- Or use Quicktime screen recording for the voice overlay
- Keep jq pretty-print on — the colored JSON reads well in video
- Zoom terminal font to 16-18pt so text is legible at 1080p compressed to Twitter's codec
