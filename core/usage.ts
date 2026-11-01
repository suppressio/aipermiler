// core/usage.ts — fetch orchestration and snapshot computation, with no Electron.
// Extracted from main.ts (docs/EVOLUTION.md 2a): everything that turns the accounts
// and the provider answers into a UsageSnapshot, with the store, the provider fetch,
// the local insights, the clock and the error reporting injected (UsageCoreDeps).
// main.ts keeps the Electron wiring (windows, tray, IPC, timers, notifications); tests
// and the headless CLI (2b) build the same core over an in-memory store.

import * as budget from '../budget';
import * as refreshPolicy from '../main/refreshPolicy';
import type { AccountFetchState, RefreshMode } from '../main/refreshPolicy';
import type {
  AccountConfig,
  AccountId,
  AccountSnapshot,
  ClaudeLocalInsights,
  DailyUsagePoint,
  HistorySettings,
  ProviderId,
  QuotaWindow,
  QuotaWindowSnapshot,
  RawAccountUsage,
  RecentUsageSample,
  RenewalRule,
  UiSettings,
  UsageSnapshot,
  WorkSchedule,
} from '../types/index';

const DEFAULT_REFRESH_INTERVAL_MS = 30 * 60 * 1000; // 30 minutes, as per AGENTS.md

/** The configured refresh interval (at least 5 minutes). */
export function refreshIntervalMs(ui: Pick<UiSettings, 'refreshIntervalMinutes'>): number {
  const intervalMinutes = ui.refreshIntervalMinutes || DEFAULT_REFRESH_INTERVAL_MS / (60 * 1000);
  return Math.max(5, intervalMinutes) * 60 * 1000;
}

/** The part of the settings store the core reads and writes (main.ts: the electron-store). */
export interface UsageStore {
  accounts(): AccountConfig[];
  ui(): Pick<UiSettings, 'chartRange' | 'refreshIntervalMinutes'>;
  history(): HistorySettings;
  setHistory(history: HistorySettings): void;
  setLastGood(accountId: AccountId, usage: StampedUsage): void;
}

export interface UsageCoreDeps {
  store: UsageStore;
  /** Calls the provider service with the account credentials (main/providers.ts). */
  fetchUsage(cfg: AccountConfig): Promise<RawAccountUsage>;
  isConnected(cfg: AccountConfig): boolean;
  /**
   * Message for an account whose provider exposes no quotas (Claude since #140): the
   * account is shown with it and never fetched, so no retries, backoff or log noise.
   * null = fetch normally.
   */
  quotasUnavailable(cfg: AccountConfig): string | null;
  /** Local Claude Code insights, cached by the caller; only asked for opted-in Claude accounts. */
  localInsights(): Promise<ClaudeLocalInsights | null>;
  /** Readable message of a fetch error, shown with the stale data. */
  describeError(err: unknown): string;
  /**
   * A failed fetch that is a state the user has to act on rather than a failure (Codex
   * signed out, #178): its message, shown as AccountSnapshot.notice instead of
   * lastError. null = an error.
   */
  accountNotice(err: unknown): string | null;
  /** Called on every failed fetch, before falling back to the last good data (format drift report). */
  onFetchError(provider: ProviderId, err: unknown): void;
  now(): Date;
  logError(message: string, ...details: unknown[]): void;
}

export type StampedUsage = RawAccountUsage & { accountId: AccountId; lastUpdatedAt: string; stale: boolean; lastError?: string; notice?: string };

// Why an account has no fresh data: an error, or a state the user has to act on.
type AccountProblem = { lastError: string } | { notice: string };

// ---------------------------------------------------------------------------
// Local history: neither Claude nor Copilot provides a daily history via API (see
// docs/RESEARCH.md), so we build it ourselves, one point per day, on every successful
// refresh.
// ---------------------------------------------------------------------------
// The store is encrypted and electron-store re-reads and decrypts the whole file on
// every get (and re-encrypts and writes it on every set), synchronously on the main
// thread: one get/set pair per window per refresh could block the app for seconds on a
// slower machine (the Settings window stayed blank). A refresh therefore works on an
// in-memory draft of the history, read once and written once (commitHistoryDraft).
export interface HistoryDraft {
  dailyUsage: DailyUsagePoint[];
  recentSamples: RecentUsageSample[];
  chartDays: number;
}

function openHistoryDraft(store: UsageStore): HistoryDraft {
  const history = store.history();
  return {
    dailyUsage: history.dailyUsage,
    recentSamples: history.recentSamples,
    chartDays: store.ui().chartRange === 'month' ? 30 : 7,
  };
}

