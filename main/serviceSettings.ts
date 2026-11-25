// main/serviceSettings.ts — the Settings window's accounts and core settings through the
// core's service (#337, part of #181; docs/CORE-CLI.md "Service", Settings #284). The
// service is the only writer of `store.json`: Electron reads `GET /api/v1/settings`,
// writes `PUT /api/v1/settings`, and never writes the file itself. With no usable service
// the calls fail with ServiceUnusableError and Settings shows the accounts read-only
// (owner decisions D1/D3 on #181: a message, no CLI fallback, no start button).
//
// The mapping AppSettings ↔ service document is pure and tested against the service
// fixtures (gocore/contract/v1/service/settings.json, error-invalid-settings.json).
// Fields only Electron knows stay in electron-store and are merged by account id: the
// Copilot OAuth App `clientId`, the connected `username`, Claude's old session fields
// and the partitions. The UI-only sections (`ui`, `updates`, `diagnostics`) never reach
// the service.

import type { AccountConfig, DayStatus, ProviderId, WorkScheduleDays } from '../types/index';
import type { CoreSettingsFieldError } from '../types/ipc';
import { defaultAccountFor, normalizeAccounts } from '../store/migrate';
import { DEFAULT_GITHUB_HOST } from '../services/githubHost';
import { ServiceUnusableError, connectService, serviceRequest, type FetchLike } from './serviceClient';

export interface ServiceSchedule {
  enabled: boolean;
  // Seven weights, Sunday first: 0, 0.5 or 1.
  days: number[];
  hoursPerDay: number;
}

/** An account of the service's settings document (optional fields omitted when unset, as Go's omitempty). */
export interface ServiceAccount {
  id: string;
  provider: ProviderId;
  label: string;
  enabled: boolean;
  // 0 = none.
  renewalDay: number;
  schedule: ServiceSchedule;
  host?: string;
  scope?: 'personal' | 'organization';
  manualQuota?: number;
  binaryPath?: string;
  localInsights?: boolean;
  authMethod?: 'pat' | 'oauth';
}

export interface ServiceGlobalSettings {
  refreshIntervalMinutes: number;
  retentionDays: number;
  notificationThresholdPercent: number;
}

export interface ServiceSettingsDocument {
  schemaVersion: 1;
  settings: ServiceGlobalSettings;
  accounts: ServiceAccount[];
}

// The store's days are Sunday first; WorkScheduleDays is keyed by name.
const WEEKDAYS = ['sun', 'mon', 'tue', 'wed', 'thu', 'fri', 'sat'] as const satisfies readonly (keyof WorkScheduleDays)[];
const PROVIDERS: readonly ProviderId[] = ['claude', 'codex', 'copilot'];

const WEIGHT: Record<DayStatus, number> = { full: 1, half: 0.5, off: 0 };

