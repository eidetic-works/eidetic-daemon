# Webhook hooks — act on engrams as they arrive

eidetic-daemon (v0.0.55+) can fire outbound HTTP webhooks when captured engrams match patterns you configure. Useful for:

- Alerting Slack / Discord / PagerDuty when "deploy failed" appears in your Claude Code session
- Forwarding noteworthy engrams to your own backend
- Counting capture events for personal analytics
- Anything you'd otherwise need a separate watcher process for

## Configuration

Drop a `~/.eidetic/hooks.json` file. Daemon picks it up on next start (or `eideticd -uninstall && eideticd -install` to bounce).

### Minimal example — alert Slack on deploy failures

```json
{
  "hooks": [
    {
      "name": "deploy-fail-to-slack",
      "url": "https://hooks.slack.com/services/T.../B.../...",
      "match_pattern": "deploy failed",
      "match_surface": "claude_code",
      "min_interval_sec": 300
    }
  ]
}
```

When any `claude_code` engram contains "deploy failed" (case-insensitive substring), the daemon POSTs to your Slack webhook:

```json
{
  "hook": "deploy-fail-to-slack",
  "surface": "claude_code",
  "timestamp": "2026-05-20T12:34:56Z",
  "pattern": "deploy failed"
}
```

`min_interval_sec: 300` means at most one fire per 5 minutes — prevents notification spam during multi-line failures.

### Full schema

```json
{
  "hooks": [
    {
      "name": "string-identifier",        // required; appears in /metrics + logs
      "url": "https://...",               // required
      "method": "POST",                   // optional; defaults to POST
      "headers": {                        // optional
        "Authorization": "Bearer ...",
        "X-Custom": "..."
      },
      "match_pattern": "deploy failed",   // optional substring (case-insensitive)
      "match_surface": "claude_code",     // optional; empty = match any surface
      "include_payload": false,           // OPT-IN; sends engram payload in body
      "min_interval_sec": 300             // optional; rate-limit per-hook
    }
  ]
}
```

## Privacy posture (ADR-020)

- Hooks are explicit user-side opt-in. No webhook fires without `~/.eidetic/hooks.json`.
- By default (`include_payload: false`), the payload sent to your webhook contains ONLY the hook name + surface + timestamp + matched pattern — **no engram content**.
- `include_payload: true` opts in to sending the FULL engram payload to your webhook URL. Use only when (a) the destination is your own infrastructure and (b) you understand the privacy boundary you're crossing. Don't enable for third-party SaaS unless you've audited the data.
- Hook destinations are NOT verified — if you set `url` to `https://evil.example.com`, the daemon will POST there. You own this.

## Triggering surface

Hooks fire from the capture path, AFTER successful `InsertBatch`. If `InsertBatch` fails, no hooks fire (the engram is also not stored). If the webhook HTTP call fails, the daemon logs nothing and continues — webhook firing is fire-and-forget by design, NOT retried.

## Common recipes

### Notify when capture-skip threshold breached

Not yet supported in v0.0.55 (per-engram only). Will land in a future version as a daemon-fired "system" hook with `surface: "_eideticd"` and payloads like `{"event":"capture_skipped","count":N}`.

### Forward all engrams to your own backend

```json
{
  "hooks": [
    {
      "name": "mirror-to-my-backend",
      "url": "https://api.mycompany.com/ingest/eidetic",
      "headers": {"Authorization": "Bearer COMPANY_TOKEN"},
      "include_payload": true
    }
  ]
}
```

No `match_pattern` = every engram fires. No `min_interval_sec` = every engram fires immediately. This will hammer your backend if you have heavy capture; consider downsampling at your end.

### Per-surface filter

```json
{
  "hooks": [
    {"name": "claude-to-x", "url": "https://...", "match_surface": "claude_code"},
    {"name": "cursor-to-y", "url": "https://...", "match_surface": "cursor"}
  ]
}
```

Two independent hooks fire per surface to different destinations.

### Multi-pattern alerting (one hook per pattern)

v0.0.55 supports one pattern per hook. To match multiple patterns to one URL, list multiple hooks with the same URL — they each fire independently and the rate-limit is per-hook.

```json
{
  "hooks": [
    {"name": "deploy-fail", "url": "https://hook.x", "match_pattern": "deploy failed"},
    {"name": "auth-error", "url": "https://hook.x", "match_pattern": "401 unauthorized"},
    {"name": "kub-crashloop", "url": "https://hook.x", "match_pattern": "CrashLoopBackOff"}
  ]
}
```

## Testing your hook before deploying

```sh
# Use a local httpbin or webhook.site to verify shape:
# 1. Start a local sink
docker run -p 8080:80 kennethreitz/httpbin
# OR use https://webhook.site for a free temporary URL

# 2. Configure hooks.json pointing to it
# 3. Restart daemon
# 4. Trigger a matching engram (e.g. write a file with "deploy failed" in Claude Code)
# 5. Inspect the request that arrived
```

## Hook count visibility

`eideticd --check` will report configured hooks (v0.0.56+, currently shows just sync state). For now: `cat ~/.eidetic/hooks.json | jq '.hooks[].name'`.

## Limits

- Hooks are best-effort, fire-and-forget. No retry, no dead-letter queue.
- HTTP client timeout: 10s. Slow webhook destinations time out silently.
- Goroutine per match — no concurrency cap. If your hook destination is slow AND match_pattern matches everything, you can accumulate inflight requests. Use `min_interval_sec`.
- One pattern per hook (substring; not regex). Regex hooks are a candidate for v0.0.56+ if there's demand.
