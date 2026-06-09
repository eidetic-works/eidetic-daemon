# Marketplace publishing — built artifacts + per-platform recipes

What's built, where it lives, and the one-command publish flow per ecosystem. Each artifact is reproducible from the source in `integrations/<name>/` via the recipe below.

## Built artifacts (this sprint)

| Platform | Artifact | Path | Size |
|---|---|---|---|
| VS Code | `eidetic-vscode-0.0.1.vsix` | `integrations/vscode/eidetic-vscode-0.0.1.vsix` | 18 KB |
| Chrome | `eidetic-chrome-0.1.0.zip` | `../ai-mvp-backend/integrations/chrome-extension/dist/eidetic-chrome-0.1.0.zip` | 11 KB |

Both are unsigned by default. VS Code Marketplace and Chrome Web Store handle signing on upload.

## VS Code Marketplace

**Repro the artifact:**
```sh
cd integrations/vscode
npm install               # one-time
npm run package           # esbuild --production
npx @vscode/vsce package --no-yarn --skip-license
# → integrations/vscode/eidetic-vscode-<version>.vsix
```

**Publish:**
```sh
# Requires a Personal Access Token from https://dev.azure.com/<your-org>/_usersSettings/tokens
# (scope: Marketplace > Manage)
export VSCE_PAT=<paste token>
npx @vscode/vsce publish --packagePath eidetic-vscode-0.0.1.vsix
```

Publisher (in `package.json`): `eidetic-works`. Must be claimed at https://marketplace.visualstudio.com/manage/publishers first (one-time, operator-keyboard).

**Bump version:** edit `package.json#version`, re-run `npx vsce package`, re-publish.

## Chrome Web Store

**Repro the artifact:**
```sh
cd ../ai-mvp-backend/integrations/chrome-extension
mkdir -p dist
zip -r dist/eidetic-chrome-0.1.0.zip . \
  -x "dist/*" "*.DS_Store" "README.md" "TESTING.md" "node_modules/*"
```

**Publish:**
1. https://chrome.google.com/webstore/devconsole (one-time $5 dev fee, operator-keyboard)
2. Click **New Item** → upload `eidetic-chrome-0.1.0.zip`
3. Fill in: store listing, privacy practices (point at `docs/PRIVACY.md`), permissions justification (host `http://127.0.0.1:8421/*` = the local daemon)
4. Submit for review (~1-3 business days)

Permissions used (manifest.json): `activeTab`, `storage`, `scripting`, `host: http://127.0.0.1:8421/*`. All justifiable for a local-daemon companion.

## JetBrains Marketplace

**Repro the artifact:**
```sh
cd integrations/jetbrains
gradle wrapper                    # generates gradlew if not present
./gradlew buildPlugin             # → build/distributions/eidetic-jetbrains-<version>.zip
```

**Publish:**
1. https://plugins.jetbrains.com/author/me (one-time JetBrains account, operator-keyboard)
2. Click **Upload plugin** → upload the .zip from `build/distributions/`
3. JetBrains review (~1-3 business days)

Target platform: 2024.1 (build 241) — loads on current IntelliJ/PyCharm/GoLand/WebStorm/Rider. `sinceBuild=241`, `untilBuild=""` per `gradle.properties`.

## Raycast Store

**No artifact build** — Raycast publishes directly from source via their CLI.

```sh
cd integrations/raycast
npm install               # one-time
npx @raycast/api@latest build      # verify it builds clean
npx @raycast/api@latest publish    # uploads + creates PR against raycast/extensions repo
```

Requires Raycast account (free, one-time operator-keyboard). Their team reviews PRs (~1-7 days). Once merged, the extension is searchable in Raycast directly.

Commands registered (per `package.json`): `recent`, `search`, `recall`, `stats`.

## Mac App Store / Notarization (menubar app)

`integrations/macos-menubar/EideticMenubar/` is a Swift scaffold. **Not in scope for this sprint** — Mac App Store distribution requires:

- Apple Developer account ($99/yr, operator-keyboard)
- App-specific bundle ID claim
- Code signing certificate
- Notarization workflow

SwiftBar plugin (`integrations/macos-menubar/eidetic.5m.sh`) works today without any of the above — users install via `brew install --cask swiftbar && cp eidetic.5m.sh ~/Library/Application\ Support/SwiftBar/Plugins/`.

## Obsidian Community Plugins

`integrations/obsidian/` is a TS plugin scaffold (3 commands). Publish flow:

1. `npm install && npm run build` → produces `main.js` + `manifest.json`
2. Tag a GitHub release with the version (e.g. `v0.1.0`)
3. Open PR against https://github.com/obsidianmd/obsidian-releases adding plugin to `community-plugins.json`
4. Obsidian team review (~1-2 weeks)

Repo currently lives inside this monorepo — for the Obsidian PR, the plugin needs its own top-level repo (or a release-only tag with main.js + manifest.json attached as release assets). One-time refactor; not blocking the sprint.

## Quick reference: cost per publish

| Platform | Account cost | Per-publish cost | Review SLA |
|---|---|---|---|
| VS Code Marketplace | Free | Free | Auto (minutes) |
| Chrome Web Store | $5 one-time | Free | 1-3 days |
| JetBrains Marketplace | Free | Free | 1-3 days |
| Raycast Store | Free | Free (open PR) | 1-7 days |
| Mac App Store | $99/yr | Free | 1-2 days |
| Obsidian Community | Free | Free (open PR) | 1-2 weeks |

Total to publish on every platform once: **$104** (Apple + Chrome). Recurring: **$99/yr** (Apple only).

## After-publish smoke tests

For each marketplace, after the listing goes live:

```sh
# VS Code: install from marketplace + verify it loads
code --install-extension eidetic-works.eidetic-vscode
code --list-extensions | grep eidetic

# Chrome: load the published version via chrome://extensions, then
curl -sI http://127.0.0.1:8421/healthz  # daemon must be reachable for the popup

# JetBrains: install via Settings > Plugins > Marketplace, restart IDE,
# confirm "Eidetic: Recall" appears under Tools menu
```

If smoke tests pass, update `SHIPPED.md` row for that surface from ✅ scaffold → ✅ **PUBLISHED**.