function statusOf(weight: number): DayStatus {
  return weight === 1 ? 'full' : weight === 0.5 ? 'half' : 'off';
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function parseJson(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}

const isNumber = (v: unknown): v is number => typeof v === 'number' && Number.isFinite(v);

// --- AppSettings → service document --------------------------------------------------

/** An app account as the service stores it: only the fields the core knows. */
export function toServiceAccount(account: AccountConfig): ServiceAccount {
  const rule = account.subscription.renewalRule;
  const day = rule.type === 'dayOfMonth' && typeof rule.day === 'number' && Number.isInteger(rule.day) ? rule.day : 0;
  const out: ServiceAccount = {
    id: account.id,
    provider: account.provider,
    label: account.label,
    enabled: account.enabled,
    renewalDay: day,
    schedule: {
      enabled: account.workSchedule.enabled,
      days: WEEKDAYS.map((name) => WEIGHT[account.workSchedule.days[name]]),
      hoursPerDay: account.workSchedule.hoursPerDay,
    },
  };
  switch (account.provider) {
    case 'copilot':
      // github.com is the core's own default: omitted like an unset host.
      if (account.host !== DEFAULT_GITHUB_HOST) out.host = account.host;
      out.scope = account.accountScope;
      if (account.manualQuota !== 0) out.manualQuota = account.manualQuota;
      out.authMethod = account.authMethod;
      break;
    case 'claude':
      if (account.binaryPath) out.binaryPath = account.binaryPath;
      if (account.localInsights) out.localInsights = true;
      break;
    case 'codex':
      if (account.binaryPath) out.binaryPath = account.binaryPath;
      break;
  }
  return out;
}

/** The core's part of the Settings form: accounts and the three global settings. */
export interface CoreSettingsInput {
  accounts: AccountConfig[];
  refreshIntervalMinutes: number;
  notificationThresholdPercent: number;
  // Not in the form: kept as the service holds it.
  retentionDays: number;
}

export function toServiceDocument(input: CoreSettingsInput): ServiceSettingsDocument {
  return {
    schemaVersion: 1,
    settings: {
      refreshIntervalMinutes: input.refreshIntervalMinutes,
      retentionDays: input.retentionDays,
      notificationThresholdPercent: input.notificationThresholdPercent,
    },
    accounts: input.accounts.map(toServiceAccount),
  };
}

// --- service document → AppSettings ----------------------------------------------------

/**
 * A service account as an app account: the core's fields from the service, the fields
 * only Electron keeps (Copilot `username` and OAuth `clientId`, partitions) from the
 * local account with the same id, else the provider's defaults. A provider change for
 * the same id (the service refuses one) starts from the defaults.
 */
export function fromServiceAccount(source: ServiceAccount, local: AccountConfig | undefined): AccountConfig {
  const base = local?.provider === source.provider ? structuredClone(local) : defaultAccountFor(source.provider, source.id, source.label);
  const days = Object.fromEntries(WEEKDAYS.map((name, i) => [name, statusOf(source.schedule.days[i] ?? 0)])) as unknown as WorkScheduleDays;
  const common = {
    label: source.label,
    enabled: source.enabled,
    // No renewal day (0): the field stays empty in Settings and is sent back as 0.
    subscription: { renewalRule: source.renewalDay > 0 ? { type: 'dayOfMonth' as const, day: source.renewalDay } : { type: 'dayOfMonth' as const } },
    workSchedule: { enabled: source.schedule.enabled, days, hoursPerDay: source.schedule.hoursPerDay },
  };
  switch (base.provider) {
    case 'copilot':
      return {
        ...base,
        ...common,
        host: source.host ?? DEFAULT_GITHUB_HOST,
        accountScope: source.scope ?? 'personal',
        manualQuota: source.manualQuota ?? 0,
        authMethod: source.authMethod ?? base.authMethod,
      };
    case 'claude':
      return { ...base, ...common, binaryPath: source.binaryPath ?? null, localInsights: source.localInsights === true };
    case 'codex':
      return { ...base, ...common, binaryPath: source.binaryPath ?? null };
  }
}

/** The accounts Settings shows: the service's, in its order, with the local fields merged by id. */
export function accountsFromService(doc: ServiceSettingsDocument, local: readonly AccountConfig[]): AccountConfig[] {
  return doc.accounts.map((a) => fromServiceAccount(a, local.find((l) => l.id === a.id)));
}

/**
 * Electron's own copy of the accounts after a read or write through the service, for
 * the widget and its timers until they read the service (#341): every service account
 * updated or added, normalized as the store is. An account the service does not have is
 * kept: before the v0.7 migration script ran, Electron's file is its only source
 * (docs/UPGRADE-TO-0.7.md), so nothing is ever dropped from it here; Settings removes an
 * account explicitly (accounts:remove).
 */
export function mirrorAccounts(local: readonly AccountConfig[], fromService: readonly AccountConfig[]): AccountConfig[] {
  const byId = new Map(fromService.map((a) => [a.id, a]));
  const merged = local.map((a) => byId.get(a.id) ?? a);
  for (const a of fromService) {
    if (!local.some((l) => l.id === a.id)) merged.push(a);
  }
  return normalizeAccounts(merged);
}

/**
 * The accounts of a Settings patch after normalizeAccounts, which gives an empty renewal
 * day the store default (1): an account whose raw patch has no day keeps none, so the
 * service's 0 survives a save.
 */
export function keepNoRenewalDay(raw: readonly unknown[], normalized: AccountConfig[]): AccountConfig[] {
  const noDay = new Set<string>();
  for (const r of raw) {
    if (!isRecord(r) || typeof r.id !== 'string') continue;
    const rule = isRecord(r.subscription) ? r.subscription.renewalRule : undefined;
    if (!isRecord(rule) || typeof rule.day !== 'number') noDay.add(r.id);
  }
  return normalized.map((a) => (noDay.has(a.id) ? { ...a, subscription: { renewalRule: { type: 'dayOfMonth' } } } : a));
}

/** A service account with the connection fields of Electron's copy: enabled, Copilot authentication and host. */
export function withConnectionFields(account: ServiceAccount, cfg: AccountConfig): ServiceAccount {
  const out: ServiceAccount = { ...account, enabled: cfg.enabled };
  if (cfg.provider === 'copilot') {
    out.authMethod = cfg.authMethod;
    if (cfg.host === DEFAULT_GITHUB_HOST) delete out.host;
    else out.host = cfg.host;
  }
  return out;
}

// --- parsing -----------------------------------------------------------------------------

function parseAccount(raw: unknown): ServiceAccount | null {
  if (!isRecord(raw) || typeof raw.id !== 'string' || raw.id === '' || typeof raw.label !== 'string' || typeof raw.enabled !== 'boolean'
    || !isNumber(raw.renewalDay)) return null;
  const provider = PROVIDERS.find((p) => p === raw.provider);
  const s = raw.schedule;
  if (provider === undefined || !isRecord(s) || typeof s.enabled !== 'boolean' || !isNumber(s.hoursPerDay)
    || !Array.isArray(s.days) || s.days.length !== 7 || !s.days.every(isNumber)) return null;
  const out: ServiceAccount = {
    id: raw.id,
    provider,
    label: raw.label,
    enabled: raw.enabled,
    renewalDay: raw.renewalDay,
    schedule: { enabled: s.enabled, days: s.days, hoursPerDay: s.hoursPerDay },
  };
  if (typeof raw.host === 'string' && raw.host !== '') out.host = raw.host;
  if (raw.scope === 'personal' || raw.scope === 'organization') out.scope = raw.scope;
  if (isNumber(raw.manualQuota)) out.manualQuota = raw.manualQuota;
  if (typeof raw.binaryPath === 'string' && raw.binaryPath !== '') out.binaryPath = raw.binaryPath;
  if (raw.localInsights === true) out.localInsights = true;
  if (raw.authMethod === 'pat' || raw.authMethod === 'oauth') out.authMethod = raw.authMethod;
  return out;
}

/** `GET`/`PUT /api/v1/settings` answer → document; anything else is a malformed service. */
export function parseSettingsDocument(body: string): ServiceSettingsDocument {
  const doc = parseJson(body);
  const malformed = (): ServiceUnusableError => new ServiceUnusableError('malformed', 'Service settings document is not readable');
  if (!isRecord(doc) || doc.schemaVersion !== 1 || !isRecord(doc.settings) || !Array.isArray(doc.accounts)) throw malformed();
  const { refreshIntervalMinutes, retentionDays, notificationThresholdPercent } = doc.settings;
  if (!isNumber(refreshIntervalMinutes) || !isNumber(retentionDays) || !isNumber(notificationThresholdPercent)) throw malformed();
  const accounts: ServiceAccount[] = [];
  for (const raw of doc.accounts) {
    const account = parseAccount(raw);
    if (!account) throw malformed();
    accounts.push(account);
  }
  return { schemaVersion: 1, settings: { refreshIntervalMinutes, retentionDays, notificationThresholdPercent }, accounts };
}

/** The service refused the settings (400 `invalid_settings`): the message starts with the field's JSON path. */
export class ServiceSettingsInvalidError extends Error {
  readonly path: string | null;

  constructor(path: string | null, message: string) {
    super(message);
    this.name = 'ServiceSettingsInvalidError';
    this.path = path;
  }
}

/** The JSON path that starts an `invalid_settings` message (`accounts[1].renewalDay: …`), or null. */
export function invalidSettingsPath(message: string): string | null {
  const m = /^([A-Za-z][A-Za-z0-9]*(?:\[\d+\])?(?:\.[A-Za-z][A-Za-z0-9]*(?:\[\d+\])?)*): /.exec(message);
  return m?.[1] ?? null;
}

function parseInvalidSettings(body: string): ServiceSettingsInvalidError | null {
  const doc = parseJson(body);
  if (!isRecord(doc) || !isRecord(doc.error) || doc.error.reason !== 'invalid_settings' || typeof doc.error.message !== 'string') return null;
  return new ServiceSettingsInvalidError(invalidSettingsPath(doc.error.message), doc.error.message);
}

// Service document field → Settings form field (data-field under accounts.<i>. or the global path).
const ACCOUNT_FIELDS: Record<string, string> = {
  label: 'label',
  enabled: 'enabled',
  renewalDay: 'subscription.renewalRule.day',
  'schedule.enabled': 'workSchedule.enabled',
  'schedule.hoursPerDay': 'workSchedule.hoursPerDay',
  host: 'host',
  scope: 'accountScope',
  manualQuota: 'manualQuota',
  binaryPath: 'binaryPath',
  localInsights: 'localInsights',
  authMethod: 'authMethod',
};
const GLOBAL_FIELDS: Record<string, string> = {
  'settings.refreshIntervalMinutes': 'ui.refreshIntervalMinutes',
  'settings.notificationThresholdPercent': 'ui.notificationThresholdPercent',
};

/**
 * The Settings field a service validation error is about, from its JSON path and the
 * accounts sent in that order: `accounts[i].<field>` names the account by id. Any other
 * path (another form, a field with no input) gives null: Settings shows the message on
 * its own.
 */
export function fieldOfInvalidSettings(path: string | null, sent: readonly { id: string }[]): CoreSettingsFieldError['field'] {
  if (path === null) return null;
  const global = GLOBAL_FIELDS[path];
  if (global) return { accountId: null, path: global };
  const m = /^accounts\[(\d+)\]\.(.+)$/.exec(path);
  const id = m ? sent[Number(m[1])]?.id : undefined;
  const field = m?.[2] !== undefined ? ACCOUNT_FIELDS[m[2]] : undefined;
  return id !== undefined && field !== undefined ? { accountId: id, path: field } : null;
}

/** A settings answer → its document; ServiceUnusableError or ServiceSettingsInvalidError otherwise. */
export function parseSettingsAnswer(status: number, body: string): ServiceSettingsDocument {
  if (status === 200) return parseSettingsDocument(body);
  if (status === 401) throw new ServiceUnusableError('unauthorized', 'Service refused the token');
  if (status === 400 || status === 413) {
    const invalid = parseInvalidSettings(body);
    if (invalid) throw invalid;
    throw new ServiceUnusableError('rejected', `Service refused the settings (${String(status)})`);
  }
  // 503 `unavailable`: the store cannot be written; nothing changed.
  throw new ServiceUnusableError('failed', `Service answered ${String(status)}`);
}

// --- client ----------------------------------------------------------------------------

export interface ServiceSettingsClient {
  get(): Promise<ServiceSettingsDocument>;
  /** Replaces settings and accounts, all or nothing; answers like get(). */
  put(doc: ServiceSettingsDocument): Promise<ServiceSettingsDocument>;
}

export interface ServiceSettingsClientOptions {
  // Resolved on every call: a service started or stopped later is found then.
  configDir: () => string;
  fetch?: FetchLike;
  timeouts?: { healthMs: number; settingsMs: number };
}

export function createServiceSettingsClient(options: ServiceSettingsClientOptions): ServiceSettingsClient {
  const fetchImpl: FetchLike = options.fetch ?? ((url, init) => fetch(url, init));
  const timeouts = options.timeouts ?? { healthMs: 2000, settingsMs: 10000 };
  const call = async (method: 'GET' | 'PUT', body?: ServiceSettingsDocument): Promise<ServiceSettingsDocument> => {
    const endpoint = await connectService(fetchImpl, options.configDir, timeouts.healthMs);
    const answer = await serviceRequest(fetchImpl, endpoint, method, '/api/v1/settings', timeouts.settingsMs, body);
    return parseSettingsAnswer(answer.status, answer.body);
  };
  return {
    get: () => call('GET'),
    put: (doc) => call('PUT', doc),
  };
}
