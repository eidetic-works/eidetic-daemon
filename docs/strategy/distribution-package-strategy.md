# Distribution Package Strategy — nucleus-mcp vs eidetic-mcp

**Decision date:** 2026-05-19  
**Status:** DECIDED

## The problem we almost made

We published `eidetic-mcp 0.0.1` to PyPI and were about to build distribution for it
from scratch — Product Hunt listing, npm listing, posts — while `nucleus-mcp` was
quietly getting 50-100 installs/month with zero distribution effort.

The instinct was to separate them: nucleus-mcp = internal/legacy, eidetic-mcp = public
brand. That instinct is wrong.

## Why cold-start kills eidetic-mcp as the install path

A new PyPI listing and npm package shows:
- 0 downloads
- 0 stars
- No README history
- No dependents

The next developer who lands on it via search or a README link sees a dead project.
That first impression is hard to overcome without a coordinated launch spike.

nucleus-mcp already has a number next to its download count. That number — even if
partially inflated by bots or CI — signals "people use this" to the next person who
arrives. Social proof is asymmetric: it's cheap to keep and expensive to rebuild.

## The telemetry gap doesn't change the decision

We have zero telemetry on where those nucleus-mcp installs come from. It could be:
- Real developers who found it via npm search
- One CI pipeline running on a schedule
- Bots/mirrors

The day-pattern test (uniform = CI/bots, lumpy = real users) would tell us more, but
even without it the asymmetry holds: mystery organic is still an asset because the
download count is public. Abandoned organic is just a number on a page that costs
nothing to keep.

## What we do instead

### nucleus-mcp = the install path

Update nucleus-mcp to wrap the eidetic daemon's MCP bridge:
- `query_engrams` — FTS5 full-text search over captured sessions
- `daemon_status` — health check
- `daemon_metrics` — engram count, surface breakdown, P95 latency

Point its README at `eidetic.works`. The install story becomes:

```bash
pip install nucleus-mcp
claude mcp add eidetic -- python -m nucleus_mcp.server
```

Existing installs keep working (same package name, extended functionality). New users
land on a package with download history.

### eidetic-mcp = brand alias, not primary

`eidetic-mcp` stays on PyPI as a thin wrapper that imports and re-exports
`nucleus_mcp`. One file. It exists so the brand name is searchable and the
`eidetic.works` landing can reference it. But it's not the install path we optimize.

```python
# eidetic_mcp/server.py — the entire package
from nucleus_mcp.server import *  # noqa: F401,F403
```

### Product Hunt launches the daemon, not the package

The launch story is:

> "Background daemon that captures every Claude Code session to local SQLite.
> 141K engrams. P95 retrieval 0.27ms. One-line install. Verify the numbers
> yourself via curl."

The install CTA on Product Hunt is `curl -fsSL https://eidetic.works/install.sh | sh`
— the binary. The Python package is a footnote in the docs ("MCP bridge for
Claude Code / Cursor: `pip install nucleus-mcp`").

This sidesteps the cold-start problem entirely. Product Hunt judges the daemon on its
own merits, not on PyPI download counts.

## Sequencing

1. **Now** — leave nucleus-mcp and eidetic-mcp as-is. No publish, no changes.
2. **Week 2-3** — update nucleus-mcp to wrap eidetic daemon. Point README at
   eidetic.works. Publish nucleus-mcp patch version.
3. **Week 3-4** — Product Hunt prep: demo GIF, maker story, tagline locked.
   Reddit r/ClaudeAI + dev.to already live as social proof.
4. **Launch day** — PH goes live. X thread + Reddit r/rust + r/ClaudeAI fire
   simultaneously. Install CTA is the curl one-liner, not pip install.
5. **Post-launch** — add telemetry ping to nucleus-mcp (fire-and-forget GET on
   import, opt-out). After 30 days you know if installs are real users or bots.
   That data shapes whether nucleus-mcp gets more investment.

## What this avoids

- Rebuilding social proof from zero on two separate package listings
- Product Hunt launching a package that looks dead next to established alternatives
- Abandoning organic gravity that cost nothing to accumulate
- Coordinating two separate install paths in docs, READMEs, and landing pages

## Hard rule going forward

**The package name is nucleus-mcp. The product name is Eidetic Works. The binary
is eideticd. These are three different things and only one of them needs a
Product Hunt launch.**
