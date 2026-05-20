// engramWebview.ts — renders an engram detail panel and `/ask` results.
//
// Two surfaces:
//   1. showEngram(engram) — single engram detail with metadata + full payload.
//   2. showAskResult(ask) — natural-language Q + cited engrams below.

import * as vscode from 'vscode';
import { AskResponse, Engram, formatEngramTs } from './daemonClient';

function esc(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

const BASE_CSS = `
  body { font-family: var(--vscode-font-family); padding: 1rem 1.5rem; color: var(--vscode-foreground); }
  h1 { font-size: 1.2rem; margin-top: 0; }
  h2 { font-size: 1rem; margin-top: 1.5rem; }
  .meta { color: var(--vscode-descriptionForeground); font-size: 0.85rem; margin-bottom: 1rem; }
  .meta span { margin-right: 1rem; }
  pre { background: var(--vscode-textCodeBlock-background); padding: 0.75rem; border-radius: 4px; overflow-x: auto; white-space: pre-wrap; word-break: break-word; }
  .engram-card { border: 1px solid var(--vscode-panel-border); padding: 0.75rem 1rem; border-radius: 6px; margin-bottom: 0.75rem; }
  .engram-card .header { display: flex; justify-content: space-between; margin-bottom: 0.4rem; font-size: 0.85rem; color: var(--vscode-descriptionForeground); }
  .question { font-size: 1.05rem; font-weight: 600; }
  .instructions { background: var(--vscode-editor-inactiveSelectionBackground); padding: 0.5rem 0.75rem; border-radius: 4px; font-style: italic; margin-bottom: 1rem; }
  .fts { font-family: var(--vscode-editor-font-family); font-size: 0.85rem; color: var(--vscode-descriptionForeground); }
`;

export function showEngram(engram: Engram, column?: vscode.ViewColumn): vscode.WebviewPanel {
  const panel = vscode.window.createWebviewPanel(
    'eidetic.engram',
    `Engram #${engram.id}`,
    column ?? vscode.ViewColumn.Active,
    { enableScripts: false, retainContextWhenHidden: true }
  );
  panel.webview.html = renderEngramHtml(engram);
  return panel;
}

export function showAskResult(ask: AskResponse, column?: vscode.ViewColumn): vscode.WebviewPanel {
  const panel = vscode.window.createWebviewPanel(
    'eidetic.recall',
    `Recall: ${truncate(ask.question, 40)}`,
    column ?? vscode.ViewColumn.Active,
    { enableScripts: false, retainContextWhenHidden: true }
  );
  panel.webview.html = renderAskHtml(ask);
  return panel;
}

function truncate(s: string, n: number): string {
  return s.length > n ? s.slice(0, n - 1) + '…' : s;
}

export function renderEngramHtml(engram: Engram): string {
  return `<!DOCTYPE html>
<html><head><meta charset="utf-8"><style>${BASE_CSS}</style></head>
<body>
  <h1>Engram #${engram.id}</h1>
  <div class="meta">
    <span><strong>surface:</strong> ${esc(engram.surface)}</span>
    <span><strong>ts:</strong> ${esc(formatEngramTs(engram.ts))}</span>
  </div>
  <h2>Payload</h2>
  <pre>${esc(engram.payload)}</pre>
  ${engram.meta ? `<h2>Meta</h2><pre>${esc(engram.meta)}</pre>` : ''}
</body></html>`;
}

export function renderAskHtml(ask: AskResponse): string {
  const cards = ask.engrams
    .map(
      (e) => `
      <div class="engram-card">
        <div class="header">
          <span>${esc(e.surface)} · #${e.id}</span>
          <span>${esc(formatEngramTs(e.ts))}</span>
        </div>
        <pre>${esc(e.snippet ?? e.payload.slice(0, 1200))}</pre>
      </div>`
    )
    .join('\n');

  return `<!DOCTYPE html>
<html><head><meta charset="utf-8"><style>${BASE_CSS}</style></head>
<body>
  <div class="question">${esc(ask.question)}</div>
  <div class="fts">FTS query: <code>${esc(ask.fts_query)}</code></div>
  <div class="instructions">${esc(ask.instructions)}</div>
  <h2>Engrams (${ask.engrams.length})</h2>
  ${ask.engrams.length === 0 ? '<p>No engrams matched.</p>' : cards}
</body></html>`;
}