// Samples are kept for the whole current day (the working span comes from the day's
// samples, budget.todayActivitySpan) and at least this long (margin above the 1h
// lookback of budget.instantaneousRate and its anchor, also right after midnight).
// Still small: one append per fetch, every 5 min at most while consumption rises.
const RECENT_SAMPLES_MIN_AGE_MS = 4 * 60 * 60 * 1000;

// Prunes and writes the draft in a single store write. The rest of `history` is read
// again here, so what was written meanwhile (lastGood, during the fetches) is kept.
function commitHistoryDraft(store: UsageStore, draft: HistoryDraft, now: Date): void {
  const history = store.history();
  const dailyCutoff = new Date(now);
  dailyCutoff.setDate(dailyCutoff.getDate() - history.retentionDays);
  const startOfToday = new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime();
  const sampleCutoff = Math.min(startOfToday, now.getTime() - RECENT_SAMPLES_MIN_AGE_MS);
  store.setHistory({
    ...history,
    dailyUsage: draft.dailyUsage.filter((h) => new Date(h.date) >= dailyCutoff),
    recentSamples: draft.recentSamples.filter((s) => new Date(s.timestamp).getTime() >= sampleCutoff),
  });
}

// Returns today's point (with its baseline and first activity, see
// budget.updateDailyPoint), or null when utilization cannot be computed.
function recordDailyUsage(draft: HistoryDraft, accountId: AccountId, window: QuotaWindow, periodStart: Date, now: Date): DailyUsagePoint | null {
  const utilization = budget.normalizedUtilization(window);
  if (utilization === null) return null;

  const todayKey = budget.localDateKey(now);
  const history = draft.dailyUsage;
  const own = history
    .filter((h) => h.accountId === accountId && h.windowId === window.id)
    .sort((a, b) => a.date.localeCompare(b.date));
  // First point stored with the wrong baseline on the period start day (see
  // budget.repairFirstDayBaseline): fixed in place before today's point is computed.
  const repaired = budget.repairFirstDayBaseline(own, periodStart);
  if (repaired) {
    const at = history.findIndex((h) => h.date === repaired.date && h.accountId === accountId && h.windowId === window.id);
    if (at >= 0) history[at] = repaired;
    const ownAt = own.findIndex((h) => h.date === repaired.date);
    if (ownAt >= 0) own[ownAt] = repaired;
  }
  const today = own.find((h) => h.date === todayKey);
  const previous = own.filter((h) => h.date < todayKey).at(-1);
  // Same rounding as the recent samples (recordRecentSample): a baseline rounded to 0.1
  // against samples rounded to 0.01 read as activity (budget.todayActivitySpan).
  const entry = budget.updateDailyPoint({
    today, previous, accountId, windowId: window.id, used: Math.round(utilization * 100) / 100, periodStart, now,
  });
  const idx = history.findIndex((h) => h.date === todayKey && h.accountId === accountId && h.windowId === window.id);
  if (idx >= 0) history[idx] = entry;
  else history.push(entry);
  return entry;
}

function getDailyHistory(draft: HistoryDraft, accountId: AccountId, windowId: string, days: number): DailyUsagePoint[] {
  return draft.dailyUsage
    .filter((h) => h.accountId === accountId && h.windowId === windowId)
    .sort((a, b) => a.date.localeCompare(b.date))
    .slice(-days);
}

function recordRecentSample(draft: HistoryDraft, accountId: AccountId, window: QuotaWindow, now: Date): void {
  const utilization = budget.normalizedUtilization(window);
  if (utilization === null) return;
  draft.recentSamples.push({ timestamp: now.toISOString(), accountId, windowId: window.id, used: Math.round(utilization * 100) / 100 });
}

function getRecentSamples(draft: HistoryDraft, accountId: AccountId, windowId: string): RecentUsageSample[] {
  return draft.recentSamples
    .filter((s) => s.accountId === accountId && s.windowId === windowId)
    .sort((a, b) => a.timestamp.localeCompare(b.timestamp));
}

