// main/serviceSnapshot.ts — the pacing snapshot of the core's service (#326, #341,
// docs/CORE-CLI.md "Service"): the account's usage document plus `pacing`, computed by
// the Go core from its own history. Electron no longer computes pace or keeps a
// history (#181): this module reads the snapshot and maps it to the types the renderer,
// the tray and the notifications already use (types/index.ts), so they stay unchanged.
//
// Also the two other reads that replaced Electron's own work: `POST /api/v1/refresh`
// (the refresh button and the tray) and `GET /api/v1/accounts/{id}/history` (the
// diagnostic report).
//
// Values are exact (I-4) and an undefined one is null (I-5): this module never rounds.

import { CoreOutputError, type CoreUsageResult } from './coreClient';
import {
  ServiceUnusableError,
  connectService,
  parseServiceUsage,
  serviceRequest,
  type FetchLike,
} from './serviceClient';
import type {
  AccountSnapshot,
  ChartDay,
  DailyTip,
  ProviderId,
  QuotaWindow,
  QuotaWindowSnapshot,
  TokenYield,
  WindowVerdict,
} from '../types/index';

// ---------------------------------------------------------------------------
// The document (only the fields the app reads; unknown ones are ignored, additive v1)
// ---------------------------------------------------------------------------

export type SnapshotVerdict = WindowVerdict['kind'];
export type SnapshotReason = 'hourly' | 'pace' | 'ratio' | 'autonomy' | 'projection' | null;
export type SnapshotTrend = 'accelerating' | 'slowing-down' | 'steady' | null;

export interface SnapshotWindow {
  id: string;
  used: number | null;
  daysToReset: number | null;
  workingUnitsToReset: number | null;
  efficiency: number | null;
  usedToday: number | null;
  todayBudget: number | null;
  paceAlert: boolean;
  redistribution: { perUnit: number; evenShare: number; units: number } | null;
  summary: { pace: number | null; projection: number | null; autonomy: number | null; preliminary: boolean } | null;
  hourly: { hoursLeft: number; projectedAtReset: number; autonomyHours: number | null } | null;
  instantPace: number | null;
  trend: SnapshotTrend;
  ceiling: number | null;
  target: number | null;
  judgement: { verdict: SnapshotVerdict; reason: SnapshotReason; autonomyHours: number | null };
  chart: ChartDay[];
  stats: { peak: number | null; average: number | null; streak: number | null };
  rating: { stars: number; average: number } | null;
  tip: DailyTip | null;
  tokenYield: TokenYield | null;
}

export interface PacingSnapshot {
  critical: number;
  windows: SnapshotWindow[];
}

/** A snapshot read: the usage document (as /usage serves it) and its pacing. */
export interface ServiceSnapshotRead {
  result: CoreUsageResult;
  pacing: PacingSnapshot;
}

class Shape extends Error {}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function record(value: unknown, what: string): Record<string, unknown> {
  if (!isRecord(value)) throw new Shape(`${what} is not an object`);
  return value;
}

function num(value: unknown, what: string): number {
  if (typeof value !== 'number' || !Number.isFinite(value)) throw new Shape(`${what} is not a number`);
  return value;
}

function numOrNull(value: unknown, what: string): number | null {
  return value === null ? null : num(value, what);
}

function bool(value: unknown, what: string): boolean {
  if (typeof value !== 'boolean') throw new Shape(`${what} is not a boolean`);
  return value;
}

function str(value: unknown, what: string): string {
  if (typeof value !== 'string') throw new Shape(`${what} is not a string`);
  return value;
}

function oneOf<T extends string>(value: unknown, allowed: readonly T[], what: string): T {
  if (typeof value !== 'string' || !(allowed as readonly string[]).includes(value)) throw new Shape(`${what} is not one of ${allowed.join(', ')}`);
  return value as T;
}

