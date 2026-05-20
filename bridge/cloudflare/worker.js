/**
 * eidetic-sync Worker — receives SQLite backup uploads from eideticd and
 * stores them in R2.
 *
 * Environment bindings (set in wrangler.toml / CF dashboard):
 *   EIDETIC_SYNC_BUCKET  — R2 bucket binding
 *   EIDETIC_KEYS_KV      — KV namespace binding (per-user keys)
 *   EIDETIC_API_KEY      — fallback single Bearer token (legacy / dev; KV takes priority)
 *
 * Auth model:
 *   KV mode (Pro, multi-user): KV namespace stores sha256(key) → JSON metadata.
 *     If KV binding is present, KV is always checked first.
 *   Fallback mode (dev/self-hosted): EIDETIC_API_KEY env var, exact match.
 *
 * R2 key layout:
 *   engrams/<device_id>/engrams-<ts>.db          — per-device backup (all tiers)
 *   engrams/team/<team_id>/<device_id>/engrams-<ts>.db — team copy (dual-written when X-Team-ID present)
 *
 * Add a Pro key:   wrangler kv:key put --namespace-id=<id> <sha256(key)> '{"email":"...","device_id":"...","added":"..."}'
 * Revoke a key:    wrangler kv:key delete --namespace-id=<id> <sha256(key)>
 *
 * Endpoints:
 *   POST /sync              — upload engrams.db blob; dual-writes to team prefix if X-Team-ID set
 *   GET  /latest            — return metadata for the most recent backup
 *   GET  /download          — stream the most recent backup blob (used by eideticd --restore)
 *   GET  /team-engrams      — list all members' latest backups for a team (requires X-Team-ID)
 *   GET  /healthz           — liveness probe
 */

export default {
  async fetch(request, env) {
    const url = new URL(request.url);

    if (url.pathname === "/healthz") {
      return new Response(JSON.stringify({ status: "ok" }), {
        headers: { "Content-Type": "application/json" },
      });
    }

    if (url.pathname === "/sync" && request.method === "POST") {
      return handleSync(request, env);
    }

    if (url.pathname === "/latest" && request.method === "GET") {
      return handleLatest(request, env);
    }

    if (url.pathname === "/download" && request.method === "GET") {
      return handleDownload(request, env);
    }

    if (url.pathname === "/team-engrams" && request.method === "GET") {
      return handleTeamEngrams(request, env);
    }

    return new Response("not found", { status: 404 });
  },
};

/**
 * Validates the Bearer token. Checks KV first (per-user keys), then falls
 * back to the single EIDETIC_API_KEY secret (legacy / self-hosted mode).
 * Returns true if valid, false otherwise.
 */
async function isAuthorized(request, env) {
  const auth = request.headers.get("Authorization") || "";
  if (!auth.startsWith("Bearer ")) return false;
  const token = auth.slice(7);
  if (!token) return false;

  // KV mode: sha256(token) → metadata lookup. Constant-time via hash comparison.
  if (env.EIDETIC_KEYS_KV) {
    const hash = await sha256hex(token);
    const meta = await env.EIDETIC_KEYS_KV.get(hash);
    if (meta !== null) return true;
    // Fall through to legacy check if KV miss (allows shared key alongside per-user keys)
  }

  // Fallback: single shared secret (dev / self-hosted)
  if (env.EIDETIC_API_KEY && token === env.EIDETIC_API_KEY) return true;

  return false;
}

async function sha256hex(text) {
  const buf = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(text));
  return Array.from(new Uint8Array(buf))
    .map((b) => b.toString(16).padStart(2, "0"))
    .join("");
}

const DEVICE_ID_RE = /^[a-z0-9_-]{4,64}$/;

async function handleSync(request, env) {
  if (!(await isAuthorized(request, env))) {
    return new Response("unauthorized", { status: 401 });
  }

  const deviceId = request.headers.get("X-Device-ID");
  if (!deviceId || !DEVICE_ID_RE.test(deviceId)) {
    return new Response("X-Device-ID header required (4-64 lowercase alphanum/-/_)", {
      status: 400,
    });
  }

  const contentLength = parseInt(request.headers.get("Content-Length") || "0", 10);
  if (contentLength > 500 * 1024 * 1024) {
    return new Response("payload too large (max 500 MB)", { status: 413 });
  }

  const body = await request.arrayBuffer();
  if (body.byteLength === 0) {
    return new Response("empty body", { status: 400 });
  }

  const tsMs = Date.now();
  const key = `engrams/${deviceId}/engrams-${tsMs}.db`;
  const uploadedAt = new Date(tsMs).toISOString();

  await env.EIDETIC_SYNC_BUCKET.put(key, body, {
    httpMetadata: { contentType: "application/x-sqlite3" },
    customMetadata: {
      deviceId,
      uploadedAt,
      byteLength: String(body.byteLength),
    },
  });

  await pruneOldBackups(env, deviceId, 5);

  // Team dual-write: if X-Team-ID is present, also write under the team prefix.
  let teamKey = null;
  const teamId = request.headers.get("X-Team-ID");
  if (teamId && DEVICE_ID_RE.test(teamId)) {
    teamKey = `engrams/team/${teamId}/${deviceId}/engrams-${tsMs}.db`;
    await env.EIDETIC_SYNC_BUCKET.put(teamKey, body, {
      httpMetadata: { contentType: "application/x-sqlite3" },
      customMetadata: {
        deviceId,
        teamId,
        uploadedAt,
        byteLength: String(body.byteLength),
      },
    });
  }

  return new Response(
    JSON.stringify({ key, teamKey, byteLength: body.byteLength, uploadedAt }),
    {
      status: 201,
      headers: { "Content-Type": "application/json" },
    }
  );
}