// ---------------------------------------------------------------------------
// Period bounds for efficiency/projection/reset:
// - the period end is the window's own resetsAt when it has one (Claude rolling
//   windows, Copilot quotas and billing report), then the renewal date reported by
//   the provider (subscriptionRenewsAt), and only then the renewal day entered by hand
//   (renewalRule — e.g. the Claude spend limit, which carries no date);
// - the period start subtracts the window length from the end: hours or days for
//   rolling windows, calendar months for a billing cycle (months differ in length).
// NOTE: for Claude's "five_hour" window the day/half-day granularity of budget.ts is
// too coarse for a truly meaningful efficiency — the value stays consistent, but it
// should be read mostly as a current indicator, not as reliable pacing on such a short
// window.
// ---------------------------------------------------------------------------
export function resolvePeriodBounds(
  criticalWindow: QuotaWindow | null,
  subscription: { renewalRule: RenewalRule },
  providerRenewsAt: Date | string | null,
  now: Date,
): { periodStart: Date; periodEnd: Date } {
  const providerEnd = criticalWindow?.resetsAt ?? providerRenewsAt;
  const periodEnd = providerEnd
    ? new Date(providerEnd)
    : budget.resolveRenewalDate(subscription.renewalRule, now);
  if (criticalWindow && criticalWindow.periodType !== 'billing-cycle') {
    const spanMs = criticalWindow.periodType === 'rolling-hours'
      ? (criticalWindow.periodLength ?? 0) * 3600 * 1000
      : (criticalWindow.periodLength ?? 0) * 24 * 3600 * 1000;
    return { periodStart: new Date(periodEnd.getTime() - spanMs), periodEnd };
  }
  const periodStart = new Date(periodEnd);
  periodStart.setMonth(periodStart.getMonth() - (criticalWindow?.periodLength ?? 1));
  return { periodStart, periodEnd };
}

// Below this many working units elapsed, projection and autonomy are extrapolated from
// too little data: the widget marks them as a preliminary estimate.
const PRELIMINARY_WORKING_UNITS = 2;

