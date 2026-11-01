// main.ts — Electron main process
// Usage comes from the Go core CLI (main/core.ts) for Codex and Copilot (#178, #179).
// When a fetch fails, the last known data is shown with its timestamp (never a blank
// screen, see AGENTS.md).

// First import, before the store: moves the store of the old app name (#200).
import { userDataMigration } from './main/userDataBootstrap';
import { app, dialog, BrowserWindow, Notification, shell, Menu, screen, session, utilityProcess } from 'electron';
import store, { DEFAULTS } from './store/index';
import { normalizeSettings } from './store/normalize';
import { isPlainRecord } from './store/merge';
import { applyCompact, createMainWindow, createSettingsWindow } from './main/windows';
import { createTray } from './main/tray';
import { badgeEntries, pickBadge, resolveLegacyView, sameChoice, trackAutoAccount } from './main/trayBadge';
import { clearClaudePartition, clearDefaultSessionClaudeCookies } from './main/claude-auth';
import * as providers from './main/providers';
import * as refreshPolicy from './main/refreshPolicy';
import type { RefreshMode } from './main/refreshPolicy';
import { defaultAccountFor, enforceSingleCodexAccount, enforceSingleLocalInsights, nextAccountLabel, normalizeAccounts, purgeClaudeSessions } from './store/migrate';
import * as budget from './budget';
import { createUsageCore, refreshIntervalMs } from './core/usage';
import path from 'path';
import { fetchLatestUpdate, guideUrl, TRUSTED_DOWNLOAD_PREFIX } from './services/updates';
import { DEFAULT_GITHUB_HOST, githubOAuthBase, normalizeGithubHost } from './services/githubHost';
import type { TrayHandle } from './main/tray';
import { FormatDriftError, shapeSignature } from './services/_shape';
import { CoreBinaryError, CoreOutputError, CoreProviderError, CoreTimeoutError } from './main/coreClient';
import { core } from './main/core';
import { copilotAccountFlags } from './main/coreRequests';
import { buildFormatDriftIssueUrl, buildReportIssueUrl, shortError } from './diagnostics/githubIssue';
import { buildDiagnosticReport, neutralize } from './diagnostics/report';
import type { ReportAccount } from './diagnostics/report';
import { LogBuffer, captureConsole } from './diagnostics/logBuffer';
import os from 'os';
import { writeFile } from 'fs/promises';
import { formatNumber, getLocale, resolveLocale, setLocale, t } from './main/i18n/index';
import { randomUUID } from 'crypto';
import { handle, listen, sendToRenderer } from './main/ipc';
import type {
  AccountConfig,
  AccountSnapshot,
  TrayBadgeChoice,
  TrayViewState,
  AccountId,
  AppSettings,
  ClaudeLocalInsights,
  ProviderId,
  UpdateSettings,
  UsageSnapshot,
  WindowStyle,
} from './types/index';

// New-version check (issue #5): at startup (with a short delay, not to overlap
// the first usage refresh) and then every 24 hours.
const UPDATE_CHECK_INTERVAL_MS = 24 * 60 * 60 * 1000;
const UPDATE_FIRST_CHECK_DELAY_MS = 10 * 1000;
// Local insights recomputation (services/claudeLocalSessions.ts): more expensive
// than a regular refresh (scanning files on disk, not a network poll), no need to do
// it on every 30-minute refresh — cached with this minimum interval, same pattern as
// advisorCache.
const LOCAL_INSIGHTS_RECOMPUTE_INTERVAL_MS = 2 * 60 * 60 * 1000; // 2 hours

// Promises launched "in the background" (timers, fire-and-forget IPC, after an IPC
// reply was already sent): never an unhandled rejection, which Node would log without
// context or, worse, would leave the app half-way with no trace (AGENTS.md: never
// silent failures).
function runDetached(label: string, task: Promise<unknown>): void {
  task.catch((err: unknown) => {
    console.error(`[main] ${label} failed:`, err);
  });
}

let mainWindow: BrowserWindow | null = null;
let settingsWindow: BrowserWindow | null = null;
// Last snapshot sent to the widget: the diagnostic report shows how the app read the data.
let lastSnapshot: UsageSnapshot | null = null;

// Last main-process errors/warnings, in memory only, for the diagnostic report: an
// installed app has no visible console (diagnostics/logBuffer.ts).
const logBuffer = new LogBuffer(50);
captureConsole(logBuffer);
for (const warning of userDataMigration.warnings) console.warn(`[main] ${warning}`);
if (userDataMigration.migrated) console.log('[main] settings moved from the folder of the old app name');


// Durations of the last refresh and of the last local insights scan, for the
// diagnostic report (a slow startup had to be inferred before).
const timings: { lastRefresh: { at: string; ms: number } | null; lastLocalInsights: { at: string; ms: number } | null } = {
  lastRefresh: null,
  lastLocalInsights: null,
};
let refreshTimer: NodeJS.Timeout | null = null;
// Whether the last refresh that fetched something saw consumption rise (fast pace).
let lastConsumptionRose = false;
let hoverPollTimer: NodeJS.Timeout | null = null;
let updateTimer: NodeJS.Timeout | null = null;
let trayHandle: TrayHandle | null = null;
let lastHoverState = false;

// ---------------------------------------------------------------------------
// Window hover detection for the "auto-hiding" title bar/buttons (user feedback):
// a pure CSS :hover, and even mouseover/mouseout on the document, do not fire
// reliably over the -webkit-app-region:drag strip, because the OS treats it as a
// non-client area (like a native title bar) and captures the mouse for dragging
// instead of dispatching regular DOM events — with that technique the bar vanished
// exactly while hovering it. The reliable fix queries the cursor position in the main
// process (always available via screen.getCursorScreenPoint(), independent of the
// renderer's event dispatch), compares it with the window bounds and sends only state
// changes to the renderer via IPC.
// ---------------------------------------------------------------------------
function startWindowHoverPolling(): void {
  if (hoverPollTimer) clearInterval(hoverPollTimer);
  hoverPollTimer = setInterval(() => {
    if (!mainWindow || mainWindow.isDestroyed()) return;
    const cursor = screen.getCursorScreenPoint();
    const bounds = mainWindow.getBounds();
    const isOver =
      cursor.x >= bounds.x && cursor.x < bounds.x + bounds.width &&
      cursor.y >= bounds.y && cursor.y < bounds.y + bounds.height;
    if (isOver !== lastHoverState) {
      lastHoverState = isOver;
      sendToRenderer(mainWindow, 'window:hoverChanged', isOver);
    }
  }, 120);
}

function configuredRefreshIntervalMs(): number {
  return refreshIntervalMs(store.get('ui'));
}

// One timer, re-armed after every refresh (main/refreshPolicy.ts): the configured
// interval, or the fast pace while consumption rises.
function scheduleNextRefresh(delayMs: number): void {
  if (refreshTimer) clearTimeout(refreshTimer);
  refreshTimer = setTimeout(() => { runDetached('refresh usage', refreshAndBroadcast('scheduled')); }, delayMs);
}

