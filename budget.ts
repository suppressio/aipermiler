// budget.ts — budget/efficiency/projection logic (see docs/ARCHITECTURE.md §0 and §3)
//
// Model: every account (Claude/Copilot) exposes one or more QuotaWindow:
//   { id, label, periodType, periodLength, unit: 'percentage'|'count', used, total, resetsAt }
// - unit 'percentage': used is already 0-100 (Claude case: no known total in tokens).
// - unit 'count': used/total are absolute values (Copilot case: premium requests/credits).
//
// All functions are pure (no I/O), testable from the terminal/test runner.

import { addDays, differenceInCalendarDays, isBefore, startOfDay, setDate, addMonths, getDaysInMonth } from 'date-fns';
import type {
  QuotaWindow,
  WorkSchedule,
  RenewalRule,
  DailyUsagePoint,
  EfficiencyRating,
  DailyDelta,
  ChartDay,
  DeltaStats,
  WindowVerdict,
  LocalDailyTokens,
  TokenYield,
  DailyTip,
  ConsumptionCause,
  TodayBudget,
  Redistribution,
  HourlyOutlook,
} from './types/index';

const DAY_KEYS = ['sun', 'mon', 'tue', 'wed', 'thu', 'fri', 'sat'] as const;

/**
 * Calendar-day key (YYYY-MM-DD) in LOCAL time — the key of history.dailyUsage and of
 * the per-day notification flags. Not `toISOString().slice(0, 10)`: that is the UTC
 * day, so a refresh between local midnight and the UTC offset landed on the previous
 * day, while every pacing function reasons in local days.
 */
export function localDateKey(date: Date): string {
  const month = String(date.getMonth() + 1).padStart(2, '0');
  const day = String(date.getDate()).padStart(2, '0');
  return `${String(date.getFullYear())}-${month}-${day}`;
}

/**
 * Local midnight of a YYYY-MM-DD key. Not `new Date(key)`, which parses a date-only
 * string as UTC midnight: west of Greenwich that is the previous local day (wrong
 * weekday, wrong working unit).
 */
export function parseDateKey(key: string): Date {
  const [year = NaN, month = NaN, day = NaN] = key.split('-').map(Number);
  return new Date(year, month - 1, day);
}

/**
 * Working unit of a single calendar day: 1 (full), 0.5 (half), 0 (off).
 * When `workSchedule.enabled` is false (schedule disabled, e.g. a personal account
 * with no days/hours to respect), every day counts as 1 regardless of `days` — pacing
 * goes back to treating calendar days uniformly. The field is always present: older
 * stores get it from store/normalize.ts.
 */
export function getDayUnit(date: Date, workSchedule: WorkSchedule): number {
  if (!workSchedule.enabled) return 1;
  const key = DAY_KEYS[date.getDay()];
  if (!key) return 0;
  const status = workSchedule.days[key];
  if (status === 'full') return 1;
  if (status === 'half') return 0.5;
  return 0;
}

/**
 * Sums the working units over the calendar days in [startDate, endDate).
 * If endDate precedes startDate, returns 0 (no negative units).
 */
export function workingUnitsBetween(startDate: Date | string, endDate: Date | string, workSchedule: WorkSchedule): number {
  const start = startOfDay(new Date(startDate));
  const end = startOfDay(new Date(endDate));
  if (!isBefore(start, end)) return 0;

  let units = 0;
  let cursor = start;
  while (isBefore(cursor, end)) {
    units += getDayUnit(cursor, workSchedule);
    cursor = addDays(cursor, 1);
  }
  return units;
}

/** Utilization normalized to a 0-100 percentage, or null when it cannot be computed (count without total). */
export function normalizedUtilization(win: QuotaWindow): number | null {
  if (win.unit === 'percentage') return win.used;
  if (typeof win.total === 'number' && win.total > 0) {
    return (win.used / win.total) * 100;
  }
  return null;
}

/**
 * Whether a window's period start can be derived, so that pacing (efficiency,
 * projection, today's budget, verdict) can be computed. A "billing-cycle" window with
 * an unknown periodLength (e.g. credits recognized only by the shape of the value in
 * services/claude.ts, both recurring and one-off: there is no way to tell them apart
 * without guessing an undocumented format) has no certain period start: no pacing
 * fabricated on a fictitious span, only daysUntilReset. Known monthly windows (Claude
 * spend limit, Copilot quotas) declare periodLength 1 and get full pacing.
 */
export function hasPacing(window: QuotaWindow): boolean {
  return window.periodType !== 'billing-cycle' || window.periodLength !== null;
}

/**
 * Picks the most critical quota window (highest normalized utilization). On equal
 * utilization a window with pacing wins: two windows reporting the same value (e.g. the same
 * budget read from two objects of the response) must not make the unpaced one the
 * default view, with every pacing metric empty.
 */
export function pickCriticalWindow(quotaWindows: QuotaWindow[]): QuotaWindow | null {
  const [first] = quotaWindows;
  if (!first) return null;
  const [top] = quotaWindows
    .map((w) => ({ window: w, utilization: normalizedUtilization(w) }))
    .filter((x): x is { window: QuotaWindow; utilization: number } => x.utilization !== null)
    .sort((a, b) => b.utilization - a.utilization || Number(hasPacing(b.window)) - Number(hasPacing(a.window)));
  return top ? top.window : first;
}

// Shortest stretch of work a day is assumed to have covered once activity is seen:
// right after the first refresh with consumption a few minutes of work would turn a
// normal first increment into an absurd pace (2% in 15 min → "out of quota by noon").
const MIN_OBSERVED_WORK_HOURS = 2;

// Smallest rise (quota percentage points) counted as activity — see todayActivitySpan.
export const ACTIVITY_EPSILON = 0.05;

/**
 * Today's working span, from the day's own data (user feedback: "from the first data
 * point of the day to the last"), not from a single recorded start up to `now`:
 * - an increase is a sample whose `used` is above the previous one; the first sample of
 *   the day is compared with `dayStartUsed` (yesterday's value, 0 after a reset). A
 *   drop is a reset, not activity;
 * - `start`: the sample just before the first increase, when it is from today (work
 *   began after it); otherwise the first increase itself (work began before the app
 *   saw it). Moved earlier to `sessionStart` (first local Claude Code session of the
 *   day) when that is earlier;
 * - `end`: the last increase — once work stops the span stops growing.
 * Null when nothing rose today. Samples of other days are ignored. A rise of at most
 * ACTIVITY_EPSILON is rounding, not work: baselines stored rounded to 0.1 against
 * samples rounded to 0.01 turned an idle morning into the minimum two hours "worked".
 */
