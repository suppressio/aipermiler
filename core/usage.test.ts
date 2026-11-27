// core/usage.test.ts — budget.ts's account snapshot, the reference side of the pacing
// parity (scripts/pacing-parity.js, #325, #323). Since #341 the app reads the core's
// snapshot instead; these cases keep the reference honest: periods, daily points and
// samples, rolling-hours and monthly metrics, reads that land around midnight.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { computeAccountSnapshot, resolvePeriodBounds, type HistoryDraft } from './usage';
import { defaultAccountFor } from '../store/migrate';
import { at } from '../tests/support/at';
import type { AccountConfig, AccountSnapshot, QuotaWindow, RawAccountUsage } from '../types/index';

const MIN = 60 * 1000;
const NOW = new Date(2026, 9, 14, 10, 0, 0); // Wednesday, local time

function claudeAccount(id = 'a1'): AccountConfig {
  return { ...defaultAccountFor('claude', id, `Claude ${id}`), enabled: true };
}

function weeklyWindow(used: number, resetsInDays = 3): QuotaWindow {
  return {
    id: 'seven_day', label: 'Weekly', periodType: 'rolling-days', periodLength: 7, unit: 'percentage',
    used, total: 100, resetsAt: new Date(NOW.getTime() + resetsInDays * 24 * 60 * MIN),
  };
}

function monthlyWindow(used: number): QuotaWindow {
  return { id: 'spend', label: 'Monthly', periodType: 'billing-cycle', periodLength: 1, unit: 'percentage', used, total: 100, resetsAt: null };
}

function usage(...quotaWindows: QuotaWindow[]): RawAccountUsage {
  return { planTier: 'pro', subscriptionRenewsAt: null, quotaWindows };
}

/** One account over an in-memory history, read as the app read it until #341. */
function account(cfg: AccountConfig = claudeAccount()) {
  const draft: HistoryDraft = { dailyUsage: [], recentSamples: [], chartDays: 7 };
  return {
    draft,
    // `readAt`: when the value was read (a service read before now); `fresh` false: a
    // document already seen, no new sample.
    read(raw: RawAccountUsage, now: Date, opts: { readAt?: Date; fresh?: boolean } = {}): AccountSnapshot {
      const lastUpdatedAt = (opts.readAt ?? now).toISOString();
      return computeAccountSnapshot(draft, { ...raw, accountId: cfg.id, lastUpdatedAt }, cfg, now, null, opts.fresh ?? true);
    },
    points: () => draft.dailyUsage.map(({ date, used, dayStartUsed }) => ({ date, used, dayStartUsed })),
  };
}

test('resolvePeriodBounds: window reset, then provider renewal, then the manual day', () => {
  const rule = { renewalRule: { type: 'dayOfMonth', day: 20 } as const };
  const rolling = weeklyWindow(10);
  const fromWindow = resolvePeriodBounds(rolling, rule, '2030-01-01T00:00:00Z', NOW);
  assert.equal(fromWindow.periodEnd.getTime(), new Date(rolling.resetsAt as Date).getTime());
  assert.equal(fromWindow.periodEnd.getTime() - fromWindow.periodStart.getTime(), 7 * 24 * 60 * MIN);

  const hours: QuotaWindow = { ...rolling, periodType: 'rolling-hours', periodLength: 5, resetsAt: new Date(NOW.getTime() + 60 * MIN) };
  const h = resolvePeriodBounds(hours, rule, null, NOW);
  assert.equal(h.periodEnd.getTime() - h.periodStart.getTime(), 5 * 60 * MIN);

  const monthly: QuotaWindow = { ...rolling, periodType: 'billing-cycle', periodLength: 1, resetsAt: null };
  const provider = resolvePeriodBounds(monthly, rule, new Date(2026, 10, 1), NOW);
  assert.equal(provider.periodEnd.getTime(), new Date(2026, 10, 1).getTime());
  assert.equal(provider.periodStart.getTime(), new Date(2026, 9, 1).getTime());

  const manual = resolvePeriodBounds(monthly, rule, null, NOW);
  assert.equal(manual.periodEnd.getDate(), 20);
  assert.equal(manual.periodEnd.getMonth() - manual.periodStart.getMonth(), 1);
});


test("a fresh read records today's point and a sample; one point per day, updated in place", () => {
  const a = account();
  const first = a.read(usage(weeklyWindow(40)), NOW);
  assert.equal(first.criticalWindow?.id, 'seven_day');
  assert.equal(at(first.windows, 0).dailyHistory.length, 1);
  assert.equal(a.draft.recentSamples.length, 1);
  a.read(usage(weeklyWindow(41)), new Date(NOW.getTime() + 10 * MIN));
  assert.equal(a.draft.recentSamples.length, 2);
  assert.deepEqual(a.points().map((p) => p.used), [41]);
  // A document already seen adds no sample.
  a.read(usage(weeklyWindow(41)), new Date(NOW.getTime() + 20 * MIN), { fresh: false });
  assert.equal(a.draft.recentSamples.length, 2);
});

