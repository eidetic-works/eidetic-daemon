// recall.tsx — Two-step: a Form takes a natural-language question, the
// submit handler navigates to a Detail rendering the daemon's /ask response.
//
// We use `useNavigation().push` so the result view sits on top of the form
// and Esc returns to the form (lets the user iterate questions).

import { useState } from "react";
import { Action, ActionPanel, Detail, Form, Icon, useNavigation } from "@raycast/api";
import { clientFromPreferences, preferredSurface } from "./lib/daemon";
import { reportDaemonError } from "./lib/errors";
import { absoluteTime, previewPayload, relativeTime, truncate } from "./lib/format";
import type { AskResponse, Engram } from "./lib/types";

const ASK_LIMIT = 8;

export default function RecallCommand() {
  const { push } = useNavigation();
  const [question, setQuestion] = useState<string>("");
  const [isSubmitting, setIsSubmitting] = useState<boolean>(false);

  async function handleSubmit() {
    const trimmed = question.trim();
    if (trimmed.length === 0) return;

    setIsSubmitting(true);
    try {
      const client = clientFromPreferences();
      const result = await client.ask(trimmed, preferredSurface(), ASK_LIMIT);
      push(<RecallResult result={result} />);
    } catch (err) {
      await reportDaemonError(err, {
        title: "Retry",
        onAction: () => {
          void handleSubmit();
        },
      });
    } finally {
      setIsSubmitting(false);
    }
  }

  return (
    <Form
      isLoading={isSubmitting}
      actions={
        <ActionPanel>
          <Action.SubmitForm title="Ask" icon={Icon.QuestionMarkCircle} onSubmit={handleSubmit} />
        </ActionPanel>
      }
    >
      <Form.TextArea
        id="question"
        title="Question"
        placeholder="What was that workaround for the SQLite cgo issue?"
        value={question}
        onChange={setQuestion}
        autoFocus
      />
      <Form.Description text="Eidetic builds an FTS5 query from your question and assembles instructions over the top engrams." />
    </Form>
  );
}

function RecallResult({ result }: { result: AskResponse }) {
  const markdown = buildRecallMarkdown(result);

  return (
    <Detail
      navigationTitle="Recall Result"
      markdown={markdown}
      metadata={
        <Detail.Metadata>
          <Detail.Metadata.Label title="Question" text={truncate(result.question, 120)} />
          <Detail.Metadata.Label title="FTS query" text={truncate(result.fts_query, 120)} />
          <Detail.Metadata.Label title="Engrams cited" text={String(result.engrams.length)} />
        </Detail.Metadata>
      }
      actions={
        <ActionPanel>
          <Action.CopyToClipboard title="Copy Instructions" content={result.instructions ?? ""} />
          <Action.CopyToClipboard
            title="Copy as JSON"
            content={JSON.stringify(result, null, 2)}
            shortcut={{ modifiers: ["cmd", "shift"], key: "j" }}
          />
        </ActionPanel>
      }
    />
  );
}

function buildRecallMarkdown(result: AskResponse): string {
  const head = [
    `# Recall: ${truncate(result.question, 80)}`,
    ``,
    `_FTS query: \`${truncate(result.fts_query, 120)}\`_`,
    ``,
    `## Instructions`,
    ``,
    (result.instructions ?? "").trim() || "_(no instructions returned)_",
    ``,
    `## Engrams`,
    ``,
  ];
  const body = result.engrams.length === 0 ? ["_No engrams cited._"] : result.engrams.map(formatEngramBlock);
  return head.concat(body).join("\n");
}

function formatEngramBlock(e: Engram): string {
  return [
    `### #${e.id} · \`${e.surface}\` · ${relativeTime(e.ts)} (${absoluteTime(e.ts)})`,
    ``,
    "```",
    previewPayload(e, 400),
    "```",
    ``,
  ].join("\n");
}
