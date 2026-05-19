# Day 8 DM Outreach — 5 Targets (send from @eidetic_works on X)

Goal: 30-min user interview call, or async response on pain points.
All 5 built in the "Claude Code context problem" space — warm audience.

---

## 1. @Claude_Memory (Alex Newman) — thedotmack/claude-mem, 76K ⭐

**Why:** Built the closest competitor — AI-compressed session memory. Different approach (cloud+AI compression vs local SQLite) but identical pain point. Could be user, collaborator, or informed critic.

**DM:**
> Hey Alex — saw claude-mem, impressive traction. I built something adjacent: eideticd, a Go daemon that captures every Claude Code session to local SQLite (278K engrams, P95 retrieval 0.27ms). No cloud, no AI compression — raw engrams in 50ms. Different tradeoff: yours trades compute for richer context, mine trades richness for zero latency + zero trust.
>
> Would love 20 min to compare notes on what users actually hit first — the context-loss problem or the privacy concern? DM or grab time: eidetic.works

---

## 2. @iannuttall (Ian Nuttall) — iannuttall/claude-sessions, 1.2K ⭐

**Why:** Slash-command based session tracking — complementary to eidetic (his is manual + structured, ours is automatic + raw). Power user of Claude Code who clearly feels the pain.

**DM:**
> Hey Ian — claude-sessions is slick. I've been solving the same problem at the OS level: eideticd is a background daemon that automatically captures every message to local SQLite (no slash commands needed, just runs). 278K real engrams captured across 803 sessions.
>
> Curious what claude-sessions users complain about most — the setup friction or the retrieval quality? Happy to share early access to eideticd if you want to poke at it. 20-min call?

---

## 3. @yigitkonur (Yigit Konur) — yigitkonur/cli-continues, 1.2K ⭐

**Why:** Built `cli-continues` — resumes AI sessions across tools. Hits the same "context survives between sessions" need from a different angle (port session state between tools vs persist everything locally).

**DM:**
> Hey Yigit — cli-continues hits the same pain I've been solving. I built eideticd: a Go daemon that tails Claude Code session files with fsnotify and stores every message to SQLite-WAL in <50ms. Then the MCP bridge lets Claude query its own history: "what was I debugging last Tuesday?" sub-millisecond.
>
> What's the biggest gap cli-continues users hit? I'm running 5 early-user interviews — would you be open to 20 min?

---

## 4. @PawelHuryn (Pawel Huryn) — phuryn/claude-usage, 1.5K ⭐

**Why:** Built a local Claude usage dashboard. Product-minded (AI PM Coach, ex-CPO). Would have strong opinions on what Claude Code users actually want to see about their history.

**DM:**
> Hey Pawel — claude-usage is the right impulse (local-first, no cloud BS). I built eideticd: captures every Claude Code message to local SQLite, then exposes it via MCP so Claude can query its own history. Think "what was I debugging last Tuesday?" answered in <1ms.
>
> As someone who built the usage dashboard, curious: do users want usage stats or context retrieval first? Running early user calls this week — 20 min?

---

## 5. RonitSachdev (Ronit Sachdev) — RonitSachdev/ccundo, 1.4K ⭐

**Why:** Built ccundo — reads Claude Code session files to enable undo. Actually parsing the same session files that eidetic captures. Technically deep.

**GitHub DM or Issue comment (no Twitter):**
> Hey Ronit — ccundo reads the same session JSONLs that eideticd captures. I built eideticd as a background daemon that persists every message to SQLite for cross-session memory. Your file-parsing approach + eidetic's engram store could compose interestingly (undo across sessions, not just the current one).
>
> Would you be open to 20 minutes to talk about the session file format edge cases you hit? I'm running early user interviews. github.com/eidetic-works/eidetic-daemon

---

## How to send

1. Log into X as @eidetic_works
2. Send DMs 1-4 in order (Alex Newman first — highest leverage)
3. For Ronit: comment on a recent commit/issue on ccundo, or LinkedIn if findable
4. Set a 48h follow-up reminder

## Interview questions (15 min call script)

1. What's the specific moment you felt the context-loss pain? (the incident)
2. How do you currently work around it? (session summary prompts, CLAUDE.md, nothing?)
3. What would you change first about that workaround?
4. If a daemon ran silently and gave Claude perfect memory — would you trust it? What's the first concern?
5. What would make you uninstall it immediately?
