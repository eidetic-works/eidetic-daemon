# Distribution Autopilot — one-time setup

After working through this doc **once** (~75 min total Lokesh-keyboard, ~$5 spend), posting + marketplace discovery + customer onboarding become a function of time. Zero recurring distribution work.

## What's already on autopilot (verified 2026-05-21)

- **Buying.** Gumroad webhook → CF Worker → Kit tag → KV API key → R2 backup → drip emails → customer dashboard. End-to-end, no human in loop.
- **Daemon releases.** Every `git tag vX.Y.Z` → release.yml builds 4 platforms → GitHub Release → Homebrew tap auto-updates.
- **MCP releases.** Each eidetic-mcp version → committed to repo → op-assistant publishes to PyPI in ~5 min.
- **Chat-app /eidetic commands.** Slack/Discord/Telegram workers deployed + verified end-to-end.
- **Customer dashboard.** eidetic.works/me serves customer Pro state from R2 + KV.

## What needs YOUR one-time setup (in priority order)

Total time budget: **~75 min keyboard, ~$5 spend.** Each section says how long + what to paste.

---

### 1. Posting on autopilot via Buffer (10 min, $0)

**Why first:** unblocks X + LinkedIn + Substack with one signup. growth-scheduler Worker is already coded; just needs Buffer credentials.

**Steps:**

```text
1. Open https://buffer.com → Sign up (free tier).
2. Connect channels:
   - X: @eidetic_works
   - LinkedIn: the Eidetic Works company page (NOT your personal account)
   - Substack: the brand newsletter (if exists; skip if not)
3. Buffer dashboard → set posting schedule per channel:
   - X: 3 slots/day (e.g. 9am, 1pm, 7pm EST)
   - LinkedIn: 1 slot/day (e.g. 8am EST)
   - Substack: 1 slot/week (e.g. Monday 9am EST)
4. Generate API token:
   - buffer.com/developers/api → Create App → "growth-scheduler"
   - Copy the access token
5. Get profile IDs:
   - For each channel, open buffer.com/profile → URL contains profile_id
6. Deploy growth-scheduler with secrets:
   cd workers/growth-scheduler
   unset CLOUDFLARE_API_TOKEN
   # Create KV namespaces first:
   wrangler kv namespace create CONTENT_QUEUE  # paste id into wrangler.toml
   wrangler kv namespace create POSTED_LOG      # paste id into wrangler.toml
   # Then secrets:
   wrangler secret put BUFFER_ACCESS_TOKEN     # paste from step 4
   wrangler secret put BUFFER_PROFILE_X        # paste from step 5
   wrangler secret put BUFFER_PROFILE_LINKEDIN # paste from step 5
   wrangler secret put BUFFER_PROFILE_SUBSTACK # paste from step 5 (or empty)
   wrangler secret put ADMIN_SECRET            # paste a random hex string
   wrangler deploy
```

**Verification:**
```sh
curl -i https://growth-scheduler.morning-lake-f944.workers.dev/healthz
# expect: HTTP 200 + {"ok":true,"service":"growth-scheduler"}

# Enqueue a smoke-test post:
curl -X POST https://growth-scheduler.morning-lake-f944.workers.dev/queue \
  -H "Authorization: Bearer $ADMIN_SECRET" \
  -H "Content-Type: application/json" \
  -d '{"channel":"buffer","target":"x","text":"Testing growth-scheduler — disregard."}'
```

Wait for the next hourly cron (:13 minute), then check Buffer's queue UI — your test post should be there. Delete it from Buffer before it fires.

**After step 1:** X + LinkedIn + Substack posting = function of time. Sunday cron auto-generates next Substack from git log.

---

### 2. VS Code Marketplace (5 min, $0)

**Why second:** largest single dev audience. Auto-republish on each daemon tag via release.yml.