function nullable<T>(value: unknown, parse: (v: Record<string, unknown>) => T, what: string): T | null {
  return value === null ? null : parse(record(value, what));
}

function list(value: unknown, what: string): unknown[] {
  if (!Array.isArray(value)) throw new Shape(`${what} is not a list`);
  return value as unknown[];
}

const VERDICTS: readonly SnapshotVerdict[] = ['exhausted', 'at-risk', 'behind', 'on-track', 'ahead', 'no-pacing'];
const REASONS = ['hourly', 'pace', 'ratio', 'autonomy', 'projection'] as const;
const TRENDS = ['accelerating', 'slowing-down', 'steady'] as const;
const TIP_KEYS: readonly DailyTip['key'][] = ['none', 'autonomy', 'rating', 'nearReset', 'nearResetToday', 'cause'];

function parseChartDay(value: unknown, at: string): ChartDay {
  const d = record(value, at);
  return {
    date: str(d.day, `${at}.day`),
    delta: numOrNull(d.delta, `${at}.delta`),
    fullShare: numOrNull(d.fullShare, `${at}.fullShare`),
    dayUnit: num(d.dayUnit, `${at}.dayUnit`),
    budget: numOrNull(d.budget, `${at}.budget`),
    upcoming: bool(d.upcoming, `${at}.upcoming`),
    overBudget: bool(d.overBudget, `${at}.overBudget`),
  };
}

function parseTip(value: unknown, at: string): DailyTip | null {
  if (value === null) return null;
  const tip = record(value, at);
  const params = record(tip.params, `${at}.params`);
  const numbers: Record<string, number> = {};
  for (const [key, v] of Object.entries(params)) numbers[key] = num(v, `${at}.params.${key}`);
  return { key: oneOf(tip.key, TIP_KEYS, `${at}.key`), params: numbers };
}

function parseWindow(value: unknown, at: string): SnapshotWindow {
  const w = record(value, at);
  const judgement = record(w.judgement, `${at}.judgement`);
  const stats = record(w.stats, `${at}.stats`);
  return {
    id: str(w.id, `${at}.id`),
    used: numOrNull(w.used, `${at}.used`),
    daysToReset: numOrNull(w.daysToReset, `${at}.daysToReset`),
    workingUnitsToReset: numOrNull(w.workingUnitsToReset, `${at}.workingUnitsToReset`),
    efficiency: numOrNull(w.efficiency, `${at}.efficiency`),
    usedToday: numOrNull(w.usedToday, `${at}.usedToday`),
    todayBudget: numOrNull(w.todayBudget, `${at}.todayBudget`),
    paceAlert: bool(w.paceAlert, `${at}.paceAlert`),
    redistribution: nullable(w.redistribution, (r) => ({
      perUnit: num(r.perUnit, `${at}.redistribution.perUnit`),
      evenShare: num(r.evenShare, `${at}.redistribution.evenShare`),
      units: num(r.units, `${at}.redistribution.units`),
    }), `${at}.redistribution`),
    summary: nullable(w.summary, (s) => ({
      pace: numOrNull(s.pace, `${at}.summary.pace`),
      projection: numOrNull(s.projection, `${at}.summary.projection`),
      autonomy: numOrNull(s.autonomy, `${at}.summary.autonomy`),
      preliminary: bool(s.preliminary, `${at}.summary.preliminary`),
    }), `${at}.summary`),
    hourly: nullable(w.hourly, (h) => ({
      hoursLeft: num(h.hoursLeft, `${at}.hourly.hoursLeft`),
      projectedAtReset: num(h.projectedAtReset, `${at}.hourly.projectedAtReset`),
      autonomyHours: numOrNull(h.autonomyHours, `${at}.hourly.autonomyHours`),
    }), `${at}.hourly`),
    instantPace: numOrNull(w.instantPace, `${at}.instantPace`),
    trend: w.trend === null ? null : oneOf(w.trend, TRENDS, `${at}.trend`),
    ceiling: numOrNull(w.ceiling, `${at}.ceiling`),
    target: numOrNull(w.target, `${at}.target`),
    judgement: {
      verdict: oneOf(judgement.verdict, VERDICTS, `${at}.judgement.verdict`),
      reason: judgement.reason === null ? null : oneOf(judgement.reason, REASONS, `${at}.judgement.reason`),
      autonomyHours: numOrNull(judgement.autonomyHours, `${at}.judgement.autonomyHours`),
    },
    chart: list(w.chart, `${at}.chart`).map((d, i) => parseChartDay(d, `${at}.chart[${String(i)}]`)),
    stats: {
      peak: numOrNull(stats.peak, `${at}.stats.peak`),
      average: numOrNull(stats.average, `${at}.stats.average`),
      streak: numOrNull(stats.streak, `${at}.stats.streak`),
    },
    rating: nullable(w.rating, (r) => ({ stars: num(r.stars, `${at}.rating.stars`), average: num(r.average, `${at}.rating.average`) }), `${at}.rating`),
    tip: parseTip(w.tip, `${at}.tip`),
    tokenYield: nullable(w.tokenYield, (y) => ({
      tokensPerPercent: num(y.tokensPerPercent, `${at}.tokenYield.tokensPerPercent`),
      trendPercent: numOrNull(y.trendPercent, `${at}.tokenYield.trendPercent`),
      daysCompared: num(y.daysCompared, `${at}.tokenYield.daysCompared`),
    }), `${at}.tokenYield`),
  };
}