export function computeWindowSnapshot(
  draft: HistoryDraft,
  accountId: AccountId,
  window: QuotaWindow,
  subscription: { renewalRule: RenewalRule },
  providerRenewsAt: Date | string | null,
  workSchedule: WorkSchedule,
  now: Date,
  localInsights: ClaudeLocalInsights | null,
  fresh: boolean,
): QuotaWindowSnapshot {
  const { periodStart, periodEnd } = resolvePeriodBounds(window, subscription, providerRenewsAt, now);
  const todayPoint = recordDailyUsage(draft, accountId, window, periodStart, now);
  // Only a value just read from the provider is a sample: last known data repeated
  // after a failed or skipped fetch would flatten the instant pace.
  if (fresh) recordRecentSample(draft, accountId, window, now);
  const { chartDays } = draft;
  // chartDays + 1 points: N+1 cumulative values are needed for N daily deltas
  // (consumption-per-day chart and rating) — the rating used to see only N-1.
  const dailyHistory = getDailyHistory(draft, accountId, window.id, chartDays + 1);
  const recentSamples = getRecentSamples(draft, accountId, window.id);

  const pacingAvailable = budget.hasPacing(window);
  const totalPeriodWorkingUnits = budget.workingUnitsBetween(periodStart, periodEnd, workSchedule);
  const isRollingHours = window.periodType === 'rolling-hours';
  // Same gate as the rating: on a window of a few hours a "daily" delta spans several
  // resets and measures nothing. Without pacing the deltas remain, but with no ideal
  // share (idealShare null).
  const dailyDeltasForWindow = isRollingHours
    ? []
    : budget.dailyDeltas(dailyHistory, workSchedule, pacingAvailable ? totalPeriodWorkingUnits : 0);
  // Actual working hours: today's span from the first to the last increase among the
  // day's samples, started earlier by today's first local Claude Code session when
  // known (budget.todayActivitySpan); the recent pace sharpens projection and autonomy
  // (budget.recentPacePerUnit).
  const sessionStartIso = localInsights?.firstSessionStartByDay?.[budget.localDateKey(now)];
  const span = budget.todayActivitySpan(
    recentSamples, todayPoint?.dayStartUsed ?? null, now, sessionStartIso ? new Date(sessionStartIso) : null,
  );
  const workedHours = span ? (span.end.getTime() - span.start.getTime()) / (3600 * 1000) : null;
  const todayElapsedUnits = budget.todayElapsedUnits(now, workedHours, workSchedule);
  const ctx = {
    window, workSchedule, periodStart, periodEnd, now, todayElapsedUnits,
    recentPacePerUnit: budget.recentPacePerUnit(dailyDeltasForWindow, workSchedule, now),
  };
  // The star rating is based on daily deltas over a window of `chartDays` days (7/30,
  // the same view chosen by the user for the chart — consistency between indicators): it
  // makes no sense for a window renewing every few hours (e.g. Claude's "five_hour", see
  // the note above resolvePeriodBounds) — there the consumption of "one day" can span
  // several resets, so an ideal/actual daily ratio is no longer meaningful. In that case
  // only the %/h gauge remains (instantRate/sustainableRate below), consistent at any
  // time scale.
  const ratingAvailable = pacingAvailable && window.periodType !== 'rolling-hours';

  // A window of a few hours is read in hours (budget.hourlyOutlook): the working-day
  // metrics (days to reset, autonomy in days, "preliminary") mean nothing there.
  const instantRate = budget.instantaneousRate(recentSamples, now);
  const hourly = isRollingHours ? budget.hourlyOutlook(window, now, instantRate) : null;
  const efficiencyIndex = pacingAvailable && !isRollingHours ? budget.efficiencyIndex(ctx) : null;
  const projectedUsage = isRollingHours
    ? hourly?.projectedAtReset ?? null
    : pacingAvailable ? budget.projectedUsage(ctx) : null;
  const daysUntilReset = isRollingHours ? null : budget.daysUntilReset(periodEnd, now);
  const workingDaysUntilReset = isRollingHours ? null : budget.workingDaysUntilReset(periodEnd, workSchedule, now, todayElapsedUnits);
  const estimatedAutonomyWorkingDays = pacingAvailable && !isRollingHours ? budget.estimatedAutonomyWorkingDays(ctx) : null;
  // Not gated by pacingAvailable when the window has its own resetsAt: knowing the
  // reset is enough, even with an unknown period start (e.g. one-off credits) — see
  // budget.sustainableHourlyRate. Without one, the renewal-rule period end is used
  // only for windows known to follow the billing cycle (pacingAvailable). Spread over
  // the remaining WORKING hours (not on the 5-hour window): working units × hours per
  // day also with the schedule disabled, where every day is a working day — over 24
  // calendar hours a day the target was several times below any pace measured while
  // working, so the gauge was always red.
  const workingHoursLeft = isRollingHours
    ? null
    : budget.remainingWorkingUnits(periodEnd, now, workSchedule, todayElapsedUnits) * workSchedule.hoursPerDay;
  const sustainableRate = budget.sustainableHourlyRate(window, window.resetsAt ?? (pacingAvailable ? periodEnd : null), now, workingHoursLeft);
  const efficiencyRating = ratingAvailable
    ? budget.efficiencyRating(dailyHistory, workSchedule, totalPeriodWorkingUnits, now, chartDays)
    : null;
  // Value per token (docs/EVOLUTION.md point 4): only when the account has local insights
  // enabled — crosses tokens per day with this window's deltas.
  const localDaily = localInsights?.daily ?? null;
  const todayBudget = pacingAvailable && !isRollingHours
    ? budget.todayBudget(ctx, todayPoint?.dayStartUsed ?? null)
    : null;
  // The remaining quota redistributed per working day: the verdict and the hint under
  // today's budget rest on it (same scope as todayBudget: no daily budget on rolling hours).
  const redistribution = pacingAvailable && !isRollingHours ? budget.redistributedQuota(ctx) : null;
  const preliminary = pacingAvailable && !isRollingHours
    && budget.elapsedWorkingUnits(periodStart, periodEnd, now, workSchedule, todayElapsedUnits) < PRELIMINARY_WORKING_UNITS;
  const pacePerUnit = pacingAvailable && !isRollingHours ? budget.currentPacePerUnit(ctx) : null;
  // Not on rolling-hours windows (no daily reading there, see dailyDeltasForWindow).
  const chart = isRollingHours ? [] : budget.chartDays({
    dailyHistory, workSchedule, periodStart, periodEnd, now, todayBudget, redistribution,
    totalPeriodWorkingUnits: pacingAvailable ? totalPeriodWorkingUnits : 0,
    pastDays: chartDays,
    upcomingDays: chartDays === 30 ? 5 : 2,
  });

  return {
    window,
    dailyHistory,
    chart,
    deltaStats: budget.deltaStats(chart, now),
    verdict: budget.windowVerdict({
      window, projectedUsage, workingDaysUntilReset, estimatedAutonomyWorkingDays, redistribution, pacePerUnit, preliminary, hourly,
    }),
    efficiencyIndex,
    projectedUsage,
    daysUntilReset,
    workingDaysUntilReset,
    estimatedAutonomyWorkingDays,
    todayBudget,
    redistribution,
    hourly,
    preliminary,
    instantRate,
    sustainableRate,
    efficiencyRating,
    tokenYield: localDaily ? budget.tokenYield(localDaily, dailyDeltasForWindow) : null,
    dailyTip: budget.generateDailyTip({
      window,
      daysUntilReset,
      workingDaysUntilReset,
      estimatedAutonomyWorkingDays,
      efficiencyRating,
      consumptionCause: localDaily ? budget.consumptionCause(localDaily, dailyDeltasForWindow) : null,
      preliminary,
    }),
  };
}

