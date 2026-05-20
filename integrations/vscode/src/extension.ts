// extension.ts — VS Code entrypoint for the eidetic-vscode extension.
//
// Wires:
//   - DaemonClient against configured socket / TCP fallback.
//   - Three commands: search / recall / recentEngrams + helper "openEngram".
//   - RecentEngramsProvider tree view (polls /surfaces + /recent).
//   - Status-bar item polling /metrics for total engram count.
//   - Config-change listener that rebuilds the client when settings change.

import * as vscode from 'vscode';
import { DaemonClient, Engram, engramPreview, formatEngramTs } from './daemonClient';
import { RecentEngramsProvider } from './recentTreeProvider';
import { showAskResult, showEngram } from './engramWebview';

let client: DaemonClient;
let recentProvider: RecentEngramsProvider;
let statusItem: vscode.StatusBarItem;
let metricsTimer: NodeJS.Timeout | undefined;

function buildClient(): DaemonClient {
  const cfg = vscode.workspace.getConfiguration('eidetic');
  return new DaemonClient({
    socketPath: cfg.get<string>('socketPath') ?? '/tmp/eidetic-daemon.sock',
    tcpHost: cfg.get<string>('tcpHost') ?? '127.0.0.1',
    tcpPort: cfg.get<number>('tcpPort') ?? 9876,
    timeoutMs: cfg.get<number>('timeoutMs') ?? 5000
  });
}

function surfaceFilter(): string {
  return (vscode.workspace.getConfiguration('eidetic').get<string>('surfaceFilter') ?? '').trim();
}

function recentPollMs(): number {
  return vscode.workspace.getConfiguration('eidetic').get<number>('recentPollMs') ?? 60_000;
}

function metricsPollMs(): number {
  return vscode.workspace.getConfiguration('eidetic').get<number>('metricsPollMs') ?? 300_000;
}

export function activate(context: vscode.ExtensionContext): void {
  client = buildClient();

  // ── Status bar (engram count) ─────────────────────────────────────────────
  statusItem = vscode.window.createStatusBarItem(vscode.StatusBarAlignment.Right, 50);
  statusItem.name = 'Eidetic';
  statusItem.tooltip = 'Eidetic engram count (click to recall)';
  statusItem.command = 'eidetic.recall';
  statusItem.text = '$(database) eidetic: …';
  statusItem.show();
  context.subscriptions.push(statusItem);

  const refreshStatus = async (): Promise<void> => {
    try {
      const m = await client.metrics();
      const tag = m.update_available && m.latest_version ? ` $(arrow-up)${m.latest_version}` : '';
      statusItem.text = `$(database) ${m.engram_total.toLocaleString()} engrams${tag}`;
      statusItem.tooltip = `Eidetic v${m.version} · ${m.engram_total.toLocaleString()} engrams across ${Object.keys(
        m.engram_by_surface
      ).length} surfaces · click to recall`;
    } catch (err) {
      statusItem.text = '$(database) eidetic: offline';
      statusItem.tooltip = `Daemon unreachable: ${(err as Error).message}`;
    }
  };
  const scheduleMetrics = (): void => {
    void refreshStatus();
    metricsTimer = setTimeout(function tick() {
      void refreshStatus().finally(() => {
        metricsTimer = setTimeout(tick, Math.max(30_000, metricsPollMs()));
      });
    }, Math.max(30_000, metricsPollMs()));
  };
  scheduleMetrics();
  context.subscriptions.push({
    dispose: () => {
      if (metricsTimer) clearTimeout(metricsTimer);
    }
  });

  // ── Recent engrams tree view ──────────────────────────────────────────────
  recentProvider = new RecentEngramsProvider(client, surfaceFilter, recentPollMs);
  context.subscriptions.push(recentProvider);
  context.subscriptions.push(
    vscode.window.createTreeView('eidetic.recent', { treeDataProvider: recentProvider, showCollapseAll: true })
  );

  // ── Commands ──────────────────────────────────────────────────────────────
  context.subscriptions.push(
    vscode.commands.registerCommand('eidetic.refreshRecent', () => void recentProvider.refresh()),

    vscode.commands.registerCommand('eidetic.openEngram', (engram: Engram) => {
      if (engram) showEngram(engram);
    }),

    vscode.commands.registerCommand('eidetic.search', async () => {
      const query = await vscode.window.showInputBox({
        prompt: 'Search engrams (FTS5 syntax — bare keywords or "quoted phrase")',
        placeHolder: 'postgres trick',
        ignoreFocusOut: true
      });
      if (!query) return;
      const items = await vscode.window.withProgress(
        { location: vscode.ProgressLocation.Notification, title: `Searching engrams: ${query}` },
        async () => {
          try {
            return await client.search(query, surfaceFilter() || undefined, 50);
          } catch (err) {
            void vscode.window.showErrorMessage(`Eidetic search failed: ${(err as Error).message}`);
            return [] as Engram[];
          }
        }
      );
      if (items.length === 0) {
        void vscode.window.showInformationMessage(`No engrams matched "${query}".`);
        return;
      }
      const pick = await vscode.window.showQuickPick(
        items.map((e) => ({
          label: `$(database) ${e.surface} #${e.id}`,
          description: formatEngramTs(e.ts),
          detail: engramPreview(e, 180),
          engram: e
        })),
        { matchOnDescription: true, matchOnDetail: true, placeHolder: `${items.length} engrams matched "${query}"` }
      );
      if (pick) showEngram(pick.engram);
    }),

    vscode.commands.registerCommand('eidetic.recall', async () => {
      const question = await vscode.window.showInputBox({
        prompt: 'Ask a question — Eidetic will retrieve matching engrams with citations',
        placeHolder: 'What was that postgres trick I learned?',
        ignoreFocusOut: true
      });
      if (!question) return;
      try {
        const result = await vscode.window.withProgress(
          { location: vscode.ProgressLocation.Notification, title: `Recall: ${question}` },
          () => client.ask(question, surfaceFilter() || undefined, 10)
        );
        showAskResult(result);
      } catch (err) {
        void vscode.window.showErrorMessage(`Eidetic recall failed: ${(err as Error).message}`);
      }
    }),

    vscode.commands.registerCommand('eidetic.recentEngrams', async () => {
      // Focus the sidebar view (it's already populated by the provider's poll).
      await vscode.commands.executeCommand('eidetic.recent.focus');
      void recentProvider.refresh();
    })
  );

  // ── Config-change listener: rebuild client when transport settings change ─
  context.subscriptions.push(
    vscode.workspace.onDidChangeConfiguration((e) => {
      if (!e.affectsConfiguration('eidetic')) return;
      const next = buildClient();
      client = next;
      recentProvider.setClient(next);
      void refreshStatus();
    })
  );
}

export function deactivate(): void {
  if (metricsTimer) clearTimeout(metricsTimer);
  metricsTimer = undefined;
  // recentProvider/statusItem are disposed via context.subscriptions.
}
