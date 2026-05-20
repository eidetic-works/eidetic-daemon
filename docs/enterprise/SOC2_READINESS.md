# SOC2 readiness — current controls mapped to Trust Services Criteria

**Honest preamble:** We are **NOT SOC2 certified**. We are pre-audit. This document maps eidetic-daemon's current technical and operational controls to the SOC2 Trust Services Criteria (TSC) so enterprise prospects can evaluate whether we're a safe choice today and what we'd need to close to certify.

If you are a procurement / compliance team evaluating us: read this end-to-end. If a critical control is missing for your use case, you have three options:
1. Use the **free tier only** (engrams stay on the local machine; nothing leaves)
2. Wait for our SOC2 (12-month plan below)
3. Sign a custom MSA covering your specific requirements

Reach `security@eidetic.works` for option 3.

---

## Status as of 2026-05-20

| Trust Services Criterion | Status |
|---|---|
| Security (CC6 series) | Partial — see table below |
| Availability (A1) | Best-effort; no SLA today |
| Confidentiality (C1) | Strong — local-first by default; Pro uses encrypted-at-rest R2 |
| Processing Integrity (PI1) | Strong — SQLite ACID + chunked-capture idempotency |
| Privacy (P series) | Strong — ADR-020 enumerates every network call |

---

## Security (CC6 series)

| Subcontrol | Current state | Gap to certify |
|---|---|---|
| **CC6.1** Logical access controls | UDS-trust model by default (file-perm `0600`); opt-in Bearer auth via `-auth` / `EIDETIC_AUTH=1`; per-user Pro keys hashed (SHA-256) and stored in Cloudflare KV; revocation via `wrangler kv:key delete <hash>` | Formal access review policy; automated revocation on role change; documented break-glass procedure |
| **CC6.2** Authentication | Pro: Bearer token in `sync.json` (32-byte URL-safe base64); Worker checks via KV lookup OR fallback shared secret. Optional UDS Bearer for local daemon | MFA enforcement on the Cloudflare account that holds R2 + KV (operator-side, not customer-facing); rotation policy |
| **CC6.3** Authorization | Single-tenant per device key; Team tier scopes additional `X-Team-ID` writes to a shared prefix. No cross-customer access path | RBAC inside Team tier (admin vs member); attribute-based access controls for future enterprise features |
| **CC6.6** Encryption in transit | HTTPS to all Workers (TLS 1.3 default via Cloudflare); UDS is loopback-only so plaintext OK | None — already strong |
| **CC6.7** Encryption at rest | Local SQLite file mode `0600`; Cloudflare R2 encrypts at rest with Cloudflare-managed keys by default | Customer-managed keys (CMK) — requires Cloudflare Enterprise tier; or per-engram client-side encryption (W3+ roadmap) |
| **CC6.8** Vulnerability management | `go mod tidy` on every build; CI smoke-tests across 4 platforms; manual review of CVEs against Go stdlib + `fsnotify` + `modernc/sqlite` | Automated dependency-CVE scanning (Snyk/Dependabot); quarterly pen-test; bug bounty (per SECURITY.md, deferred until MRR justifies) |

**Audit trail:** see `SECURITY.md` for the full threat model; ADR-020 in `docs/DECISIONS.md` for the network-call audit recipe.

---

## Availability (A1)

eidetic-daemon is a **single-instance** local process. There is no built-in HA today.

| Component | Availability story |
|---|---|
| Local daemon | One process per machine. Crash → launchd/systemd-user restart (`KeepAlive=true` / `Restart=always`). No SLA. |
| `eidetic-sync` Worker | Inherits Cloudflare's [99.99% SLA](https://www.cloudflare.com/r2/sla/) on R2 storage + Workers compute |
| `gumroad-kit-sync` Worker | Same inheritance |
| Web dashboard (eidetic.works/dashboard) | Cloudflare Pages, same inheritance |

**Customer SLO:** best-effort. We don't sign uptime guarantees yet. If you need a contractual SLO, contact us — we can write one against the Cloudflare-backed components but not the local daemon (which lives on your hardware, by design).

---

## Confidentiality (C1)

The **strongest** part of the product.

