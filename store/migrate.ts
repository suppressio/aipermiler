// store/migrate.ts — account registry: per-provider defaults and migration from the
// legacy two-slot schema `{ claude, copilot }` to the `AccountConfig[]` array (issue
// #4, docs/EVOLUTION.md point 3). Pure functions, no Electron dependency: tested in
// store/migrate.test.ts.

import type {
  AccountConfig,
  AccountId,
  ClaudeAccountSettings,
  CodexAccountSettings,
  CopilotAccountSettings,
  DayStatus,
  HistorySettings,
  ProviderId,
  WorkSchedule,
  TrayBadgeSettings,
  QuotaWindow,
} from '../types/index';
import path from 'node:path';
import { isPlainRecord, mergeWithDefaults } from './merge';
import { pickCriticalWindow } from '../budget';
import { DEFAULT_WORK_SCHEDULE } from './defaults';
import { DEFAULT_GITHUB_HOST, normalizeGithubHost } from '../services/githubHost';

export const PROVIDER_DISPLAY_NAMES: Record<ProviderId, string> = {
  claude: 'Claude',
  codex: 'Codex (ChatGPT)',
  copilot: 'GitHub Copilot',
};

export function claudePartitionFor(id: AccountId): string {
  return `persist:account-${id}`;
}

export function defaultClaudeAccount(id: AccountId, label = PROVIDER_DISPLAY_NAMES.claude): ClaudeAccountSettings {
  return {
    id,
    provider: 'claude',
    label,
    enabled: false,
    subscription: { renewalRule: { type: 'dayOfMonth', day: 1 } },
    workSchedule: structuredClone(DEFAULT_WORK_SCHEDULE),
    authMethod: 'password',
    session: { sessionKey: null, organizationId: null, capturedAt: null, expiresAt: null },
    partition: claudePartitionFor(id),
    localInsights: false,
  };
}

export function defaultCodexAccount(id: AccountId, label = PROVIDER_DISPLAY_NAMES.codex): CodexAccountSettings {
  return {
    id,
    provider: 'codex',
    label,
    // Enabled from the start: there is no Connect step, the core uses the Codex sign-in
    // of this computer (#178).
    enabled: true,
    subscription: { renewalRule: { type: 'dayOfMonth', day: 1 } },
    workSchedule: structuredClone(DEFAULT_WORK_SCHEDULE),
    partition: claudePartitionFor(id),
    binaryPath: null,
  };
}

export function defaultCopilotAccount(id: AccountId, label = PROVIDER_DISPLAY_NAMES.copilot): CopilotAccountSettings {
  return {
    id,
    provider: 'copilot',
    label,
    enabled: false,
    accountScope: 'personal',
    subscription: { renewalRule: { type: 'dayOfMonth', day: 1 } },
    workSchedule: structuredClone(DEFAULT_WORK_SCHEDULE),
    authMethod: 'pat',
    host: DEFAULT_GITHUB_HOST,
    credentials: { token: null, username: null },
    oauthApp: { clientId: null },
    // Copilot's billing API does not expose the plan's total quota: value configured
    // by hand (see docs/ARCHITECTURE.md §0 and docs/RESEARCH.md v3 §3).
    manualQuota: 300,
    experimentalWarningAcknowledged: false,
  };
}

const DAY_STATUSES: readonly DayStatus[] = ['full', 'half', 'off'];

/**
 * Any value → a valid WorkSchedule (a fresh object, never shared between accounts):
 * missing fields and wrong types from the defaults, unknown day values back to the
 * default for that day.
 */
export function normalizeWorkSchedule(raw: unknown): WorkSchedule {
  const merged = mergeWithDefaults(structuredClone(DEFAULT_WORK_SCHEDULE), raw) as WorkSchedule;
  const days = { ...DEFAULT_WORK_SCHEDULE.days };
  for (const key of Object.keys(days) as (keyof WorkSchedule['days'])[]) {
    const value = merged.days[key];
    if (DAY_STATUSES.includes(value)) days[key] = value;
  }
  return { enabled: merged.enabled, days, hoursPerDay: merged.hoursPerDay };
}

// The account's own schedule, or — for stores written before 0.4.6, where the schedule
// was one global setting — the inherited global one, so the pacing does not change.
function scheduleFor(raw: Record<string, unknown>, inheritedSchedule: unknown): WorkSchedule {
  return normalizeWorkSchedule(isPlainRecord(raw.workSchedule) ? raw.workSchedule : inheritedSchedule);
}

export function defaultAccountFor(provider: ProviderId, id: AccountId, label?: string): AccountConfig {
  switch (provider) {
    case 'claude': return defaultClaudeAccount(id, label);
    case 'copilot': return defaultCopilotAccount(id, label);
    case 'codex': return defaultCodexAccount(id, label);
  }
}

/** Display name of a new account: "Claude", then "Claude 2", "Claude 3"… */
export function nextAccountLabel(provider: ProviderId, existing: AccountConfig[]): string {
  const base = PROVIDER_DISPLAY_NAMES[provider];
  const labels = new Set(existing.map((a) => a.label));
  if (!labels.has(base)) return base;
  let n = 2;
  while (labels.has(`${base} ${n}`)) n += 1;
  return `${base} ${n}`;
}

