// main.ts — Eidetic Engrams plugin for Obsidian.
//
// Three features wired in onload():
//   1. Capture-on-save  — debounced /engrams POST when a note is modified.
//   2. Recall command   — palette command "Eidetic: Recall" → /ask → insert at cursor.
//   3. Status-bar dot   — polls /healthz; green = reachable, red = unreachable.

import {
  App,
  Editor,
  type MarkdownFileInfo,
  MarkdownView,
  Modal,
  Notice,
  Plugin,
  TAbstractFile,
  TFile
} from 'obsidian';
import { DaemonClient, formatEngramTs } from './daemonClient';
import type { Engram } from './daemonClient';
import { DEFAULT_SETTINGS, EideticSettings, EideticSettingTab } from './settings';

export default class EideticPlugin extends Plugin {
  settings!: EideticSettings;
  private client!: DaemonClient;
  private statusBarEl?: HTMLElement;
  private healthInterval?: number;
  /** file-path → pending timer id. */
  private debounceTimers = new Map<string, number>();
  /** Have we already nagged the user that the daemon URL is unusable? */
  private warnedUnreachable = false;

  async onload(): Promise<void> {
    await this.loadSettings();

    this.client = new DaemonClient({
      url: this.settings.daemonUrl,
      token: this.settings.bearerToken || undefined,
      timeoutMs: this.settings.timeoutMs
    });

    // ── feature 1 — capture-on-save ──────────────────────────────────────
    this.registerEvent(
      this.app.vault.on('modify', (file: TAbstractFile) => {
        if (!(file instanceof TFile)) return;
        if (file.extension !== 'md') return;
        if (!this.settings.captureOnModify) return;
        this.queueCapture(file);
      })
    );

    // ── feature 2 — recall command ──────────────────────────────────────
    this.addCommand({
      id: 'eidetic-recall',
      name: 'Recall (ask eidetic-daemon)',
      editorCallback: (editor: Editor, _ctx: MarkdownView | MarkdownFileInfo) => {
        new RecallModal(this.app, this, async (question, modal) => {
          await this.runRecall(question, editor, modal);
        }).open();
      }
    });

    // ── feature 3 — status bar health indicator ─────────────────────────
    this.statusBarEl = this.addStatusBarItem();
    this.statusBarEl.addClass('eidetic-status');
    this.setStatusUnknown();
    this.statusBarEl.addEventListener('click', () => this.pollHealth(true));

    // start polling
    this.pollHealth(false);
    this.healthInterval = window.setInterval(
      () => this.pollHealth(false),
      Math.max(5000, this.settings.healthPollMs)
    );
    this.registerInterval(this.healthInterval);

    // settings tab
    this.addSettingTab(new EideticSettingTab(this.app, this));

    // mobile/UDS one-time warning
    if (!this.client.isReachableFromHere() && !this.warnedUnreachable) {
      this.warnedUnreachable = true;
      new Notice(
        'Eidetic: daemon URL not usable on this device. Set an http:// URL in Settings → Eidetic Engrams.',
        8000
      );
    }
  }

  onunload(): void {
    for (const t of this.debounceTimers.values()) window.clearTimeout(t);
    this.debounceTimers.clear();
    if (this.healthInterval !== undefined) {
      window.clearInterval(this.healthInterval);
      this.healthInterval = undefined;
    }
  }

  // ── settings persistence ──────────────────────────────────────────────

  async loadSettings(): Promise<void> {
    this.settings = Object.assign({}, DEFAULT_SETTINGS, await this.loadData());
  }

  async saveSettings(): Promise<void> {
    await this.saveData(this.settings);
    // rewire the client so URL/token changes take effect immediately
    this.client.update({
      url: this.settings.daemonUrl,
      token: this.settings.bearerToken || undefined,
      timeoutMs: this.settings.timeoutMs
    });
    // allow a fresh one-time warning if the user changed URL
    this.warnedUnreachable = false;
  }

  // ── feature 1 helpers ─────────────────────────────────────────────────

  private queueCapture(file: TFile): void {
    if (!this.client.isReachableFromHere()) return;
    const existing = this.debounceTimers.get(file.path);
    if (existing !== undefined) window.clearTimeout(existing);
    const timer = window.setTimeout(() => {
      this.debounceTimers.delete(file.path);
      void this.captureNote(file);
    }, Math.max(250, this.settings.debounceMs));
    this.debounceTimers.set(file.path, timer);
  }

  private async captureNote(file: TFile): Promise<void> {
    try {
      const body = await this.app.vault.cachedRead(file);
      if (!body || body.trim().length === 0) return;
      await this.client.capture({
        surface: 'obsidian',
        payload: body,
        meta: {
          vault_name: this.app.vault.getName(),
          file_path: file.path,
          basename: file.basename
        }
      });
    } catch (err) {
      // Silent in the happy path; one-shot Notice in the error path so the
      // user knows captures are dropping but we don't spam them.
      console.warn('[eidetic] capture failed', err);
      if (!this.warnedUnreachable) {
        this.warnedUnreachable = true;
        new Notice(`Eidetic: capture failed (${(err as Error).message}). Disabled until you fix the URL.`, 6000);
      }
    }
  }

  // ── feature 2 helpers ─────────────────────────────────────────────────