export function todayActivitySpan(
  samples: { timestamp: Date | string; used: number }[],
  dayStartUsed: number | null,
  now: Date,
  sessionStart: Date | null,
): { start: Date; end: Date } | null {
  const todayKey = localDateKey(now);
  const today = samples
    .map((s) => ({ time: new Date(s.timestamp), used: s.used }))
    .filter((s) => localDateKey(s.time) === todayKey && s.time <= now)
    .sort((a, b) => a.time.getTime() - b.time.getTime());

  let start: Date | null = null;
  let end: Date | null = null;
  for (const [i, sample] of today.entries()) {
    const previous = today[i - 1];
    const base = previous ? previous.used : dayStartUsed;
    if (base === null || sample.used - base <= ACTIVITY_EPSILON) continue;
    start ??= previous ? previous.time : sample.time;
    end = sample.time;
  }
  if (!start || !end) return null;
  const sessionToday = sessionStart && localDateKey(sessionStart) === todayKey ? sessionStart : null;
  return { start: sessionToday && sessionToday < start ? sessionToday : start, end };
}

/**
 * Working units of TODAY already spent, from the actual working hours:
 * - schedule enabled: the hours worked today (todayActivitySpan) over `hoursPerDay`, at
 *   least MIN_OBSERVED_WORK_HOURS once work started, capped at the day unit (0.5 on a
 *   half day). No activity yet → 0: before work starts, today is still entirely ahead.
 * - schedule disabled (calendar days, e.g. a personal account): the fraction of the
 *   calendar day elapsed, no working hours to infer.
 */
export function todayElapsedUnits(now: Date, workedHours: number | null, workSchedule: WorkSchedule): number {
  if (!workSchedule.enabled) {
    return (now.getTime() - startOfDay(now).getTime()) / (24 * 3600 * 1000);
  }
  const unit = getDayUnit(now, workSchedule);
  if (unit === 0 || workedHours === null) return 0;
  if (workSchedule.hoursPerDay <= 0) return unit;
  return Math.min(unit, Math.max(MIN_OBSERVED_WORK_HOURS, workedHours) / workSchedule.hoursPerDay);
}

/** Working unit of the current day, 0 when the day is outside [periodStart, periodEnd). */
function todayUnitInPeriod(periodStart: Date | string, periodEnd: Date | string, now: Date, workSchedule: WorkSchedule): number {
  const today = startOfDay(now);
  if (isBefore(today, startOfDay(new Date(periodStart)))) return 0;
  if (!isBefore(today, startOfDay(new Date(periodEnd)))) return 0;
  return getDayUnit(today, workSchedule);
}

/**
 * Working units already spent in the period at `now`: the full days before today plus
 * the part of today already worked (`todayElapsed`, see todayElapsedUnits). Counting
 * today only once it is over left 0 elapsed units on the first day of a period, so
 * projection/autonomy/efficiency ignored a whole day of heavy use. 0 before the
 * period starts.
 */
export function elapsedWorkingUnits(periodStart: Date | string, periodEnd: Date | string, now: Date, workSchedule: WorkSchedule, todayElapsed: number): number {
  const today = startOfDay(now);
  if (isBefore(today, startOfDay(new Date(periodStart)))) return 0;
  const todayPart = Math.min(todayElapsed, todayUnitInPeriod(periodStart, periodEnd, now, workSchedule));
  return workingUnitsBetween(periodStart, today, workSchedule) + todayPart;
}

/**
 * Working units left in the period: what remains of today plus the days after it
 * (elapsed + remaining = the whole period, see elapsedWorkingUnits).
 */
export function remainingWorkingUnits(periodEnd: Date | string, now: Date, workSchedule: WorkSchedule, todayElapsed: number): number {
  const today = startOfDay(now);
  const todayUnit = isBefore(today, startOfDay(new Date(periodEnd))) ? getDayUnit(today, workSchedule) : 0;
  const todayLeft = Math.max(0, todayUnit - todayElapsed);
  return todayLeft + workingUnitsBetween(addDays(today, 1), periodEnd, workSchedule);
}

/** Quota percentage below which usage counts as none for ratios (efficiency index). */
export const NEGLIGIBLE_UTILIZATION = 0.1;

export interface PeriodContext {
  window: QuotaWindow;
  workSchedule: WorkSchedule;
  periodStart: Date | string;
  periodEnd: Date | string;
  now?: Date;
  // Part of today already worked (todayElapsedUnits). Required: a "safe" default
  // would hide a missing wiring (see AGENTS.md, lessons learned).
  todayElapsedUnits: number;
  // Pace of the last completed working days (recentPacePerUnit), null when there are
  // not enough of them — projection and autonomy blend it with the period average.
  recentPacePerUnit: number | null;
}

/**
 * Efficiency index: ratio between the ideal pace and the actual pace, computed on
 * working units (not calendar days). ~1 = on budget; >1 = consuming less than planned;
 * <1 = consuming more than sustainable. Returns null when it cannot be computed.
 * Cumulative since the period start by definition: no blending with the recent pace.
 * Null below NEGLIGIBLE_UTILIZATION: a ratio on almost nothing used is noise
 * (an index in the hundreds or thousands).
 */
export function efficiencyIndex({ window, workSchedule, periodStart, periodEnd, now = new Date(), todayElapsedUnits: todayElapsed }: PeriodContext): number | null {
  const utilization = normalizedUtilization(window);
  if (utilization === null || utilization < NEGLIGIBLE_UTILIZATION) return null;

  const totalUnits = workingUnitsBetween(periodStart, periodEnd, workSchedule);
  const elapsedUnits = elapsedWorkingUnits(periodStart, periodEnd, now, workSchedule, todayElapsed);
  if (totalUnits <= 0 || elapsedUnits <= 0) return null;

  const idealPace = 100 / totalUnits;
  const actualPace = utilization / elapsedUnits;
  if (actualPace === 0) return null;

  return Math.round((idealPace / actualPace) * 100) / 100;
}

const RECENT_PACE_DAYS = 3;
const RECENT_PACE_MIN_DAYS = 2;

/**
 * Pace (% per working unit) of the last RECENT_PACE_DAYS COMPLETED working days
 * (today excluded: still running), from the daily deltas. Null with fewer than
 * RECENT_PACE_MIN_DAYS usable days (non-working days and resets without a baseline are
 * skipped).
 */