// Fields of older versions that no longer exist: the merge with the defaults keeps
// unknown keys, so they are dropped here. `planTier` (both providers) and the Claude
// `accountScope` were entered by hand and used by no computation (2026-10).
const CLAUDE_OBSOLETE_FIELDS = ['planTier', 'accountScope'];
const COPILOT_OBSOLETE_FIELDS = ['planTier'];
// The captured chatgpt.com session (#82) is gone since Codex is read through the core (#178).
const CODEX_OBSOLETE_FIELDS = ['session'];

/** A codex binary path from disk or Settings: absolute, trimmed, else auto-detection (null). */
export function normalizeCodexBinaryPath(value: unknown): string | null {
  if (typeof value !== 'string') return null;
  const trimmed = value.trim();
  return trimmed !== '' && path.isAbsolute(trimmed) ? trimmed : null;
}

function withoutObsoleteFields(value: unknown, fields: readonly string[]): unknown {
  if (!isPlainRecord(value)) return value;
  const copy = { ...value };
  for (const field of fields) Reflect.deleteProperty(copy, field);
  return copy;
}

/**
 * Converts `accounts` into `AccountConfig[]`. Idempotent: an already migrated array
 * comes back normalized. Legacy accounts keep the ids `'claude'`/`'copilot'` — the same
 * values already used as `accountId` in history.dailyUsage/recentSamples/lastGood,
 * which therefore stay valid without rewriting the history. A legacy slot never
 * configured (neither enabled nor with credentials) produces no account: the Settings
 * table starts empty instead of with ghost rows.
 */
export function migrateAccounts(rawAccounts: unknown, legacyLocalInsightsEnabled: boolean, inheritedSchedule?: unknown): AccountConfig[] {
  if (Array.isArray(rawAccounts)) return normalizeAccounts(rawAccounts, inheritedSchedule);
  if (!isPlainRecord(rawAccounts)) return [];

  const result: AccountConfig[] = [];

  const claude = rawAccounts.claude;
  if (isPlainRecord(claude)) {
    const session = isPlainRecord(claude.session) ? claude.session : {};
    if (claude.enabled === true || typeof session.sessionKey === 'string') {
      const migrated = withoutObsoleteFields(mergeWithDefaults(defaultClaudeAccount('claude'), claude), CLAUDE_OBSOLETE_FIELDS) as ClaudeAccountSettings;
      migrated.id = 'claude';
      migrated.provider = 'claude';
      migrated.partition = claudePartitionFor('claude');
      migrated.localInsights = legacyLocalInsightsEnabled;
      migrated.workSchedule = scheduleFor(claude, inheritedSchedule);
      result.push(migrated);
    }
  }

  const copilot = rawAccounts.copilot;
  if (isPlainRecord(copilot)) {
    const credentials = isPlainRecord(copilot.credentials) ? copilot.credentials : {};
    if (copilot.enabled === true || typeof credentials.token === 'string') {
      const migrated = withoutObsoleteFields(mergeWithDefaults(defaultCopilotAccount('copilot'), copilot), COPILOT_OBSOLETE_FIELDS) as CopilotAccountSettings;
      migrated.id = 'copilot';
      migrated.provider = 'copilot';
      migrated.workSchedule = scheduleFor(copilot, inheritedSchedule);
      result.push(migrated);
    }
  }

  return result;
}

/**
 * At most one Claude account can have `localInsights: true`: local Claude Code
 * sessions do not say which account they belong to. If more have it (patch from the
 * renderer), the first in order wins.
 */
/**
 * At most one enabled Codex account (#178): the core reads the one Codex sign-in of
 * this computer, so two enabled accounts would show the same data under two names (and
 * accounts migrated from separate ChatGPT logins would all get it). The first enabled
 * one is kept; reading another login per account is #188.
 */
export function enforceSingleCodexAccount(accounts: AccountConfig[]): AccountConfig[] {
  let seen = false;
  return accounts.map((a) => {
    if (a.provider !== 'codex' || !a.enabled) return a;
    if (seen) return { ...a, enabled: false };
    seen = true;
    return a;
  });
}

export function enforceSingleLocalInsights(accounts: AccountConfig[]): AccountConfig[] {
  let seen = false;
  return accounts.map((a) => {
    if (a.provider !== 'claude' || !a.localInsights) return a;
    if (seen) return { ...a, localInsights: false };
    seen = true;
    return a;
  });
}

/**
 * #140 cleanup, pure part: Claude accounts lose the stored claude.ai session and their
 * last fetched quotas (`history.lastGood`), which would otherwise be shown as stale data
 * forever. Accounts, settings and the daily history stay. main.ts also clears the
 * partitions (cookies) and records that the cleanup ran.
 */
