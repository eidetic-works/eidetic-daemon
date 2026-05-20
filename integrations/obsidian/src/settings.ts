// settings.ts — settings schema + PluginSettingTab for eidetic-obsidian.

import { App, PluginSettingTab, Setting } from 'obsidian';
import type EideticPlugin from './main';

export interface EideticSettings {
  /** Daemon URL — either `uds:/tmp/eidetic-daemon.sock` or `http://127.0.0.1:9876`. */
  daemonUrl: string;
  /** Optional Bearer token for daemon auth. */
  bearerToken: string;
  /** When true, modified notes are captured (debounced) to /engrams. */
  captureOnModify: boolean;
  /** Debounce interval (ms) for capture-on-modify. */
  debounceMs: number;
  /** Health-check poll interval (ms) for the status-bar indicator. */
  healthPollMs: number;
  /** Request timeout in ms. */
  timeoutMs: number;
}

export const DEFAULT_SETTINGS: EideticSettings = {
  daemonUrl: 'uds:/tmp/eidetic-daemon.sock',
  bearerToken: '',
  captureOnModify: true,
  debounceMs: 2500,
  healthPollMs: 60000,
  timeoutMs: 5000
};

export class EideticSettingTab extends PluginSettingTab {
  private plugin: EideticPlugin;

  constructor(app: App, plugin: EideticPlugin) {
    super(app, plugin);
    this.plugin = plugin;
  }

  display(): void {
    const { containerEl } = this;
    containerEl.empty();

    containerEl.createEl('h2', { text: 'Eidetic Engrams' });
    containerEl.createEl('p', {
      text: 'Capture vault notes to eidetic-daemon and recall engrams without leaving Obsidian.',
      cls: 'eidetic-settings-blurb'
    });

    new Setting(containerEl)
      .setName('Daemon URL')
      .setDesc(
        'Where eidetic-daemon listens. Use `uds:/tmp/eidetic-daemon.sock` for the local socket on macOS/Linux, ' +
          'or `http://127.0.0.1:9876` for the TCP bridge (required on Windows and mobile).'
      )
      .addText((text) =>
        text
          .setPlaceholder('uds:/tmp/eidetic-daemon.sock')
          .setValue(this.plugin.settings.daemonUrl)
          .onChange(async (value) => {
            this.plugin.settings.daemonUrl = value.trim();
            await this.plugin.saveSettings();
          })
      );

    new Setting(containerEl)
      .setName('Bearer token')
      .setDesc('Optional. Required when the daemon is exposed over a Cloudflare tunnel.')
      .addText((text) =>
        text
          .setPlaceholder('paste token, or leave empty for local UDS')
          .setValue(this.plugin.settings.bearerToken)
          .onChange(async (value) => {
            this.plugin.settings.bearerToken = value.trim();
            await this.plugin.saveSettings();
          })
      );

    new Setting(containerEl)
      .setName('Capture on save')
      .setDesc(
        'Send modified notes to the daemon as engrams (debounced). When disabled, only the manual Recall command runs.'
      )
      .addToggle((toggle) =>
        toggle.setValue(this.plugin.settings.captureOnModify).onChange(async (value) => {
          this.plugin.settings.captureOnModify = value;
          await this.plugin.saveSettings();
        })
      );

    new Setting(containerEl)
      .setName('Capture debounce (ms)')
      .setDesc('How long to wait after the last edit before sending the engram. Avoids per-keystroke noise.')
      .addText((text) =>
        text
          .setPlaceholder('2500')
          .setValue(String(this.plugin.settings.debounceMs))
          .onChange(async (value) => {
            const n = Number(value);
            if (Number.isFinite(n) && n >= 250) {
              this.plugin.settings.debounceMs = Math.floor(n);
              await this.plugin.saveSettings();
            }
          })
      );

    new Setting(containerEl)
      .setName('Health poll (ms)')
      .setDesc('How often the status bar pings /healthz. Minimum 5 000 ms.')
      .addText((text) =>
        text
          .setPlaceholder('60000')
          .setValue(String(this.plugin.settings.healthPollMs))
          .onChange(async (value) => {
            const n = Number(value);
            if (Number.isFinite(n) && n >= 5000) {
              this.plugin.settings.healthPollMs = Math.floor(n);
              await this.plugin.saveSettings();
            }
          })
      );

    new Setting(containerEl)
      .setName('Request timeout (ms)')
      .setDesc('Per-request HTTP timeout.')
      .addText((text) =>
        text
          .setPlaceholder('5000')
          .setValue(String(this.plugin.settings.timeoutMs))
          .onChange(async (value) => {
            const n = Number(value);
            if (Number.isFinite(n) && n >= 250) {
              this.plugin.settings.timeoutMs = Math.floor(n);
              await this.plugin.saveSettings();
            }
          })
      );

    containerEl.createEl('p', {
      text:
        'Privacy: this plugin only contacts the URL above. No analytics, no third-party endpoints. ' +
        'Your notes never leave your machine unless you explicitly set the URL to a remote bridge.',
      cls: 'eidetic-settings-footer'
    });
  }
}
