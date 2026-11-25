// types/ipc.ts — the IPC contract between main, preload and renderer: every channel
// name with its request/response types, and the bridge the preload exposes.
// Type declarations only (same rule as ./index.ts: the renderer build compiles it too).
// Main registers handlers through main/ipc.ts and the preload calls through its own
// typed helpers: both are keyed on these maps, so a misspelled channel, a wrong
// argument or a wrong result type fails the compilation.

import type {
  AccountId,
  AppSettings,
  ProviderId,
  UpdateSettings,
  UsageSnapshot,
  WindowStyle,
  TrayViewState,
  UsageSourceStatus,
  UsageSourceReason,
} from './index';

export interface CopilotOAuthPayload {
  clientId: string;
  clientSecret: string;
  host: string;
}

// Name of the error accounts:connectCopilotOAuth fails with when another sign-in runs
// (`login_in_progress`): IPC keeps only the error's text, so Settings finds it by name
// and offers to cancel (#338).
export type CopilotLoginInProgressErrorName = 'CopilotLoginInProgressError';

// With the source of each enabled account's last usage read (#182): the service or the CLI.
export type CoreStatus = ({ ok: true; version: string } | { ok: false; error: string }) & { sources: UsageSourceStatus[] };

export interface ServiceStatus {
  running: boolean;
}

// Settings window (#337): the accounts and core settings come from the core's service;
// without a usable one they are shown read-only with the reason (owner decision on #181).
export type CoreSettingsSource = { available: true } | { available: false; reason: UsageSourceReason };

export interface SettingsLoad {
  settings: AppSettings;
  service: CoreSettingsSource;
  // Electron's own store still holds the accounts of v0.6.x: the dismissable notice
  // linking docs/UPGRADE-TO-0.7 (migration script #311, decision on #310).
  upgradeNotice: boolean;
}

/** The core's part of the Settings form, written through the service. */
export interface CoreSettingsPatch {
  accounts: AppSettings['accounts'];
  refreshIntervalMinutes: number;
  notificationThresholdPercent: number;
}

/**
 * A field the service refused, by its JSON path: `field` names the Settings input
 * (`path` under the account with that id, or a global path), null when no input matches.
 */
export interface CoreSettingsFieldError {
  field: { accountId: string | null; path: string } | null;
  message: string;
}

export type CoreSettingsSaveResult = { ok: true; settings: AppSettings } | ({ ok: false } & CoreSettingsFieldError);

// A handler with nothing to return (`void` is only allowed as a generic argument).
type NoResult = Awaited<Promise<void>>;

/** renderer → main, `ipcRenderer.invoke` / `ipcMain.handle`: arguments and result. */
export interface IpcInvokeChannels {
  'view:get': { args: []; result: TrayViewState };
  'settings:get': { args: []; result: AppSettings };
  'settings:set': { args: [patch: Partial<AppSettings>]; result: AppSettings };
  // Settings window (#337): accounts and core settings read from and written to the service.
  'settings:load': { args: []; result: SettingsLoad };
  'settings:saveCore': { args: [patch: CoreSettingsPatch]; result: CoreSettingsSaveResult };
  'settings:dismissUpgradeNotice': { args: []; result: NoResult };
  // Opens docs/UPGRADE-TO-0.7 in the browser; the URL is fixed in main.
  'settings:openUpgradeGuide': { args: []; result: NoResult };
  'window:setAlwaysOnTop': { args: [value: boolean]; result: boolean };
  'window:setCompact': { args: [value: boolean]; result: boolean };
  'window:setStyle': { args: [style: WindowStyle]; result: WindowStyle };
  'accounts:add': { args: [provider: ProviderId]; result: AccountId };
  'accounts:remove': { args: [id: AccountId]; result: NoResult };
  'accounts:connectCopilot': { args: [id: AccountId, token: string, host: string]; result: { username: string } };
  'accounts:connectCopilotOAuth': { args: [id: AccountId, payload: CopilotOAuthPayload]; result: { username: string } };
  // Cancels the account's running OAuth sign-in in the core's service (#338).
  'accounts:cancelCopilotOAuth': { args: [id: AccountId]; result: NoResult };
  'accounts:disconnect': { args: [id: AccountId]; result: NoResult };
  // After a confirmation, saves the report file in Downloads and opens a GitHub issue
  // draft; `created` false when the user cancelled.
  'diagnostics:createReport': { args: []; result: { created: boolean; fileName: string | null } };
  'app:getVersion': { args: []; result: string };
  // The bundled Go core (#177): its version, or why it cannot run (localized).
  'core:getStatus': { args: []; result: CoreStatus };
  // Whether the core's always-on service runs: the Copilot account actions need it (#338).
  'service:getStatus': { args: []; result: ServiceStatus };
  'updates:check': { args: []; result: UpdateSettings };
  'updates:download': { args: []; result: NoResult };
  'updates:openReleaseNotes': { args: []; result: NoResult };
  // Opens the user guide (README on GitHub) in the browser; the URL is fixed in main.
  'app:openGuide': { args: []; result: NoResult };
}