```text
1. https://marketplace.visualstudio.com/manage/publishers → Sign in with Microsoft account
2. Create publisher → identifier MUST be exactly: eidetic-works
3. https://dev.azure.com → Personal Access Tokens → New Token
   - Name: vsce-publish
   - Organization: all accessible
   - Scopes: Marketplace > Manage
   - Expiry: 1 year (renew annually)
4. Copy the PAT (one-time view).
5. https://github.com/eidetic-works/eidetic-daemon/settings/secrets/actions
   → New repository secret
   - Name: VSCE_PAT
   - Value: <paste PAT>
6. Tag a new daemon version (or re-fire release.yml on v0.0.61):
   gh workflow run release.yml --ref v0.0.61
```

**Verification:** within ~5 min, visit https://marketplace.visualstudio.com/items?itemName=eidetic-works.eidetic-vscode. Listing should appear; install via VS Code.

---

### 3. Chrome Web Store (15 min, $5 one-time)

```text
1. https://chrome.google.com/webstore/devconsole → Sign in with Google account
2. Pay $5 one-time developer registration fee
3. Click "New item" → upload integrations/chrome-extension/dist/eidetic-chrome-0.1.0.zip
   (pre-built; in the ai-mvp-backend repo. If not present, run:
    cd ../ai-mvp-backend/integrations/chrome-extension && \
    mkdir -p dist && zip -r dist/eidetic-chrome-0.1.0.zip . -x "dist/*" "*.DS_Store" "README.md" "TESTING.md" "node_modules/*")
4. Fill in: store listing, screenshots, privacy practices.
   Privacy permissions justification:
     - activeTab: read the page the user is currently viewing for "capture this"
     - storage: save daemon URL + bearer token locally
     - host http://127.0.0.1:8421/*: communicate with the user's own local daemon
5. Submit for review (~1-3 business days).
6. Once approved + you have the extension ID:
   https://console.cloud.google.com → Create project → Enable "Chrome Web Store API"
   → OAuth 2.0 credentials → Web application
   → Authorized redirect URIs: http://localhost (just for token mint)
7. Use the Chrome Web Store API quick-start to get a refresh token (one-time).
8. Add 4 secrets to the eidetic-daemon repo:
     CWS_CLIENT_ID, CWS_CLIENT_SECRET, CWS_REFRESH_TOKEN, CWS_EXTENSION_ID
9. Tag a new daemon version. release.yml fan-out re-uploads + re-publishes.
```

After step 3 only: extension is live. Steps 6-9 only needed if you want auto-update on every daemon tag. (Manual upload via console works fine for low version cadence.)

---

### 4. JetBrains Marketplace (10 min, $0)

```text
1. https://plugins.jetbrains.com/author/me → Sign in (free JetBrains account)
2. Click "Upload plugin" → upload build/distributions/eidetic-jetbrains-*.zip
   (built once via: cd integrations/jetbrains && gradle wrapper && ./gradlew buildPlugin)
3. Approve the plugin name/description.
4. Generate Hub Permanent Token:
   - https://hub.jetbrains.com/users/me?tab=authentification
   - New permanent token, scope: Marketplace
5. Add as JETBRAINS_HUB_TOKEN secret on eidetic-daemon repo.
6. Tag a new daemon version. release.yml's publish-jetbrains job auto-publishes future versions.
```

JetBrains review SLA: 1-3 days for first publish; auto-publish for updates after approval.

---

### 5. Raycast Store (10 min, $0)

```text
1. https://raycast.com → Sign up (free; macOS only).
2. https://raycast.com/account → API tokens → Generate new token.
3. Add as RAYCAST_API_TOKEN secret on eidetic-daemon repo.
4. Tag a new daemon version. release.yml's publish-raycast job runs `npx ray publish`
   which OPENS A PR against raycast/extensions on GitHub. Their team merges (1-7 days).
```

After their merge: extension auto-installs for any Raycast user who searches "eidetic."

---

### 6. Obsidian Community Plugins (15 min, $0, longer review)

Obsidian's submission flow is unusual — the plugin needs its OWN top-level GitHub repo (not subdirectory of monorepo), then a PR against `obsidianmd/obsidian-releases`.