export function computeAccountSnapshot(
  draft: HistoryDraft,
  raw: RawAccountUsage & { accountId: AccountId; lastUpdatedAt?: string; stale?: boolean; lastError?: string; notice?: string },
  cfg: AccountConfig,
  now: Date,
  localInsights: ClaudeLocalInsights | null,
  fresh: boolean,
): AccountSnapshot {
  const { subscription, workSchedule } = cfg;
  const identity = { accountId: cfg.id, provider: cfg.provider, label: cfg.label };
  const windows = raw.quotaWindows.map((w) => computeWindowSnapshot(draft, cfg.id, w, subscription, raw.subscriptionRenewsAt, workSchedule, now, localInsights, fresh));
  // The window needing attention first (at risk/exhausted), then the most used.
  const criticalSnapshot = budget.pickCriticalSnapshot(windows) ?? undefined;
  const criticalWindow = criticalSnapshot?.window ?? null;

  if (!criticalWindow || !criticalSnapshot) {
    return {
      ...raw,
      ...identity,
      windows,
      criticalWindow: null,
      dailyHistory: [],
      efficiencyIndex: null,
      projectedUsage: null,
      daysUntilReset: null,
      workingDaysUntilReset: null,
      estimatedAutonomyWorkingDays: null,
    };
  }

  return {
    ...raw,
    ...identity,
    windows,
    criticalWindow,
    dailyHistory: criticalSnapshot.dailyHistory,
    efficiencyIndex: criticalSnapshot.efficiencyIndex,
    projectedUsage: criticalSnapshot.projectedUsage,
    daysUntilReset: criticalSnapshot.daysUntilReset,
    workingDaysUntilReset: criticalSnapshot.workingDaysUntilReset,
    estimatedAutonomyWorkingDays: criticalSnapshot.estimatedAutonomyWorkingDays,
  };
}

// Placeholder shown when an account is connected/enabled but the fetch failed and
// there is no previous data yet (store.history.lastGood.*): without it, the renderer
// cannot tell "not connected" from "connected but the sync just failed", and shows the
// wrong message ("No account connected") even when the account IS connected — see
// AGENTS.md, "never a blank screen or silent failure".
function emptyAccountSnapshot(cfg: AccountConfig, problem: AccountProblem): AccountSnapshot {
  return {
    accountId: cfg.id,
    provider: cfg.provider,
    label: cfg.label,
    planTier: null,
    subscriptionRenewsAt: null,
    quotaWindows: [],
    windows: [],
    criticalWindow: null,
    dailyHistory: [],
    efficiencyIndex: null,
    projectedUsage: null,
    daysUntilReset: null,
    workingDaysUntilReset: null,
    estimatedAutonomyWorkingDays: null,
    stale: true,
    ...problem,
  };
}

// The last result of an account not fetched in this refresh: stale with the reason
// while it is in backoff after a failure.
function cachedUsage(lastGood: Omit<StampedUsage, 'stale'>, problem: AccountProblem | undefined): StampedUsage {
  return problem === undefined ? { ...lastGood, stale: false } : { ...lastGood, stale: true, ...problem };
}

// Whether any window of a freshly fetched account rose since its last sample: the
// next refresh then comes at the fast pace (main/refreshPolicy.ts).
function windowsRose(draft: HistoryDraft, accountId: AccountId, windows: QuotaWindow[]): boolean {
  return windows.some((window) => {
    const utilization = budget.normalizedUtilization(window);
    const last = getRecentSamples(draft, accountId, window.id).at(-1);
    return utilization !== null && last !== undefined && utilization - last.used > budget.ACTIVITY_EPSILON;
  });
}

