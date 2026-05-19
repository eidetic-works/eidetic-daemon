/**
 * eidetic-sync Worker — receives SQLite backup uploads from eideticd and
 * stores them in R2 at engrams/{device_id}/engrams-{ts}.db
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
 * Add a Pro key:   wrangler kv:key put --namespace-id=<id> <sha256(key)> '{"email":"...","device_id":"...","added":"..."}'
 * Revoke a key:    wrangler kv:key delete --namespace-id=<id> <sha256(key)>
 *
 * Endpoints:
 *   POST /sync           — upload engrams.db blob for a device
 *   GET  /latest         — return metadata for the most recent backup
 *   GET  /healthz        — liveness probe
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

async function handleSync(request, env) {
  if (!(await isAuthorized(request, env))) {
    return new Response("unauthorized", { status: 401 });
  }

  const deviceId = request.headers.get("X-Device-ID");
  if (!deviceId || !/^[a-z0-9_-]{4,64}$/.test(deviceId)) {
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

  await env.EIDETIC_SYNC_BUCKET.put(key, body, {
    httpMetadata: { contentType: "application/x-sqlite3" },
    customMetadata: {
      deviceId,
      uploadedAt: new Date(tsMs).toISOString(),
      byteLength: String(body.byteLength),
    },
  });

  await pruneOldBackups(env, deviceId, 5);

  return new Response(
    JSON.stringify({ key, byteLength: body.byteLength, uploadedAt: new Date(tsMs).toISOString() }),
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
