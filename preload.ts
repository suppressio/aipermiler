// preload.ts — secure renderer <-> main bridge via contextBridge
// nodeIntegration: false and contextIsolation: true are mandatory (see AGENTS.md).
// Exposes only the needed IPC channels, no direct Node/Electron access in the renderer.
// Channel names and payload types come from the shared contract (types/ipc.ts).

import { contextBridge, ipcRenderer } from 'electron';
import type { AipermilerBridge, IpcEventChannels, IpcInvokeChannels, IpcSendChannels } from './types/ipc';

function invoke<C extends keyof IpcInvokeChannels>(channel: C, ...args: IpcInvokeChannels[C]['args']): Promise<IpcInvokeChannels[C]['result']> {
  return ipcRenderer.invoke(channel, ...args) as Promise<IpcInvokeChannels[C]['result']>;
}

function send<C extends keyof IpcSendChannels>(channel: C, ...args: IpcSendChannels[C]): void {
  ipcRenderer.send(channel, ...args);
}

function subscribe<C extends keyof IpcEventChannels>(channel: C, callback: (...args: IpcEventChannels[C]) => void): () => void {
  const listener = (_event: unknown, ...args: unknown[]) => { callback(...(args as IpcEventChannels[C])); };
  ipcRenderer.on(channel, listener);
  return () => ipcRenderer.removeListener(channel, listener);
}

const bridge: AipermilerBridge = {
  getView: () => invoke('view:get'),
  onViewUpdate: (callback) => subscribe('view:update', callback),
  getSettings: () => invoke('settings:get'),
  setSettings: (patch) => invoke('settings:set', patch),

  onUsageUpdate: (callback) => subscribe('usage:update', callback),
  onUsageRefreshing: (callback) => subscribe('usage:refreshing', callback),
  // Notifies the widget when Settings change (e.g. accent color, language) while it
  // is open: without it, a field with no dedicated IPC (unlike
  // ui.windowStyle/ui.alwaysOnTop) applied only after the next restart.
  onSettingsUpdate: (callback) => subscribe('settings:update', callback),
  // Window hover state computed in the main process (screen.getCursorScreenPoint), not
  // from DOM mouse events: see main.ts (startWindowHoverPolling) for why.
  onWindowHoverChanged: (callback) => subscribe('window:hoverChanged', callback),
  requestUsageRefresh: () => { send('usage:refreshRequest'); },

  openSettingsWindow: () => { send('window:openSettings'); },
  setAlwaysOnTop: (value) => invoke('window:setAlwaysOnTop', value),
  setCompact: (value) => invoke('window:setCompact', value),
  setWindowStyle: (style) => invoke('window:setStyle', style),

  minimizeWindow: () => { send('window:minimize'); },
  closeWindow: () => { send('window:close'); },

  // Account registry (issue #4): every operation is per account id, not per provider.
  addAccount: (provider) => invoke('accounts:add', provider),
  removeAccount: (id) => invoke('accounts:remove', id),
  connectCopilot: (id, token, host) => invoke('accounts:connectCopilot', id, token, host),
  connectCopilotOAuth: (id, clientId, clientSecret, host) => invoke('accounts:connectCopilotOAuth', id, { clientId, clientSecret, host }),
  disconnectAccount: (id) => invoke('accounts:disconnect', id),
  // Diagnostic report: after a confirmation, saves the report file in Downloads and opens
  // a GitHub issue draft; `created` false when the user cancelled.
  createDiagnosticReport: () => invoke('diagnostics:createReport'),

  // Updates (issue #5): the URL to open is decided by the main process, never by the renderer.
  getAppVersion: () => invoke('app:getVersion'),
  getCoreStatus: () => invoke('core:getStatus'),
  checkForUpdates: () => invoke('updates:check'),
  downloadUpdate: () => invoke('updates:download'),
  openReleaseNotes: () => invoke('updates:openReleaseNotes'),
  // Opens the user guide (README on GitHub) in the browser; the URL is fixed in main.
  openGuide: () => invoke('app:openGuide'),
};

contextBridge.exposeInMainWorld('aipermiler', bridge);