export function recentPacePerUnit(deltas: DailyDelta[], workSchedule: WorkSchedule, now: Date): number | null {
  const todayKey = localDateKey(now);
  const usable = deltas
    .filter((d): d is DailyDelta & { delta: number } => d.delta !== null && d.date < todayKey)
    .map((d) => ({ delta: d.delta, unit: getDayUnit(parseDateKey(d.date), workSchedule) }))
    .filter((d) => d.unit > 0)
    .slice(-RECENT_PACE_DAYS);
  if (usable.length < RECENT_PACE_MIN_DAYS) return null;
  const units = usable.reduce((sum, d) => sum + d.unit, 0);
  return usable.reduce((sum, d) => sum + d.delta, 0) / units;
}

/**
 * Pace used by projection and autonomy: the period average, blended 50/50 with the
 * recent pace when available — the average alone reacts slowly to a change of habit
 * (a heavy week late in the period barely moves it). Null when no pace is measurable
 * yet (nothing elapsed).
 */
function blendedPacePerUnit(ctx: PeriodContext, utilization: number, now: Date): number | null {
  const elapsedUnits = elapsedWorkingUnits(ctx.periodStart, ctx.periodEnd, now, ctx.workSchedule, ctx.todayElapsedUnits);
  if (elapsedUnits <= 0) return ctx.recentPacePerUnit;
  const average = utilization / elapsedUnits;
  return ctx.recentPacePerUnit === null ? average : (average + ctx.recentPacePerUnit) / 2;
}

/**
 * The pace (% per working unit) projection and autonomy use (blendedPacePerUnit), for
 * the verdict: null when utilization is unknown or no pace is measurable yet.
 */
export function currentPacePerUnit(ctx: PeriodContext): number | null {
  const utilization = normalizedUtilization(ctx.window);
  if (utilization === null) return null;
  return blendedPacePerUnit(ctx, utilization, ctx.now ?? new Date());
}

/**
 * A window of a few hours (rolling-hours) read in hours: working days and "preliminary"
 * mean nothing on a 5-hour window (the day-based metrics read "0 days to reset" and
 * were always preliminary). Hours left to the reset, usage projected at the reset at
 * the instant pace (instantaneousRate, %/h — none measured counts as no consumption)
 * and hours of autonomy at that pace. Null without a reset moment or utilization.
 */
export function hourlyOutlook(window: QuotaWindow, now: Date, instantRate: number | null): HourlyOutlook | null {
  const utilization = normalizedUtilization(window);
  if (utilization === null || !window.resetsAt) return null;
  const hoursLeft = Math.max(0, (new Date(window.resetsAt).getTime() - now.getTime()) / (3600 * 1000));
  const rate = instantRate ?? 0;
  const autonomyHours = utilization >= 100 ? 0 : rate > 0 ? round1((100 - utilization) / rate) : null;
  return { hoursLeft: round2(hoursLeft), projectedAtReset: round1(utilization + rate * hoursLeft), autonomyHours };
}

/**
 * Projected usage (%) at the end of the period, extrapolating the pace
 * (blendedPacePerUnit) over the remaining working units. NOT capped at 100: "227%"
 * says how far over the limit the current pace leads, and windowVerdict needs the
 * value above 100 to flag the risk.
 */
export function projectedUsage(ctx: PeriodContext): number | null {
  const { window, workSchedule, periodEnd, now = new Date(), todayElapsedUnits: todayElapsed } = ctx;
  const utilization = normalizedUtilization(window);
  if (utilization === null) return null;

  const pace = blendedPacePerUnit(ctx, utilization, now);
  if (pace === null) return round1(utilization);
  const remainingUnits = remainingWorkingUnits(periodEnd, now, workSchedule, todayElapsed);
  return round1(utilization + pace * remainingUnits);
}

/** Calendar days left until the reset (>= 0). */
export function daysUntilReset(resetsAt: Date | string, now: Date = new Date()): number {
  return Math.max(0, differenceInCalendarDays(new Date(resetsAt), now));
}

/**
 * Working days/units left until the reset (>= 0): what remains of today plus the days
 * after it — the same horizon `estimatedAutonomyWorkingDays` is compared with.
 */
export function workingDaysUntilReset(resetsAt: Date | string, workSchedule: WorkSchedule, now: Date, todayElapsed: number): number {
  return round1(remainingWorkingUnits(resetsAt, now, workSchedule, todayElapsed));
}

/**
 * Estimated remaining autonomy in working units at the current pace
 * (blendedPacePerUnit, the same one as projectedUsage): how many working units are
 * left before reaching 100%. Returns Infinity when the pace is ~0 (no consumption
 * observed).
 */
export function estimatedAutonomyWorkingDays(ctx: PeriodContext): number | null {
  const utilization = normalizedUtilization(ctx.window);
  if (utilization === null) return null;
  if (utilization >= 100) return 0;

  const pace = blendedPacePerUnit(ctx, utilization, ctx.now ?? new Date());
  if (pace === null || pace <= 0) return Infinity;

  return round1((100 - utilization) / pace);
}

/** Ratio of today's consumption to today's budget above which the pace notification fires. */
export const PACE_ALERT_RATIO = 1.5;

// A drop smaller than this (percentage points) is not a reset: stored values come at
// different precisions (older builds rounded to 0.1) and a provider can fall back to an
// integer percent. A missed small reset costs at most this much, a false one the whole period.
export const RESET_TOLERANCE = 1;

/**
 * Today's budget: the share of the quota today can use so that the rest lasts until
 * the reset — what was left at the START of the day spread over the working units
 * from today on, times today's unit. Fixed for the whole day (consuming does not
 * shrink it while the day runs), recomputed every new day from what is left.
 * `usedToday` is the consumption since the start of the day (a reset during the day
 * leaves the new count only). Null without a baseline, on a non-working day or when
 * no working unit is left.
 */
export function todayBudget(ctx: Omit<PeriodContext, 'todayElapsedUnits' | 'recentPacePerUnit'>, dayStartUsed: number | null): TodayBudget | null {
  const { window, workSchedule, periodStart, periodEnd, now = new Date() } = ctx;
  const utilization = normalizedUtilization(window);
  if (utilization === null || dayStartUsed === null) return null;
  const todayUnit = todayUnitInPeriod(periodStart, periodEnd, now, workSchedule);
  if (todayUnit <= 0) return null;
  const unitsFromToday = remainingWorkingUnits(periodEnd, now, workSchedule, 0);
  if (unitsFromToday <= 0) return null;

  const budget = (Math.max(0, 100 - dayStartUsed) / unitsFromToday) * todayUnit;
  // Same precision as the stored baseline.
  const used = round2(utilization);
  const usedToday = used < dayStartUsed - RESET_TOLERANCE ? used : Math.max(0, used - dayStartUsed);
  return { budget: round2(budget), usedToday: round2(usedToday) };
}

