// daemonClient.ts — eidetic-daemon HTTP client for the Obsidian plugin.
//
// Transport selection:
//   - Desktop, daemon URL is "uds:<socket-path>" → node:http with socketPath.
//   - Desktop, daemon URL is "http://host:port" → Obsidian's requestUrl (no CORS).
//   - Mobile (no node:http) → only http(s) URLs work; UDS gracefully degrades to "unreachable".
//
// We use Obsidian's `requestUrl` for HTTP so the same call path works on
// desktop and mobile; this also bypasses CORS and tracking-protection logic
// that Obsidian's normal fetch would apply.

import { requestUrl, type RequestUrlParam } from 'obsidian';

export interface Engram {
  id: number;
  surface: string;
  ts: number;
  payload: string;
  meta?: string;
  snippet?: string;
}

export interface AskResponse {
  question: string;
  fts_query: string;
  instructions: string;
  engrams: Engram[];
}

export interface CapturePayload {
  surface: string;
  payload: string;
  ts?: number;
  meta?: Record<string, unknown>;
}

export interface DaemonClientOptions {
  /** Daemon URL. Either `uds:/tmp/eidetic-daemon.sock` or `http://127.0.0.1:9876`. */
  url: string;
  /** Optional Bearer token; sent as `Authorization: Bearer <token>` on every request. */
  token?: string;
  /** Per-request timeout in ms (UDS path only — requestUrl has its own). */
  timeoutMs?: number;
}

/**
 * Parse a daemon URL into either a UDS socket-path or an http(s) base URL.
 * Returns `{ kind: 'uds', socketPath }` or `{ kind: 'http', base }`.
 */
function parseUrl(url: string): { kind: 'uds'; socketPath: string } | { kind: 'http'; base: string } | { kind: 'invalid' } {
  if (!url || url.trim().length === 0) {
    return { kind: 'invalid' };
  }
  const trimmed = url.trim();
  if (trimmed.startsWith('uds:')) {
    return { kind: 'uds', socketPath: trimmed.slice(4) };
  }
  if (trimmed.startsWith('http://') || trimmed.startsWith('https://')) {
    // strip trailing slash so we can concat paths reliably
    return { kind: 'http', base: trimmed.replace(/\/+$/, '') };
  }
  return { kind: 'invalid' };
}

/** True when running in Obsidian Desktop (Electron) — node built-ins are available. */
function isDesktop(): boolean {
  try {
    // `process` is the node global on desktop; undefined on mobile builds.
    const p = (globalThis as { process?: { versions?: { node?: string } } }).process;
    return typeof p !== 'undefined' && typeof p.versions?.node === 'string';
  } catch {
    return false;
  }
}

export class DaemonClient {
  private url: string;
  private token?: string;
  private timeoutMs: number;

  constructor(opts: DaemonClientOptions) {
    this.url = opts.url;
    this.token = opts.token;
    this.timeoutMs = opts.timeoutMs ?? 5000;
  }

  update(opts: DaemonClientOptions): void {
    this.url = opts.url;
    this.token = opts.token;
    this.timeoutMs = opts.timeoutMs ?? this.timeoutMs;
  }

  /** Returns true if the current URL is usable on this device (mobile rejects UDS). */
  isReachableFromHere(): boolean {
    const parsed = parseUrl(this.url);
    if (parsed.kind === 'invalid') return false;
    if (parsed.kind === 'uds') return isDesktop();
    return true;
  }

  // ── endpoint wrappers ────────────────────────────────────────────────────

  async healthz(): Promise<{ status: string }> {
    return this.request<{ status: string }>('GET', '/healthz');
  }

  async capture(p: CapturePayload): Promise<{ ok: boolean; id?: number }> {
    return this.request<{ ok: boolean; id?: number }>('POST', '/engrams', p);
  }

  async ask(question: string, limit?: number): Promise<AskResponse> {
    const qs = new URLSearchParams({ question });
    if (limit) qs.set('limit', String(limit));
    return this.request<AskResponse>('GET', `/ask?${qs.toString()}`);
  }