// Per-account fetch attempts, in memory (main/refreshPolicy.ts): the backoff of a
// failing provider and the short cache of manual refreshes. Lost on restart, which
// simply means the first refresh fetches everything.
// Last fetch error of each account, until a fetch succeeds: a refresh that skips a
// failing account (backoff) still shows its data as stale, with the reason.
export function createUsageCore(deps: UsageCoreDeps): { buildUsageSnapshot(mode: RefreshMode): Promise<{ snapshot: UsageSnapshot; consumptionRose: boolean | null }> } {
  const { store } = deps;
  const accountFetchStates = new Map<AccountId, AccountFetchState>();
  const accountFetchErrors = new Map<AccountId, AccountProblem>();

  function describeProblem(err: unknown): AccountProblem {
    const notice = deps.accountNotice(err);
    return notice === null ? { lastError: deps.describeError(err) } : { notice };
  }

  async function fetchAccountOrFallback(cfg: AccountConfig): Promise<StampedUsage> {
    const accountId = cfg.id;
    try {
      const raw = await deps.fetchUsage(cfg);
      accountFetchStates.set(accountId, refreshPolicy.recordFetchAttempt(accountFetchStates.get(accountId), deps.now().getTime(), true));
      accountFetchErrors.delete(accountId);
      const stamped: StampedUsage = { ...raw, accountId, lastUpdatedAt: deps.now().toISOString(), stale: false };
      store.setLastGood(accountId, stamped);
      return stamped;
    } catch (err) {
      accountFetchStates.set(accountId, refreshPolicy.recordFetchAttempt(accountFetchStates.get(accountId), deps.now().getTime(), false));
      const error = err as Error;
      deps.logError(`[main] refresh ${accountId} failed:`, error.message);
      // Reported here (not only in the caller) because a fallback to valid previous data
      // "absorbs" the error below — without this call a format drift appearing AFTER the
      // first successful fetch would never be detected.
      deps.onFetchError(cfg.provider, err);
      const lastGood = store.history().lastGood?.[accountId];
      const problem = describeProblem(err);
      accountFetchErrors.set(accountId, problem);
      if (!lastGood) throw err; // no previous data: propagate, the caller decides how to show it
      return { ...lastGood, stale: true, ...problem };
    }
  }

  // `consumptionRose` is null when no account was fetched (all reused from the last
  // refresh): nothing new is known, the pace stays as it was.
  async function buildUsageSnapshot(mode: RefreshMode): Promise<{ snapshot: UsageSnapshot; consumptionRose: boolean | null }> {
    const now = deps.now();
    const snapshot: UsageSnapshot = { generatedAt: now.toISOString(), accounts: [] };
    const intervalMs = refreshIntervalMs(store.ui());
    let consumptionRose: boolean | null = null;

    // Read once here, written once at the end (see HistoryDraft).
    const draft = openHistoryDraft(store);
    for (const cfg of store.accounts()) {
      if (!cfg.enabled || !deps.isConnected(cfg)) continue;
      // Local source independent of the account fetch: computed first (the per-window
      // yield needs it) and shown even if the provider did not answer.
      const localInsights = cfg.provider === 'claude' && cfg.localInsights ? await deps.localInsights() : null;
      let account: AccountSnapshot;
      const unavailable = deps.quotasUnavailable(cfg);
      if (unavailable !== null) {
        account = emptyAccountSnapshot(cfg, { lastError: unavailable });
      } else {
        try {
          // An account in backoff, or fetched moments ago, keeps its last result: nothing
          // new is learned, so no sample is recorded (a repeated old value at a new time
          // would read as a pause in the instant pace).
          const lastGood = store.history().lastGood?.[cfg.id];
          const fetchNow = !lastGood || refreshPolicy.shouldFetchAccount(accountFetchStates.get(cfg.id), mode, now.getTime(), intervalMs);
          const raw = fetchNow
            ? await fetchAccountOrFallback(cfg)
            : cachedUsage(lastGood, accountFetchErrors.get(cfg.id));
          const fresh = fetchNow && !raw.stale;
          if (fetchNow) consumptionRose = consumptionRose === true || (fresh && windowsRose(draft, cfg.id, raw.quotaWindows));
          account = computeAccountSnapshot(draft, raw, cfg, now, localInsights, fresh);
        } catch (err) {
          const problem = describeProblem(err);
          deps.logError(`[main] ${cfg.label} unavailable and no previous data:`, 'notice' in problem ? problem.notice : problem.lastError);
          account = emptyAccountSnapshot(cfg, problem);
        }
      }
      if (localInsights) account.localInsights = localInsights;
      snapshot.accounts.push(account);
    }
    commitHistoryDraft(store, draft, now);

    return { snapshot, consumptionRose };
  }

  return { buildUsageSnapshot };
}