// Startup, a settings change (new interval) or a failed refresh: the pace stays fast
// if the last refresh saw consumption rise.
function scheduleRefreshLoop(): void {
  scheduleNextRefresh(refreshPolicy.nextRefreshDelayMs(lastConsumptionRose, configuredRefreshIntervalMs()));
}

// ---------------------------------------------------------------------------
// Local insights from Claude Code sessions (services/claudeLocalSessions.ts, see
// docs/RESEARCH.md §5): opt-in per account (ClaudeAccountSettings.localInsights, at most
// one Claude account — the caller decides which), cached because more expensive than
// a regular refresh (scanning files on disk, not a network poll) — recomputed at most
// every LOCAL_INSIGHTS_RECOMPUTE_INTERVAL_MS.
// ---------------------------------------------------------------------------
// The scan runs in an Electron utility process (services/claudeLocalSessions.worker.ts):
// parsing the session files on the main process could freeze the app for seconds at
// startup with many or long sessions. A stale cache is shown meanwhile and the widget refreshed
// once the new result arrives; with no usable cache the refresh waits for it.
// Analysis window = the view chosen for the chart (7/30 days, +1 day as for the
// daily deltas): insights, yield and chart look at the same period.
const LOCAL_INSIGHTS_TIMEOUT_MS = 2 * 60 * 1000;
let localInsightsRunning: Promise<ClaudeLocalInsights | null> | null = null;

async function computeLocalInsightsIfNeeded(): Promise<ClaudeLocalInsights | null> {
  const windowDays = (store.get('ui').chartRange === 'month' ? 30 : 7) + 1;
  const cached = store.get('localInsightsCache').claudeCode;
  const cacheAgeMs = cached ? Date.now() - new Date(cached.computedAt).getTime() : Infinity;
  // Previous cache without `daily` (before point 4) or `firstSessionStartByDay`, or for
  // another window: recompute.
  const usable = cached !== null && Array.isArray(cached.daily) && cached.firstSessionStartByDay !== undefined
    && typeof cached.totalOutputTokens === 'number'
    && typeof cached.highContextOutputTokens === 'number'
    && typeof cached.longSessionOutputTokens === 'number'
    && cached.windowDays === windowDays;
  if (usable && cacheAgeMs < LOCAL_INSIGHTS_RECOMPUTE_INTERVAL_MS) return cached;

  const running = localInsightsRunning ?? startLocalInsights(windowDays, usable);
  return usable ? cached : running;
}

function startLocalInsights(windowDays: number, refreshWhenDone: boolean): Promise<ClaudeLocalInsights | null> {
  const cached = store.get('localInsightsCache').claudeCode;
  const startedAt = Date.now();
  localInsightsRunning = computeLocalInsightsInProcess(windowDays)
    .then((result) => {
      timings.lastLocalInsights = { at: new Date().toISOString(), ms: Date.now() - startedAt };
      store.set('localInsightsCache.claudeCode', result);
      // The refresh that started this used the stale cache: show the new insights.
      if (refreshWhenDone) runDetached('refresh usage', refreshAndBroadcast('manual'));
      return result;
    })
    .catch((err: unknown) => {
      // Never block the account refresh because of a problem with the optional local
      // source: log it and fall back to the last valid cache (even if expired), if any.
      console.error('[main] Claude Code local insights computation failed:', err instanceof Error ? err.message : String(err));
      return cached;
    })
    .finally(() => { localInsightsRunning = null; });
  return localInsightsRunning;
}

function computeLocalInsightsInProcess(windowDays: number): Promise<ClaudeLocalInsights | null> {
  return new Promise((resolve, reject) => {
    const child = utilityProcess.fork(path.join(__dirname, 'services', 'claudeLocalSessions.worker.js'), [], {
      serviceName: 'AIpermiler local insights',
    });
    let settled = false;
    const settle = (action: () => void): void => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      child.kill();
      action();
    };
    const timer = setTimeout(() => { settle(() => { reject(new Error('local insights: timed out')); }); }, LOCAL_INSIGHTS_TIMEOUT_MS);
    child.once('message', (message: unknown) => {
      settle(() => {
        if (isPlainRecord(message) && message.ok === true) resolve((message.result ?? null) as ClaudeLocalInsights | null);
        else reject(new Error(`local insights: ${isPlainRecord(message) && typeof message.message === 'string' ? message.message : 'invalid reply'}`));
      });
    });
    child.once('exit', (code) => { settle(() => { reject(new Error(`local insights: process exited (${String(code)})`)); }); });
    child.postMessage({ windowDays });
  });
}

// Always a valid array: the store is normalized at startup and on every write from
// IPC (store/normalize.ts). A copy, because callers modify it.
function getAccounts(): AccountConfig[] {
  return [...store.get('accounts')];
}

function findAccount(id: AccountId): AccountConfig | undefined {
  return getAccounts().find((a) => a.id === id);
}

function updateAccount(id: AccountId, update: (cfg: AccountConfig) => AccountConfig): AccountConfig {
  const accounts = getAccounts();
  const idx = accounts.findIndex((a) => a.id === id);
  const current = accounts[idx];
  if (!current) throw new Error(`Account not found: ${id}`);
  const updated = update(current);
  accounts[idx] = updated;
  store.set('accounts', accounts);
  return updated;
}

// 401/403 from a service (expired or revoked sessionKey/PAT) is the only case where
// we can give practical advice instead of the service's raw message — see AGENTS.md,
// progress log, session about the account_session_invalid error.
function friendlyErrorMessage(err: unknown): string {
  const error = err as Error & { status?: number };
  // Codex signed out is a state, not a failure: say where to sign in (#178).
  if (err instanceof CoreProviderError && err.code === 'signed_out' && err.provider === 'codex') {
    return t('error.codexSignedOut');
  }
  if (error.status === 401 || error.status === 403) {
    return t('error.sessionExpired', { detail: error.message });
  }
  if (err instanceof CoreProviderError && err.provider === 'copilot') {
    if (err.reason === 'enterprise_managed_seat') return t('error.copilotEnterpriseManaged');
    if (err.code === 'signed_out') return t('error.copilotSignedOut');
    if (err.reason === 'keyring_unavailable') return t('error.keyringUnavailable', { detail: err.message });
  }
  if (err instanceof providers.ClaudeUsageUnavailableError) {
    return t('error.claudeQuotasUnavailable');
  }
  if (err instanceof CoreBinaryError) return t('error.coreMissing', { detail: error.message });
  if (err instanceof CoreTimeoutError) return t('error.coreTimeout');
  if (err instanceof CoreOutputError) return t('error.coreBadOutput', { detail: error.message });
  return error.message;
}

// Main-process language (tray, notifications, dialogs): same rule as the
// renderer — ui.language, or the system language for 'auto'. Re-applied after
// every settings:set, which also rebuilds the tray menu.
function applyMainLocale(): void {
  setLocale(resolveLocale(store.get('ui').language, app.getLocale()));
  trayHandle?.refreshMenu();
  if (lastSnapshot) trayHandle?.updateUsage({ ...lastSnapshot, accounts: badgeAccounts() });
}

