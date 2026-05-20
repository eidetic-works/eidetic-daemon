// daemon.ts — thin typed HTTP client for the eidetic-daemon JSON API.
//
// Mirrors the shape of integrations/vscode/src/daemonClient.ts so behavior
// stays consistent across surfaces. Raycast is macOS-only, so this client
// only speaks UDS via node:http with `socketPath`; we keep a TCP fallback
// constructor option for tests but never use it at runtime.

import * as http from "node:http";
import { getPreferenceValues } from "@raycast/api";
import type { AskResponse, Engram, MetricsResponse, Preferences, SurfacesResponse } from "./types";

export interface DaemonClientOptions {
  /** Unix-domain socket path. */
  socketPath?: string;
  /** TCP host (test injection only). */
  tcpHost?: string;
  /** TCP port (test injection only). */
  tcpPort?: number;
  /** Per-request timeout in ms. */
  timeoutMs?: number;
  /** Force TCP — test injection only; Raycast is macOS so UDS is default. */
  forceTcp?: boolean;
}

export class DaemonClient {
  private readonly socketPath: string;
  private readonly tcpHost: string;
  private readonly tcpPort: number;
  private readonly timeoutMs: number;
  private readonly useTcp: boolean;

  constructor(opts: DaemonClientOptions = {}) {
    this.socketPath = opts.socketPath ?? "/tmp/eidetic-daemon.sock";
    this.tcpHost = opts.tcpHost ?? "127.0.0.1";
    this.tcpPort = opts.tcpPort ?? 9876;
    this.timeoutMs = opts.timeoutMs ?? 5000;
    this.useTcp = opts.forceTcp === true;
  }

  // ── endpoints ───────────────────────────────────────────────────────────

  async healthz(): Promise<{ status: string }> {
    return this.getJson<{ status: string }>("/healthz");
  }

  async surfaces(): Promise<SurfacesResponse> {
    return this.getJson<SurfacesResponse>("/surfaces");
  }

  async search(query: string, surface?: string, limit?: number): Promise<Engram[]> {
    const qs = new URLSearchParams({ q: query });
    if (surface) qs.set("surface", surface);
    if (limit) qs.set("limit", String(limit));
    return this.getJson<Engram[]>(`/search?${qs.toString()}`);
  }

  async recent(limit = 50, surface?: string): Promise<Engram[]> {
    const qs = new URLSearchParams({ limit: String(limit) });
    if (surface) qs.set("surface", surface);
    return this.getJson<Engram[]>(`/recent?${qs.toString()}`);
  }

  async ask(question: string, surface?: string, limit?: number): Promise<AskResponse> {
    const qs = new URLSearchParams({ question });
    if (surface) qs.set("surface", surface);
    if (limit) qs.set("limit", String(limit));
    return this.getJson<AskResponse>(`/ask?${qs.toString()}`);
  }

  async metrics(): Promise<MetricsResponse> {
    return this.getJson<MetricsResponse>("/metrics", { Accept: "application/json" });
  }

  // ── transport ───────────────────────────────────────────────────────────

  /**
   * GET against the daemon, decoded as JSON.
   * Rejects on non-2xx, timeout, or JSON-parse failure with the raw body
   * included so callers can surface daemon text verbatim.
   */
  private getJson<T>(path: string, headers: Record<string, string> = {}): Promise<T> {
    return new Promise<T>((resolve, reject) => {
      const baseHeaders = {
        "User-Agent": "eidetic-raycast/0.0.1",
        Host: "localhost",
        ...headers,
      };
      const reqOpts: http.RequestOptions = this.useTcp
        ? {
            host: this.tcpHost,
            port: this.tcpPort,
            path,
            method: "GET",
            headers: baseHeaders,
          }
        : {
            socketPath: this.socketPath,
            path,
            method: "GET",
            headers: baseHeaders,
          };

      const req = http.request(reqOpts, (res) => {
        const chunks: Buffer[] = [];
        res.on("data", (c: Buffer) => chunks.push(c));
        res.on("end", () => {
          const body = Buffer.concat(chunks).toString("utf8");
          const status = res.statusCode ?? 0;
          if (status < 200 || status >= 300) {
            reject(new Error(`daemon ${status}: ${body.trim() || res.statusMessage || ""}`));
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
      req.on("error", (err) => reject(err));
      req.end();
    });
  }
}

/**
 * Build a DaemonClient from current Raycast preferences. Centralized so each
 * command resolves prefs the same way and timeouts/sockets stay consistent.
 */
export function clientFromPreferences(): DaemonClient {
  const prefs = getPreferenceValues<Preferences>();
  const timeoutMs = Number.parseInt(prefs.timeoutMs ?? "5000", 10);
  return new DaemonClient({
    socketPath: prefs.socketPath || "/tmp/eidetic-daemon.sock",
    timeoutMs: Number.isFinite(timeoutMs) && timeoutMs > 0 ? timeoutMs : 5000,
  });
}

/** Surface filter from preferences (empty string → undefined). */
export function preferredSurface(): string | undefined {
  const prefs = getPreferenceValues<Preferences>();
  const s = (prefs.surfaceFilter ?? "").trim();
  return s.length > 0 ? s : undefined;
}

/** Search debounce from preferences (ms). Falls back to 250ms. */
export function preferredDebounceMs(): number {
  const prefs = getPreferenceValues<Preferences>();
  const n = Number.parseInt(prefs.searchDebounceMs ?? "250", 10);
  return Number.isFinite(n) && n >= 0 ? n : 250;
}