/**
 * The remaining quota redistributed (user feedback: "a redefinition of
 * the remaining quota based on what was consumed, whether too much or too little"):
 * what is left NOW spread over the working units from today on (today counted whole,
 * as in todayBudget) — down after heavy days, up after light ones — next to the even
 * share of the whole period. The primary pacing signal (windowVerdict, tip
 * the "Today" hint): it needs no history, so it is there from the first refresh of the
 * morning, and it follows today's consumption while the day runs (todayBudget stays
 * fixed). Null when utilization is unknown or no working unit is left.
 */
export function redistributedQuota(ctx: Omit<PeriodContext, 'todayElapsedUnits' | 'recentPacePerUnit'>): Redistribution | null {
  const { window, workSchedule, periodStart, periodEnd, now = new Date() } = ctx;
  const utilization = normalizedUtilization(window);
  if (utilization === null) return null;
  const unitsLeft = remainingWorkingUnits(periodEnd, now, workSchedule, 0);
  const totalUnits = workingUnitsBetween(periodStart, periodEnd, workSchedule);
  if (unitsLeft <= 0 || totalUnits <= 0) return null;
  return {
    perUnit: round2(Math.max(0, 100 - utilization) / unitsLeft),
    idealPerUnit: round2(100 / totalUnits),
    unitsLeft: round1(unitsLeft),
  };
}

/**
 * Recent consumption pace (%/h) over about the last `lookbackMinutes` (default 1h):
 * from the last sample at or before the start of the lookback (the anchor, so the
 * measured span covers the whole hour even when refreshes are sparse) to the newest
 * sample. Without a recent enough anchor (an older one would average the night or a
 * weekend in), from the oldest sample inside the lookback. Not truly instantaneous —
 * samples arrive with the refreshes, every 5 minutes while consumption rises (see
 * gocore/internal/refresh/policy) — but a pause shows within the hour, a burst no longer
 * lingers for three. Returns null when samples are insufficient or the interval is
 * too short (< 5 min) to be meaningful. A negative delta (quota window reset in
 * between) is clamped to 0 instead of showing a negative pace that means nothing to
 * the user.
 */
export function instantaneousRate(
  samples: { timestamp: Date | string; used: number }[],
  now: Date = new Date(),
  lookbackMinutes = 60,
): number | null {
  if (!Array.isArray(samples) || samples.length < 2) return null;

  const lookbackMs = lookbackMinutes * 60 * 1000;
  const cutoff = now.getTime() - lookbackMs;
  const points = samples
    .map((s) => ({ time: new Date(s.timestamp).getTime(), used: s.used }))
    .filter((s) => s.time <= now.getTime())
    .sort((a, b) => a.time - b.time);

  const latest = points.at(-1);
  if (!latest) return null;
  const anchor = points.filter((s) => s.time <= cutoff && s.time >= cutoff - lookbackMs).at(-1);
  const oldest = anchor ?? points.find((s) => s.time > cutoff);
  if (!oldest || oldest === latest) return null;
  const elapsedHours = (latest.time - oldest.time) / (3600 * 1000);
  if (elapsedHours < 5 / 60) return null;

  const delta = Math.max(0, latest.used - oldest.used);
  return Math.round((delta / elapsedHours) * 100) / 100;
}

/**
 * Maximum sustainable hourly pace (%/h) to reach exactly 100% at the window
 * reset — the "target" marker of the instant consumption gauge. Needs only the reset
 * moment, not `periodStart`: unlike `efficiencyIndex`/`projectedUsage` it also works
 * for windows with an unknown reference period (e.g. one-off credits with their own
 * `resetsAt`, see budget.hasPacing), because knowing when the period started
 * is not needed to know how much time is left. `resetsAt` is passed explicitly: a
 * window without its own (e.g. a monthly spend limit) resets at the end of the
 * billing period resolved by the caller.
 * `workingHoursLeft` (remaining working units × hoursPerDay, from the caller) spreads
 * the remainder over the hours actually worked: the instant rate is measured while
 * working, so a target spread over nights and weekends too was several times too
 * strict — also with the schedule disabled, where every day counts as a working day
 * of hoursPerDay hours. Null = calendar hours (5-hour window); 0 or less also falls
 * back to calendar hours (no working time left before the reset).
 * Returns null when `resetsAt` is missing or utilization cannot be computed.
 */
export function sustainableHourlyRate(
  window: QuotaWindow,
  resetsAt: Date | string | null,
  now: Date,
  workingHoursLeft: number | null,
): number | null {
  const utilization = normalizedUtilization(window);
  if (utilization === null || !resetsAt) return null;

  const calendarHours = (new Date(resetsAt).getTime() - now.getTime()) / (3600 * 1000);
  if (calendarHours <= 0) return 0;
  const hoursUntilReset = workingHoursLeft !== null && workingHoursLeft > 0 ? workingHoursLeft : calendarHours;

  const remainingPercent = Math.max(0, 100 - utilization);
  return Math.round((remainingPercent / hoursUntilReset) * 100) / 100;
}

const EFFICIENCY_RATING_MAX_RATIO = 3;

/**
 * Star efficiency rating (1-5) over the last `days` days: average ratio between the
 * day's ideal share and the consumption observed that day (>1 = consumed less than the
 * ideal). Unlike `efficiencyIndex` (cumulative snapshot since the start of the
 * period), this looks day by day over a moving window — how consistently usage stayed
 * close to the ideal pace lately, not just the total so far. A non-working day is
 * excluded (no ideal share to respect); a negative delta (window reset in between) is
 * excluded as in instantaneousRate, not attributable to that day's usage. Each ratio
 * is capped at EFFICIENCY_RATING_MAX_RATIO so a single zero-consumption day does not
 * dominate the average. Only completed days: today is still running, and in the
 * morning its 0% scored as a perfect day. Returns null when there is not enough
 * valid data.
 */