export function purgeClaudeSessions(
  accounts: AccountConfig[],
  lastGood: HistorySettings['lastGood'],
): { accounts: AccountConfig[]; lastGood: HistorySettings['lastGood'] } {
  const claudeIds = new Set(accounts.filter((a) => a.provider === 'claude').map((a) => a.id));
  const cleaned = accounts.map((a) => (a.provider === 'claude'
    ? { ...a, session: { sessionKey: null, organizationId: null, capturedAt: null, expiresAt: null } }
    : a));
  if (!lastGood) return { accounts: cleaned, lastGood };
  return {
    accounts: cleaned,
    lastGood: Object.fromEntries(Object.entries(lastGood).filter(([id]) => !claudeIds.has(id))),
  };
}

/**
 * Account registry on disk (already in array form) → valid AccountConfig[]: each
 * entry is completed with its provider's defaults (fields added in later versions,
 * wrong types), entries without an id or with an unknown provider are dropped.
 * `provider`, `id` and (for Claude) `partition` cannot be changed by the content on
 * disk: the partition always derives from the id.
 */
export function normalizeAccounts(rawAccounts: unknown[], inheritedSchedule?: unknown): AccountConfig[] {
  const result: AccountConfig[] = [];
  for (const raw of rawAccounts) {
    if (!isPlainRecord(raw) || typeof raw.id !== 'string' || raw.id === '') continue;
    const id = raw.id;
    if (raw.provider === 'claude') {
      const cfg = withoutObsoleteFields(mergeWithDefaults(defaultClaudeAccount(id), raw), CLAUDE_OBSOLETE_FIELDS) as ClaudeAccountSettings;
      result.push({ ...cfg, id, provider: 'claude', partition: claudePartitionFor(id), workSchedule: scheduleFor(raw, inheritedSchedule) });
    } else if (raw.provider === 'codex') {
      const cfg = withoutObsoleteFields(mergeWithDefaults(defaultCodexAccount(id), raw), CODEX_OBSOLETE_FIELDS) as CodexAccountSettings;
      result.push({
        ...cfg,
        id,
        provider: 'codex',
        partition: claudePartitionFor(id),
        binaryPath: normalizeCodexBinaryPath(cfg.binaryPath),
        workSchedule: scheduleFor(raw, inheritedSchedule),
      });
    } else if (raw.provider === 'copilot') {
      const cfg = withoutObsoleteFields(mergeWithDefaults(defaultCopilotAccount(id), raw), COPILOT_OBSOLETE_FIELDS) as CopilotAccountSettings;
      // An unsupported host falls back to github.com: the token is never sent elsewhere.
      const host = normalizeGithubHost(cfg.host) ?? DEFAULT_GITHUB_HOST;
      result.push({ ...cfg, id, provider: 'copilot', host, workSchedule: scheduleFor(raw, inheritedSchedule) });
    }
  }
  return enforceSingleCodexAccount(enforceSingleLocalInsights(result));
}

/** Migrates the old string preference; null window defers only an uncached legacy choice. */
export function migrateTrayBadge(raw: unknown, history: unknown): TrayBadgeSettings {
  const fallback: TrayBadgeSettings = { view: { mode: 'auto' }, critical: true };
  if (typeof raw === 'string') {
    if (raw === 'off') return { ...fallback, view: { mode: 'off' } };
    if (raw === 'critical' || raw === '') return fallback;
    const cached = isPlainRecord(history) && isPlainRecord(history.lastGood) ? history.lastGood[raw] : undefined;
    const windows = isPlainRecord(cached) && Array.isArray(cached.quotaWindows) ? cached.quotaWindows : [];
    // Persisted history is not trusted: only validated fields reach budget helpers.
    const known: QuotaWindow[] = windows.flatMap((win: unknown): QuotaWindow[] => {
      if (!isPlainRecord(win) || typeof win.id !== 'string' || typeof win.used !== 'number'
        || (win.total !== null && typeof win.total !== 'number') || (win.unit !== 'percentage' && win.unit !== 'count')) return [];
      return [{ id: win.id, label: '', used: win.used, total: win.total, unit: win.unit,
        periodType: win.periodType === 'rolling-hours' || win.periodType === 'rolling-days' ? win.periodType : 'billing-cycle',
        periodLength: typeof win.periodLength === 'number' ? win.periodLength : null, resetsAt: null }];
    });
    return { ...fallback, view: { mode: 'manual', accountId: raw, windowId: pickCriticalWindow(known)?.id ?? null } };
  }
  if (!isPlainRecord(raw)) return fallback;
  const critical = typeof raw.critical === 'boolean' ? raw.critical : true;
  const view = raw.view;
  if (!isPlainRecord(view)) return { ...fallback, critical };
  if (view.mode === 'off' || view.mode === 'auto') return { view: { mode: view.mode }, critical };
  if (view.mode === 'manual' && typeof view.accountId === 'string' && view.accountId !== ''
    && (view.windowId === null || (typeof view.windowId === 'string' && view.windowId !== ''))) {
    return { view: { mode: 'manual', accountId: view.accountId, windowId: view.windowId }, critical };
  }
  return { ...fallback, critical };
}