// ---------------------------------------------------------------------------
// Automatic "format drift" report (user feedback, Day 3): when a service detects that
// an endpoint format is no longer the expected one (FormatDriftError, see
// services/_shape.ts), we open a pre-filled GitHub issue draft in the browser — NEVER
// real values, only structure (field names/types) — that the user must review and
// submit by hand. Deduplicated by structure signature: the same draft is not reopened
// on every refresh (every 30 minutes).
// ---------------------------------------------------------------------------
function maybeReportFormatDrift(provider: ProviderId, err: unknown): void {
  if (!(err instanceof FormatDriftError)) return;
  const diagnostics = store.get('diagnostics');
  if (!diagnostics.autoReportFormatDrift) return;

  const signature = shapeSignature(err.shape);
  const reported = { ...diagnostics.reportedSignatures };
  if (reported[signature]) return; // already reported for this shape: do not reopen

  const url = buildFormatDriftIssueUrl({ provider, endpointLabel: err.endpointLabel, shape: err.shape });
  shell.openExternal(url).catch((openErr: unknown) => {
    console.error('[main] could not open the report draft in the browser:', openErr);
  });

  if (Notification.isSupported()) {
    new Notification({
      title: 'AIpermiler',
      body: t('notify.formatDrift', { provider: providers.providerDisplayName(provider) }),
    }).show();
  }

  reported[signature] = new Date().toISOString();
  store.set('diagnostics.reportedSignatures', reported);
}
// ---------------------------------------------------------------------------
// Diagnostic report sections (see diagnostics:createReport). Built WITHOUT account
// labels, ids, credentials or tenant names: only what affects pacing and parsing.
// ---------------------------------------------------------------------------
function reportConfig(cfg: AccountConfig): Record<string, unknown> {
  const common = {
    renewalRule: cfg.subscription.renewalRule,
    workSchedule: cfg.workSchedule,
  };
  switch (cfg.provider) {
    // Never the path itself (it can hold the user name): only whether one is set.
    case 'codex': return { ...common, binaryPath: cfg.binaryPath ? 'set' : 'auto-detect' };
    case 'claude':
      return { ...common, authMethod: cfg.authMethod, localInsights: cfg.localInsights };
    case 'copilot':
      return {
        ...common,
        authMethod: cfg.authMethod,
        accountScope: cfg.accountScope,
        host: cfg.host === DEFAULT_GITHUB_HOST ? DEFAULT_GITHUB_HOST : 'a .ghe.com tenant',
        // Read only by the personal-scope fallback: on a company seat it is not used.
        ...(cfg.accountScope === 'personal' ? { manualQuota: cfg.manualQuota } : {}),
      };
  }
}

// What helps diagnose this app on this machine, and nothing that identifies it: time
// zone (day boundaries), screens (widget layout and scaling), memory/CPU of the app's
// own processes, internal timings, core count and RAM rounded to GB. Never CPU/GPU
// models, computer or user names, paths, display or process ids.
function reportSystem(now: Date): Record<string, unknown> {
  const primary = screen.getPrimaryDisplay();
  return {
    timeZone: Intl.DateTimeFormat().resolvedOptions().timeZone,
    utcOffsetMinutes: -now.getTimezoneOffset(),
    cpuCores: os.cpus().length,
    totalMemoryGB: Math.round(os.totalmem() / 1024 ** 3),
    displays: screen.getAllDisplays().map((d) => ({
      primary: d.id === primary.id,
      size: { width: d.size.width, height: d.size.height },
      workArea: { width: d.workArea.width, height: d.workArea.height },
      scaleFactor: d.scaleFactor,
    })),
    widgetSize: { width: store.get('ui').bounds.width, height: store.get('ui').bounds.height },
    // workingSetSize is in KB.
    appProcesses: app.getAppMetrics().map((m) => ({
      type: m.type,
      ...(m.serviceName ? { service: m.serviceName } : {}),
      cpuPercent: Math.round(m.cpu.percentCPUUsage * 10) / 10,
      memoryMB: Math.round(m.memory.workingSetSize / 1024),
    })),
    uptimeMinutes: Math.round(process.uptime() / 60),
    lastRefresh: timings.lastRefresh,
    lastLocalInsights: timings.lastLocalInsights,
  };
}

// Last 7 days of daily points and today's samples, per window.
function reportHistory(accountId: AccountId, now: Date): unknown {
  const cutoff = new Date(now);
  cutoff.setDate(cutoff.getDate() - 7);
  const cutoffKey = budget.localDateKey(cutoff);
  const todayKey = budget.localDateKey(now);
  const history = store.get('history');
  return {
    daily: history.dailyUsage
      .filter((h) => h.accountId === accountId && h.date >= cutoffKey)
      .sort((a, b) => a.date.localeCompare(b.date))
      .map(({ date, windowId, used, dayStartUsed }) => ({ date, windowId, used, dayStartUsed })),
    samplesToday: history.recentSamples
      .filter((s) => s.accountId === accountId && budget.localDateKey(new Date(s.timestamp)) === todayKey)
      .map(({ timestamp, windowId, used }) => ({ timestamp, windowId, used })),
  };
}

// How the app read the account in the last refresh: windows and their metrics.
function reportInterpretation(accountId: AccountId, accounts: Pick<ReportAccount, 'name' | 'label' | 'id'>[]): unknown {
  const account = lastSnapshot?.accounts.find((a) => a.accountId === accountId);
  if (!account) return null;
  return {
    lastUpdatedAt: account.lastUpdatedAt ?? null,
    stale: account.stale ?? false,
    lastError: account.lastError ? neutralize(account.lastError, accounts) : null,
    notice: account.notice ?? null,
    criticalWindow: account.criticalWindow?.id ?? null,
    windows: account.windows.map((w) => ({
      id: w.window.id,
      periodType: w.window.periodType,
      periodLength: w.window.periodLength,
      unit: w.window.unit,
      used: w.window.used,
      total: w.window.total,
      resetsAt: w.window.resetsAt,
      verdict: w.verdict,
      redistribution: w.redistribution,
      todayBudget: w.todayBudget,
      preliminary: w.preliminary,
      efficiencyIndex: w.efficiencyIndex,
      projectedUsage: w.projectedUsage,
      estimatedAutonomyWorkingDays: w.estimatedAutonomyWorkingDays,
      workingDaysUntilReset: w.workingDaysUntilReset,
      deltaStats: w.deltaStats,
      efficiencyRating: w.efficiencyRating,
      dailyTip: w.dailyTip,
    })),
  };
}

// Per-day notification flags ("<account>:<day>", "<account>:pace:<day>"): only
// today's are kept, older ones are dropped instead of piling up in the store.
function flagsOfToday(todayKey: string): Record<string, boolean> {
  return Object.fromEntries(Object.entries(store.get('meta').notifiedToday).filter(([key]) => key.endsWith(`:${todayKey}`)));
}