export function efficiencyRating(
  dailyHistory: DailyUsagePoint[],
  workSchedule: WorkSchedule,
  totalPeriodWorkingUnits: number,
  now: Date,
  days = 7,
): EfficiencyRating | null {
  const todayKey = localDateKey(now);
  const completed = Array.isArray(dailyHistory) ? dailyHistory.filter((p) => p.date < todayKey) : [];
  if (completed.length === 0 || totalPeriodWorkingUnits <= 0) return null;

  const sorted = [...completed].sort((a, b) => a.date.localeCompare(b.date)).slice(-(days + 1));
  const ratios: number[] = [];
  let consumed = false;

  for (const { delta, idealShare } of dailyDeltas(sorted, workSchedule, totalPeriodWorkingUnits)) {
    if (idealShare === null || idealShare <= 0 || delta === null) continue;
    if (delta > 0) consumed = true;
    const ratio = delta === 0 ? EFFICIENCY_RATING_MAX_RATIO : Math.min(EFFICIENCY_RATING_MAX_RATIO, idealShare / delta);
    ratios.push(ratio);
  }

  // No consumption at all on the rated days: five stars for an unused quota (and the
  // tip "room for a longer session") said nothing.
  if (ratios.length === 0 || !consumed) return null;
  const avgRatio = ratios.reduce((s, r) => s + r, 0) / ratios.length;
  const stars = avgRatio >= 1.5 ? 5 : avgRatio >= 1.1 ? 4 : avgRatio >= 0.9 ? 3 : avgRatio >= 0.6 ? 2 : 1;
  return { stars, avgRatio: Math.round(avgRatio * 100) / 100 };
}

/**
 * Consumption of each day (difference with the day's baseline `dayStartUsed`, or with
 * the previous history point for older points, in quota percentage points) next to that day's ideal share — docs/EVOLUTION.md point 1: the
 * widget chart shows this, no longer the cumulative % per day that mirrored the
 * provider dashboard.
 * - `delta` null: the window was reset in between (negative delta), the value is not
 *   attributable to that day's usage (same rule as instantaneousRate);
 * - `idealShare` null: pacing not available (period of unknown length,
 *   totalPeriodWorkingUnits <= 0); 0 on a non-working day.
 * The first history point produces a delta only when it carries its own `dayStartUsed`.
 */
export function dailyDeltas(
  dailyHistory: DailyUsagePoint[],
  workSchedule: WorkSchedule,
  totalPeriodWorkingUnits: number,
): DailyDelta[] {
  if (!Array.isArray(dailyHistory)) return [];
  const sorted = [...dailyHistory].sort((a, b) => a.date.localeCompare(b.date));
  const result: DailyDelta[] = [];
  let prev: DailyUsagePoint | undefined;
  for (const curr of sorted) {
    // The day's own baseline (updateDailyPoint) when recorded: exact even on the day
    // of a reset, and for the first day of history. Older points: previous point.
    const base = curr.dayStartUsed ?? prev?.used;
    if (base !== undefined) {
      const rawDelta = curr.used - base;
      const idealShare = totalPeriodWorkingUnits > 0
        ? round2(getDayUnit(parseDateKey(curr.date), workSchedule) * (100 / totalPeriodWorkingUnits))
        : null;
      result.push({ date: curr.date, delta: rawDelta < 0 ? null : round2(rawDelta), idealShare });
    }
    prev = curr;
  }
  return result;
}

/**
 * The slots of the daily chart: `pastDays` days ending today, then `upcomingDays` days
 * to come (user feedback: the dashed line should be "the moving part" — how much was
 * really available on a past day, how much can still be spent today and next). See
 * ChartDay for the fields. The moving budget uses the same rule as todayBudget: what
 * was left at the start of the day spread over the working units from that day to the
 * reset, times the day's unit — so a heavy day lowers the line of the days after it.
 * Days outside the current period get no share and no budget (another period, another
 * total).
 * `todayBudget`/`redistribution` are the values already computed for today.
 */
export function chartDays(args: {
  dailyHistory: DailyUsagePoint[];
  workSchedule: WorkSchedule;
  periodStart: Date | string;
  periodEnd: Date | string;
  totalPeriodWorkingUnits: number;
  now: Date;
  pastDays: number;
  upcomingDays: number;
  todayBudget: TodayBudget | null;
  redistribution: Redistribution | null;
}): ChartDay[] {
  const { workSchedule, periodStart, periodEnd, totalPeriodWorkingUnits, now } = args;
  const pacing = totalPeriodWorkingUnits > 0;
  const fullShare = pacing ? round2(100 / totalPeriodWorkingUnits) : null;
  const deltas = new Map(dailyDeltas(args.dailyHistory, workSchedule, totalPeriodWorkingUnits).map((d) => [d.date, d.delta]));

  // Each history day's baseline, same rule as dailyDeltas.
  const bases = new Map<string, number>();
  let prev: DailyUsagePoint | undefined;
  for (const point of [...args.dailyHistory].sort((a, b) => a.date.localeCompare(b.date))) {
    const base = point.dayStartUsed ?? prev?.used;
    if (base !== undefined) bases.set(point.date, base);
    prev = point;
  }

  const firstDay = startOfDay(new Date(periodStart));
  const lastDay = startOfDay(new Date(periodEnd));
  const today = startOfDay(now);
  const todayKey = localDateKey(today);
  const slots: ChartDay[] = [];
  for (let offset = 1 - args.pastDays; offset <= args.upcomingDays; offset++) {
    const day = addDays(today, offset);
    const date = localDateKey(day);
    const dayUnit = getDayUnit(day, workSchedule);
    const inPeriod = pacing && !isBefore(day, firstDay) && isBefore(day, lastDay);
    let budget: number | null = null;
    if (inPeriod) {
      if (date === todayKey) {
        budget = args.todayBudget?.budget ?? null;
      } else if (offset > 0) {
        budget = args.redistribution ? round2(args.redistribution.perUnit * dayUnit) : null;
      } else {
        const base = bases.get(date);
        const unitsFromDay = workingUnitsBetween(day, periodEnd, workSchedule);
        budget = base !== undefined && unitsFromDay > 0 ? round2((Math.max(0, 100 - base) / unitsFromDay) * dayUnit) : null;
      }
    }
    const delta = offset > 0 ? null : deltas.get(date) ?? null;
    const share = inPeriod ? fullShare : null;
    const limit = budget ?? (share !== null ? share * dayUnit : null);
    slots.push({
      date,
      delta,
      fullShare: share,
      dayUnit,
      budget,
      upcoming: offset > 0,
      overBudget: delta !== null && limit !== null && delta > limit,
    });
  }
  return slots;
}