function parsePacing(value: unknown, windowIds: string[]): PacingSnapshot {
  const pacing = record(value, 'pacing');
  const critical = num(pacing.critical, 'pacing.critical');
  const windows = list(pacing.windows, 'pacing.windows').map((w, i) => parseWindow(w, `pacing.windows[${String(i)}]`));
  // One pacing window per usage window, in the same order (docs/CORE-CLI.md).
  if (windows.length !== windowIds.length || windows.some((w, i) => w.id !== windowIds[i])) {
    throw new Shape('pacing.windows does not follow usage.quotaWindows');
  }
  if (!Number.isInteger(critical) || critical < -1 || critical >= windows.length) throw new Shape('pacing.critical is out of range');
  return { critical, windows };
}

/**
 * Reads the service's answer to `GET /accounts/{id}/snapshot`. 200 is the usage
 * document plus `pacing`; an error document of the provider is thrown as the CLI's
 * would be, and every other answer means no usable service (parseServiceUsage).
 */
export function parseServiceSnapshot(status: number, body: string, provider: string): ServiceSnapshotRead {
  const result = parseServiceUsage(status, body, provider);
  try {
    const doc = JSON.parse(body) as unknown;
    return { result, pacing: parsePacing(record(doc, 'snapshot').pacing, result.usage.quotaWindows.map((w) => w.id)) };
  } catch (err) {
    if (err instanceof Shape || err instanceof SyntaxError) {
      throw new ServiceUnusableError('malformed', `Service snapshot: ${err.message}`, { cause: err });
    }
    throw err;
  }
}

// ---------------------------------------------------------------------------
// Mapping to the renderer's types
// ---------------------------------------------------------------------------

// The verdict's numbers, by the rule that decided it (docs/PACING.md §3.10): the
// renderer's sentence (renderer/app.ts formatVerdict) picks its form from which ones
// are there, as it did with budget.windowVerdict.
function verdictOf(w: SnapshotWindow): WindowVerdict {
  const { verdict, reason } = w.judgement;
  const r = w.redistribution;
  if ((reason === 'ratio' || reason === 'pace') && r) {
    const quota = { perUnit: r.perUnit, idealPerUnit: r.evenShare };
    return reason === 'pace' && w.summary?.pace != null ? { kind: verdict, ...quota, pacePerUnit: w.summary.pace } : { kind: verdict, ...quota };
  }
  if (verdict === 'at-risk') {
    if (reason === 'hourly' && w.judgement.autonomyHours !== null) return { kind: verdict, autonomyHours: w.judgement.autonomyHours };
    if (reason === 'autonomy' && w.summary?.autonomy != null) return { kind: verdict, autonomyWorkingDays: w.summary.autonomy };
    if (reason === 'projection' && w.summary?.projection != null) return { kind: verdict, projectedUsage: w.summary.projection };
  }
  return { kind: verdict };
}