// ---------------------------------------------------------------------------
// Threshold notifications (default 80%, configurable) — at most once a day
// ---------------------------------------------------------------------------
function maybeNotifyThreshold(snapshot: UsageSnapshot): void {
  const threshold = store.get('ui').notificationThresholdPercent;
  const todayKey = budget.localDateKey(new Date());
  const notifiedToday = flagsOfToday(todayKey);

  for (const account of snapshot.accounts) {
    // The most used window, not the one shown first (that one may be chosen for being
    // at risk while another is past the threshold).
    const mostUsed = budget.pickCriticalWindow(account.windows.map((w) => w.window));
    if (!mostUsed) continue;
    const utilization = budget.normalizedUtilization(mostUsed);
    if (utilization === null || utilization < threshold) continue;

    const flagKey = `${account.accountId}:${todayKey}`;
    if (notifiedToday[flagKey]) continue;

    if (Notification.isSupported()) {
      new Notification({
        title: 'AIpermiler',
        body: t('notify.threshold', { account: account.label, threshold }),
      }).show();
    }
    notifiedToday[flagKey] = true;
  }
  store.set('meta.notifiedToday', notifiedToday);
}

// ---------------------------------------------------------------------------
// Pace notification — today's consumption well above today's budget
// (budget.PACE_ALERT_RATIO), at most once a day per account. The absolute threshold
// above fires only near the end of the quota; this one fires on the day the pace
// goes wrong, while there is still time to adjust.
// ---------------------------------------------------------------------------
function maybeNotifyPace(snapshot: UsageSnapshot): void {
  const todayKey = budget.localDateKey(new Date());
  const notifiedToday = flagsOfToday(todayKey);

  for (const account of snapshot.accounts) {
    const flagKey = `${account.accountId}:pace:${todayKey}`;
    if (notifiedToday[flagKey]) continue;
    const over = account.windows
      .map((w) => w.todayBudget)
      .find((b) => b !== null && b.budget > 0 && b.usedToday > b.budget * budget.PACE_ALERT_RATIO);
    if (!over) continue;

    if (Notification.isSupported()) {
      new Notification({
        title: 'AIpermiler',
        body: t('notify.pace', { account: account.label, used: formatNumber(over.usedToday), budget: formatNumber(over.budget) }),
      }).show();
    }
    notifiedToday[flagKey] = true;
  }
  store.set('meta.notifiedToday', notifiedToday);
}

// Fetch orchestration and snapshot computation (core/usage.ts) over the electron-store.
const usageCore = createUsageCore({
  store: {
    accounts: getAccounts,
    ui: () => store.get('ui'),
    history: () => store.get('history'),
    setHistory: (history) => { store.set('history', history); },
    setLastGood: (accountId, usage) => { store.set(`history.lastGood.${accountId}`, usage); },
  },
  fetchUsage: providers.fetchUsage,
  isConnected: providers.isConnected,
  // Signed out is a state, not a failure: Codex (sign in with Codex), Copilot (reconnect).
  accountNotice: (err) => {
    if (!(err instanceof CoreProviderError) || err.code !== 'signed_out') return null;
    if (err.provider === 'codex') return t('error.codexSignedOut');
    if (err.provider === 'copilot') return t('error.copilotSignedOut');
    return null;
  },
  quotasUnavailable: (cfg) => (cfg.provider === 'claude' ? t('error.claudeQuotasUnavailable') : null),
  localInsights: computeLocalInsightsIfNeeded,
  describeError: friendlyErrorMessage,
  onFetchError: maybeReportFormatDrift,
  now: () => new Date(),
  logError: (message, ...details) => { console.error(message, ...details); },
});

// One refresh at a time: the startup refresh, the widget's own request and the timer
// used to overlap, each repeating the same fetches and store writes. A request
// arriving during a refresh queues exactly one more run (it may follow a settings
// change the running one did not see), with the stronger of the queued modes.
let refreshRunning: Promise<void> | null = null;
let refreshQueued: RefreshMode | null = null;

// Waits for the refresh in flight, whatever its outcome. Disconnect and removal call it
// first: a refresh reading a Copilot token while the core deletes it could write the
// token back (the core moves it from the pre-rename keyring service on first read, #200).
async function settleRefresh(): Promise<void> {
  // A loop: a queued refresh starts as soon as the running one ends.
  while (refreshRunning) await refreshRunning.catch(() => undefined);
}

function refreshAndBroadcast(mode: RefreshMode): Promise<void> {
  if (refreshRunning) {
    refreshQueued = refreshQueued ? refreshPolicy.strongerMode(refreshQueued, mode) : mode;
    return refreshRunning;
  }
  refreshQueued = null;
  refreshRunning = (async () => {
    let next: RefreshMode | null = mode;
    while (next) {
      await refreshOnce(next);
      next = takeQueuedRefresh();
    }
  })().finally(() => { refreshRunning = null; });
  return refreshRunning;
}

// Read-and-clear in a function: set by another IPC call while refreshOnce awaits.
function takeQueuedRefresh(): RefreshMode | null {
  const queued = refreshQueued;
  refreshQueued = null;
  return queued;
}

async function refreshOnce(mode: RefreshMode): Promise<void> {
  if (mainWindow && !mainWindow.isDestroyed()) {
    sendToRenderer(mainWindow, 'usage:refreshing', true);
  }
  let built: { snapshot: UsageSnapshot; consumptionRose: boolean | null };
  const startedAt = Date.now();
  try {
    built = await usageCore.buildUsageSnapshot(mode);
  } catch (err) {
    if (mainWindow && !mainWindow.isDestroyed()) {
      sendToRenderer(mainWindow, 'usage:refreshing', false);
    }
    console.error('[main] usage refresh failed:', err);
    scheduleRefreshLoop();
    return;
  }
  const { snapshot } = built;
  lastConsumptionRose = built.consumptionRose ?? lastConsumptionRose;
  scheduleNextRefresh(refreshPolicy.nextRefreshDelayMs(lastConsumptionRose, configuredRefreshIntervalMs()));
  timings.lastRefresh = { at: new Date().toISOString(), ms: Date.now() - startedAt };
  updateTrayView(snapshot);
  maybeNotifyThreshold(snapshot);
  maybeNotifyPace(snapshot);
  if (mainWindow && !mainWindow.isDestroyed()) {
    sendToRenderer(mainWindow, 'usage:update', snapshot);
  }
}

// Main owns the persisted choice; Auto activity is session-local and starts at the highest usage.
let autoBadgeAccountId: string | null = null;

function updateTrayView(snapshot: UsageSnapshot): void {
  autoBadgeAccountId = trackAutoAccount(autoBadgeAccountId, lastSnapshot?.accounts ?? [], snapshot.accounts);
  const ui = store.get('ui');
  const resolved = resolveLegacyView(ui.trayBadge.view, snapshot.accounts);
  if (!sameChoice(resolved, ui.trayBadge.view)) store.set('ui.trayBadge', { ...ui.trayBadge, view: resolved });
  lastSnapshot = snapshot;
  broadcastView();
}

function badgeAccounts(): AccountSnapshot[] {
  const enabled = new Set(getAccounts().filter(a => a.enabled).map(a => a.id));
  return (lastSnapshot?.accounts ?? []).filter(a => enabled.has(a.accountId));
}

