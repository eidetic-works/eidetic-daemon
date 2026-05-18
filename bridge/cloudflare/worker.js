/**
 * eidetic-sync Worker — receives SQLite backup uploads from eideticd and
 * stores them in R2 at engrams/{device_id}/engrams-{ts}.db
 *
 * Environment bindings (set in wrangler.toml / CF dashboard):
 *   EIDETIC_SYNC_BUCKET  — R2 bucket binding
 *   EIDETIC_API_KEY      — Bearer token for upload auth
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

    return new Response("not found", { status: 404 });
  },
};

async function handleSync(request, env) {
  // Auth
  const auth = request.headers.get("Authorization") || "";
  if (!auth.startsWith("Bearer ") || auth.slice(7) !== env.EIDETIC_API_KEY) {
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
    // 500 MB guard — SQLite file should never be this large in W2 scope
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

  // Prune old backups for this device — keep 5 most recent
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
  const auth = request.headers.get("Authorization") || "";
  if (!auth.startsWith("Bearer ") || auth.slice(7) !== env.EIDETIC_API_KEY) {
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

  // Objects are listed in key-lexicographic order; timestamps are zero-padded
  // so the last entry is the most recent
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

async function pruneOldBackups(env, deviceId, keepN) {
  const listed = await env.EIDETIC_SYNC_BUCKET.list({
    prefix: `engrams/${deviceId}/`,
  });

  if (!listed.objects || listed.objects.length <= keepN) {
    return;
  }

  // Sort by key (timestamps embedded in key, zero-padded → lex order = time order)
  const sorted = listed.objects.slice().sort((a, b) => a.key.localeCompare(b.key));
  const toDelete = sorted.slice(0, sorted.length - keepN);

  await Promise.all(toDelete.map((obj) => env.EIDETIC_SYNC_BUCKET.delete(obj.key)));
}