/** One window of the snapshot as the widget reads it (QuotaWindowSnapshot). */
export function windowSnapshotOf(window: QuotaWindow, w: SnapshotWindow): QuotaWindowSnapshot {
  const rollingHours = window.periodType === 'rolling-hours';
  return {
    window,
    // The history lives in the service (#181): the widget draws the chart slots.
    dailyHistory: [],
    chart: w.chart,
    deltaStats: { peak: w.stats.peak, avg: w.stats.average, streakUnderBudget: w.stats.streak },
    verdict: verdictOf(w),
    efficiencyIndex: w.efficiency,
    projectedUsage: rollingHours ? w.hourly?.projectedAtReset ?? null : w.summary?.projection ?? null,
    // Rolling-hours windows are read in hours (`hourly`), not days.
    daysUntilReset: rollingHours ? null : w.daysToReset,
    workingDaysUntilReset: w.workingUnitsToReset,
    estimatedAutonomyWorkingDays: w.summary?.autonomy ?? null,
    todayBudget: w.todayBudget !== null && w.usedToday !== null ? { budget: w.todayBudget, usedToday: w.usedToday } : null,
    paceAlert: w.paceAlert,
    redistribution: w.redistribution ? { perUnit: w.redistribution.perUnit, idealPerUnit: w.redistribution.evenShare, unitsLeft: w.redistribution.units } : null,
    hourly: w.hourly ? { hoursLeft: w.hourly.hoursLeft, projectedAtReset: w.hourly.projectedAtReset, autonomyHours: w.hourly.autonomyHours } : null,
    preliminary: w.summary?.preliminary ?? false,
    instantRate: w.instantPace,
    // The gauge's reference is the target T of D-3.13 (never above the ceiling).
    sustainableRate: w.target,
    efficiencyRating: w.rating ? { stars: w.rating.stars, avgRatio: w.rating.average } : null,
    dailyTip: w.tip ?? { key: 'none', params: {} },
    tokenYield: w.tokenYield,
  };
}

/** Why an account has no current data: an error, a state the user acts on, or the service still reading it. */
export type AccountProblem = { lastError: string } | { notice: string } | { refreshing: true };

export interface AccountIdentity {
  accountId: string;
  provider: ProviderId;
  label: string;
}

// The fields of the critical window, kept on the account (tray, default view).
function withCritical(base: Omit<AccountSnapshot, 'criticalWindow' | 'dailyHistory' | 'efficiencyIndex' | 'projectedUsage' | 'daysUntilReset' | 'workingDaysUntilReset' | 'estimatedAutonomyWorkingDays'>, critical: number): AccountSnapshot {
  const c = base.windows[critical];
  return {
    ...base,
    criticalWindow: c?.window ?? null,
    dailyHistory: [],
    efficiencyIndex: c?.efficiencyIndex ?? null,
    projectedUsage: c?.projectedUsage ?? null,
    daysUntilReset: c?.daysUntilReset ?? null,
    workingDaysUntilReset: c?.workingDaysUntilReset ?? null,
    estimatedAutonomyWorkingDays: c?.estimatedAutonomyWorkingDays ?? null,
  };
}

/**
 * The account as the widget reads it, from a service snapshot. A stale snapshot carries
 * the last known values with the problem of the last read.
 */
