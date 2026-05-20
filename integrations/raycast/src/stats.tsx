// stats.tsx — Detail view backed by /metrics. Shows daemon version + engram
// totals + per-surface breakdown. If an update is available we link to the
// known download URL (matched to the VS Code extension's behavior).

import { useEffect, useState } from "react";
import { Action, ActionPanel, Detail, Icon } from "@raycast/api";
import { clientFromPreferences } from "./lib/daemon";
import { reportDaemonError } from "./lib/errors";
import { absoluteTime, formatBytes, formatUptime } from "./lib/format";
import type { MetricsResponse } from "./lib/types";

const UPDATE_URL = "https://eidetic.works/download";

export default function StatsCommand() {
  const [metrics, setMetrics] = useState<MetricsResponse | null>(null);
  const [isLoading, setIsLoading] = useState<boolean>(true);
  const [refreshTick, setRefreshTick] = useState<number>(0);

  useEffect(() => {
    let cancelled = false;
    setIsLoading(true);
    const client = clientFromPreferences();
    client
      .metrics()
      .then((m) => {
        if (cancelled) return;
        setMetrics(m);
      })
      .catch(async (err) => {
        if (cancelled) return;
        await reportDaemonError(err, {
          title: "Retry",
          onAction: () => setRefreshTick((t) => t + 1),
        });
        setMetrics(null);
      })
      .finally(() => {
        if (!cancelled) setIsLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [refreshTick]);

  const markdown = metrics ? buildStatsMarkdown(metrics) : "# Eidetic Daemon\n\n_Loading…_";

  return (
    <Detail
      isLoading={isLoading}
      navigationTitle="Eidetic Stats"
      markdown={markdown}
      metadata={
        metrics ? (
          <Detail.Metadata>
            <Detail.Metadata.Label title="Version" text={metrics.version} />
            <Detail.Metadata.Label title="Engrams (total)" text={metrics.engram_total.toLocaleString()} />
            <Detail.Metadata.Label title="Uptime" text={formatUptime(metrics.uptime_seconds)} />
            <Detail.Metadata.Label title="DB size" text={formatBytes(metrics.db_size_bytes)} />
            <Detail.Metadata.Label title="DB path" text={metrics.db_path} />
            {typeof metrics.last_sync_ts === "number" ? (
              <Detail.Metadata.Label title="Last sync" text={absoluteTime(metrics.last_sync_ts)} />
            ) : null}
            {metrics.update_available ? (
              <Detail.Metadata.Link title="Update available" target={UPDATE_URL} text={metrics.latest_version ?? "download"} />
            ) : (
              <Detail.Metadata.Label title="Up to date" text="yes" />
            )}
          </Detail.Metadata>
        ) : null
      }
      actions={
        <ActionPanel>
          <Action
            title="Refresh"
            icon={Icon.ArrowClockwise}
            shortcut={{ modifiers: ["cmd"], key: "r" }}
            onAction={() => setRefreshTick((t) => t + 1)}
          />
          {metrics?.update_available ? <Action.OpenInBrowser title="Download Update" url={UPDATE_URL} /> : null}
          {metrics ? (
            <Action.CopyToClipboard
              title="Copy Metrics JSON"
              content={JSON.stringify(metrics, null, 2)}
              shortcut={{ modifiers: ["cmd", "shift"], key: "j" }}
            />
          ) : null}
        </ActionPanel>
      }
    />
  );
}

function buildStatsMarkdown(m: MetricsResponse): string {
  const lines: string[] = [];
  lines.push(`# Eidetic Daemon`, ``, `**Version:** \`${m.version}\``, ``);

  if (m.update_available) {
    lines.push(`> Update available: \`${m.latest_version ?? "unknown"}\` → [${UPDATE_URL}](${UPDATE_URL})`, ``);
  }

  lines.push(`## Engrams`, ``, `**Total:** ${m.engram_total.toLocaleString()}`, ``);

  const surfaces = Object.entries(m.engram_by_surface ?? {}).sort((a, b) => b[1] - a[1]);
  if (surfaces.length === 0) {
    lines.push(`_No surfaces have captured engrams yet._`, ``);
  } else {
    lines.push(`| Surface | Count |`, `| --- | ---: |`);
    for (const [surface, count] of surfaces) {
      lines.push(`| \`${surface}\` | ${count.toLocaleString()} |`);
    }
    lines.push(``);
  }

  lines.push(`## Daemon`, ``);
  lines.push(`- **Uptime:** ${formatUptime(m.uptime_seconds)}`);
  lines.push(`- **DB size:** ${formatBytes(m.db_size_bytes)}`);
  lines.push(`- **DB path:** \`${m.db_path}\``);
  if (typeof m.last_sync_ts === "number") {
    lines.push(`- **Last sync:** ${absoluteTime(m.last_sync_ts)}`);
  }

  return lines.join("\n");
}
