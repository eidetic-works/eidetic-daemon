// recentTreeProvider.ts — TreeDataProvider for the "Recent Engrams" sidebar view.
//
// Polls /surfaces + /recent every `pollMs` (default 60s). Cancellable: dispose()
// clears the interval and aborts the in-flight refresh future. Errors are shown
// as a synthetic tree node so the sidebar never goes blank silently.

import * as vscode from 'vscode';
import { DaemonClient, Engram, engramPreview, formatEngramTs, SurfacesResponse } from './daemonClient';

type Node = SurfaceNode | EngramNode | MessageNode;

class SurfaceNode extends vscode.TreeItem {
  readonly kind = 'surface' as const;
  constructor(public readonly surface: string, public readonly count: number, public readonly engrams: Engram[]) {
    super(`${surface} (${count})`, vscode.TreeItemCollapsibleState.Expanded);
    this.iconPath = new vscode.ThemeIcon('database');
    this.contextValue = 'eidetic.surface';
  }
}

class EngramNode extends vscode.TreeItem {
  readonly kind = 'engram' as const;
  constructor(public readonly engram: Engram) {
    super(engramPreview(engram), vscode.TreeItemCollapsibleState.None);
    this.description = formatEngramTs(engram.ts);
    this.tooltip = new vscode.MarkdownString(
      `**${engram.surface}** · ${formatEngramTs(engram.ts)}\n\n\`\`\`\n${engram.payload.slice(0, 600)}\n\`\`\``
    );
    this.iconPath = new vscode.ThemeIcon('note');
    this.contextValue = 'eidetic.engram';
    this.command = {
      command: 'eidetic.openEngram',
      title: 'Open Engram',
      arguments: [engram]
    };
  }
}

class MessageNode extends vscode.TreeItem {
  readonly kind = 'message' as const;
  constructor(label: string, icon = 'info') {
    super(label, vscode.TreeItemCollapsibleState.None);
    this.iconPath = new vscode.ThemeIcon(icon);
    this.contextValue = 'eidetic.message';
  }
}

export class RecentEngramsProvider implements vscode.TreeDataProvider<Node>, vscode.Disposable {
  private readonly _onDidChangeTreeData = new vscode.EventEmitter<Node | undefined>();
  readonly onDidChangeTreeData = this._onDidChangeTreeData.event;

  private surfaces: SurfacesResponse = {};
  private recent: Engram[] = [];
  private errorMsg: string | undefined;
  private timer: NodeJS.Timeout | undefined;
  private aborter: AbortController | undefined;

  constructor(
    private client: DaemonClient,
    private getSurfaceFilter: () => string,
    private getPollMs: () => number
  ) {
    void this.refresh();
    this.scheduleNext();
  }

  setClient(client: DaemonClient): void {
    this.client = client;
    void this.refresh();
  }

  refresh(): Promise<void> {
    this.aborter?.abort();
    const aborter = new AbortController();
    this.aborter = aborter;

    return Promise.all([this.client.surfaces(), this.client.recent(50, this.getSurfaceFilter() || undefined)])
      .then(([surfaces, recent]) => {
        if (aborter.signal.aborted) return;
        this.surfaces = surfaces;
        this.recent = recent;
        this.errorMsg = undefined;
        this._onDidChangeTreeData.fire(undefined);
      })
      .catch((err: unknown) => {
        if (aborter.signal.aborted) return;
        this.errorMsg = (err as Error).message;
        this._onDidChangeTreeData.fire(undefined);
      });
  }

  private scheduleNext(): void {
    const ms = Math.max(5000, this.getPollMs());
    this.timer = setTimeout(async () => {
      await this.refresh();
      if (this.timer !== undefined) this.scheduleNext(); // not disposed
    }, ms);
  }

  dispose(): void {
    if (this.timer) {
      clearTimeout(this.timer);
      this.timer = undefined;
    }
    this.aborter?.abort();
    this._onDidChangeTreeData.dispose();
  }

  getTreeItem(el: Node): vscode.TreeItem {
    return el;
  }

  getChildren(el?: Node): Node[] {
    if (el === undefined) {
      if (this.errorMsg) {
        return [new MessageNode(`daemon unreachable: ${this.errorMsg}`, 'error')];
      }
      const surfaceNames = Object.keys(this.surfaces).sort();
      if (surfaceNames.length === 0) {
        return [new MessageNode('No engrams yet. Start a Claude Code session.', 'info')];
      }
      // Group recent engrams by surface so each surface node lists its own.
      return surfaceNames.map((name) => {
        const subset = this.recent.filter((e) => e.surface === name).slice(0, 20);
        return new SurfaceNode(name, this.surfaces[name] ?? 0, subset);
      });
    }
    if (el.kind === 'surface') {
      return el.engrams.length === 0
        ? [new MessageNode('(no recent engrams in window)', 'info')]
        : el.engrams.map((e) => new EngramNode(e));
    }
    return [];
  }
}