function getTrayViewState(): TrayViewState {
  const selection = store.get('ui').trayBadge;
  const accounts = badgeAccounts();
  const cached = store.get('history').lastGood;
  const registry = getAccounts().map(a => ({ ...a, quotaWindows: cached?.[a.id]?.quotaWindows ?? [] }));
  return { selection, shown: pickBadge(accounts, selection, autoBadgeAccountId)?.view ?? null,
    entries: badgeEntries(registry, accounts, selection.view) };
}

function broadcastView(): TrayViewState {
  const state = getTrayViewState();
  if (lastSnapshot) trayHandle?.updateUsage({ ...lastSnapshot, accounts: badgeAccounts() });
  else trayHandle?.refreshMenu();
  for (const win of [mainWindow, settingsWindow]) {
    if (win && !win.isDestroyed()) sendToRenderer(win, 'view:update', state);
  }
  return state;
}

function setTrayView(choice: TrayBadgeChoice): TrayViewState {
  store.set('ui.trayBadge', { ...store.get('ui').trayBadge, view: choice });
  return broadcastView();
}

// ---------------------------------------------------------------------------
// IPC
// ---------------------------------------------------------------------------
// The renderer must never receive real secrets (sessionKey, PAT) — see AGENTS.md
// "Electron security". `store.store` holds them in clear (the main process needs them
// to authenticate calls), so every time it crosses the IPC boundary towards the
// renderer they are replaced with a placeholder: it keeps the "is connected?" boolean
// (used by renderer/settings.ts for "Connected"/"Not connected") without ever exposing
// the real value.
function redactSecretsForRenderer(settings: AppSettings): AppSettings {
  const accounts = Array.isArray(settings.accounts) ? settings.accounts : [];
  return { ...settings, accounts: accounts.map(providers.redactSecrets) };
}

// The renderer always receives the placeholder version (never the real value): when
// it saves the settings after editing ANOTHER field (e.g. planTier), it sends back the
// whole `accounts` array as is, placeholder included. Without this defence, that
// placeholder would silently overwrite the real sessionKey/token in the store. Secrets
// change ONLY through the dedicated flows (accounts:connect*/accounts:disconnect),
// never through the generic save — see providers.preserveSecrets.
// ---------------------------------------------------------------------------
// IPC input validation: values come from the renderer, i.e. from outside the main
// process — TypeScript types guarantee nothing there at runtime. Every handler
// receives `unknown` and validates it before use.
// ---------------------------------------------------------------------------
function requireString(value: unknown, what: string): string {
  if (typeof value !== 'string' || value.trim() === '') throw new Error(`${what}: invalid value`);
  return value;
}

function requireBoolean(value: unknown, what: string): boolean {
  if (typeof value !== 'boolean') throw new Error(`${what}: invalid value`);
  return value;
}

const PROVIDER_IDS: readonly ProviderId[] = ['claude', 'copilot', 'codex'];
const WINDOW_STYLES: readonly WindowStyle[] = ['filled', 'filled-dark', 'transparent-digital'];

function requireOneOf<T extends string>(value: unknown, allowed: readonly T[], what: string): T {
  const match = allowed.find((candidate) => candidate === value);
  if (match === undefined) throw new Error(`${what}: unsupported value (${String(value)})`);
  return match;
}

// The token is sent to this host: only github.com or <tenant>.ghe.com (user input, so
// the message is translated).
function requireGithubHost(value: unknown): string {
  const host = normalizeGithubHost(value ?? DEFAULT_GITHUB_HOST);
  if (!host) throw new Error(t('error.invalidGithubHost'));
  return host;
}

// Sections the Settings window may write via settings:set: history, meta, caches
// and the like are owned by the main process only (schema validation: Day 3 backlog).
const RENDERER_EDITABLE_KEYS = new Set<string>(['accounts', 'ui', 'diagnostics', 'updates']);

function preserveRealSecretsOnWrite(key: string, value: unknown): unknown {
  // `updates` is owned by the main process (check results): from the renderer only
  // the autoCheck preference is accepted, otherwise a draft opened before an automatic
  // check would overwrite its result on the first "Save".
  if (key === 'updates') {
    const incoming = isPlainRecord(value) ? value : {};
    return { ...getUpdateSettings(), autoCheck: incoming.autoCheck !== false };
  }
  // Same principle for diagnostics: already reported signatures are written only by the main process.
  if (key === 'diagnostics') {
    const incoming = isPlainRecord(value) ? value : {};
    return { ...store.get('diagnostics'), autoReportFormatDrift: incoming.autoReportFormatDrift !== false };
  }
  // Window geometry and the compact view follow the widget, not the Settings draft: a
  // Save after moving or compacting the widget would otherwise write back the values
  // the draft was opened with (compact flag lost, 260×132 then saved as the full size).
  if (key === 'ui') {
    const incoming = isPlainRecord(value) ? value : {};
    const { bounds, compact } = store.get('ui');
    return { ...incoming, bounds, compact };
  }
  if (key !== 'accounts' || !Array.isArray(value)) return value;
  const current = getAccounts();
  // Validated before being touched: the renderer may send any value.
  const merged = normalizeAccounts(value)
    .map((incoming) => providers.preserveSecrets(incoming, current.find((c) => c.id === incoming.id)))
    .filter((a): a is AccountConfig => a !== null);
  // An account present in the store but missing from the patch is not removed here:
  // removal goes only through accounts:remove (which also clears the partition).
  for (const cur of current) {
    if (!merged.some((m) => m.id === cur.id)) merged.push(cur);
  }
  return enforceSingleCodexAccount(enforceSingleLocalInsights(merged));
}

// ---------------------------------------------------------------------------
// Updates (issue #5, services/updates.ts): only check + download in the browser, no
// automatic installation (unsigned packages, no extra dependency — user's choice).
// The state lives in store.updates, so the Settings window and the tray read it like
// any other setting.
// ---------------------------------------------------------------------------
function getUpdateSettings(): UpdateSettings {
  return store.get('updates');
}

function openSettingsWindow(): void {
  settingsWindow = createSettingsWindow(settingsWindow);
  // Return focus to the widget when Settings closes so that the next mouse click
  // lands on the widget content directly rather than first focusing the window
  // (the "focus-click" issue: on some platforms, the first click on an unfocused
  // window activates it but is not forwarded to the web content — seen as
  // "tab not clickable right after closing Settings", issue #25).
  settingsWindow.once('closed', () => {
    if (mainWindow && !mainWindow.isDestroyed()) mainWindow.focus();
  });
}

async function checkForUpdates(source: 'auto' | 'manual'): Promise<UpdateSettings> {
  const current = getUpdateSettings();
  if (source === 'auto' && !current.autoCheck) return current;

  let next: UpdateSettings;
  try {
    const available = await fetchLatestUpdate(app.getVersion(), {
      platform: process.platform,
      arch: process.arch,
      isAppImage: !!process.env.APPIMAGE,
    });
    next = { ...current, available, lastCheckedAt: new Date().toISOString(), lastError: null };
  } catch (err) {
    // Never blocking: the last known result (available) stays valid, the error is shown
    // in Settings next to "Check now".
    console.error('[main] update check failed:', (err as Error).message);
    next = { ...current, lastCheckedAt: new Date().toISOString(), lastError: (err as Error).message };
  }

  if (next.available && next.available.version !== next.notifiedVersion && Notification.isSupported()) {
    const notification = new Notification({
      title: 'AIpermiler',
      body: t('notify.updateAvailable', { version: next.available.version }),
    });
    notification.on('click', openSettingsWindow);
    notification.show();
    next.notifiedVersion = next.available.version;
  }

  store.set('updates', next);
  broadcastSettings();
  trayHandle?.refreshMenu();
  return next;
}

