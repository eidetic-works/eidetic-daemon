// format.ts — pure formatters shared by every command.
//
// Kept small + dependency-free so they're trivially unit-testable and don't
// pull React/Raycast at import time.

import type { Engram } from "./types";

/**
 * Format a unix-ns timestamp as a relative-time string ("3m ago", "yesterday",
 * "Mar 12"). Falls back to a locale string if the input is suspect.
 */
export function relativeTime(tsNanos: number, now: number = Date.now()): string {
  if (!Number.isFinite(tsNanos) || tsNanos <= 0) return "—";
  const ms = Math.floor(tsNanos / 1_000_000);
  const diffSec = Math.round((now - ms) / 1000);

  if (diffSec < 0) return "just now";
  if (diffSec < 5) return "just now";
  if (diffSec < 60) return `${diffSec}s ago`;
  const diffMin = Math.round(diffSec / 60);
  if (diffMin < 60) return `${diffMin}m ago`;
  const diffHr = Math.round(diffMin / 60);
  if (diffHr < 24) return `${diffHr}h ago`;
  const diffDay = Math.round(diffHr / 24);
  if (diffDay === 1) return "yesterday";
  if (diffDay < 7) return `${diffDay}d ago`;
  if (diffDay < 30) {
    const w = Math.round(diffDay / 7);
    return `${w}w ago`;
  }

  // Older than ~1 month → calendar date in user's locale.
  return new Date(ms).toLocaleDateString(undefined, { month: "short", day: "numeric" });
}

/** Absolute timestamp for tooltips / detail headers. */
export function absoluteTime(tsNanos: number): string {
  if (!Number.isFinite(tsNanos) || tsNanos <= 0) return "—";
  return new Date(Math.floor(tsNanos / 1_000_000)).toLocaleString();
}

/** Truncate a payload to a single-line preview for list rows. */
export function previewPayload(e: Engram, max = 80): string {
  const src = (e.snippet ?? e.payload ?? "").replace(/\s+/g, " ").trim();
  if (src.length <= max) return src;
  return `${src.slice(0, max - 1)}…`;
}

/**
 * Truncate a long string with an ellipsis; never breaks in the middle of a
 * surrogate pair so emoji surface correctly.
 */
export function truncate(s: string, max: number): string {
  if (s.length <= max) return s;
  const trimmed = s.slice(0, max - 1);
  // avoid splitting a surrogate pair
  const lastCode = trimmed.charCodeAt(trimmed.length - 1);
  if (lastCode >= 0xd800 && lastCode <= 0xdbff) {
    return `${trimmed.slice(0, -1)}…`;
  }
  return `${trimmed}…`;
}

/** Format bytes for the stats view. */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return "—";
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let v = bytes / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 ? 2 : 1)} ${units[i]}`;
}

/** Format uptime (seconds) as "3d 4h 12m". */
export function formatUptime(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return "—";
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const parts: string[] = [];
  if (d > 0) parts.push(`${d}d`);
  if (h > 0 || d > 0) parts.push(`${h}h`);
  parts.push(`${m}m`);
  return parts.join(" ");
}

/** Build the detail-pane Markdown for a single engram. */
export function engramToMarkdown(e: Engram): string {
  const ts = absoluteTime(e.ts);
  const body = (e.payload ?? "").trim() || "_(empty payload)_";
  return [
    `# Engram #${e.id}`,
    ``,
    `**Surface:** \`${e.surface}\`  `,
    `**Captured:** ${ts}`,
    ``,
    "```",
    body,
    "```",
  ].join("\n");
}