/**
 * Next value of TODAY's history point for a window, given the point already recorded
 * today (if any), the last point of an earlier day (if any) and the current
 * utilization (already rounded by the caller):
 * - `dayStartUsed` (set once, when the day's point is created): the previous point's
 *   value when it belongs to the current period; 0 when the period started after it
 *   or the value dropped (reset in between); with no history at all, 0 when the
 *   period started today (everything used so far was used today), otherwise the
 *   current value (consumption before the first refresh is unknown).
 * An older point of today without a baseline (recorded before the field existed)
 * gets one computed the same way. See also repairFirstDayBaseline.
 */
export function updateDailyPoint(args: {
  today: DailyUsagePoint | undefined;
  previous: DailyUsagePoint | undefined;
  accountId: DailyUsagePoint['accountId'];
  windowId: string;
  used: number;
  periodStart: Date | string;
  now: Date;
}): DailyUsagePoint {
  const { today, previous, accountId, windowId, used, periodStart, now } = args;
  let dayStartUsed = today?.dayStartUsed;
  if (dayStartUsed === undefined) {
    if (!previous) dayStartUsed = periodStartedToday(periodStart, now) ? 0 : today?.used ?? used;
    else if (previous.used > used + RESET_TOLERANCE || isBefore(parseDateKey(previous.date), startOfDay(new Date(periodStart)))) dayStartUsed = 0;
    else dayStartUsed = previous.used;
  }
  return { date: localDateKey(now), accountId, windowId, used, dayStartUsed };
}

function periodStartedToday(periodStart: Date | string, now: Date): boolean {
  const start = new Date(periodStart);
  return localDateKey(start) === localDateKey(now) && start.getTime() <= now.getTime();
}

/**
 * Repairs the baseline of a window's FIRST history point when it is dated on the
 * period start day (`points` = one window's points): before updateDailyPoint knew
 * that rule, it stored the first value read — on a window first seen after usage
 * had started on its period start day (e.g. a window added by an app update) the
 * whole day's consumption became 0 (empty chart, 5 stars, "room for a longer
 * session"). Returns the point to store again, or null when nothing changes.
 */
export function repairFirstDayBaseline(points: DailyUsagePoint[], periodStart: Date | string): DailyUsagePoint | null {
  const [first] = [...points].sort((a, b) => a.date.localeCompare(b.date));
  if (!first?.dayStartUsed) return null;
  if (first.date !== localDateKey(new Date(periodStart))) return null;
  return { ...first, dayStartUsed: 0 };
}

/**
 * Daily consumption peak/average and streak of consecutive days (from the most
 * recent) under budget, on the chart slots (chartDays): the same days and the same
 * limit as the bars, so a day drawn over budget never counts in the streak (it used
 * to compare with the even share while the bars compared with the moving budget).
 * Days with a reset (delta null) are ignored; a day off with no consumption neither
 * extends nor breaks the streak; the streak is null without any limit to compare with
 * or without any consumption. Only completed days: today is still running.
 */
export function deltaStats(chart: ChartDay[], now: Date): DeltaStats {
  const todayKey = localDateKey(now);
  const valid = chart.filter((d): d is ChartDay & { delta: number } => !d.upcoming && d.delta !== null && d.date < todayKey);
  if (valid.length === 0) return { peak: null, avg: null, streakUnderBudget: null };
  const values = valid.map((d) => d.delta);
  const peak = round2(Math.max(...values));
  const avg = round2(values.reduce((sum, v) => sum + v, 0) / values.length);

  let streakUnderBudget: number | null = null;
  if (valid.some((d) => d.budget !== null || d.fullShare !== null) && valid.some((d) => d.delta > 0)) {
    streakUnderBudget = 0;
    for (const day of [...valid].reverse()) {
      if (day.dayUnit === 0 && day.delta === 0) continue;
      if (day.overBudget || (day.budget === null && day.fullShare === null)) break;
      streakUnderBudget += 1;
    }
  }
  return { peak, avg, streakUnderBudget };
}

export interface WindowVerdictContext {
  window: QuotaWindow;
  projectedUsage: number | null;
  workingDaysUntilReset: number | null;
  estimatedAutonomyWorkingDays: number | null;
  // The remaining quota redistributed (redistributedQuota); null on windows without a
  // daily budget (rolling hours) or without pacing.
  redistribution: Redistribution | null;
  // Current pace (currentPacePerUnit) and whether it rests on too little data: a pace
  // well above the redistributed quota makes the window at risk.
  pacePerUnit: number | null;
  preliminary: boolean;
  // Rolling-hours windows: the reading in hours (hourlyOutlook), null otherwise.
  hourly: HourlyOutlook | null;
}

// Redistributed quota over the ideal one (see windowVerdict): below AT_RISK the days
// left get less than half of their even share; within the ON_TRACK band (±5%) the
// difference is treated as noise. Narrow on purpose: a deviation of a single day is
// spread over every working day left, so even a day at twice or three times the even
// share moves the quota per day by only a few percent early in a monthly period — a
// wider band would hide it until late in the period. Heuristics, like the other
// pacing constants: to be verified with real use (AGENTS.md, open items).
export const REDISTRIBUTION_AT_RISK_RATIO = 0.5;
export const REDISTRIBUTION_ON_TRACK_LOW = 0.95;
export const REDISTRIBUTION_ON_TRACK_HIGH = 1.05;

/**
 * Short verdict of a quota window for the widget window list (docs/EVOLUTION.md point 1:
 * replacing tabs that only lined up the provider's metrics). The text is composed by
 * the renderer (reset date formatting is a UI concern).
 * - exhausted first;
 * - rolling-hours windows (hourly): at risk when the instant pace reaches 100% before
 *   the reset, otherwise on track;
 * - with a redistribution (every other paced window): how the quota left per working
 *   day compares with the even share — at risk / behind / on track / ahead. The
 *   redistribution alone says what is left to spend, not where the current pace
 *   leads: when the pace (not preliminary) is above PACE_ALERT_RATIO times the quota
 *   left per day, the window is at risk whatever the band (a "quota reduced" next to a
 *   projection of 150% understated it);
 * - otherwise autonomy/projection → no pacing.
 */