function startUpdateChecks(): void {
  // In development (`npm start`, unpackaged app) no automatic check: the local version
  // does not match an installed build. The "Check now" button still works. `autoCheck`
  // is re-read on every round, so toggling it in Settings takes effect without a restart.
  if (!app.isPackaged) return;
  setTimeout(() => { runDetached('controllo aggiornamenti', checkForUpdates('auto')); }, UPDATE_FIRST_CHECK_DELAY_MS);
  updateTimer = setInterval(() => { runDetached('controllo aggiornamenti', checkForUpdates('auto')); }, UPDATE_CHECK_INTERVAL_MS);
}

function broadcastSettings(): void {
  broadcastView();
  const redacted = redactSecretsForRenderer(store.store);
  for (const win of [mainWindow, settingsWindow]) {
    if (win && !win.isDestroyed()) sendToRenderer(win, 'settings:update', redacted);
  }
}

function registerIpcHandlers(): void {
  handle('view:get', () => getTrayViewState());
  handle('settings:get', () => redactSecretsForRenderer(store.store));

  handle('settings:set', (_event, patch: unknown) => {
    if (!isPlainRecord(patch)) throw new Error('Settings: invalid patch');
    let next: AppSettings = store.store;
    for (const [key, value] of Object.entries(patch)) {
      if (!RENDERER_EDITABLE_KEYS.has(key)) throw new Error(`Settings: section not editable from the renderer: ${key}`);
      next = { ...next, [key]: preserveRealSecretsOnWrite(key, value) };
    }
    // Same normalization as at startup (store/normalize.ts): a value of the wrong type
    // or a field missing from the patch never reaches the store.
    store.store = normalizeSettings(next, DEFAULTS);
    applyMainLocale();
    broadcastView();
    scheduleRefreshLoop();
    const redacted = redactSecretsForRenderer(store.store);
    // Propagates the change to the widget if open: some fields (e.g. accent color) have
    // no dedicated IPC like ui.windowStyle/ui.alwaysOnTop and would otherwise apply only
    // after the next window restart (user feedback).
    if (mainWindow && !mainWindow.isDestroyed()) {
      sendToRenderer(mainWindow, 'settings:update', redacted);
    }
    return redacted;
  });

  listen('usage:refreshRequest', () => { runDetached('refresh usage', refreshAndBroadcast('manual')); });

  listen('window:openSettings', () => {
    settingsWindow = createSettingsWindow(settingsWindow);
  });

  handle('window:setAlwaysOnTop', (_event, rawValue: unknown) => {
    const value = requireBoolean(rawValue, 'Always on top');
    store.set('ui.alwaysOnTop', value);
    if (mainWindow && !mainWindow.isDestroyed()) mainWindow.setAlwaysOnTop(value, 'floating');
    // Also propagates to the Settings window if open (e.g. toggled from the pin in the
    // widget title bar): same logic as settings:set above, so that the "Always on top"
    // checkbox does not drift.
    if (settingsWindow && !settingsWindow.isDestroyed()) {
      sendToRenderer(settingsWindow, 'settings:update', redactSecretsForRenderer(store.store));
    }
    return value;
  });

  handle('window:setCompact', (_event, rawValue: unknown) => {
    const value = requireBoolean(rawValue, 'Compact view');
    store.set('ui.compact', value);
    if (mainWindow && !mainWindow.isDestroyed()) applyCompact(mainWindow, store, value);
    return value;
  });

  handle('window:setStyle', (_event, rawStyle: unknown) => {
    const style = requireOneOf(rawStyle, WINDOW_STYLES, 'Window style');
    store.set('ui.windowStyle', style);
    const wasVisible = mainWindow ? mainWindow.isVisible() : true;
    if (mainWindow && !mainWindow.isDestroyed()) mainWindow.destroy();
    const newWindow = createMainWindow(store);
    mainWindow = newWindow;
    if (!wasVisible) newWindow.hide();
    return style;
  });

  listen('window:minimize', () => {
    if (mainWindow && !mainWindow.isDestroyed()) mainWindow.minimize();
  });

  listen('window:close', () => {
    if (mainWindow && !mainWindow.isDestroyed()) mainWindow.hide();
  });

  handle('accounts:add', (_event, rawProvider: unknown) => {
    const provider = requireOneOf(rawProvider, PROVIDER_IDS, 'Provider');
    const accounts = getAccounts();
    const id = `${provider}-${randomUUID().slice(0, 8)}`;
    accounts.push(defaultAccountFor(provider, id, nextAccountLabel(provider, accounts)));
    // A new Codex account starts enabled unless another one already is (#178).
    store.set('accounts', enforceSingleCodexAccount(accounts));
    broadcastSettings();
    // Codex has no Connect step that would trigger the first read: refresh now.
    runDetached('refresh usage', refreshAndBroadcast('forced'));
    return id;
  });

  handle('accounts:remove', async (_event, rawId: unknown) => {
    const id = requireString(rawId, 'Account');
    const cfg = findAccount(id);
    if (!cfg) return;
    // Full disconnect before removing the row: for Claude it also clears the partition,
    // otherwise the cookies would stay on disk with no owner.
    await settleRefresh();
    await providers.disconnect(cfg);
    store.set('accounts', getAccounts().filter((a) => a.id !== id));
    broadcastSettings();
    runDetached('refresh usage', refreshAndBroadcast('forced'));
  });


  handle('accounts:connectCopilot', async (_event, rawId: unknown, rawToken: unknown, rawHost: unknown) => {
    const id = requireString(rawId, 'Account');
    const token = requireString(rawToken, 'Token').trim();
    const host = requireGithubHost(rawHost);
    const cfg = findAccount(id);
    if (!cfg || cfg.provider !== 'copilot') throw new Error(`Copilot account not found: ${id}`);
    // The core checks the token with GitHub and keeps it in the OS keyring (#179): it
    // goes on the core's stdin, never in argv, and is not written to the store.
    const flags = copilotAccountFlags({ ...cfg, host });
    const username = await core.tokenSet({ provider: 'copilot', flags, token, verify: true });
    await dropTokenIfAccountRemoved(id, flags);
    updateAccount(id, (a) => (a.provider === 'copilot'
      ? { ...a, enabled: true, authMethod: 'pat', host, credentials: { token: null, username } }
      : a));
    broadcastSettings();
    runDetached('refresh usage', refreshAndBroadcast('forced'));
    return { username };
  });

  // Alternative to a hand-pasted PAT, through an OAuth App registered by the user on the
  // account's GitHub domain. Verified on a personal Free account (2026-10-01): with an
  // OAuth token copilot_internal/user returns the quota snapshots — see docs/RESEARCH.md §2.2.
  handle('accounts:connectCopilotOAuth', async (_event, rawId: unknown, rawPayload: unknown) => {
    const id = requireString(rawId, 'Account');
    const payloadRecord = isPlainRecord(rawPayload) ? rawPayload : {};
    const payload = {
      clientId: requireString(payloadRecord.clientId, 'Client ID'),
      clientSecret: requireString(payloadRecord.clientSecret, 'Client Secret'),
      host: requireGithubHost(payloadRecord.host),
    };
    const cfg = findAccount(id);
    if (!cfg || cfg.provider !== 'copilot') throw new Error(`Copilot account not found: ${id}`);
    // The core runs the PKCE loopback and keeps the token in the OS keyring (#179); the
    // app only opens the authorization URL it announces, checked against the host.
    const authorizePrefix = `${githubOAuthBase(payload.host)}/authorize?`;
    const accountFlags = copilotAccountFlags({ ...cfg, host: payload.host });
    const username = await core.login({
      provider: 'copilot',
      flags: [...accountFlags, '--client-id', payload.clientId, '--locale', getLocale()],
      clientSecret: payload.clientSecret,
      onAuthorize: (url) => {
        if (!url.startsWith(authorizePrefix)) {
          console.error('[main] the core announced an unexpected sign-in URL; not opened');
          return;
        }
        shell.openExternal(url).catch((err: unknown) => {
          console.error('[main] could not open the GitHub sign-in page:', err);
        });
      },
    });
    await dropTokenIfAccountRemoved(id, accountFlags);
    updateAccount(id, (a) => (a.provider === 'copilot'
      ? { ...a, enabled: true, authMethod: 'oauth', host: payload.host, credentials: { token: null, username }, oauthApp: { clientId: payload.clientId } }
      : a));
    broadcastSettings();
    runDetached('refresh usage', refreshAndBroadcast('forced'));
    return { username };
  });

  handle('app:getVersion', () => app.getVersion());

  // The bundled core's version, or why it cannot run (Settings, next to the app version).
  handle('core:getStatus', async () => {
    try {
      const v = await core.version();
      return { ok: true, version: `${v.name} ${v.version} (${v.os}/${v.arch})` };
    } catch (err) {
      return { ok: false, error: friendlyErrorMessage(err) };
    }
  });

  handle('updates:check', () => checkForUpdates('manual'));

  // The URL to open comes from the store (written only by checkForUpdates), never
  // from the renderer, and is restricted to the project repository anyway.
  const openTrustedUpdateUrl = async (pick: (a: NonNullable<UpdateSettings['available']>) => string) => {
    const available = getUpdateSettings().available;
    const url = available ? pick(available) : null;
    if (!url || !url.startsWith(TRUSTED_DOWNLOAD_PREFIX)) throw new Error('No update available');
    await shell.openExternal(url);
  };
  handle('updates:download', () => openTrustedUpdateUrl((a) => a.downloadUrl));
  handle('updates:openReleaseNotes', () => openTrustedUpdateUrl((a) => a.releaseUrl));
  // The user guide (README on GitHub) in the interface language; fixed URL.
  handle('app:openGuide', () => shell.openExternal(guideUrl(getLocale())));

  // Manual diagnostic report (Settings → Diagnostics, diagnostics/report.ts): after an
  // explicit confirmation listing what goes in, one text file in Downloads with the
  // redacted responses, pacing settings, the app's reading of the data, recent
  // history and log of every connected account; the folder is shown and a short
  // GitHub issue draft opens, to which the user may attach it. Nothing is sent by the
  // app. The URL is built here with the fixed repository prefix.
  handle('diagnostics:createReport', async () => {
    const connected = getAccounts().filter((cfg) => providers.isConnected(cfg));
    if (connected.length === 0) throw new Error(t('error.reportNoAccounts'));
    const confirmOptions = {
      type: 'question' as const,
      buttons: [t('report.confirm'), t('report.cancel')],
      defaultId: 0,
      cancelId: 1,
      noLink: true,
      title: 'AIpermiler',
      message: t('report.confirmTitle'),
      detail: t('report.confirmDetail', { accounts: connected.length }),
    };
    const parent = settingsWindow && !settingsWindow.isDestroyed() ? settingsWindow : null;
    const { response } = parent ? await dialog.showMessageBox(parent, confirmOptions) : await dialog.showMessageBox(confirmOptions);
    if (response !== 0) return { created: false, fileName: null };

    const now = new Date();
    const counters = new Map<ProviderId, number>();
    const accounts = await Promise.all(connected.map(async (cfg): Promise<ReportAccount> => {
      const n = (counters.get(cfg.provider) ?? 0) + 1;
      counters.set(cfg.provider, n);
      const name = `${providers.providerDisplayName(cfg.provider)} #${String(n)}`;
      const base = {
        name, provider: cfg.provider, enabled: cfg.enabled, label: cfg.label, id: cfg.id,
        config: reportConfig(cfg), interpretation: null as unknown, history: reportHistory(cfg.id, now),
      };
      try {
        const raw = await providers.fetchRawResponse(cfg);
        return { ...base, endpointLabel: raw.endpointLabel, response: raw.response };
      } catch (err) {
        return { ...base, endpointLabel: null, readError: err instanceof Error ? err.message : String(err) };
      }
    }));
    // The app's reading of the data, once every name is known (errors are neutralized).
    for (const account of accounts) account.interpretation = reportInterpretation(account.id, accounts);

    // The core's version, or the reason it cannot run (no paths: shortError).
    const coreVersion = await core.version()
      .then((v) => `${v.version} (${v.os}/${v.arch})`)
      .catch((err: unknown) => `unavailable: ${shortError(err instanceof Error ? err.message : String(err))}`);
    const text = buildDiagnosticReport({
      generatedAt: now,
      environment: {
        app: app.getVersion(),
        core: coreVersion,
        platform: process.platform,
        arch: process.arch,
        osRelease: os.release(),
        electron: process.versions.electron,
        node: process.versions.node,
      },
      settings: {
        language: store.get('ui').language,
        locale: resolveLocale(store.get('ui').language, app.getLocale()),
        chartRange: store.get('ui').chartRange,
        notificationThresholdPercent: store.get('ui').notificationThresholdPercent,
        windowStyle: store.get('ui').windowStyle,
      },
      system: reportSystem(now),
      accounts,
      log: logBuffer.list(),
    });
    const fileName = `aipermiler-report-${budget.localDateKey(now)}-${String(now.getHours()).padStart(2, '0')}${String(now.getMinutes()).padStart(2, '0')}${String(now.getSeconds()).padStart(2, '0')}.txt`;
    const filePath = path.join(app.getPath('downloads'), fileName);
    await writeFile(filePath, text, 'utf8');
    shell.showItemInFolder(filePath);
    await shell.openExternal(buildReportIssueUrl({ appVersion: app.getVersion(), fileName, accounts: accounts.map((a) => a.name) }));
    return { created: true, fileName };
  });

  handle('accounts:disconnect', async (_event, rawId: unknown) => {
    const id = requireString(rawId, 'Account');
    const cfg = findAccount(id);
    if (!cfg) return;
    await settleRefresh();
    const cleared = await providers.disconnect(cfg);
    updateAccount(id, () => cleared);
    broadcastSettings();
    runDetached('refresh usage', refreshAndBroadcast('forced'));
  });
}

