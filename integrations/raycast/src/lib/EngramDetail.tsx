// EngramDetail.tsx — shared Detail view used by Recent + Search results.
//
// Pulled into its own file so each command stays small and we render engrams
// the same way everywhere (metadata sidebar + payload markdown).

import { Action, ActionPanel, Detail } from "@raycast/api";
import type { Engram } from "./types";
import { absoluteTime, engramToMarkdown, relativeTime } from "./format";

export function EngramDetail({ engram }: { engram: Engram }) {
  const markdown = engramToMarkdown(engram);

  return (
    <Detail
      navigationTitle={`Engram #${engram.id}`}
      markdown={markdown}
      metadata={
        <Detail.Metadata>
          <Detail.Metadata.Label title="ID" text={String(engram.id)} />
          <Detail.Metadata.Label title="Surface" text={engram.surface} />
          <Detail.Metadata.Label title="Captured" text={absoluteTime(engram.ts)} />
          <Detail.Metadata.Label title="Relative" text={relativeTime(engram.ts)} />
          {engram.meta ? <Detail.Metadata.Label title="Meta" text={engram.meta} /> : null}
        </Detail.Metadata>
      }
      actions={
        <ActionPanel>
          <Action.CopyToClipboard title="Copy Payload" content={engram.payload ?? ""} />
          <Action.CopyToClipboard
            title="Copy Engram ID"
            content={String(engram.id)}
            shortcut={{ modifiers: ["cmd"], key: "i" }}
          />
          <Action.CopyToClipboard
            title="Copy as JSON"
            content={JSON.stringify(engram, null, 2)}
            shortcut={{ modifiers: ["cmd", "shift"], key: "j" }}
          />
        </ActionPanel>
      }
    />
  );
}
