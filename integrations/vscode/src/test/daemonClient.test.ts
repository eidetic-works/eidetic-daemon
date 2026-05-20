// daemonClient.test.ts — smoke tests against a mock HTTP server. Exercises
// the JSON-decode happy path, error-status surfacing, and engram preview
// helpers. No real daemon required; tests boot a local http.Server on an
// ephemeral port and point DaemonClient at it via the TCP fallback path.

import * as assert from 'node:assert';
import * as http from 'node:http';
import { AddressInfo } from 'node:net';
import { DaemonClient, engramPreview, formatEngramTs, Engram } from '../daemonClient';

interface Routes {
  [path: string]: (req: http.IncomingMessage, res: http.ServerResponse) => void;
}

function startMockServer(routes: Routes): Promise<{ host: string; port: number; close: () => Promise<void> }> {
  return new Promise((resolve) => {
    const srv = http.createServer((req, res) => {
      const url = req.url ?? '/';
      // Match by pathname only; ignore query string for routing.
      const pathOnly = url.split('?')[0];
      const handler = routes[pathOnly] ?? routes[url];
      if (handler) {
        handler(req, res);
      } else {
        res.statusCode = 404;
        res.end('not found');
      }
    });
    srv.listen(0, '127.0.0.1', () => {
      const addr = srv.address() as AddressInfo;
      resolve({
        host: '127.0.0.1',
        port: addr.port,
        close: () =>
          new Promise<void>((r) => {
            srv.close(() => r());
          })
      });
    });
  });
}

function makeClient(host: string, port: number): DaemonClient {
  return new DaemonClient({ tcpHost: host, tcpPort: port, forceTcp: true, timeoutMs: 2000 });
}

describe('DaemonClient', () => {
  it('decodes /healthz JSON', async () => {
    const server = await startMockServer({
      '/healthz': (_req, res) => {
        res.setHeader('content-type', 'application/json');
        res.end(JSON.stringify({ status: 'ok' }));
      }
    });
    try {
      const client = makeClient(server.host, server.port);
      const got = await client.healthz();
      assert.strictEqual(got.status, 'ok');
    } finally {
      await server.close();
    }
  });

  it('decodes /surfaces map', async () => {
    const server = await startMockServer({
      '/surfaces': (_req, res) => {
        res.setHeader('content-type', 'application/json');
        res.end(JSON.stringify({ claude_code: 42, cursor: 7 }));
      }
    });
    try {
      const client = makeClient(server.host, server.port);
      const surfaces = await client.surfaces();
      assert.strictEqual(surfaces.claude_code, 42);
      assert.strictEqual(surfaces.cursor, 7);
    } finally {
      await server.close();
    }
  });

  it('decodes /search engram array', async () => {
    const sample: Engram[] = [
      { id: 1, surface: 'claude_code', ts: 1_700_000_000_000_000_000, payload: 'hello world', snippet: 'hello' }
    ];
    const server = await startMockServer({
      '/search': (req, res) => {
        // Verify the query string carried through.
        const url = new URL(req.url ?? '', 'http://x');
        assert.strictEqual(url.searchParams.get('q'), 'hello');
        res.setHeader('content-type', 'application/json');
        res.end(JSON.stringify(sample));
      }
    });
    try {
      const client = makeClient(server.host, server.port);
      const rows = await client.search('hello');
      assert.strictEqual(rows.length, 1);
      assert.strictEqual(rows[0].id, 1);
      assert.strictEqual(rows[0].surface, 'claude_code');
    } finally {
      await server.close();
    }
  });

  it('decodes /ask response', async () => {
    const server = await startMockServer({
      '/ask': (_req, res) => {
        res.setHeader('content-type', 'application/json');
        res.end(
          JSON.stringify({
            question: 'q',
            fts_query: 'q OR a',
            instructions: 'use these engrams',
            engrams: []
          })
        );
      }
    });
    try {
      const client = makeClient(server.host, server.port);
      const r = await client.ask('q');
      assert.strictEqual(r.fts_query, 'q OR a');
      assert.deepStrictEqual(r.engrams, []);
    } finally {
      await server.close();
    }
  });

  it('decodes /metrics JSON', async () => {
    const server = await startMockServer({
      '/metrics': (_req, res) => {
        res.setHeader('content-type', 'application/json');
        res.end(
          JSON.stringify({
            version: 'v0.0.45',
            uptime_seconds: 10,
            engram_total: 100,
            engram_by_surface: { claude_code: 100 },
            db_path: '/tmp/x.db',
            db_size_bytes: 1024
          })
        );
      }
    });
    try {
      const client = makeClient(server.host, server.port);
      const m = await client.metrics();
      assert.strictEqual(m.engram_total, 100);
      assert.strictEqual(m.version, 'v0.0.45');
    } finally {
      await server.close();
    }
  });

  it('surfaces non-2xx body text in thrown error', async () => {
    const server = await startMockServer({
      '/search': (_req, res) => {
        res.statusCode = 400;
        res.end('q required');
      }
    });
    try {
      const client = makeClient(server.host, server.port);
      await assert.rejects(() => client.search('anything'), /400.*q required/);
    } finally {
      await server.close();
    }
  });

  it('rejects on JSON parse failure', async () => {
    const server = await startMockServer({
      '/healthz': (_req, res) => {
        res.setHeader('content-type', 'application/json');
        res.end('this is not json');
      }
    });
    try {
      const client = makeClient(server.host, server.port);
      await assert.rejects(() => client.healthz(), /non-JSON/);
    } finally {
      await server.close();
    }
  });
});

describe('engram helpers', () => {
  it('formatEngramTs converts unix-ns to a non-empty locale string', () => {
    const out = formatEngramTs(1_700_000_000_000_000_000);
    assert.ok(out.length > 0);
    assert.notStrictEqual(out, 'Invalid Date');
  });

  it('engramPreview truncates long payloads with ellipsis', () => {
    const long = 'a'.repeat(200);
    const e: Engram = { id: 1, surface: 's', ts: 0, payload: long };
    const out = engramPreview(e, 50);
    assert.strictEqual(out.length, 50);
    assert.ok(out.endsWith('…'));
  });

  it('engramPreview prefers snippet over payload', () => {
    const e: Engram = { id: 1, surface: 's', ts: 0, payload: 'long', snippet: 'short' };
    assert.strictEqual(engramPreview(e), 'short');
  });

  it('engramPreview collapses whitespace', () => {
    const e: Engram = { id: 1, surface: 's', ts: 0, payload: 'a\n\n\tb  c' };
    assert.strictEqual(engramPreview(e), 'a b c');
  });
});
