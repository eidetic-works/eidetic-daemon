# Security policy

eidetic-daemon reads user-private filesystem state (AI assistant session logs, editor history) and exposes it over a local socket. This document is the explicit threat model + reporting channel before W1 public release.

## Reporting a vulnerability

**Email:** security@eidetic.works (or hi@eidetic.works as fallback)

Please include:
- Affected component (daemon, eidetic-mcp Python bridge, eidetic-sync Worker, gumroad-kit-sync Worker, landing/dashboard)
- Affected version (`eideticd --version` for the daemon)
- Reproduction steps
- Impact assessment (what an attacker can read, modify, or exfiltrate)

**Response timeline:**
- Acknowledgement: within 48 hours
- Triage + initial assessment: within 5 business days
- Fix or mitigation timeline: within 14 days for high/critical, 30 days for medium, best-effort for low

**Coordinated disclosure:** please give us 30 days before public disclosure for high/critical issues. We'll credit you in the fix release notes unless you prefer anonymity.

**No bug bounty (yet)** — we're a 1-operator shop. We'll consider a paid program once MRR supports it.

### Scope

In scope:
- `eidetic-works/eidetic-daemon` Go binary + capture/parse code
- `eidetic-mcp` PyPI package (bridge/python/eidetic_mcp/)
- `eidetic-sync` Cloudflare Worker (bridge/cloudflare/worker.js)
- `gumroad-kit-sync` Cloudflare Worker (this repo's webhook bridge)
- `eidetic.works` landing + `/dashboard`

Out of scope:
- DoS against R2 bucket via authenticated overage (Cloudflare's rate-limit is the safeguard; we'll handle quota issues operationally)
- Social engineering against Lokesh or future Eidetic Works employees
- Physical access to the user's machine (the threat model below assumes physical access = total trust)
- Issues in upstream dependencies that have no Eidetic-specific impact (file an issue upstream, then cc us)

## Threat model

## Trust boundary

The daemon is a **single-user, single-host** process. Everything it reads, stores, and serves is private to the local user account.

- **Trusted:** the local user account that runs the daemon (`uid=$(id -u)`).
- **Untrusted:** every other local user, every network actor, every other process not running as the same uid.

The daemon does not authenticate callers. The Unix-domain socket is the trust boundary; filesystem permissions enforce it.

## Storage

| Item | Path | Mode | Notes |
|---|---|---|---|
| SQLite store | `~/.eidetic/engrams.db` | `0700` dir, `0600` file | Plus WAL sidecars `.db-wal`, `.db-shm` — same modes |
| State offsets | `~/.eidetic/state.json` | `0700` dir, `0600` file | Atomic-rename writes (`state.json.tmp` → `state.json`) |
| UDS listener | `/tmp/eidetic-daemon.sock` (Mac) / `/var/run/eidetic.sock` (Linux) | `0600` | Daemon `chmod`s after `net.Listen` |
| Logs (launchd) | `/tmp/eideticd.{out,err}.log` | OS default | May contain payload prefixes from parse-error paths — see Known limitations |

`EIDETIC_DATA_DIR` overrides the data root; the daemon still creates parents at `0700`.

## Network exposure

**Default:** none. Daemon listens on a Unix-domain socket only.

**Opt-in TCP:** setting `EIDETIC_TCP=1` makes the daemon listen on `127.0.0.1:9876`. Loopback only — the listener does not bind to `0.0.0.0`. There is no built-in authentication on the TCP path; treat `EIDETIC_TCP=1` as "any local process can read every engram" and only enable it if you understand that.

## Capture path

The daemon watches three default surface roots (paths shown for macOS):

- `~/.claude/projects/**/*.jsonl` (Claude Code session transcripts)
- `~/.cowork/sessions/**/*.json`
- `~/Library/Application Support/Cursor/User/workspaceStorage/**/*.json`

Files are read with the daemon's effective user, which means **the daemon can read anything that user can read**. There is no path-traversal protection on globs; configurations beyond `DefaultSurfaces()` are at the operator's risk.

Parsers treat each newline-delimited record as opaque text. The daemon does **not** rewrite, exfiltrate, or upload captured content in W1. Cloud-sync is explicitly **not** in W1 scope (see [`docs/SPEC.md`](docs/SPEC.md) section 1; W2+).

## What the daemon does NOT do

- Send any data over the network (no telemetry, no analytics, no auto-update)
- Expose any control endpoint (no `POST`, no `DELETE` — the API is read-only `GET /engrams`)
- Run user-supplied code (no plugin loading, no eval, no shell-out)
- Persist secrets or credentials (the engram payload column is opaque text from the user's own AI session logs; if those logs contain secrets, they are stored as-is — see Known limitations)
- Modify the watched files (capture is read-only — fsnotify gives us events, parsers `os.Open` for read)

## Known limitations (W1)

1. **Captured content may contain secrets.** Claude Code session JSONLs and Cursor workspace storage can contain pasted API keys, passwords, file contents from secret-bearing files, etc. The daemon stores everything verbatim in `engrams.db` at `0600`. There is no scrubbing, no allow/deny list, no per-record encryption. **Treat `~/.eidetic/engrams.db` as sensitive as your AI session history.**
2. **No process isolation.** The daemon runs as the user, with full filesystem access. A bug allowing path-injection into the parser configuration would let a caller read any user-readable file.
3. **No rate limiting on the API.** A local process making a fast request loop will not be throttled. This is acceptable on the trust model (any caller already has same-uid privilege) but means the daemon cannot self-protect against a compromised co-resident process.
4. **launchd/systemd logs.** When parsing fails, the parser logs `capture: parse <surface> <path>: <error>` to stderr, which goes to `/tmp/eideticd.err.log` (Mac) or systemd journal (Linux). The path is logged; the payload is not. Path itself may be sensitive depending on the user's directory layout.
5. **No signing / verification on installer.** `curl -fsSL https://eidetic.works/install.sh | sh` trusts TLS. Verify the binary's GitHub release checksum against the source-built binary before relying on it for sensitive workflows.

## Reporting a vulnerability

Email `security@eidetic.works` (route TBD; until then `hello@nucleusos.dev`). Please include:
- Affected version (output of `eideticd -version`)
- Reproduction steps
- Impact assessment

We will acknowledge within 5 business days. There is no bug bounty in W1.

For non-sensitive issues, please open a GitHub issue.

## Hardening for future releases (out of W1 scope)

- **W2+:** per-record encryption-at-rest with a user-held key
- **W2+:** path allow-list enforcement at parser-config time
- ~~**W2+:** caller authentication on the API (e.g., per-process token in an HTTP header)~~ **SHIPPED in v0.0.9** — opt-in via `EIDETIC_AUTH=1` env var or `-auth` flag. Bearer-token authentication using 64-char hex tokens from `crypto/rand`, stored at `<dataDir>/auth-token` (0600 perms), constant-time validation, token rotates every restart. `/healthz` stays open; `/engrams` + `/metrics` require `Authorization: Bearer <token>`. See [README § Caller auth](./README.md#caller-auth-v009-opt-in).
- **W2+:** payload scrubbing pass for known secret patterns
- **W3+:** Cloud sync (D1+R2+Workers per ADR-005) with user-key-encrypted blobs only
