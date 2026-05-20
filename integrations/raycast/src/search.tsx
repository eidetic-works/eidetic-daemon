// search.tsx — List with a server-side debounced search.
//
// Each keystroke schedules a debounced call to GET /search?q=... with the
// preference-controlled debounce window (default 250ms). The previous timer
// is cleared on every keystroke so we never fire the daemon mid-typing.

import { useEffect, useRef, useState } from "react";
import { Action, ActionPanel, Icon, List } from "@raycast/api";
import {
  clientFromPreferences,
  preferredDebounceMs,
  preferredSurface,
} from "./lib/daemon";
import { EngramDetail } from "./lib/EngramDetail";
import { reportDaemonError } from "./lib/errors";
import { previewPayload, relativeTime } from "./lib/format";
import type { Engram } from "./lib/types";

const SEARCH_LIMIT = 50;

export default function SearchCommand() {
  const [query, setQuery] = useState<string>("");
  const [results, setResults] = useState<Engram[]>([]);
  const [isLoading, setIsLoading] = useState<boolean>(false);
  const debounceRef = useRef<NodeJS.Timeout | null>(null);
  const requestSeqRef = useRef<number>(0);

  useEffect(() => {
    if (debounceRef.current) {
      clearTimeout(debounceRef.current);
      debounceRef.current = null;
    }

    const trimmed = query.trim();
    if (trimmed.length === 0) {
      setResults([]);
      setIsLoading(false);
      return;
    }

    const debounceMs = preferredDebounceMs();
    setIsLoading(true);
    const mySeq = ++requestSeqRef.current;

    debounceRef.current = setTimeout(() => {
      const client = clientFromPreferences();
      client
        .search(trimmed, preferredSurface(), SEARCH_LIMIT)
        .then((rows) => {
          // Drop the response if a newer keystroke superseded us.
          if (mySeq !== requestSeqRef.current) return;
          setResults(rows);
        })
        .catch(async (err) => {
          if (mySeq !== requestSeqRef.current) return;
          await reportDaemonError(err, {
            title: "Retry",
            onAction: () => setQuery((q) => q + ""),
          });
          setResults([]);
        })
        .finally(() => {
          if (mySeq === requestSeqRef.current) setIsLoading(false);
        });
    }, debounceMs);

    return () => {
      if (debounceRef.current) {
        clearTimeout(debounceRef.current);
        debounceRef.current = null;
      }
    };
  }, [query]);

  return (
    <List
      isLoading={isLoading}
      onSearchTextChange={setQuery}
      searchBarPlaceholder="Search engrams (FTS5)…"
      throttle={false}
    >
      {query.trim().length === 0 ? (
        <List.EmptyView
          icon={Icon.MagnifyingGlass}
          title="Type to search"
          description="Eidetic searches the local engram store via FTS5."
        />
      ) : results.length === 0 && !isLoading ? (
        <List.EmptyView icon={Icon.QuestionMark} title="No matches" description={`No engrams matched "${query}".`} />
      ) : (
        results.map((e) => (
          <List.Item
            key={e.id}
            title={previewPayload(e) || `Engram #${e.id}`}
            subtitle={e.surface}
            accessories={[{ text: relativeTime(e.ts) }]}
            icon={Icon.Document}
            actions={
              <ActionPanel>
                <Action.Push title="Open Engram" icon={Icon.Eye} target={<EngramDetail engram={e} />} />
                <Action.CopyToClipboard
                  title="Copy Payload"
                  content={e.payload ?? ""}
                  shortcut={{ modifiers: ["cmd"], key: "c" }}
                />
                <Action.CopyToClipboard
                  title="Copy Engram ID"
                  content={String(e.id)}
                  shortcut={{ modifiers: ["cmd"], key: "i" }}
                />
              </ActionPanel>
            }
          />
        ))
      )}
    </List>
  );
}
