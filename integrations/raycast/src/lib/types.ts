// types.ts — shared types for the Eidetic Raycast extension.
//
// These mirror the daemon's HTTP-JSON contract (see internal/engram.Engram and
// the /search /recent /ask /metrics endpoint responses) and are kept in sync
// with the VS Code extension's daemonClient.ts.

/** Engram shape returned by /search, /recent, /engrams. */
export interface Engram {
  id: number;
  surface: string;
  /** Unix nanoseconds. Divide by 1_000_000 for ms. */
  ts: number;
  payload: string;
  meta?: string;
  /** Populated by /search FTS5 highlights. */
  snippet?: string;
}

/** /ask response body. */
export interface AskResponse {
  question: string;
  fts_query: string;
  instructions: string;
  engrams: Engram[];
}

/** /metrics JSON response (subset we consume). */
export interface MetricsResponse {
  version: string;
  uptime_seconds: number;
  engram_total: number;
  engram_by_surface: Record<string, number>;
  db_path: string;
  db_size_bytes: number;
  latest_version?: string;
  update_available?: boolean;
  last_sync_ts?: number;
}

/** /surfaces returns surface → count. */
export type SurfacesResponse = Record<string, number>;

/** Raycast preference shape (from package.json `preferences`). */
export interface Preferences {
  socketPath: string;
  surfaceFilter: string;
  searchDebounceMs: string;
  timeoutMs: string;
}
