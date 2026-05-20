// recent.tsx — List view of the latest 50 engrams across all surfaces.
//
// On Enter → opens a Detail with the full payload. The search bar acts as a
// client-side filter (Raycast's default List filtering); we don't push the
// query to the daemon here — see `search` for that.

import { useEffect, useMemo, useState } from "react";
import { Action, ActionPanel, Icon, List } from "@raycast/api";
import { clientFromPreferences, preferredSurface } from "./lib/daemon";
import { EngramDetail } from "./lib/EngramDetail";
import { reportDaemonError } from "./lib/errors";
import { previewPayload, relativeTime } from "./lib/format";
import type { Engram } from "./lib/types";

const DEFAULT_LIMIT = 50;

export default function RecentCommand() {
  const [engrams, setEngrams] = useState<Engram[]>([]);
  const [isLoading, setIsLoading] = useState<boolean>(true);
  const [surfaceFilter, setSurfaceFilter] = useState<string | undefined>(preferredSurface());
  const [refreshTick, setRefreshTick] = useState<number>(0);

  // Collect the set of distinct surfaces seen so we can populate the dropdown
  // without making an extra /surfaces call on every render.
  const surfaceOptions = useMemo(() => {
    const set = new Set<string>();
    for (const e of engrams) set.add(e.surface);
    return Array.from(set).sort();
  }, [engrams]);

  useEffect(() => {
    let cancelled = false;
    setIsLoading(true);
    const client = clientFromPreferences();
    client
      .recent(DEFAULT_LIMIT, surfaceFilter)
      .then((result) => {
        if (cancelled) return;
        setEngrams(result);
      })
      .catch(async (err) => {
        if (cancelled) return;
        await reportDaemonError(err, {
          title: "Retry",
          onAction: () => setRefreshTick((t) => t + 1),
        });
        setEngrams([]);
      })
      .finally(() => {
        if (!cancelled) setIsLoading(false);
      });
    return () => {
      cancelled = true;
    };
  }, [surfaceFilter, refreshTick]);

  return (
    <List
      isLoading={isLoading}
      searchBarPlaceholder="Filter recent engrams…"
      searchBarAccessory={
        <List.Dropdown
          tooltip="Filter by surface"
          value={surfaceFilter ?? ""}
          onChange={(next) => setSurfaceFilter(next === "" ? undefined : next)}
        >
          <List.Dropdown.Item title="All surfaces" value="" />
          {surfaceOptions.map((s) => (
            <List.Dropdown.Item key={s} title={s} value={s} />
          ))}
        </List.Dropdown>
      }
    >
      {engrams.length === 0 && !isLoading ? (
        <List.EmptyView
          icon={Icon.Tray}
          title="No engrams"
          description="Eidetic-daemon has not captured anything yet — or it is not running."
          actions={
            <ActionPanel>
              <Action title="Refresh" icon={Icon.ArrowClockwise} onAction={() => setRefreshTick((t) => t + 1)} />
            </ActionPanel>
          }
        />
      ) : (
        engrams.map((e) => (
          <List.Item
            key={e.id}
            title={previewPayload(e) || `Engram #${e.id}`}
            subtitle={e.surface}
            accessories={[{ text: relativeTime(e.ts) }]}
            icon={Icon.Document}
            actions={
              <ActionPanel>
                <Action.Push title="Open Engram" icon={Icon.Eye} target={<EngramDetail engram={e} />} />
                <Action
                  title="Refresh"
                  icon={Icon.ArrowClockwise}
                  shortcut={{ modifiers: ["cmd"], key: "r" }}
                  onAction={() => setRefreshTick((t) => t + 1)}
                />
                <Action.CopyToClipboard
                  title="Copy Payload"
                  content={e.payload ?? ""}
                  shortcut={{ modifiers: ["cmd"], key: "c" }}
                />
              </ActionPanel>
            }
          />
        ))
      )}
    </List>
  );
}
