// daemonClient.ts — thin typed HTTP client for the eidetic-daemon JSON API.
//
// All endpoints return JSON (except /export which is NDJSON, not consumed here).
// Transport selection:
//   - macOS / Linux → Unix-domain socket via `http.request({ socketPath })`
//   - Windows       → TCP fallback to http://127.0.0.1:9876
//
// The `http` request URL when using `socketPath` is `http://unix:<sock>:/path`
// per node's documented quirk. We sidestep that by passing host/path explicitly
// in the options object and letting Node construct the request line.

import * as http from 'node:http';

/** Engram shape returned by /search, /recent, /engrams (mirrors internal/engram.Engram). */
export interface Engram {
  id: number;
  surface: string;
  ts: number;          // unix nanoseconds
  payload: string;
  meta?: string;
  snippet?: string;    // populated by /search FTS5
}

/** /ask response body. */
export interface AskResponse {
  question: string;
  fts_query: string;
  instructions: string;
  engrams: Engram[];
}

/** /metrics JSON shape (subset we use). */
export interface MetricsResponse {
  version: string;
  uptime_seconds: number;
  engram_total: number;
  engram_by_surface: Record<string, number>;
  db_path: string;
  db_size_bytes: number;
  latest_version?: string;
  update_available?: boolean;
}

/** /surfaces returns surface → count. */
export type SurfacesResponse = Record<string, number>;

export interface DaemonClientOptions {
  /** Unix-domain socket path. Used on macOS / Linux. */
  socketPath?: string;
  /** TCP host (Windows fallback). */
  tcpHost?: string;
  /** TCP port (Windows fallback). */
  tcpPort?: number;
  /** Per-request timeout in ms. */
  timeoutMs?: number;
  /** Force TCP even on macOS/Linux (test injection). */
  forceTcp?: boolean;
}

export class DaemonClient {
  private readonly socketPath?: string;
  private readonly tcpHost: string;
  private readonly tcpPort: number;
  private readonly timeoutMs: number;
  private readonly useTcp: boolean;

  constructor(opts: DaemonClientOptions = {}) {
    this.socketPath = opts.socketPath ?? '/tmp/eidetic-daemon.sock';
    this.tcpHost = opts.tcpHost ?? '127.0.0.1';
    this.tcpPort = opts.tcpPort ?? 9876;
    this.timeoutMs = opts.timeoutMs ?? 5000;
    this.useTcp = opts.forceTcp === true || process.platform === 'win32';
  }

  // ── public endpoint wrappers ──────────────────────────────────────────────

  async healthz(): Promise<{ status: string }> {
    return this.getJson<{ status: string }>('/healthz');
  }

  async surfaces(): Promise<SurfacesResponse> {
    return this.getJson<SurfacesResponse>('/surfaces');
  }

  async search(query: string, surface?: string, limit?: number): Promise<Engram[]> {
    const qs = new URLSearchParams({ q: query });
    if (surface) qs.set('surface', surface);
    if (limit) qs.set('limit', String(limit));
    return this.getJson<Engram[]>(`/search?${qs.toString()}`);
  }

  async recent(limit = 50, surface?: string): Promise<Engram[]> {
    const qs = new URLSearchParams({ limit: String(limit) });
    if (surface) qs.set('surface', surface);
    return this.getJson<Engram[]>(`/recent?${qs.toString()}`);
  }

  async ask(question: string, surface?: string, limit?: number): Promise<AskResponse> {
    const qs = new URLSearchParams({ question });
    if (surface) qs.set('surface', surface);
    if (limit) qs.set('limit', String(limit));
    return this.getJson<AskResponse>(`/ask?${qs.toString()}`);
  }

  async metrics(): Promise<MetricsResponse> {
    return this.getJson<MetricsResponse>('/metrics', { Accept: 'application/json' });
  }

  // ── transport ─────────────────────────────────────────────────────────────

  /**
   * Performs a GET against the daemon and decodes the body as JSON.
   * Rejects on non-2xx, on timeout, and on JSON-parse failure with the raw
   * body included so the caller can surface daemon error text verbatim.
   */
  private getJson<T>(path: string, headers: Record<string, string> = {}): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      const reqOpts: http.RequestOptions = this.useTcp
        ? {
            host: this.tcpHost,
            port: this.tcpPort,
            path,
            method: 'GET',
            headers: { 'User-Agent': 'eidetic-vscode/0.0.1', ...headers }
          }
        : {
            socketPath: this.socketPath,
            path,
            method: 'GET',
            headers: { 'User-Agent': 'eidetic-vscode/0.0.1', Host: 'localhost', ...headers }
          };

      const req = http.request(reqOpts, (res) => {
        const chunks: Buffer[] = [];
        res.on('data', (c: Buffer) => chunks.push(c));
        res.on('end', () => {
          const body = Buffer.concat(chunks).toString('utf8');
          const status = res.statusCode ?? 0;
          if (status < 200 || status >= 300) {
            reject(new Error(`daemon ${status}: ${body.trim() || res.statusMessage || ''}`));
            return;
          }
          try {
            resolve(JSON.parse(body) as T);
          } catch (err) {
            reject(new Error(`daemon returned non-JSON body: ${(err as Error).message}`));
          }
        });
      });

      req.setTimeout(this.timeoutMs, () => {
        req.destroy(new Error(`daemon request timed out after ${this.timeoutMs}ms`));
      });
      req.on('error', (err) => reject(err));
      req.end();
    });
  }
}

/** Format an engram timestamp (unix-ns → locale string). */
export function formatEngramTs(tsNanos: number): string {
  const ms = Math.floor(tsNanos / 1_000_000);
  return new Date(ms).toLocaleString();
}

/** Pull a 1-line preview from an engram payload. */
export function engramPreview(e: Engram, max = 80): string {
  const src = (e.snippet ?? e.payload ?? '').replace(/\s+/g, ' ').trim();
  return src.length > max ? src.slice(0, max - 1) + '…' : src;
}