// #140: once, remove every claude.ai session stored by earlier versions (account
// partitions, the pre-#4 defaultSession cookies, the stored sessionKey) and the last
// fetched Claude quotas. Retried on the next start if anything fails.
// #178: once, remove the chatgpt.com cookies that the Codex login window stored in each
// account partition before Codex moved to the core. The store side (the `session`
// field) is already dropped by normalizeAccounts.
async function purgeStoredCodexSessions(): Promise<void> {
  if (store.get('meta').codexSessionsPurged === true) return;
  let failures = 0;
  for (const cfg of getAccounts()) {
    if (cfg.provider !== 'codex') continue;
    try {
      await session.fromPartition(cfg.partition).clearStorageData();
    } catch (err) {
      failures += 1;
      console.error(`[main] removing the chatgpt.com cookies of account '${cfg.label}' failed:`, (err as Error).message);
    }
  }
  if (failures === 0) {
    store.set('meta', { ...store.get('meta'), codexSessionsPurged: true });
  } else {
    console.error(`[main] ${String(failures)} Codex cookie store(s) not cleared, retrying at the next start`);
  }
}

// The account can be removed while its sign-in is still open in the browser (or a token
// check is running): the core has just stored a token nobody could delete any more.
// Remove it and fail the connection.
async function dropTokenIfAccountRemoved(id: AccountId, flags: string[]): Promise<void> {
  if (findAccount(id)?.provider === 'copilot') return;
  await core.tokenDelete({ provider: 'copilot', flags });
  throw new Error(t('error.accountRemovedDuringSignIn'));
}

