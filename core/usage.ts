// core/usage.ts — budget.ts's snapshot of an account, kept only as the reference side
// of the pacing parity (scripts/pacing-parity.js, #325; real histories #323) until the
// port to Go closes (#75, #166). The app no longer uses it (#341): the core's service
// keeps the history and computes the pace, Electron reads its snapshot
// (main/usageReader.ts). No fetch, no store, no refresh policy here: pure functions over
// an in-memory history draft.

import * as budget from '../budget';
import type {
  AccountConfig,
  AccountId,
  AccountSnapshot,
  ClaudeLocalInsights,
  DailyUsagePoint,
  QuotaWindow,
  QuotaWindowSnapshot,
  RawAccountUsage,
  RecentUsageSample,
  RenewalRule,
  WorkSchedule,
} from '../types/index';

// The history the snapshot reads and updates, in memory: daily points and recent
// samples, as Electron kept them until #341 (the parity script builds it per scenario).
export interface HistoryDraft {
  dailyUsage: DailyUsagePoint[];
  recentSamples: RecentUsageSample[];
  chartDays: number;
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
  // When the values were read: now for a CLI read, the service's fetchedAt for a
  // document of the core's service (#182), which may be minutes earlier.
  readAt: Date,
): QuotaWindowSnapshot {
  const { periodStart, periodEnd } = resolvePeriodBounds(window, subscription, providerRenewsAt, now);
  // A fresh value read on an earlier day (a service read at 23:58 picked up at 00:03)
  // belongs to that day. Today's point, if already written this morning from the older
  // value, loses its start: the recordDailyUsage below computes it again from the updated
  // earlier point (budget.updateDailyPoint: that value, or 0 on a renewal day or after a
  // reset), so the delta does not count on both days.
  const todayKey = budget.localDateKey(now);
  if (fresh && budget.localDateKey(readAt) < todayKey) {
    recordDailyUsage(draft, accountId, window, periodStart, readAt);
    const at = draft.dailyUsage.findIndex((h) => h.date === todayKey && h.accountId === accountId && h.windowId === window.id);
    const today = draft.dailyUsage[at];
    if (today) {
      draft.dailyUsage[at] = {
        date: today.date, accountId: today.accountId, windowId: today.windowId, used: today.used,
        ...(today.meta ? { meta: today.meta } : {}),
      };
    }
  }
  const todayPoint = recordDailyUsage(draft, accountId, window, periodStart, now);
  // Only a value just read from the provider is a sample: last known data repeated
  // after a failed or skipped fetch would flatten the instant pace.
  if (fresh) recordRecentSample(draft, accountId, window, readAt);
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
  const sessionStartIso = localInsights?.firstSessionStartByDay[budget.localDateKey(now)];
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
    paceAlert: todayBudget !== null && todayBudget.budget > 0 && todayBudget.usedToday > todayBudget.budget * budget.PACE_ALERT_RATIO,
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
  raw: RawAccountUsage & { accountId: AccountId; lastUpdatedAt?: string; stale?: boolean; lastError?: string; notice?: string; refreshing?: boolean },
  cfg: AccountConfig,
  now: Date,
  localInsights: ClaudeLocalInsights | null,
  fresh: boolean,
): AccountSnapshot {
  const { subscription, workSchedule } = cfg;
  const identity = { accountId: cfg.id, provider: cfg.provider, label: cfg.label };
  // A fresh value's own read time, never later than the refresh: a service document's
  // fetchedAt is earlier; a CLI read is stamped when it ends, after the refresh began, and
  // counts at `now` (budget.instantaneousRate and todayActivitySpan only see samples up
  // to now).
  const readAt = fresh && raw.lastUpdatedAt ? new Date(Math.min(Date.parse(raw.lastUpdatedAt), now.getTime())) : now;
  const windows = raw.quotaWindows.map((w) => computeWindowSnapshot(draft, cfg.id, w, subscription, raw.subscriptionRenewsAt, workSchedule, now, localInsights, fresh, readAt));
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
