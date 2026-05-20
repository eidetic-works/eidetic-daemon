// errors.ts — daemon-unreachable + generic failure helpers.
//
// Every command's failure path runs through `reportDaemonError` so the toast
// text + retry action stay consistent. Detecting socket-level errors is
// brittle (ENOENT, ECONNREFUSED, EPIPE) so we just sniff the message.

import { Toast, getPreferenceValues, showToast } from "@raycast/api";
import type { Preferences } from "./types";

const SOCKET_HINTS = [
  "ENOENT",
  "ECONNREFUSED",
  "EPIPE",
  "ECONNRESET",
  "EACCES",
  "socket hang up",
  "request timed out",
];

export function isDaemonUnreachable(err: unknown): boolean {
  const msg = err instanceof Error ? err.message : String(err);
  return SOCKET_HINTS.some((h) => msg.toLowerCase().includes(h.toLowerCase()));
}

export interface RetryAction {
  title?: string;
  onAction: () => void;
}

/**
 * Show a Failure toast for an error. When `retry` is provided, the primary
 * toast action becomes a Retry button.
 */
export async function reportDaemonError(err: unknown, retry?: RetryAction): Promise<void> {
  const prefs = getPreferenceValues<Preferences>();
  const unreachable = isDaemonUnreachable(err);
  const message = err instanceof Error ? err.message : String(err);

  const title = unreachable ? "Eidetic daemon unreachable" : "Eidetic request failed";
  const body = unreachable
    ? `socket: ${prefs.socketPath || "/tmp/eidetic-daemon.sock"} — ${message}`
    : message;

  await showToast({
    style: Toast.Style.Failure,
    title,
    message: body,
    primaryAction: retry
      ? {
          title: retry.title ?? "Retry",
          onAction: () => {
            retry.onAction();
          },
        }
      : undefined,
  });
}