/** renderer → main, fire and forget (`ipcRenderer.send` / `ipcMain.on`): arguments. */
export interface IpcSendChannels {
  'usage:refreshRequest': [];
  'window:openSettings': [];
  'window:minimize': [];
  'window:close': [];
}

/** main → renderer (`webContents.send` / `ipcRenderer.on`): arguments of the event. */
export interface IpcEventChannels {
  'usage:refreshing': [refreshing: boolean];
  'usage:update': [snapshot: UsageSnapshot];
  'view:update': [state: TrayViewState];
  'settings:update': [settings: AppSettings];
  'window:hoverChanged': [isHovering: boolean];
}

/** API exposed by the preload to the renderer (`window.aipermiler`). */
export interface AipermilerBridge {
  getView(): Promise<TrayViewState>;
  onViewUpdate(callback: (state: TrayViewState) => void): () => void;
  getSettings(): Promise<AppSettings>;
  setSettings(patch: Partial<AppSettings>): Promise<AppSettings>;
  loadSettings(): Promise<SettingsLoad>;
  saveCoreSettings(patch: CoreSettingsPatch): Promise<CoreSettingsSaveResult>;
  dismissUpgradeNotice(): Promise<void>;
  openUpgradeGuide(): Promise<void>;
  onUsageUpdate(callback: (snapshot: UsageSnapshot) => void): () => void;
  onUsageRefreshing(callback: (refreshing: boolean) => void): () => void;
  onSettingsUpdate(callback: (settings: AppSettings) => void): () => void;
  onWindowHoverChanged(callback: (isHovering: boolean) => void): () => void;
  requestUsageRefresh(): void;
  openSettingsWindow(): void;
  setAlwaysOnTop(value: boolean): Promise<boolean>;
  setCompact(value: boolean): Promise<boolean>;
  setWindowStyle(style: WindowStyle): Promise<WindowStyle>;
  minimizeWindow(): void;
  closeWindow(): void;
  addAccount(provider: ProviderId): Promise<AccountId>;
  removeAccount(id: AccountId): Promise<void>;
  connectCopilot(id: AccountId, token: string, host: string): Promise<{ username: string }>;
  connectCopilotOAuth(id: AccountId, clientId: string, clientSecret: string, host: string): Promise<{ username: string }>;
  cancelCopilotOAuth(id: AccountId): Promise<void>;
  disconnectAccount(id: AccountId): Promise<void>;
  createDiagnosticReport(): Promise<{ created: boolean; fileName: string | null }>;
  getAppVersion(): Promise<string>;
  getCoreStatus(): Promise<CoreStatus>;
  getServiceStatus(): Promise<ServiceStatus>;
  checkForUpdates(): Promise<UpdateSettings>;
  downloadUpdate(): Promise<void>;
  openReleaseNotes(): Promise<void>;
  openGuide(): Promise<void>;
}