export function windowVerdict(ctx: WindowVerdictContext): WindowVerdict {
  const utilization = normalizedUtilization(ctx.window);
  if (utilization !== null && utilization >= 100) return { kind: 'exhausted' };
  if (ctx.hourly) {
    if (ctx.hourly.projectedAtReset > 100) {
      return ctx.hourly.autonomyHours !== null ? { kind: 'at-risk', autonomyHours: ctx.hourly.autonomyHours } : { kind: 'at-risk' };
    }
    return { kind: 'on-track' };
  }
  const r = ctx.redistribution;
  if (r && r.idealPerUnit > 0) {
    const params = { perUnit: r.perUnit, idealPerUnit: r.idealPerUnit };
    if (!ctx.preliminary && ctx.pacePerUnit !== null && ctx.pacePerUnit > r.perUnit * PACE_ALERT_RATIO) {
      return { kind: 'at-risk', ...params, pacePerUnit: round1(ctx.pacePerUnit) };
    }
    const ratio = r.perUnit / r.idealPerUnit;
    if (ratio < REDISTRIBUTION_AT_RISK_RATIO) return { kind: 'at-risk', ...params };
    if (ratio < REDISTRIBUTION_ON_TRACK_LOW) return { kind: 'behind', ...params };
    if (ratio <= REDISTRIBUTION_ON_TRACK_HIGH) return { kind: 'on-track', ...params };
    return { kind: 'ahead', ...params };
  }
  if (
    ctx.estimatedAutonomyWorkingDays !== null &&
    ctx.workingDaysUntilReset !== null &&
    ctx.estimatedAutonomyWorkingDays < ctx.workingDaysUntilReset
  ) {
    return { kind: 'at-risk', autonomyWorkingDays: round1(ctx.estimatedAutonomyWorkingDays) };
  }
  if (ctx.projectedUsage !== null && ctx.projectedUsage > 100) {
    return { kind: 'at-risk', projectedUsage: round1(ctx.projectedUsage) };
  }
  if (ctx.projectedUsage !== null) return { kind: 'on-track' };
  return { kind: 'no-pacing' };
}

const VERDICT_RANK: Partial<Record<WindowVerdict['kind'], number>> = { exhausted: 2, 'at-risk': 1 };

/**
 * The window the widget opens on: an exhausted or at-risk window before the others,
 * then the highest utilization, then one with pacing (pickCriticalWindow's tie-break).
 * By utilization alone a 5-hour window at 67% hid a weekly one heading over its limit.
 */
export function pickCriticalSnapshot<T extends { window: QuotaWindow; verdict: WindowVerdict }>(snapshots: T[]): T | null {
  const ranked = snapshots.map((s) => ({ s, rank: VERDICT_RANK[s.verdict.kind] ?? 0, utilization: normalizedUtilization(s.window) ?? -1 }));
  ranked.sort((a, b) => b.rank - a.rank || b.utilization - a.utilization || Number(hasPacing(b.s.window)) - Number(hasPacing(a.s.window)));
  return ranked[0]?.s ?? null;
}

// ---------------------------------------------------------------------------
// Value per token (docs/EVOLUTION.md point 4): crossing the tokens produced in local Claude
// Code sessions (counted by the Go core, `aipermiler insights claude`, per day) with the same day's quota
// consumption (dailyDeltas). Only days present in both sources are compared: a day
// with quota used but no local session (e.g. claude.ai in the browser) could not be
// attributed.
// ---------------------------------------------------------------------------

const TOKEN_YIELD_MIN_DAYS = 3;
const TOKEN_YIELD_MIN_TOTAL_DELTA = 1; // quota percentage points: below this the ratio is noise
const CAUSE_MIN_DAYS = 5;
const CAUSE_MIN_RATIO = 1.5;
const CAUSE_MIN_GAP_POINTS = 20;

interface PairedDay { date: string; delta: number; outputTokens: number; highContextOutputTokens: number }

function pairDays(localDaily: LocalDailyTokens[], deltas: DailyDelta[]): PairedDay[] {
  const local = new Map(localDaily.map((d) => [d.date, d]));
  const paired: PairedDay[] = [];
  for (const d of deltas) {
    const l = local.get(d.date);
    if (d.delta === null || d.delta <= 0 || !l || l.outputTokens <= 0) continue;
    paired.push({ date: d.date, delta: d.delta, outputTokens: l.outputTokens, highContextOutputTokens: l.highContextOutputTokens });
  }
  return paired.sort((a, b) => a.date.localeCompare(b.date));
}

function medianOf(values: number[]): number | null {
  const sorted = [...values].sort((a, b) => a - b);
  const mid = Math.floor(sorted.length / 2);
  const upper = sorted[mid];
  if (upper === undefined) return null;
  if (sorted.length % 2) return upper;
  const lower = sorted[mid - 1];
  return lower === undefined ? upper : (lower + upper) / 2;
}

function yieldOf(days: PairedDay[]): number | null {
  const totalDelta = days.reduce((s, d) => s + d.delta, 0);
  if (totalDelta <= 0) return null;
  return days.reduce((s, d) => s + d.outputTokens, 0) / totalDelta;
}

/**
 * "Yield": output tokens produced per percentage point of quota used, on days
 * present in both sources. A measure of value, not of pace: for the same work, a lower
 * yield means each token cost more quota (typically a very large context re-read on
 * every turn). Trend: yield of the second half of the compared days vs the first.
 * null with fewer than TOKEN_YIELD_MIN_DAYS days or a too low total consumption.
 */
export function tokenYield(localDaily: LocalDailyTokens[], deltas: DailyDelta[]): TokenYield | null {
  const paired = pairDays(localDaily, deltas);
  if (paired.length < TOKEN_YIELD_MIN_DAYS) return null;
  if (paired.reduce((s, d) => s + d.delta, 0) < TOKEN_YIELD_MIN_TOTAL_DELTA) return null;
  const overall = yieldOf(paired);
  if (overall === null) return null;

  const half = Math.floor(paired.length / 2);
  const first = yieldOf(paired.slice(0, half));
  const second = yieldOf(paired.slice(paired.length - half));
  const trendPercent = first !== null && second !== null && first > 0 ? round1((second / first - 1) * 100) : null;

  return { tokensPerPercent: Math.round(overall), trendPercent, daysCompared: paired.length };
}

/**
 * Link between quota consumption and large context, stated ONLY when clear: at
 * least CAUSE_MIN_DAYS compared days, and on days above the consumption median the
 * share of tokens produced with context >150k is at least CAUSE_MIN_RATIO times (and
 * CAUSE_MIN_GAP_POINTS points above) that of days below the median. Otherwise null:
 * better no sentence than a weak correlation presented as a cause (docs/EVOLUTION.md, "a bad
 * insight undermines trust more than no insight").
 */