export function accountFromSnapshot(identity: AccountIdentity, read: ServiceSnapshotRead, problem?: AccountProblem): AccountSnapshot {
  const { usage, fetchedAt } = read.result;
  const windows = usage.quotaWindows.map((window, i) => {
    const w = read.pacing.windows[i];
    if (!w) throw new CoreOutputError('Service snapshot: pacing window missing');
    return windowSnapshotOf(window, w);
  });
  return withCritical({
    ...usage,
    ...identity,
    windows,
    lastUpdatedAt: fetchedAt,
    ...(problem ? { stale: true, ...problem } : { stale: false }),
  }, read.pacing.critical);
}

/**
 * The account without a service (owner decision D1 on #181): the raw values the CLI
 * read, no pacing, and `serviceDown` so the widget says why. The most used window
 * opens first.
 */
export function accountWithoutService(identity: AccountIdentity, result: CoreUsageResult | null, problem?: AccountProblem): AccountSnapshot {
  return accountWithoutPacing(identity, result, problem, true);
}

/** An account with only raw values (or none): without a service, or before its first good read. */
export function accountWithoutPacing(identity: AccountIdentity, result: CoreUsageResult | null, problem: AccountProblem | undefined, serviceDown: boolean): AccountSnapshot {
  const usage = result?.usage ?? { planTier: null, subscriptionRenewsAt: null, quotaWindows: [] };
  const windows = usage.quotaWindows.map((window): QuotaWindowSnapshot => ({
    window,
    dailyHistory: [],
    chart: [],
    deltaStats: { peak: null, avg: null, streakUnderBudget: null },
    verdict: { kind: window.total !== null && window.used >= window.total ? 'exhausted' : 'no-pacing' },
    efficiencyIndex: null,
    projectedUsage: null,
    daysUntilReset: null,
    workingDaysUntilReset: null,
    estimatedAutonomyWorkingDays: null,
    todayBudget: null,
    paceAlert: false,
    redistribution: null,
    hourly: null,
    preliminary: false,
    instantRate: null,
    sustainableRate: null,
    efficiencyRating: null,
    dailyTip: { key: 'none', params: {} },
    tokenYield: null,
  }));
  const ratio = (w: QuotaWindow): number => (w.total ? w.used / w.total : w.unit === 'percentage' ? w.used / 100 : -1);
  let critical = -1;
  windows.forEach((w, i) => {
    const best = windows[critical];
    if (!best || ratio(w.window) > ratio(best.window)) critical = i;
  });
  return withCritical({
    ...usage,
    ...identity,
    windows,
    ...(serviceDown ? { serviceDown: true } : {}),
    ...(result ? { lastUpdatedAt: result.fetchedAt } : {}),
    ...(problem ? { stale: true, ...problem } : { stale: false }),
  }, critical);
}

// ---------------------------------------------------------------------------
// History for the diagnostic report (#339)
// ---------------------------------------------------------------------------

export interface ServiceHistory {
  days: number;
  windows: { id: string; daily: { day: string; used: number; dayStartUsed: number | null }[]; samplesToday: { at: string; used: number }[] }[];
}

