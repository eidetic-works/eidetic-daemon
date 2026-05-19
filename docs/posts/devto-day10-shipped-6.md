# I shipped 6 versions of my Claude Code memory daemon in 36 hours — here's what changed and why

**Cover:** screenshot of `git tag | tail -7` output, v0.0.32 through v0.0.38, all stamped 2026-05-19 / 2026-05-20.

---

Two days ago I had a daemon that captured Claude Code session JSONLs and let you grep them. Today it has cloud sync, hot-reload, AI-powered recall via MCP, a web dashboard, and a $99 team tier.

This is the changelog, why each ship happened, and what it tells you about what users actually want from a "memory layer" tool.

## The setup

eidetic-daemon ships at github.com/eidetic-works/eidetic-daemon. ~3K lines of Go. Captures every Claude Code session JSONL via fsnotify, stores in SQLite with FTS5, exposes over UDS + MCP. Free + MIT. The premium tier ($29/mo Pro, $99/mo Team) adds managed Cloudflare R2 sync.

Two days ago I had 277K engrams from my own sessions across 8 months. I'd shipped the free distribution (Homebrew tap, dev.to article, X replies). The conversion bottleneck was Pro onboarding friction: "drop sync.json, restart daemon, hope it works" was 3 steps too many.

Here's the 6-ship sprint, oldest first.

## v0.0.32: `--restore` flag

```sh
eideticd --restore
# ✓ Downloaded 3.3 MB engrams.db from cloud backup
# key: engrams/macbook-m2/engrams-1748300000000.db
# previous db saved to ~/.eidetic/engrams.db.bak
# restart eideticd to use the restored database
```

The landing copy promised "restore on a new machine in 60 seconds." The implementation didn't exist yet — I'd shipped the upload path but never the download. So I built the Worker `/download` endpoint, the `RestoreFromConfig` Go function (runs before `store.Open` to avoid SQLite write-lock contention), and atomic file replacement with .bak backup.

3 tests. ~80 LOC. Cost: 45 minutes. Lesson: **don't put copy on your landing page that doesn't have a curl behind it.**

## v0.0.33: sync-state persistence

After `--sync-now` ran successfully, you had no way to tell when it last fired. The daemon forgot across restarts. `--stats` now shows a cloud sync block:

```
  cloud sync:
    last sync:  2026-05-20 09:13:42
    last key:   engrams/macbook-m2/engrams-...
    last size:  3.3 MB
```

State persists to `sync-state.json` in `dataDir`, atomic write (tmp → rename, 0600). Cost: 20 minutes.

## v0.0.34: `eideticd --check`

This is a Pro-onboarding-debug tool. New customer drops sync.json, daemon doesn't sync, customer emails support. What's the first thing I want them to run?

```sh
$ eideticd --check
eideticd v0.0.34 — sync check

  worker_url: https://eidetic-sync.morning-lake-f944.workers.dev
  device_id:  customer-mac-01
  interval:   60 min (default)
  worker:     ✓ reachable (200 OK)
  last sync:  2026-05-20 09:13 (3m ago)

  status: ✓ sync healthy
```

If the worker's down: `worker: ✗ unreachable: ...`. If the key's wrong: `worker: ✗ auth failed (401)`. Exit code 1 on failure so you can script it.

Cost: 30 minutes. **The cheapest customer support feature is one that prevents the ticket.**

## v0.0.35: sync.json hot-reload

This was the friction killer. Before: drop sync.json → `launchctl kickstart -k gui/$(id -u)/works.eidetic.eideticd`. After: drop sync.json, done.

fsnotify on the dataDir, 300ms debounce, dynamically swap the Syncer behind an RWMutex. Initial upload fires automatically to confirm.

```
sync: hot-reload — config applied (worker=https://eidetic-sync.morning-lake-f944.workers.dev device=macbook-m2)
sync: hot-reload initial upload complete
```

Lesson: **the step you ask the user to do "just once" is the one they get wrong.** Remove the step.

## v0.0.36: `--backups` history

You can't trust a backup you can't see. SyncState got a ring buffer (last 10 uploads), `--backups` prints them:

```
$ eideticd --backups
eideticd v0.0.36 — cloud backup history

  2026-05-20 09:13  engrams/macbook-m2/engrams-...  (3.3 MB)
  2026-05-20 08:13  engrams/macbook-m2/engrams-...  (3.3 MB)
  2026-05-20 07:13  engrams/macbook-m2/engrams-...  (3.3 MB)
  ...
```

Ring buffer cap, newest first, atomic write. 1 new test verifies the cap. Cost: 25 minutes.

## v0.0.5 (MCP) + v0.0.38 (HTTP): nucleus_ask

This is the killer feature. The landing copy promised "AI-powered recall coming soon." This is it.

```python
# From any MCP client (Claude Code, Cursor, Cline):
nucleus_ask(question="What was that Postgres trick I learned last week?")
```

The tool extracts keywords from the question (stop-word stripped, OR-joined), retrieves top-10 engrams via FTS5, returns them wrapped in answer-scaffolding:

```json
{
  "question": "What was that Postgres trick I learned?",
  "fts_query": "postgres OR trick OR learned",
  "instructions": "You are answering the question above using ONLY the engram excerpts below. Cite the surface + timestamp when you reference one. If the engrams don't answer the question, say so honestly — do NOT fabricate.",
  "engrams": [...]
}
```

The "AI" is the host LLM (your Claude Code session). Your engrams never leave your machine. No external API calls, no embeddings service, no GPU. Just FTS5 retrieval + careful prompt framing.

I also exposed it as `GET /ask?question=...` on the daemon's HTTP API so the web dashboard (next ship) can call it without MCP.

7 unit tests on the keyword extraction. 4 HTTP integration tests. Total cost: ~3h including the dashboard wiring.

## Bonus: the web dashboard

Single-page Astro at `/dashboard`. Paste your bridge URL + bearer token. Browse engrams, search, filter by surface. localStorage-persisted creds (never POSTed anywhere). Talks only to your daemon — no proxy.

## What I learned shipping 6 versions in 36 hours

1. **Every gap between "landing promises" and "code works" loses you the customer.** Don't write copy you can't ship in 48h.
2. **The cheapest support ticket is one you prevent.** `--check` will save me hours.
3. **The "just once" step is the friction.** Hot-reload was the highest-leverage ship — it removed the moment a customer thinks "is this thing even on?"
4. **AI features don't need an LLM call.** Retrieval + prompt framing is 90% of the value. The user's host LLM does the synthesis. Bonus: your data stays local.
5. **Tests are cheap.** All 6 ships have integration coverage. I haven't introduced a regression yet.

If you want to try it: `brew tap eidetic-works/nucleus && brew install eideticd && eideticd -install` and you're capturing in 30 seconds.

If you want cloud sync: there's a $29/mo Pro tier at eideticworks.gumroad.com/l/eidetic-pro. First 50 keep this price forever.

Either way, the source is at github.com/eidetic-works/eidetic-daemon. PRs welcome.

— Lokesh / Eidetic Works