export function consumptionCause(localDaily: LocalDailyTokens[], deltas: DailyDelta[]): ConsumptionCause | null {
  const paired = pairDays(localDaily, deltas);
  if (paired.length < CAUSE_MIN_DAYS) return null;

  const median = medianOf(paired.map((d) => d.delta));
  if (median === null) return null;
  const high = paired.filter((d) => d.delta > median);
  const low = paired.filter((d) => d.delta <= median);
  if (high.length === 0 || low.length === 0) return null;

  const share = (days: PairedDay[]) => {
    const out = days.reduce((s, d) => s + d.outputTokens, 0);
    return out > 0 ? (days.reduce((s, d) => s + d.highContextOutputTokens, 0) / out) * 100 : 0;
  };
  const highShare = share(high);
  const lowShare = share(low);
  if (highShare < lowShare * CAUSE_MIN_RATIO || highShare - lowShare < CAUSE_MIN_GAP_POINTS) return null;

  return { highDaysHighContextPercent: Math.round(highShare), lowDaysHighContextPercent: Math.round(lowShare), daysCompared: paired.length };
}

function round1(value: number): number {
  return Math.round(value * 10) / 10;
}

function round2(value: number): number {
  return Math.round(value * 100) / 100;
}

export interface DailyTipContext {
  window: QuotaWindow;
  daysUntilReset: number | null;
  workingDaysUntilReset: number | null;
  estimatedAutonomyWorkingDays: number | null;
  efficiencyRating: EfficiencyRating | null;
  // Only for the Claude account with local insights enabled — see consumptionCause.
  consumptionCause?: ConsumptionCause | null;
  // Projection/autonomy rest on too little data (see main.ts PRELIMINARY_WORKING_UNITS):
  // the tips built on them are skipped.
  preliminary: boolean;
}

export const NO_TIP: DailyTip = { key: 'none', params: {} };

/**
 * Daily tip as DATA, not a sentence: a message key plus the real numbers it
 * states. The renderer turns it into text in the active language
 * (renderer/i18n, keys `tips.<key>`), so the tip follows a language switch
 * without a refresh. Every candidate below has an explicit condition on values
 * already computed in this file — never a generic tip picked at random. When
 * several hold, one is chosen among the applicable ones (variety without ever
 * stating something false); when none holds, `none` and the widget shows no tip.
 * Only tips that add an action or a cause to what the widget already shows (user
 * feedback: a rich dashboard is not useful if it repeats itself): the pace above
 * target is the instant gauge, a projection over 100% is the projection, the quota
 * redistributed per day is the hint under today's budget — those tips are gone.
 */
export function generateDailyTip(ctx: DailyTipContext, random: () => number = Math.random): DailyTip {
  const {
    window,
    daysUntilReset,
    workingDaysUntilReset,
    estimatedAutonomyWorkingDays,
    efficiencyRating,
    consumptionCause: cause,
    preliminary,
  } = ctx;
  const utilization = normalizedUtilization(window);
  const candidates: DailyTip[] = [];

  // 1. Estimated autonomy at the current pace is shorter than the time left to
  // the reset: a concrete risk of running out first. Includes the slowdown
  // needed to make it (ratio between the two durations).
  if (
    !preliminary &&
    estimatedAutonomyWorkingDays !== null && Number.isFinite(estimatedAutonomyWorkingDays) &&
    workingDaysUntilReset !== null && workingDaysUntilReset > 0 &&
    estimatedAutonomyWorkingDays < workingDaysUntilReset
  ) {
    candidates.push({
      key: 'autonomy',
      params: {
        autonomyDays: round1(estimatedAutonomyWorkingDays),
        daysToReset: round1(workingDaysUntilReset),
        reductionPercent: Math.round((1 - estimatedAutonomyWorkingDays / workingDaysUntilReset) * 100),
      },
    });
  }

  // 2. High rating over the last days: real room for heavier use today.
  if (efficiencyRating !== null && efficiencyRating.stars >= 4) {
    candidates.push({
      key: 'rating',
      params: { stars: efficiencyRating.stars, avgRatio: efficiencyRating.avgRatio },
    });
  }

  // 3. Few days to the reset and usage already high: better to ration what is left.
  if (daysUntilReset !== null && daysUntilReset <= 2 && utilization !== null && utilization >= 70) {
    candidates.push({
      key: daysUntilReset === 0 ? 'nearResetToday' : 'nearReset',
      params: { days: daysUntilReset, utilization: round1(utilization) },
    });
  }

  // 4. Strong link between high-consumption days and large context (local
  // Claude Code sessions) — see consumptionCause: present only when the signal is clear.
  if (cause) {
    candidates.push({
      key: 'cause',
      params: {
        highPercent: cause.highDaysHighContextPercent,
        lowPercent: cause.lowDaysHighContextPercent,
        days: cause.daysCompared,
      },
    });
  }

  // Index capped to the last candidate: an injected random returning 1
  // (Math.random() never does, tests may) would otherwise yield undefined.
  const index = Math.min(candidates.length - 1, Math.floor(random() * candidates.length));
  return candidates[index] ?? NO_TIP;
}

/**
 * Resolves the next subscription renewal date from a renewalRule.
 * Only { type: 'dayOfMonth', day } is supported today. { type: 'rrule', rrule } is not
 * implemented yet (it would need a dedicated library; to be evaluated if a recurrence
 * more complex than a day of the month is ever really needed).
 */
export function resolveRenewalDate(renewalRule: RenewalRule, referenceDate: Date = new Date()): Date {
  if (renewalRule.type === 'dayOfMonth' && typeof renewalRule.day === 'number') {
    const day = renewalRule.day;
    // setDate on a shorter month would overflow into the next one: clamp to its last day.
    const inMonthOf = (monthStart: Date): Date => setDate(monthStart, Math.min(day, getDaysInMonth(monthStart)));
    const monthStart = setDate(startOfDay(new Date(referenceDate)), 1);
    let candidate = inMonthOf(monthStart);
    if (!isBefore(referenceDate, candidate)) {
      candidate = inMonthOf(addMonths(monthStart, 1));
    }
    return candidate;
  }
  throw new Error(`resolveRenewalDate: renewalRule.type "${renewalRule.type}" not supported`);
}