test('a rolling-hours window gets hours-based metrics and no day metrics or chart', () => {
  const fiveHour: QuotaWindow = {
    id: 'five_hour', label: '5h', periodType: 'rolling-hours', periodLength: 5, unit: 'percentage',
    used: 20, total: 100, resetsAt: new Date(NOW.getTime() + 2 * 60 * MIN),
  };
  const w = at(account().read(usage(fiveHour), NOW).windows, 0);
  assert.notEqual(w.hourly, null);
  assert.equal(w.daysUntilReset, null);
  assert.equal(w.workingDaysUntilReset, null);
  assert.equal(w.todayBudget, null);
  assert.equal(w.efficiencyRating, null);
  assert.equal(w.paceAlert, false);
  assert.deepEqual(w.chart, []);
});

test('a monthly window gets day metrics: reset, budget and redistribution', () => {
  const monthly: QuotaWindow = { ...monthlyWindow(30), resetsAt: new Date(2026, 10, 1) };
  const snap = account().read(usage(monthly), NOW);
  const w = at(snap.windows, 0);
  assert.equal(w.hourly, null);
  assert.notEqual(w.todayBudget, null);
  assert.notEqual(w.redistribution, null);
  assert.equal(w.daysUntilReset, snap.daysUntilReset);
  assert.equal(snap.workingDaysUntilReset, w.workingDaysUntilReset);
  assert.ok(w.chart.length > 0);
});

test("pace alert: today's consumption above PACE_ALERT_RATIO × today's budget", () => {
  const a = account();
  a.read(usage(weeklyWindow(10)), new Date(2026, 9, 14, 8, 0));
  const w = at(a.read(usage(weeklyWindow(60)), NOW).windows, 0);
  assert.ok(w.todayBudget);
  assert.equal(w.paceAlert, w.todayBudget.usedToday > w.todayBudget.budget * 1.5);
  assert.equal(w.paceAlert, true);
});

test('a service read from before midnight, picked up after it, lands on its own day (#182)', () => {
  const a = account();
  a.read(usage(weeklyWindow(40)), NOW);
  const beforeMidnight = new Date(2026, 9, 14, 23, 58);
  a.read(usage(weeklyWindow(50)), new Date(2026, 9, 15, 0, 3), { readAt: beforeMidnight });
  assert.deepEqual(a.points(), [
    { date: '2026-10-14', used: 50, dayStartUsed: 40 },
    { date: '2026-10-15', used: 50, dayStartUsed: 50 },
  ]);
  assert.equal(a.draft.recentSamples.at(-1)?.timestamp, beforeMidnight.toISOString());
});

test('a late service read from yesterday moves the start of a today point already written (no double count)', () => {
  const a = account();
  const lastEvening = new Date(2026, 9, 14, 23, 50);
  a.read(usage(weeklyWindow(40)), lastEvening);
  // 00:01: the 23:50 document again: no new value, but today's point is written.
  a.read(usage(weeklyWindow(40)), new Date(2026, 9, 15, 0, 1), { readAt: lastEvening, fresh: false });
  assert.equal(a.points().find((p) => p.date === '2026-10-15')?.dayStartUsed, 40);
  // 00:03: the 23:58 read (50) belongs to yesterday; today starts from it.
  a.read(usage(weeklyWindow(50)), new Date(2026, 9, 15, 0, 3), { readAt: new Date(2026, 9, 14, 23, 58) });
  assert.deepEqual(a.points(), [
    { date: '2026-10-14', used: 50, dayStartUsed: 40 },
    { date: '2026-10-15', used: 50, dayStartUsed: 50 },
  ]);
});

test('a late service read from yesterday keeps the zero start of a renewal day (#182)', () => {
  const cfg = claudeAccount();
  cfg.subscription = { ...cfg.subscription, renewalRule: { type: 'dayOfMonth', day: 15 } };
  const a = account(cfg);
  const lastEvening = new Date(2026, 9, 14, 23, 50);
  a.read(usage(monthlyWindow(80)), lastEvening);
  a.read(usage(monthlyWindow(80)), new Date(2026, 9, 15, 0, 1), { readAt: lastEvening, fresh: false });
  const startOf = (date: string): number | undefined => a.points().find((p) => p.date === date)?.dayStartUsed;
  assert.equal(startOf('2026-10-15'), 0);
  a.read(usage(monthlyWindow(90)), new Date(2026, 9, 15, 0, 3), { readAt: new Date(2026, 9, 14, 23, 58) });
  assert.equal(a.points().find((p) => p.date === '2026-10-14')?.used, 90);
  assert.equal(startOf('2026-10-15'), 0);
  const snap = a.read(usage(monthlyWindow(5)), new Date(2026, 9, 15, 10, 0));
  assert.equal(startOf('2026-10-15'), 0);
  assert.equal(at(snap.windows, 0).todayBudget?.usedToday, 5);
});

test('a late service read from yesterday that follows a reset starts today from that value (#182)', () => {
  const a = account();
  const lastEvening = new Date(2026, 9, 14, 23, 50);
  a.read(usage(weeklyWindow(80)), lastEvening);
  a.read(usage(weeklyWindow(80)), new Date(2026, 9, 15, 0, 1), { readAt: lastEvening, fresh: false });
  a.read(usage(weeklyWindow(2)), new Date(2026, 9, 15, 0, 3), { readAt: new Date(2026, 9, 14, 23, 58) });
  assert.equal(a.points().find((p) => p.date === '2026-10-15')?.dayStartUsed, 2);
  const snap = a.read(usage(weeklyWindow(7)), new Date(2026, 9, 15, 10, 0));
  assert.equal(at(snap.windows, 0).todayBudget?.usedToday, 5);
});