// #179: the Copilot token of an earlier version, kept in the encrypted store, is handed
// to the core once (stdin, unchecked: it was checked when connected, and startup must
// not wait on GitHub) and then removed from the store. A failure (keyring not reachable,
// core missing) keeps it in the store and retries at the next start; meanwhile the core
// cannot read it, so the account shows the error.
async function moveCopilotTokensToKeyring(): Promise<void> {
  for (const cfg of getAccounts()) {
    if (cfg.provider !== 'copilot' || !cfg.credentials.token) continue;
    try {
      await core.tokenSet({ provider: 'copilot', flags: copilotAccountFlags(cfg), token: cfg.credentials.token, verify: false });
      updateAccount(cfg.id, (a) => (a.provider === 'copilot' ? { ...a, credentials: { ...a.credentials, token: null } } : a));
      console.log(`[main] Copilot token of account '${cfg.label}' moved to the OS keyring (#179)`);
    } catch (err) {
      console.error(`[main] moving the Copilot token of account '${cfg.label}' to the OS keyring failed, retrying at the next start:`, friendlyErrorMessage(err));
    }
  }
}

async function purgeStoredClaudeSessions(): Promise<void> {
  if (store.get('meta').claudeSessionsPurged === true) return;
  // The store part runs first and always: it cannot fail on a single partition.
  const history = store.get('history');
  const purged = purgeClaudeSessions(getAccounts(), history.lastGood);
  store.set('accounts', purged.accounts);
  store.set('history', { ...history, lastGood: purged.lastGood });
  // Each cookie store separately: one failure must not skip the others.
  let failures = 0;
  const attempt = async (what: string, clear: () => Promise<unknown>) => {
    try {
      await clear();
    } catch (err) {
      failures += 1;
      console.error(`[main] removing the claude.ai cookies of ${what} failed:`, (err as Error).message);
    }
  };
  for (const cfg of getAccounts()) {
    if (cfg.provider === 'claude') await attempt(`account '${cfg.label}'`, () => clearClaudePartition(cfg.partition));
  }
  await attempt('the default session', clearDefaultSessionClaudeCookies);
  if (failures === 0) {
    store.set('meta', { ...store.get('meta'), claudeSessionsPurged: true });
    console.log('[main] stored claude.ai sessions removed (#140)');
  } else {
    console.error(`[main] ${failures} claude.ai cookie store(s) not cleared, retrying at the next start`);
  }
}

// ---------------------------------------------------------------------------
// App lifecycle
// ---------------------------------------------------------------------------
// The widget is plain HTML/SVG with no animations: the GPU process was the largest
// share of the tray-only footprint (#99, docs/EVOLUTION.md). Must run before ready.
app.disableHardwareAcceleration();
app.whenReady().then(async () => {
  // It is a widget, not a document app: Electron's default menu bar
  // (File/Edit/View/Window/Help) is not needed and weighed down the "filled" skin (user
  // feedback — see also setMenuBarVisibility(false) on every window in main/windows.ts
  // as an extra per-window defence).
  Menu.setApplicationMenu(null);

  registerIpcHandlers();
  applyMainLocale();
  await purgeStoredClaudeSessions();
  await purgeStoredCodexSessions();
  await moveCopilotTokensToKeyring();

  const win = createMainWindow(store);
  mainWindow = win;
  trayHandle = createTray({
    getMainWindow: () => mainWindow,
    getViewState: getTrayViewState,
    selectView: (choice) => { setTrayView(choice); },
    openSettings: openSettingsWindow,
    refreshNow: () => { runDetached('refresh usage', refreshAndBroadcast('manual')); },
    store,
  });

  // The refresh on first load is already triggered by the renderer itself
  // (renderer/app.ts calls requestUsageRefresh() on DOMContentLoaded, both at first
  // start and after a skin change that recreates the window) — a second trigger here
  // would duplicate the Claude/Copilot API call every time the widget opens.

  scheduleRefreshLoop();
  startWindowHoverPolling();
  // A core that cannot run is reported in the log buffer (diagnostic report) at once,
  // not only when a provider first reads through it.
  runDetached('core version check', core.version().then((v) => {
    console.log(`[main] core ${v.name} ${v.version} (${v.os}/${v.arch})`);
  }, (err: unknown) => {
    console.warn('[main] core unavailable:', friendlyErrorMessage(err));
  }));
  startUpdateChecks();

  app.on('activate', () => {
    if (BrowserWindow.getAllWindows().length === 0) {
      mainWindow = createMainWindow(store);
    }
  });
}).catch((err: unknown) => {
  // Startup failed (window, tray, migration…): without this the app would stay alive
  // in the background with no UI and no message at all. Better to say so and quit.
  console.error('[main] startup failed:', err);
  dialog.showErrorBox(t('startup.failedTitle'), err instanceof Error ? err.message : String(err));
  app.quit();
});

app.on('window-all-closed', () => {
  // The app stays alive in the tray even with the window closed (background refresh);
  // it quits only from the "Quit" entry of the tray menu.
});

app.on('before-quit', () => {
  if (refreshTimer) clearTimeout(refreshTimer);
  if (hoverPollTimer) clearInterval(hoverPollTimer);
  if (updateTimer) clearInterval(updateTimer);
});
