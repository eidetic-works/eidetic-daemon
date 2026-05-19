# Package Naming Brainstorm — 2026-05-19

**Status:** OPEN — decision deferred until product has real users  
**Revisit trigger:** first real user surfaces, or Product Hunt prep begins

## What we know

- `nucleus-mcp` on PyPI/npm: 50-100 installs/month
- Zero telemetry on source
- Zero real users have ever surfaced from it
- Conclusion: installs are bots or CI pipelines. The number is noise.

- `eidetic-mcp 0.0.1` published to PyPI (Task #284 done)
- Zero installs, zero distribution effort behind it yet

## Options we walked through

### Option A: eidetic-mcp as primary
- Fresh listings on PyPI + npm
- Product Hunt launches the daemon, eidetic-mcp is the MCP install path
- Requires full cold-start effort: PH listing, posts, DMs, social proof from scratch
- Clean brand: one name, one landing, one package
- **Con:** new listings look dead on arrival without a coordinated launch spike

### Option B: nucleus-mcp as primary (ride mystery organic)
- Update nucleus-mcp to wrap the eidetic daemon
- Point README at eidetic.works
- Ride the existing download count as social proof
- eidetic-mcp becomes a thin alias
- **Con:** name mismatch (nucleus-mcp → eidetic.works confuses first-time users), carries old Nucleus project baggage, breaks any real users of the old system
- **Killed by:** telemetry confirmed zero real users — the organic is noise, not an asset

### Option C: keep both, touch nothing (conservative)
- Don't update nucleus-mcp (don't risk breaking mystery users)
- Don't push eidetic-mcp (don't fight cold-start without data)
- Add telemetry ping to nucleus-mcp in week 3, decide after 30 days
- **Killed by:** users wouldn't mind an update anyway, and telemetry already confirmed no real users

### Option D: deprecate nucleus-mcp, commit to eidetic-mcp (decided direction)
- nucleus-mcp gets a deprecation notice pointing to eidetic-mcp
- eidetic-mcp is the single install path
- Product Hunt launches the daemon (curl one-liner is the CTA, not pip install)
- Package is a footnote in docs, not the hero of the launch
- **Status:** directionally correct but not actioned yet — waiting for product to work first

## Key insight

Product Hunt launches `eideticd` the binary, not the Python package. The install
CTA is `curl -fsSL https://eidetic.works/install.sh | sh`. The MCP bridge is a
detail in the docs. This sidesteps the cold-start problem for the package entirely
— PH judges the daemon on its own merits.

## Hard rules going forward

- Don't invest in nucleus-mcp distribution. It's legacy.
- Don't push eidetic-mcp as primary until the daemon has real users who ask for it.
- Product Hunt timing: only when `eideticd` works reliably enough that a spike of
  installs won't surface embarrassing bugs. Not before.
- Decision gate: first real user surfaces OR PH prep actively begins, whichever comes first.

## What "real user" means here

Someone who installed eideticd, ran it, and either filed an issue, sent a DM,
replied to a post, or showed up in Kit with a real email domain. Not a download
count. An actual human who did something.
