# BAA template + HIPAA path

**Honest preamble:** Eidetic Works is **NOT HIPAA-certified** and does **NOT yet sign Business Associate Agreements (BAAs)**. This document is the template we WOULD use once we cross the threshold for it, and the guidance we offer to healthcare developers TODAY.

If your use case involves Protected Health Information (PHI), read this end-to-end before installing eidetic-daemon. The short version: **use the free tier exclusively, don't drop a `sync.json`, and your engrams never leave your machine.**

---

## What eidetic-daemon does that could intersect with PHI

eidetic-daemon captures AI assistant session logs (Claude Code, Cursor, Cowork JSONLs). If you use those AI tools to discuss or generate clinical documentation, your engrams may contain:

- Patient identifiers (names, MRNs, dates of birth) typed into your AI prompts
- Clinical reasoning chains discussed with the assistant
- Snippets of EHR data pasted into chat
- Generated draft clinical notes, prescriptions, or care plans

**By default (free tier), all of this stays on your local machine, in `~/.eidetic/engrams.db` (mode 0600).** It's no more accessible than your AI session logs would be without eidetic-daemon — but it IS now consolidated in one searchable place.

---

## Mitigations available today (free-tier-only path)

| Risk | Mitigation |
|---|---|
| Engrams aggregating PHI on one disk | Use `EIDETIC_DATA_DIR=/encrypted/volume/eidetic` so engrams live on an encrypted volume |
| Shared workstation = shared engrams | Use per-user dataDirs; uninstall + purge between users: `eideticd -uninstall -purge` |
| Backup snapshots leaking engrams | Add `~/.eidetic/` to your backup-tool exclusion list, OR use `eideticd -uninstall -purge` before scheduled backup runs |
| Cloud sync accidentally enabled | **Do not drop `sync.json` ever.** No `sync.json` = zero network egress for engrams (per ADR-020) |
| Searching surfaces old PHI | Use `eideticd-compliance` daemon with a strict retention policy (e.g., 30-day TTL on the `claude_code` surface) — see `cmd/eideticd-compliance/` |
| Right to delete | `--engrams DELETE` API endpoint with surface + before-timestamp filter; or surgical single-engram delete via `DELETE /engrams/{id}` |

**Verification:** run `eideticd --check` (v0.0.34+). If `sync.json` is absent, the output will say `sync: disabled` — confirming no engram payload can leave your machine.

---

## When we WILL sign BAAs

The conditions, in order:

1. **≥ 10 healthcare-vertical inbound asks** for BAAs — establishes market demand
2. **SOC2 Type 1 audit in flight** (per SOC2_READINESS.md timeline) — needed for any meaningful BAA to mean what it says
3. **Cloudflare Enterprise tier with their BAA** — Cloudflare offers BAAs at higher tiers; we need to be on one before we can pass through the requirement to our R2/Worker dependency
4. **Healthcare-aware features shipped** — at minimum: customer-managed key encryption for R2 (so we provably cannot read your engrams), audit logs for every Pro access path, formal data-residency choice (currently all R2 buckets are in Cloudflare's auto-region pool)

Estimated calendar: 12-18 months from first qualified inbound ask.

**In the meantime, healthcare developers can:**
- Use the free tier (as above) — fully compatible with most HIPAA workflows since data doesn't move
- Sign a custom NDA + indemnification agreement with us for the free tier (no BAA, but covers the operator-side commitments we CAN honor today)
- Wait for the Pro tier BAA when the conditions above are met
- Defer to your Privacy Officer's call

---

## The template (verbatim — would be issued under DocuSign once Pro BAA conditions are met)

```
BUSINESS ASSOCIATE AGREEMENT

This Business Associate Agreement ("Agreement") is entered into between
Eidetic Works LLC ("Business Associate") and __________________
("Covered Entity"), effective __________________ ("Effective Date").

1. DEFINITIONS

   Terms not defined in this Agreement have the meanings ascribed in
   HIPAA (45 C.F.R. §§ 160, 162, 164).

   "PHI" means Protected Health Information as defined in 45 CFR § 160.103.
   "Services" means the eidetic-daemon Pro tier provided by Eidetic Works
   under Covered Entity's account (https://eideticworks.gumroad.com/l/eidetic-pro).

2. PERMITTED USES AND DISCLOSURES OF PHI

   Business Associate may use or disclose PHI only:
   (a) As necessary to perform the Services (i.e., transport and store
       encrypted engram blobs at the Covered Entity's direction);
   (b) As Required by Law; or
   (c) For data aggregation services to the Covered Entity, if applicable.

   Business Associate shall NOT use or disclose PHI for any other purpose,
   including marketing, model training, advertising, or product analytics.

3. PROHIBITED USES

   Business Associate explicitly will NOT:
   (a) Decrypt customer engram blobs (architecturally infeasible per
       customer-managed key model);
   (b) Access individual engram contents under any circumstance;
   (c) Sell, license, or otherwise transfer PHI to any third party;
   (d) Use PHI to train AI/ML models, internal or external;
   (e) Aggregate PHI across customer accounts.

4. SAFEGUARDS

   Business Associate shall implement administrative, physical, and
   technical safeguards consistent with 45 CFR § 164.308–314, including:
   (a) Encryption in transit (TLS 1.3) and at rest (customer-managed
       keys, see § 6);
   (b) Access controls (Bearer auth + per-customer namespacing);
   (c) Audit logging of all access paths to customer R2 namespaces;
   (d) Personnel training on PHI handling (when personnel exist; sole
       operator today);
   (e) Annual security risk analysis (per SOC2_READINESS.md cadence).

5. REPORTING

   Business Associate shall report to Covered Entity:
   (a) Any Security Incident (45 CFR § 164.304) within 72 hours;
   (b) Any Breach of Unsecured PHI (45 CFR § 164.402) within 5
       business days of discovery;
   (c) Any unauthorized use or disclosure of PHI not provided for by
       this Agreement.

   Initial notification: security@eidetic.works.

6. ENCRYPTION (CUSTOMER-MANAGED KEYS)

   For HIPAA-tier customers, eidetic-daemon will be configured to use
   customer-managed encryption keys (CMK) such that Business Associate
   has no path to decrypt customer engram blobs. The CMK is provisioned
   at Covered Entity's R2 sub-account; Business Associate stores only
   encrypted ciphertext.

   (Note: this requires Cloudflare Enterprise tier — not yet provisioned;
   this clause becomes operative when condition #3 in "When we WILL sign
   BAAs" above is met.)

7. SUBCONTRACTORS

   Business Associate uses the following Subcontractors that may have
   incidental access to PHI ciphertext (never plaintext):
   - Cloudflare, Inc. (R2 storage, Workers compute, KV authn) — covered
     by Cloudflare's own BAA at Enterprise tier
   - ConvertKit, Inc. (email marketing) — receives only email address,
     never PHI

   Each Subcontractor is bound by a written agreement consistent with
   this BAA before access is granted.

8. TERMINATION + RETURN/DESTRUCTION

   Upon termination, Business Associate shall:
   (a) Return or destroy all PHI within 30 days;
   (b) Retain no copies in any form (including backups);
   (c) Provide written certification of destruction to Covered Entity
       upon request.

   Customer can self-serve destruction via `eideticd -uninstall -purge`
   (local data) + `DELETE /engrams?surface=...` (cloud data); operator-side
   cloud purge via security@eidetic.works confirmed within 30 days.

9. TERM

   This Agreement is effective on the Effective Date and continues until
   the Services are terminated.

10. AMENDMENT

    No amendment except in writing signed by both parties.

11. SURVIVAL

    Sections 2 (Permitted Uses), 3 (Prohibited Uses), 5 (Reporting), 7
    (Subcontractors), 8 (Termination) survive termination.

12. GOVERNING LAW

    This Agreement is governed by the laws of __________________
    (Covered Entity's choice).

SIGNED:

Eidetic Works LLC                Covered Entity

_________________________        _________________________
Lokesh Garg, Owner               Name, Title
Date: _________________          Date: _________________
```

---

## Decision tree — should I use eidetic-daemon for my AI clinical workflow?

```
Are you a HIPAA-regulated entity (provider, insurer, business associate)?
  ├─ NO  → Use any tier you want.
  └─ YES → Do your AI sessions touch PHI?
            ├─ NO  → Use any tier you want.
            └─ YES → Free tier ONLY (no sync.json).
                     Encrypt your local volume.
                     Add ~/.eidetic/ to backup exclusions.
                     Set retention policy via eideticd-compliance.
                     If you need cloud sync: wait for our BAA, OR
                       use BYOR2 mode (your own R2 + your own BAA with Cloudflare).
```

---

## Where to go from here

- **Free tier deployment for healthcare:** the install paths in README.md all apply; the only difference is "don't drop sync.json"
- **BYOR2 mode** (your own Cloudflare account + your own BAA with Cloudflare): instructions in `bridge/cloudflare/README.md`
- **Questions on this template:** `security@eidetic.works` — we triage compliance-tagged inbox within 48h
- **Want to be the first BAA customer when conditions are met?** Email us and we'll keep your name on the waitlist; you'll get the first Pro BAA contract when we sign one

---

## References

- `SECURITY.md` — threat model + disclosure policy
- `docs/enterprise/SOC2_READINESS.md` — companion doc covering Trust Services Criteria
- `docs/DECISIONS.md` — ADR-020 (privacy posture: every network call enumerated and opt-in)
- 45 CFR §§ 160, 162, 164 (HIPAA regulations) — public, full text at hhs.gov

**Last reviewed:** 2026-05-20. Document will be re-reviewed when any "When we WILL sign BAAs" condition flips, or annually, whichever comes first.