  private async runRecall(question: string, editor: Editor, modal: RecallModal): Promise<void> {
    if (!this.client.isReachableFromHere()) {
      new Notice('Eidetic: daemon not reachable. Configure URL in Settings.');
      return;
    }
    try {
      modal.setBusy(true);
      const res = await this.client.ask(question, 10);
      const block = renderRecallBlock(question, res.instructions, res.engrams);
      editor.replaceSelection(block);
      modal.close();
      new Notice(`Eidetic: inserted ${res.engrams.length} engram(s).`);
    } catch (err) {
      modal.setBusy(false);
      new Notice(`Eidetic: recall failed — ${(err as Error).message}`, 6000);
    }
  }

  // ── feature 3 helpers ─────────────────────────────────────────────────

  private async pollHealth(showNotice: boolean): Promise<void> {
    if (!this.statusBarEl) return;
    if (!this.client.isReachableFromHere()) {
      this.setStatusDown('not configured on this device');
      if (showNotice) new Notice('Eidetic: daemon URL not usable on this device.');
      return;
    }
    try {
      const h = await this.client.healthz();
      this.setStatusUp(h.status || 'ok');
      if (showNotice) new Notice('Eidetic: daemon reachable.');
    } catch (err) {
      this.setStatusDown((err as Error).message);
      if (showNotice) new Notice(`Eidetic: daemon unreachable — ${(err as Error).message}`, 6000);
    }
  }

  private setStatusUnknown(): void {
    if (!this.statusBarEl) return;
    this.statusBarEl.empty();
    this.statusBarEl.createSpan({ cls: 'eidetic-dot eidetic-dot-unknown', text: '●' });
    this.statusBarEl.createSpan({ cls: 'eidetic-label', text: ' eidetic' });
    this.statusBarEl.setAttr('aria-label', 'Eidetic daemon: checking…');
  }

  private setStatusUp(status: string): void {
    if (!this.statusBarEl) return;
    this.statusBarEl.empty();
    this.statusBarEl.createSpan({ cls: 'eidetic-dot eidetic-dot-up', text: '●' });
    this.statusBarEl.createSpan({ cls: 'eidetic-label', text: ' eidetic' });
    this.statusBarEl.setAttr('aria-label', `Eidetic daemon: ${status}`);
  }

  private setStatusDown(reason: string): void {
    if (!this.statusBarEl) return;
    this.statusBarEl.empty();
    this.statusBarEl.createSpan({ cls: 'eidetic-dot eidetic-dot-down', text: '●' });
    this.statusBarEl.createSpan({ cls: 'eidetic-label', text: ' eidetic' });
    this.statusBarEl.setAttr('aria-label', `Eidetic daemon down: ${reason}`);
  }
}

// ── recall modal ────────────────────────────────────────────────────────

class RecallModal extends Modal {
  private question = '';
  private onSubmit: (question: string, modal: RecallModal) => Promise<void>;
  private input!: HTMLInputElement;
  private submitBtn!: HTMLButtonElement;

  constructor(app: App, _plugin: EideticPlugin, onSubmit: (question: string, modal: RecallModal) => Promise<void>) {
    super(app);
    this.onSubmit = onSubmit;
  }

  onOpen(): void {
    const { contentEl } = this;
    contentEl.empty();
    contentEl.addClass('eidetic-recall-modal');
    contentEl.createEl('h3', { text: 'Eidetic: Recall' });
    contentEl.createEl('p', {
      text: 'Ask a question. Matching engrams are inserted as a markdown block at the cursor.',
      cls: 'eidetic-recall-blurb'
    });

    this.input = contentEl.createEl('input', {
      type: 'text',
      placeholder: 'e.g. what was that postgres trick from yesterday?',
      cls: 'eidetic-recall-input'
    });
    this.input.addEventListener('input', () => {
      this.question = this.input.value;
    });
    this.input.addEventListener('keydown', (e) => {
      if (e.key === 'Enter' && this.question.trim().length > 0) {
        e.preventDefault();
        void this.onSubmit(this.question.trim(), this);
      }
    });

    const buttonRow = contentEl.createDiv({ cls: 'eidetic-recall-buttons' });
    this.submitBtn = buttonRow.createEl('button', { text: 'Recall', cls: 'mod-cta' });
    this.submitBtn.addEventListener('click', () => {
      if (this.question.trim().length === 0) return;
      void this.onSubmit(this.question.trim(), this);
    });
    const cancelBtn = buttonRow.createEl('button', { text: 'Cancel' });
    cancelBtn.addEventListener('click', () => this.close());

    setTimeout(() => this.input.focus(), 50);
  }

  setBusy(busy: boolean): void {
    if (!this.submitBtn) return;
    this.submitBtn.disabled = busy;
    this.submitBtn.textContent = busy ? 'Recalling…' : 'Recall';
  }

  onClose(): void {
    this.contentEl.empty();
  }
}

// ── markdown rendering ──────────────────────────────────────────────────

function renderRecallBlock(question: string, instructions: string, engrams: Engram[]): string {
  const lines: string[] = [];
  lines.push('');
  lines.push(`> [!info] Eidetic recall — ${question}`);
  if (instructions && instructions.trim().length > 0) {
    for (const ln of instructions.split('\n')) lines.push(`> ${ln}`);
    lines.push('>');
  }
  if (engrams.length === 0) {
    lines.push('> _no matching engrams_');
  } else {
    for (const e of engrams) {
      const ts = formatEngramTs(e.ts);
      const snippet = (e.snippet ?? e.payload ?? '').replace(/\s+/g, ' ').trim().slice(0, 240);
      lines.push(`> - **${e.surface}** [#${e.id}] _${ts}_ — ${snippet}`);
    }
  }
  lines.push('');
  return lines.join('\n');
}