/** `GET /accounts/{id}/history`: 200 is the history; anything else means no usable service. */
export function parseServiceHistory(status: number, body: string, provider: string): ServiceHistory {
  if (status === 401) throw new ServiceUnusableError('unauthorized', 'Service refused the token');
  if (status === 404) throw new ServiceUnusableError('unknown_account', 'Account not served by the service');
  if (status !== 200) throw new ServiceUnusableError('failed', `Service answered ${String(status)}`);
  try {
    const doc = record(JSON.parse(body) as unknown, 'history document');
    if (doc.schemaVersion !== 1 || doc.provider !== provider) throw new Shape('history document is for another version or provider');
    const history = record(doc.history, 'history');
    return {
      days: num(history.days, 'history.days'),
      windows: list(history.windows, 'history.windows').map((value, i) => {
        const at = `history.windows[${String(i)}]`;
        const w = record(value, at);
        return {
          id: str(w.id, `${at}.id`),
          daily: list(w.daily, `${at}.daily`).map((p, j) => {
            const d = record(p, `${at}.daily[${String(j)}]`);
            return { day: str(d.day, `${at}.daily.day`), used: num(d.used, `${at}.daily.used`), dayStartUsed: d.dayStartUsed === undefined ? null : numOrNull(d.dayStartUsed, `${at}.daily.dayStartUsed`) };
          }),
          samplesToday: list(w.samplesToday, `${at}.samplesToday`).map((p, j) => {
            const s = record(p, `${at}.samplesToday[${String(j)}]`);
            return { at: str(s.at, `${at}.samplesToday.at`), used: num(s.used, `${at}.samplesToday.used`) };
          }),
        };
      }),
    };
  } catch (err) {
    if (err instanceof Shape || err instanceof SyntaxError) {
      throw new ServiceUnusableError('malformed', `Service history: ${err.message}`, { cause: err });
    }
    throw err;
  }
}

// ---------------------------------------------------------------------------
// Client
// ---------------------------------------------------------------------------

export type RefreshRequestMode = 'scheduled' | 'manual' | 'forced';

export interface SnapshotTimeouts {
  healthMs: number;
  // The snapshot is a view of what the service holds: it answers at once.
  snapshotMs: number;
  // POST /refresh waits for a whole run: one read at a time, each bounded at 135 s.
  refreshMs: number;
}

const DEFAULT_TIMEOUTS: SnapshotTimeouts = { healthMs: 2000, snapshotMs: 10000, refreshMs: 5 * 60 * 1000 };

export interface SnapshotClient {
  /** The account's pacing snapshot; ServiceUnusableError when there is no usable service. */
  snapshot(accountId: string, provider: string, chartDays: 7 | 30): Promise<ServiceSnapshotRead>;
  /** Asks the service's loop for a run and waits for it to end. */
  refresh(mode: RefreshRequestMode): Promise<void>;
  /** The account's history as the service keeps it, for the diagnostic report. */
  history(accountId: string, provider: string, days: number): Promise<ServiceHistory>;
}

export interface SnapshotClientOptions {
  configDir: () => string;
  fetch?: FetchLike;
  timeouts?: SnapshotTimeouts;
}

export function createSnapshotClient(options: SnapshotClientOptions): SnapshotClient {
  const fetchImpl: FetchLike = options.fetch ?? ((url, init) => fetch(url, init));
  const timeouts = options.timeouts ?? DEFAULT_TIMEOUTS;
  const account = (id: string): string => `/api/v1/accounts/${encodeURIComponent(id)}`;
  return {
    async snapshot(accountId, provider, chartDays) {
      const endpoint = await connectService(fetchImpl, options.configDir, timeouts.healthMs);
      const { status, body } = await serviceRequest(fetchImpl, endpoint, 'GET', `${account(accountId)}/snapshot?chartDays=${String(chartDays)}`, timeouts.snapshotMs);
      return parseServiceSnapshot(status, body, provider);
    },
    async refresh(mode) {
      const endpoint = await connectService(fetchImpl, options.configDir, timeouts.healthMs);
      const { status } = await serviceRequest(fetchImpl, endpoint, 'POST', '/api/v1/refresh', timeouts.refreshMs, { mode });
      if (status === 401) throw new ServiceUnusableError('unauthorized', 'Service refused the token');
      if (status !== 200) throw new ServiceUnusableError('failed', `Service refresh answered ${String(status)}`);
    },
    async history(accountId, provider, days) {
      const endpoint = await connectService(fetchImpl, options.configDir, timeouts.healthMs);
      const { status, body } = await serviceRequest(fetchImpl, endpoint, 'GET', `${account(accountId)}/history?days=${String(days)}`, timeouts.snapshotMs);
      return parseServiceHistory(status, body, provider);
    },
  };
}