  // ── transport ────────────────────────────────────────────────────────────

  private async request<T>(method: 'GET' | 'POST', path: string, body?: unknown): Promise<T> {
    const parsed = parseUrl(this.url);
    if (parsed.kind === 'invalid') {
      throw new Error('daemon URL not configured');
    }
    if (parsed.kind === 'uds') {
      if (!isDesktop()) {
        throw new Error('UDS sockets are unavailable on mobile — set an http:// URL in settings');
      }
      return this.requestUds<T>(parsed.socketPath, method, path, body);
    }
    return this.requestHttp<T>(parsed.base, method, path, body);
  }

  /** UDS transport via node:http. Desktop only. */
  private requestUds<T>(socketPath: string, method: string, path: string, body?: unknown): Promise<T> {
    // require() is intentional — keeps `node:http` out of the mobile bundle's
    // static graph. If esbuild evaluates this branch on mobile the throw above
    // fires first.
    // eslint-disable-next-line @typescript-eslint/no-var-requires
    const http = require('node:http') as typeof import('node:http');
    const headers: Record<string, string> = {
      Host: 'localhost',
      'User-Agent': 'eidetic-obsidian/0.0.1'
    };
    if (this.token) headers['Authorization'] = `Bearer ${this.token}`;
    let bodyBuf: Buffer | undefined;
    if (body !== undefined) {
      bodyBuf = Buffer.from(JSON.stringify(body), 'utf8');
      headers['Content-Type'] = 'application/json';
      headers['Content-Length'] = String(bodyBuf.length);
    }
    return new Promise<T>((resolve, reject) => {
      const req = http.request({ socketPath, path, method, headers }, (res) => {
        const chunks: Buffer[] = [];
        res.on('data', (c: Buffer) => chunks.push(c));
        res.on('end', () => {
          const text = Buffer.concat(chunks).toString('utf8');
          const status = res.statusCode ?? 0;
          if (status < 200 || status >= 300) {
            reject(new Error(`daemon ${status}: ${text.trim() || res.statusMessage || ''}`));
            return;
          }
          try {
            resolve(text ? (JSON.parse(text) as T) : ({} as T));
          } catch (err) {
            reject(new Error(`daemon returned non-JSON body: ${(err as Error).message}`));
          }
        });
      });
      req.setTimeout(this.timeoutMs, () => {
        req.destroy(new Error(`daemon request timed out after ${this.timeoutMs}ms`));
      });
      req.on('error', (err) => reject(err));
      if (bodyBuf) req.write(bodyBuf);
      req.end();
    });
  }

  /** TCP transport via Obsidian's requestUrl — works on desktop + mobile. */
  private async requestHttp<T>(base: string, method: string, path: string, body?: unknown): Promise<T> {
    const headers: Record<string, string> = {
      'User-Agent': 'eidetic-obsidian/0.0.1'
    };
    if (this.token) headers['Authorization'] = `Bearer ${this.token}`;
    if (body !== undefined) headers['Content-Type'] = 'application/json';

    const opts: RequestUrlParam = {
      url: `${base}${path}`,
      method,
      headers,
      throw: false
    };
    if (body !== undefined) opts.body = JSON.stringify(body);

    const res = await requestUrl(opts);
    if (res.status < 200 || res.status >= 300) {
      throw new Error(`daemon ${res.status}: ${(res.text || '').trim()}`);
    }
    if (!res.text) return {} as T;
    try {
      return JSON.parse(res.text) as T;
    } catch (err) {
      throw new Error(`daemon returned non-JSON body: ${(err as Error).message}`);
    }
  }
}

/** Format a unix-ns timestamp as a locale string. */
export function formatEngramTs(tsNanos: number): string {
  if (!tsNanos) return '';
  const ms = Math.floor(tsNanos / 1_000_000);
  return new Date(ms).toLocaleString();
}