- Engrams **never leave the local machine** unless the user drops a `sync.json` (per ADR-020 § 1).
- Pro sync uses per-customer namespaced R2 prefixes (`engrams/<device_id>/...`). No cross-customer read path exists in the Worker code.
- Team tier shared prefix (`engrams/team/<team_id>/<device_id>/...`) requires the customer to explicitly set `team_id` in sync.json — opt-in.
- Key revocation: `wrangler kv:key delete <sha256_of_key>` instantly invalidates a Pro subscriber's access without affecting their data (data persists in R2 with no readable key).
- Right to delete: `eideticd -uninstall -purge` wipes local data. Cloud objects deleted on request via `security@eidetic.works` within 30 days (we'll automate this when volume justifies a `/purge-customer` Worker route).

---

## Processing Integrity (PI1)

| Mechanism | What it guarantees |
|---|---|
| SQLite WAL + `synchronous=NORMAL` | ACID writes; partial writes never visible to readers (validated by 9/9 path-portability + write-suite tests) |
| Chunked-capture (ADR-018) | Records > 7 MiB split into sha256-prefix-tagged chunks; idempotent on re-parse (proven by `parser_chunked_test.go`'s 6 tests including state-offset-advances-past-oversized) |
| Atomic state.json writes | All state updates go through `tmp → rename` pattern; survives mid-write crash |
| Sync upload idempotency | R2 objects keyed by `<ts>` — re-uploading the same file overwrites cleanly with no orphans |

Test coverage: `internal/store/*_test.go`, `internal/capture/*_test.go`, `internal/sync/syncer_test.go` — all green across all releases.

---

## Privacy (P series)

| TSC subcontrol | Compliance evidence |
|---|---|
| **P1** Notice | Landing page + SECURITY.md + ADR-020 publicly describe every data flow |
| **P3** Consent | Free tier: no data leaves; consent implicit in install. Pro tier: explicit `sync.json` opt-in. Web dashboard: localStorage-only credentials |
| **P4** Use, retention, disposal | No PII collected in product. Retention is customer-controlled via `retention-policy.json` + `eideticd-compliance` daemon. Disposal: `--purge` or contact us for cloud-side wipe |
| **P6** Disclosure to third parties | None today. Cloudflare is a sub-processor for Pro tier (storage + compute); covered by their DPA |
| **P7** Quality / data subject rights | Right to access: `/export` endpoint. Right to delete: `--uninstall --purge`. Right to portability: NDJSON export format |

**No PII collected:** ADR-020 enumerates every network call. None send engram payloads, file paths, hostnames, OS info, or user identity. The 24h GitHub releases poll is HEAD-only and contains no per-install identifier.

---

## Gap analysis — 12-month plan to certify

| Quarter | Milestone | Approx cost |
|---|---|---|
| Q1 (months 0-3) | Engage Type 1 SOC2 auditor (Drata/Vanta/Secureframe); document policies (access control, change management, vendor risk, incident response, BCP); MFA enforcement audit on operator Cloudflare account | $5-8K |
| Q2 (months 3-6) | Drata/Vanta evidence-collection setup; pen-test (one cycle); formal vulnerability management workflow; quarterly access reviews start | $8-12K |
| Q3 (months 6-9) | Type 1 audit window; remediation; bug bounty launch (if MRR > $5K/mo) | $3-5K |
| Q4 (months 9-12) | Type 1 report issued; Type 2 observation period begins | $0 (continuing observation) |

**Total Type 1 estimate: $15-25K + 6 months operator time.** Type 2 (continuous observation report) adds 6 more months and ~$10K/year for ongoing evidence.

**When we'll start:** earliest of (a) MRR > $5K/mo for 3 consecutive months, (b) two enterprise prospects with formal SOC2-required RFPs, (c) Series A close. Current MRR is in single digits — we are far from the start gate.

---

## In the meantime — risk mitigations for enterprise prospects

1. **Use only the free tier** — by definition no data leaves the customer machine; SOC2 doesn't apply to data we never touch
2. **Run your own R2 bucket** — drop your own `sync.json` with your bucket's Worker URL; we never see the data (BYOR2 mode)
3. **Custom MSA** — we can sign mutually-agreeable contractual security commitments (covering our actual current controls) without a Type 1 report. Contact `security@eidetic.works`

---

## References

- `SECURITY.md` (root of this repo) — full threat model + disclosure policy
- `docs/DECISIONS.md` — ADR-020 (local-first privacy posture, network-call audit), ADR-018 (chunked capture), ADR-019 (R2 sync architecture)
- `docs/enterprise/BAA_TEMPLATE.md` — HIPAA path for healthcare developers
- Cloudflare R2 SLA: https://www.cloudflare.com/r2/sla/
- Cloudflare DPA: https://www.cloudflare.com/cloudflare-customer-dpa/

**Questions?** `security@eidetic.works` — 48h acknowledgement per SECURITY.md.