```text
1. Create new GitHub repo: eidetic-works/obsidian-eidetic
2. Push integrations/obsidian/ contents to it as the repo root.
3. Tag a release on that repo: v0.1.0. Attach main.js + manifest.json + styles.css as release assets.
4. Fork github.com/obsidianmd/obsidian-releases
5. Edit community-plugins.json, append your plugin entry:
   {
     "id": "eidetic",
     "name": "Eidetic Engrams",
     "author": "Eidetic Works",
     "description": "Capture vault notes as engrams; recall via nucleus_ask.",
     "repo": "eidetic-works/obsidian-eidetic"
   }
6. Open PR. Obsidian team review: 1-2 weeks typically.
```

This one is more friction than the others. Defer until first 5 above are live.

---

### 7. Optional: ProductHunt + IndieHackers + Hacker News (your call, not automated)

These don't have publishing APIs that make sense. Manual submission when ready:

- **ProductHunt:** https://producthunt.com/posts/new — schedule a launch (Tuesday or Wednesday best). Use docs/posts/x-thread-day12.md as the gallery + hunter outreach copy.
- **IndieHackers:** indiehackers.com/post — best after first paid Pro lands ("first revenue" milestone post).
- **Hacker News:** Show HN drafts at docs/posts/hn-show-hn-day{8,10,11}.md. You said skip for this sprint.

---

## After you've finished steps 1-5

**Posting = function of time.** growth-scheduler fires every hour; weekly digest runs every Sunday; Buffer publishes from its queue per your channel schedule.

**Marketplace presence = function of time.** Each new daemon tag (`git tag vX.Y.Z && git push origin vX.Y.Z`) auto-fires:
- GitHub Release
- Homebrew tap update
- VS Code Marketplace publish
- Chrome Web Store re-publish (if step 3.6-9 done)
- JetBrains Marketplace publish
- Raycast PR
- Obsidian release-asset attachment

**Buying = function of time.** Already done end-to-end pre-this-doc.

**Discovery = function of time.** Customers find you via marketplace search, Substack subscribers, X/LinkedIn followers, Google indexing the landing + docs.

**Customer success = function of time.** Kit drip emails fire on schedule; eidetic.works/me lets them self-serve.

## Recurring work after autopilot is live

| Cadence | Who | What |
|---|---|---|
| Daily | growth-scheduler | Posts 1 X tweet + 1 LinkedIn (when queue non-empty) |
| Weekly | growth-scheduler | Generates Substack draft from git log; auto-enqueues |
| On daemon tag | release.yml | Republishes to 5 marketplaces + Homebrew |
| On Gumroad sale | gumroad-kit-sync | Provisions API key + Kit tag + Telegram ping to Lokesh |
| Manual, optional | Lokesh | Hand-paste a post into growth-scheduler /queue when something noteworthy happens off-cadence |

That last row is the only manual work — and it's **opt-in** (the cron generates content from git log automatically; manual queueing just lets you front-run topics).

## Failure modes + monitoring

- **Buffer free tier limits:** 10 queued posts per channel. growth-scheduler doesn't enforce this; queue depth visible at `GET /growth`. If you hit the cap, either pause cron generation or upgrade to Buffer Essentials ($6/mo, 100 queued/channel).
- **Marketplace publish failures:** release.yml jobs log warnings if a publisher token isn't set. Once set, real failures show as red checks on the tag; `gh run view` to debug.
- **Cron silent failures:** all errors log to `eidetic-analytics` with event name `growth_error`. Query via `GET /stats?days=7` on eidetic-analytics worker.
- **Kit drip stuck:** `kit_tag_apply` events should match `purchase` 1:1 in funnel. If skew widens, Zapier-bridge is broken; check that worker's recent invocations.

## Cost summary

| Item | Cost |
|---|---|
| Buffer free tier | $0/mo |
| Cloudflare workers | $0/mo (free tier covers everything) |
| Chrome Web Store dev fee | $5 one-time |
| VS Code Marketplace | $0 |
| JetBrains Marketplace | $0 |
| Raycast Store | $0 |
| Obsidian Community | $0 |
| **Total recurring** | **$0/mo** |
| **Total one-time** | **$5** |

Upgrades only if you outgrow free tiers: Buffer Essentials $6/mo (>10 queued/channel), Cloudflare Workers Paid $5/mo (>100K req/day), Apple Developer $99/yr (Mac App Store, optional).