async function handleLatest(request, env) {
  if (!(await isAuthorized(request, env))) {
    return new Response("unauthorized", { status: 401 });
  }

  const deviceId = request.headers.get("X-Device-ID");
  if (!deviceId) {
    return new Response("X-Device-ID header required", { status: 400 });
  }

  const listed = await env.EIDETIC_SYNC_BUCKET.list({
    prefix: `engrams/${deviceId}/`,
  });

  if (!listed.objects || listed.objects.length === 0) {
    return new Response(JSON.stringify({ latest: null }), {
      headers: { "Content-Type": "application/json" },
    });
  }

  const latest = listed.objects[listed.objects.length - 1];

  return new Response(
    JSON.stringify({
      latest: {
        key: latest.key,
        size: latest.size,
        uploadedAt: latest.customMetadata?.uploadedAt,
      },
    }),
    { headers: { "Content-Type": "application/json" } }
  );
}

async function handleDownload(request, env) {
  if (!(await isAuthorized(request, env))) {
    return new Response("unauthorized", { status: 401 });
  }

  const deviceId = request.headers.get("X-Device-ID");
  if (!deviceId) {
    return new Response("X-Device-ID header required", { status: 400 });
  }

  const listed = await env.EIDETIC_SYNC_BUCKET.list({
    prefix: `engrams/${deviceId}/`,
  });

  if (!listed.objects || listed.objects.length === 0) {
    return new Response(JSON.stringify({ error: "no backup found" }), {
      status: 404,
      headers: { "Content-Type": "application/json" },
    });
  }

  const sorted = listed.objects.slice().sort((a, b) => a.key.localeCompare(b.key));
  const latest = sorted[sorted.length - 1];

  const obj = await env.EIDETIC_SYNC_BUCKET.get(latest.key);
  if (!obj) {
    return new Response("backup object not found", { status: 404 });
  }

  return new Response(obj.body, {
    headers: {
      "Content-Type": "application/x-sqlite3",
      "Content-Disposition": 'attachment; filename="engrams.db"',
      "X-Backup-Key": latest.key,
      "X-Uploaded-At": latest.customMetadata?.uploadedAt ?? "",
    },
  });
}

/**
 * Lists all members' latest backups for a team.
 * Requires X-Team-ID header. Reads from the engrams/team/<team_id>/ prefix.
 * Optional ?since=<unix-ts> filters to objects uploaded after that timestamp.
 * Caps at 100 R2 objects (covers 100 individual uploads, not 100 members).
 */
async function handleTeamEngrams(request, env) {
  if (!(await isAuthorized(request, env))) {
    return new Response("unauthorized", { status: 401 });
  }

  const teamId = request.headers.get("X-Team-ID");
  if (!teamId || !DEVICE_ID_RE.test(teamId)) {
    return new Response("X-Team-ID header required (4-64 lowercase alphanum/-/_)", {
      status: 400,
    });
  }

  const sinceParam = new URL(request.url).searchParams.get("since");
  const sinceMs = sinceParam ? parseInt(sinceParam, 10) * 1000 : 0;

  const listed = await env.EIDETIC_SYNC_BUCKET.list({
    prefix: `engrams/team/${teamId}/`,
    limit: 100,
  });

  // Group by device_id, keep only the latest key per device.
  // Key shape: engrams/team/<team_id>/<device_id>/engrams-<ts>.db
  const memberMap = new Map();
  for (const obj of listed.objects ?? []) {
    const parts = obj.key.split("/");
    const devId = parts[3];
    if (!devId) continue;

    const uploadedAt = obj.customMetadata?.uploadedAt ?? null;
    if (sinceMs && uploadedAt) {
      if (new Date(uploadedAt).getTime() < sinceMs) continue;
    }

    // customMetadata not returned by R2 list() — parse ts from key name as fallback.
    // Key: engrams/team/<tid>/<did>/engrams-<tsMs>.db
    let resolvedAt = uploadedAt;
    if (!resolvedAt) {
      const filename = obj.key.split("/").pop() ?? "";
      const tsMs = parseInt(filename.replace("engrams-", "").replace(".db", ""), 10);
      if (!isNaN(tsMs)) resolvedAt = new Date(tsMs).toISOString();
    }

    const existing = memberMap.get(devId);
    if (!existing || obj.key > existing.latest_key) {
      memberMap.set(devId, {
        device_id: devId,
        latest_key: obj.key,
        uploaded_at: resolvedAt,
        bytes: obj.size,
      });
    }
  }

  return new Response(
    JSON.stringify({ team_id: teamId, members: Array.from(memberMap.values()) }),
    { headers: { "Content-Type": "application/json" } }
  );
}

async function pruneOldBackups(env, deviceId, keepN) {
  const listed = await env.EIDETIC_SYNC_BUCKET.list({
    prefix: `engrams/${deviceId}/`,
  });

  if (!listed.objects || listed.objects.length <= keepN) {
    return;
  }

  const sorted = listed.objects.slice().sort((a, b) => a.key.localeCompare(b.key));
  const toDelete = sorted.slice(0, sorted.length - keepN);

  await Promise.all(toDelete.map((obj) => env.EIDETIC_SYNC_BUCKET.delete(obj.key)));
}
