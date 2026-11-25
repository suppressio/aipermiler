// core/usage.test.ts — characterization tests of the fetch orchestration and snapshot
// computation extracted from main.ts (docs/EVOLUTION.md 2a). They pin the behaviour the
// widget relies on, over an in-memory store: no Electron, no network.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createUsageCore, refreshIntervalMs, resolvePeriodBounds } from './usage';
import type { StampedUsage, UsageCoreDeps, UsageStore } from './usage';
import { defaultAccountFor } from '../store/migrate';
import { DEFAULTS } from '../store/defaults';
import { at } from '../tests/support/at';
import type { AccountConfig, ClaudeLocalInsights, HistorySettings, ProviderId, QuotaWindow, RawAccountUsage } from '../types/index';

const MIN = 60 * 1000;
const NOW = new Date(2026, 9, 14, 10, 0, 0); // Wednesday, local time

function claudeAccount(id = 'a1', overrides: Partial<AccountConfig> = {}): AccountConfig {
  const base = defaultAccountFor('claude', id, `Claude ${id}`);
  if (base.provider !== 'claude') throw new Error('unexpected provider');
  return { ...base, enabled: true, session: { ...base.session, sessionKey: 'k' }, ...overrides } as AccountConfig;
}

function weeklyWindow(used: number, resetsInDays = 3): QuotaWindow {
  return {
    id: 'seven_day', label: 'Weekly', periodType: 'rolling-days', periodLength: 7, unit: 'percentage',
    used, total: 100, resetsAt: new Date(NOW.getTime() + resetsInDays * 24 * 60 * MIN),
  };
}

function usage(...quotaWindows: QuotaWindow[]): RawAccountUsage {
  return { planTier: 'pro', subscriptionRenewsAt: null, quotaWindows };
}

interface Harness {
  core: ReturnType<typeof createUsageCore>;
  history: HistorySettings;
  clock: { now: Date };
  fetches: string[];
  errors: { provider: ProviderId; err: unknown }[];
  logs: string[];
  localInsightsCalls: { count: number };
  answer: { next: () => Promise<RawAccountUsage> };
  // fetchedAt of the read: null for a CLI read, the service's read time otherwise (#182).
  served: { fetchedAt: string | null };
}

function harness(accounts: AccountConfig[], first: RawAccountUsage, extra: Partial<UsageCoreDeps> = {}): Harness {
  const history: HistorySettings = structuredClone(DEFAULTS.history);
  const clock = { now: NOW };
  const fetches: string[] = [];
  const errors: { provider: ProviderId; err: unknown }[] = [];
  const logs: string[] = [];
  const localInsightsCalls = { count: 0 };
  const answer = { next: async (): Promise<RawAccountUsage> => first };
  const served: { fetchedAt: string | null } = { fetchedAt: null };
  const store: UsageStore = {
    accounts: () => [...accounts],
    ui: () => DEFAULTS.ui,
    history: () => history,
    setHistory: (h) => { Object.assign(history, h); },
    setLastGood: (id, stamped: StampedUsage) => { history.lastGood = { ...history.lastGood, [id]: stamped }; },
  };
  const core = createUsageCore({
    store,
    fetchUsage: async (cfg) => { fetches.push(cfg.id); return { usage: await answer.next(), fetchedAt: served.fetchedAt }; },
    isConnected: (cfg) => cfg.provider === 'claude' && !!cfg.session.sessionKey,
    localInsights: async () => { localInsightsCalls.count += 1; return null as ClaudeLocalInsights | null; },
    describeError: (err) => `friendly: ${(err as Error).message}`,
    accountNotice: (err) => ((err as Error).message === 'signed out' ? 'sign in please' : null),
    refreshing: (err) => (err as Error).message === 'not read yet',
    onFetchError: (provider, err) => { errors.push({ provider, err }); },
    now: () => clock.now,
    logError: (message) => { logs.push(message); },
    ...extra,
  });
  return { core, history, clock, fetches, errors, logs, localInsightsCalls, answer, served };
}

test('refreshIntervalMs: configured minutes, at least 5, 30 when unset', () => {
  assert.equal(refreshIntervalMs({ refreshIntervalMinutes: 15 }), 15 * MIN);
  assert.equal(refreshIntervalMs({ refreshIntervalMinutes: 1 }), 5 * MIN);
  assert.equal(refreshIntervalMs({ refreshIntervalMinutes: 0 }), 30 * MIN);
});

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

test('first refresh: fetches, stores the last good result, records history and a sample', async () => {
  const h = harness([claudeAccount()], usage(weeklyWindow(40)));
  const { snapshot, consumptionRose } = await h.core.buildUsageSnapshot('forced');

  assert.deepEqual(h.fetches, ['a1']);
  assert.equal(snapshot.generatedAt, NOW.toISOString());
  assert.equal(snapshot.accounts.length, 1);
  const account = at(snapshot.accounts, 0);
  assert.equal(account.accountId, 'a1');
  assert.equal(account.provider, 'claude');
  assert.equal(account.stale, false);
  assert.equal(account.criticalWindow?.id, 'seven_day');
  assert.equal(at(account.windows, 0).window.used, 40);
  assert.equal(at(account.windows, 0).dailyHistory.length, 1);

  assert.equal(h.history.lastGood?.a1?.lastUpdatedAt, NOW.toISOString());
  assert.equal(h.history.dailyUsage.length, 1);
  assert.equal(at(h.history.dailyUsage, 0).used, 40);
  assert.equal(h.history.recentSamples.length, 1);
  assert.equal(at(h.history.recentSamples, 0).used, 40);
  // Nothing to compare the first sample with: no rise.
  assert.equal(consumptionRose, false);
});

test('a rise since the last sample is reported; no rise is not', async () => {
  const h = harness([claudeAccount()], usage(weeklyWindow(40)));
  await h.core.buildUsageSnapshot('forced');

  h.clock.now = new Date(NOW.getTime() + 10 * MIN);
  h.answer.next = async () => usage(weeklyWindow(41));
  assert.equal((await h.core.buildUsageSnapshot('forced')).consumptionRose, true);

  h.clock.now = new Date(NOW.getTime() + 20 * MIN);
  assert.equal((await h.core.buildUsageSnapshot('forced')).consumptionRose, false);
  assert.equal(h.history.recentSamples.length, 3);
  // One daily point per window and day, updated in place.
  assert.equal(h.history.dailyUsage.length, 1);
  assert.equal(at(h.history.dailyUsage, 0).used, 41);
});

test('a Claude account is fetched through the provider and keeps its local insights (#180)', async () => {
  const insights = { computedAt: NOW.toISOString(), firstSessionStartByDay: {} } as unknown as ClaudeLocalInsights;
  const h = harness([claudeAccount('a1', { localInsights: true })], usage(weeklyWindow(40)), {
    localInsights: async () => insights,
  });
  const { snapshot } = await h.core.buildUsageSnapshot('forced');

  assert.deepEqual(h.fetches, ['a1']);
  const account = at(snapshot.accounts, 0);
  assert.equal(account.lastError, undefined);
  assert.equal(account.windows.length, 1);
  assert.equal(account.localInsights, insights);
});

test('disabled and not connected accounts are skipped', async () => {
  const off = claudeAccount('off', { enabled: false });
  const noKey = defaultAccountFor('claude', 'nokey');
  const h = harness([off, { ...noKey, enabled: true }, claudeAccount('on')], usage(weeklyWindow(10)));
  const { snapshot } = await h.core.buildUsageSnapshot('forced');
  assert.deepEqual(snapshot.accounts.map((a) => a.accountId), ['on']);
  assert.deepEqual(h.fetches, ['on']);
});

test('failed fetch with a previous result: stale data with the friendly error, drift hook called', async () => {
  const h = harness([claudeAccount()], usage(weeklyWindow(40)));
  await h.core.buildUsageSnapshot('forced');

  h.clock.now = new Date(NOW.getTime() + 10 * MIN);
  const failure = new Error('boom');
  h.answer.next = async () => { throw failure; };
  const { snapshot, consumptionRose } = await h.core.buildUsageSnapshot('forced');

  const account = at(snapshot.accounts, 0);
  assert.equal(account.stale, true);
  assert.equal(account.lastError, 'friendly: boom');
  assert.equal(account.lastUpdatedAt, NOW.toISOString());
  assert.equal(at(account.windows, 0).window.used, 40);
  assert.deepEqual(h.errors, [{ provider: 'claude', err: failure }]);
  assert.equal(consumptionRose, false);
  // Stale data is not a new sample.
  assert.equal(h.history.recentSamples.length, 1);
});

test('failed fetch with no previous result: empty stale snapshot with the reason', async () => {
  const h = harness([claudeAccount()], usage(weeklyWindow(40)));
  h.answer.next = async () => { throw new Error('down'); };
  const { snapshot } = await h.core.buildUsageSnapshot('forced');

  const account = at(snapshot.accounts, 0);
  assert.equal(account.stale, true);
  assert.equal(account.lastError, 'friendly: down');
  assert.deepEqual(account.windows, []);
  assert.equal(account.criticalWindow, null);
  assert.equal(account.label, 'Claude a1');
  assert.equal(h.errors.length, 1);
  assert.ok(h.logs.some((l) => l.includes('unavailable and no previous data')));
});

test('a failing account in backoff is not fetched on a scheduled tick and stays stale with the reason', async () => {
  const h = harness([claudeAccount()], usage(weeklyWindow(40)));
  await h.core.buildUsageSnapshot('forced');
  h.answer.next = async () => { throw new Error('boom'); };
  h.clock.now = new Date(NOW.getTime() + 10 * MIN);
  await h.core.buildUsageSnapshot('forced');
  assert.equal(h.fetches.length, 2);

  // One failure: the gap is 10 minutes; 5 minutes later a scheduled tick skips the account.
  h.clock.now = new Date(NOW.getTime() + 15 * MIN);
  const { snapshot, consumptionRose } = await h.core.buildUsageSnapshot('scheduled');
  assert.equal(h.fetches.length, 2);
  assert.equal(consumptionRose, null);
  assert.equal(at(snapshot.accounts, 0).stale, true);
  assert.equal(at(snapshot.accounts, 0).lastError, 'friendly: boom');

  // After the gap it fetches again.
  h.clock.now = new Date(NOW.getTime() + 21 * MIN);
  await h.core.buildUsageSnapshot('scheduled');
  assert.equal(h.fetches.length, 3);
});

test('a manual refresh within a minute of the last fetch reuses it: fresh data, no new sample', async () => {
  const h = harness([claudeAccount()], usage(weeklyWindow(40)));
  await h.core.buildUsageSnapshot('forced');
  h.clock.now = new Date(NOW.getTime() + 30 * 1000);
  const { snapshot, consumptionRose } = await h.core.buildUsageSnapshot('manual');
  assert.equal(h.fetches.length, 1);
  assert.equal(consumptionRose, null);
  assert.equal(at(snapshot.accounts, 0).stale, false);
  assert.equal(h.history.recentSamples.length, 1);

  h.clock.now = new Date(NOW.getTime() + 2 * MIN);
  await h.core.buildUsageSnapshot('manual');
  assert.equal(h.fetches.length, 2);
});

test('local insights are asked only for Claude accounts that opted in, and attached to the snapshot', async () => {
  const insights = { computedAt: NOW.toISOString(), windowDays: 8 } as ClaudeLocalInsights;
  const plain = claudeAccount('plain');
  const opted = { ...claudeAccount('opted'), localInsights: true } as AccountConfig;
  const h = harness([plain, opted], usage(weeklyWindow(10)), { localInsights: async () => insights });
  const { snapshot } = await h.core.buildUsageSnapshot('forced');
  assert.equal(at(snapshot.accounts, 0).localInsights, undefined);
  assert.equal(at(snapshot.accounts, 1).localInsights, insights);

  const counter = harness([plain, opted], usage(weeklyWindow(10)));
  await counter.core.buildUsageSnapshot('forced');
  assert.equal(counter.localInsightsCalls.count, 1);
});

test('history is pruned on commit: old daily points by retention, old samples by age', async () => {
  const h = harness([claudeAccount()], usage(weeklyWindow(40)));
  h.history.dailyUsage.push({ date: '2026-01-01', accountId: 'a1', windowId: 'seven_day', used: 5, dayStartUsed: 0 });
  h.history.recentSamples.push(
    { timestamp: new Date(2026, 9, 12, 9, 0).toISOString(), accountId: 'a1', windowId: 'seven_day', used: 1 },
    { timestamp: new Date(2026, 9, 14, 7, 0).toISOString(), accountId: 'a1', windowId: 'seven_day', used: 2 },
  );
  await h.core.buildUsageSnapshot('forced');
  assert.deepEqual(h.history.dailyUsage.map((p) => p.date), ['2026-10-14']);
  // The day before is gone; this morning's sample (same local day) and the new one stay.
  assert.deepEqual(h.history.recentSamples.map((s) => s.used), [2, 40]);
});

test('a rolling-hours window gets hours-based metrics and no day metrics or chart', async () => {
  const fiveHour: QuotaWindow = {
    id: 'five_hour', label: '5h', periodType: 'rolling-hours', periodLength: 5, unit: 'percentage',
    used: 20, total: 100, resetsAt: new Date(NOW.getTime() + 2 * 60 * MIN),
  };
  const h = harness([claudeAccount()], usage(fiveHour));
  const w = at(at((await h.core.buildUsageSnapshot('forced')).snapshot.accounts, 0).windows, 0);
  assert.notEqual(w.hourly, null);
  assert.equal(w.daysUntilReset, null);
  assert.equal(w.workingDaysUntilReset, null);
  assert.equal(w.todayBudget, null);
  assert.equal(w.efficiencyRating, null);
  assert.deepEqual(w.chart, []);
});

test('a monthly window pace gets day metrics: reset, budget and redistribution', async () => {
  const monthly: QuotaWindow = {
    id: 'spend', label: 'Spend', periodType: 'billing-cycle', periodLength: 1, unit: 'percentage',
    used: 30, total: 100, resetsAt: new Date(2026, 10, 1),
  };
  const h = harness([claudeAccount()], usage(monthly));
  const account = at((await h.core.buildUsageSnapshot('forced')).snapshot.accounts, 0);
  const w = at(account.windows, 0);
  assert.equal(w.hourly, null);
  assert.notEqual(w.todayBudget, null);
  assert.notEqual(w.redistribution, null);
  assert.equal(w.daysUntilReset, account.daysUntilReset);
  assert.equal(account.workingDaysUntilReset, w.workingDaysUntilReset);
  assert.ok(w.chart.length > 0);
});

test('a failed fetch that is a state (signed out) is a notice, not an error, with or without previous data (#178)', async () => {
  const h = harness([claudeAccount()], usage(weeklyWindow(40)));
  h.answer.next = async () => { throw new Error('signed out'); };
  const first = at((await h.core.buildUsageSnapshot('forced')).snapshot.accounts, 0);
  assert.equal(first.notice, 'sign in please');
  assert.equal(first.lastError, undefined);
  assert.equal(first.stale, true);

  h.answer.next = async () => usage(weeklyWindow(40));
  h.clock.now = new Date(NOW.getTime() + 30 * MIN);
  const ok = at((await h.core.buildUsageSnapshot('forced')).snapshot.accounts, 0);
  assert.equal(ok.notice, undefined);
  assert.equal(ok.stale, false);

  h.answer.next = async () => { throw new Error('signed out'); };
  h.clock.now = new Date(NOW.getTime() + 60 * MIN);
  const later = at((await h.core.buildUsageSnapshot('forced')).snapshot.accounts, 0);
  assert.equal(later.notice, 'sign in please');
  assert.equal(later.lastError, undefined);
  assert.equal(later.stale, true);
  assert.equal(at(later.windows, 0).window.used, 40, 'the last data stays shown');

  // A scheduled tick during the backoff keeps the notice.
  h.clock.now = new Date(NOW.getTime() + 62 * MIN);
  const skipped = at((await h.core.buildUsageSnapshot('scheduled')).snapshot.accounts, 0);
  assert.equal(skipped.notice, 'sign in please');
});

test('a service read records a sample only when its fetchedAt is newer than the last one (#182 (a))', async () => {
  const h = harness([claudeAccount()], usage(weeklyWindow(40)));
  const servedAt = new Date(NOW.getTime() - 2 * MIN).toISOString();
  h.served.fetchedAt = servedAt;
  const first = at((await h.core.buildUsageSnapshot('forced')).snapshot.accounts, 0);
  assert.equal(first.stale, false);
  assert.equal(first.lastUpdatedAt, servedAt, 'the service read time, not the app clock');
  assert.equal(h.history.recentSamples.length, 1);

  // The same document again (the service has not read the account since): shown as
  // current, no duplicate sample, no rise.
  h.clock.now = new Date(NOW.getTime() + 10 * MIN);
  h.answer.next = async () => usage(weeklyWindow(40));
  const again = await h.core.buildUsageSnapshot('forced');
  const same = at(again.snapshot.accounts, 0);
  assert.equal(same.stale, false);
  assert.equal(same.lastUpdatedAt, servedAt);
  assert.equal(h.history.recentSamples.length, 1);
  assert.equal(again.consumptionRose, false);

  // An older document than the app's own last read (a CLI read meanwhile): the newer data stays.
  h.served.fetchedAt = null;
  h.clock.now = new Date(NOW.getTime() + 20 * MIN);
  h.answer.next = async () => usage(weeklyWindow(45));
  await h.core.buildUsageSnapshot('forced');
  assert.equal(h.history.recentSamples.length, 2);
  h.served.fetchedAt = new Date(NOW.getTime() + 15 * MIN).toISOString();
  h.clock.now = new Date(NOW.getTime() + 30 * MIN);
  h.answer.next = async () => usage(weeklyWindow(43));
  const older = at((await h.core.buildUsageSnapshot('forced')).snapshot.accounts, 0);
  assert.equal(at(older.windows, 0).window.used, 45);
  assert.equal(h.history.recentSamples.length, 2);

  // A newer service read: a new sample at its own time.
  const newer = new Date(NOW.getTime() + 38 * MIN).toISOString();
  h.served.fetchedAt = newer;
  h.clock.now = new Date(NOW.getTime() + 40 * MIN);
  h.answer.next = async () => usage(weeklyWindow(50));
  const fresh = await h.core.buildUsageSnapshot('forced');
  assert.equal(at(fresh.snapshot.accounts, 0).lastUpdatedAt, newer);
  assert.equal(h.history.recentSamples.length, 3);
  assert.equal(h.history.recentSamples.at(-1)?.timestamp, newer, 'the sample is stamped with the service read time');
  assert.equal(fresh.consumptionRose, true);
});

test('not read yet by a restarted service: a neutral refreshing state, not a failure (#182)', async () => {
  const h = harness([claudeAccount()], usage(weeklyWindow(40)));
  h.answer.next = async () => { throw new Error('not read yet'); };
  const empty = at((await h.core.buildUsageSnapshot('forced')).snapshot.accounts, 0);
  assert.equal(empty.refreshing, true);
  assert.equal(empty.lastError, undefined);
  assert.equal(empty.notice, undefined);
  assert.equal(empty.stale, true);
  assert.deepEqual(h.logs, [], 'not logged as a failure');

  h.answer.next = async () => usage(weeklyWindow(40));
  h.clock.now = new Date(NOW.getTime() + 10 * MIN);
  const ok = at((await h.core.buildUsageSnapshot('forced')).snapshot.accounts, 0);
  assert.equal(ok.refreshing, undefined);

  h.answer.next = async () => { throw new Error('not read yet'); };
  h.clock.now = new Date(NOW.getTime() + 20 * MIN);
  const later = at((await h.core.buildUsageSnapshot('forced')).snapshot.accounts, 0);
  assert.equal(later.refreshing, true);
  assert.equal(later.lastError, undefined);
  assert.equal(later.stale, true);
  assert.equal(at(later.windows, 0).window.used, 40, 'the last data stays shown');
  assert.equal(later.lastUpdatedAt, new Date(NOW.getTime() + 10 * MIN).toISOString());

  // Retried soon (the first backoff step), and still refreshing meanwhile.
  h.clock.now = new Date(NOW.getTime() + 22 * MIN);
  const skipped = at((await h.core.buildUsageSnapshot('scheduled')).snapshot.accounts, 0);
  assert.equal(skipped.refreshing, true);
  assert.deepEqual(h.logs, []);
});

test('a service read from before midnight, picked up after it, lands on its own day (#182)', async () => {
  const h = harness([claudeAccount()], usage(weeklyWindow(40)));
  await h.core.buildUsageSnapshot('forced');

  const beforeMidnight = new Date(2026, 9, 14, 23, 58).toISOString();
  h.served.fetchedAt = beforeMidnight;
  h.clock.now = new Date(2026, 9, 15, 0, 3);
  h.answer.next = async () => usage(weeklyWindow(50));
  await h.core.buildUsageSnapshot('forced');

  const points = h.history.dailyUsage.filter((p) => p.windowId === 'seven_day').map(({ date, used, dayStartUsed }) => ({ date, used, dayStartUsed }));
  assert.deepEqual(points, [
    { date: '2026-10-14', used: 50, dayStartUsed: 40 },
    { date: '2026-10-15', used: 50, dayStartUsed: 50 },
  ]);
  assert.equal(h.history.recentSamples.at(-1)?.timestamp, beforeMidnight);
});

test('a CLI read that takes a few seconds keeps its sample in the instant pace of its refresh', async () => {
  const h = harness([claudeAccount()], usage(weeklyWindow(10)));
  // The fetch takes 5 s of the fake clock: the read is stamped after the refresh began.
  const slowRead = (used: number) => async (): Promise<RawAccountUsage> => {
    h.clock.now = new Date(h.clock.now.getTime() + 5000);
    return usage(weeklyWindow(used));
  };
  h.answer.next = slowRead(10);
  await h.core.buildUsageSnapshot('forced');
  h.clock.now = new Date(NOW.getTime() + 30 * MIN);
  h.answer.next = slowRead(20);
  await h.core.buildUsageSnapshot('forced');
  h.clock.now = new Date(NOW.getTime() + 60 * MIN);
  h.answer.next = slowRead(50);
  const { snapshot } = await h.core.buildUsageSnapshot('forced');
  assert.equal(at(at(snapshot.accounts, 0).windows, 0).instantRate, 40);
  // The samples carry the time the refresh began, as before #182.
  assert.equal(h.history.recentSamples.at(-1)?.timestamp, new Date(NOW.getTime() + 60 * MIN).toISOString());
});

test('a refresh started before midnight whose CLI read ends after it writes no point for the next day', async () => {
  const h = harness([claudeAccount()], usage(weeklyWindow(40)));
  const started = new Date(2026, 9, 14, 23, 59, 50);
  h.clock.now = started;
  h.answer.next = async () => {
    h.clock.now = new Date(2026, 9, 15, 0, 0, 20);
    return usage(weeklyWindow(40));
  };
  await h.core.buildUsageSnapshot('forced');
  assert.deepEqual(h.history.dailyUsage.map((p) => p.date), ['2026-10-14']);
  assert.equal(h.history.recentSamples.at(-1)?.timestamp, started.toISOString());
});

test('a late service read from yesterday moves the start of a today point already written (no double count)', async () => {
  const h = harness([claudeAccount()], usage(weeklyWindow(40)));
  const lastEvening = new Date(2026, 9, 14, 23, 50);
  h.clock.now = lastEvening;
  await h.core.buildUsageSnapshot('forced');

  // 00:01: the service still holds the 23:50 document: no new value, but today's point is written.
  h.served.fetchedAt = lastEvening.toISOString();
  h.clock.now = new Date(2026, 9, 15, 0, 1);
  await h.core.buildUsageSnapshot('forced');
  assert.deepEqual(h.history.dailyUsage.find((p) => p.date === '2026-10-15')?.dayStartUsed, 40);

  // 00:03: the service's 23:58 read (50) belongs to yesterday; today starts from it.
  h.served.fetchedAt = new Date(2026, 9, 14, 23, 58).toISOString();
  h.clock.now = new Date(2026, 9, 15, 0, 3);
  h.answer.next = async () => usage(weeklyWindow(50));
  await h.core.buildUsageSnapshot('forced');
  const points = h.history.dailyUsage.map(({ date, used, dayStartUsed }) => ({ date, used, dayStartUsed }));
  assert.deepEqual(points, [
    { date: '2026-10-14', used: 50, dayStartUsed: 40 },
    { date: '2026-10-15', used: 50, dayStartUsed: 50 },
  ]);
});

test('a late service read from yesterday keeps the zero start of a renewal day (#182)', async () => {
  const account = claudeAccount();
  account.subscription = { ...account.subscription, renewalRule: { type: 'dayOfMonth', day: 15 } };
  const monthly = (used: number): QuotaWindow => ({
    id: 'spend', label: 'Monthly', periodType: 'billing-cycle', periodLength: 1, unit: 'percentage', used, total: 100, resetsAt: null,
  });
  const h = harness([account], usage(monthly(80)));
  const lastEvening = new Date(2026, 9, 14, 23, 50);
  h.clock.now = lastEvening;
  await h.core.buildUsageSnapshot('forced');

  // 00:01 on the renewal day: same document, today's point starts at 0 (new period).
  h.served.fetchedAt = lastEvening.toISOString();
  h.clock.now = new Date(2026, 9, 15, 0, 1);
  await h.core.buildUsageSnapshot('forced');
  const startOf = (date: string): number | undefined => h.history.dailyUsage.find((p) => p.date === date)?.dayStartUsed;
  assert.equal(startOf('2026-10-15'), 0);

  // 00:03: the service's 23:58 read (90) belongs to the old period; today still starts at 0.
  h.served.fetchedAt = new Date(2026, 9, 14, 23, 58).toISOString();
  h.clock.now = new Date(2026, 9, 15, 0, 3);
  h.answer.next = async () => usage(monthly(90));
  await h.core.buildUsageSnapshot('forced');
  assert.equal(h.history.dailyUsage.find((p) => p.date === '2026-10-14')?.used, 90);
  assert.equal(startOf('2026-10-15'), 0);

  // 10:00: the new period's first value; today's consumption is 5.
  h.served.fetchedAt = null;
  h.clock.now = new Date(2026, 9, 15, 10, 0);
  h.answer.next = async () => usage(monthly(5));
  const { snapshot } = await h.core.buildUsageSnapshot('forced');
  assert.equal(startOf('2026-10-15'), 0);
  assert.equal(at(at(snapshot.accounts, 0).windows, 0).todayBudget?.usedToday, 5);
});

test('a late service read from yesterday that follows a reset starts today from that value (#182)', async () => {
  const h = harness([claudeAccount()], usage(weeklyWindow(80)));
  const lastEvening = new Date(2026, 9, 14, 23, 50);
  h.clock.now = lastEvening;
  await h.core.buildUsageSnapshot('forced');
  h.served.fetchedAt = lastEvening.toISOString();
  h.clock.now = new Date(2026, 9, 15, 0, 1);
  await h.core.buildUsageSnapshot('forced');

  // The window reset at 23:55: the service's 23:58 read is 2.
  h.served.fetchedAt = new Date(2026, 9, 14, 23, 58).toISOString();
  h.clock.now = new Date(2026, 9, 15, 0, 3);
  h.answer.next = async () => usage(weeklyWindow(2));
  await h.core.buildUsageSnapshot('forced');
  assert.equal(h.history.dailyUsage.find((p) => p.date === '2026-10-15')?.dayStartUsed, 2);

  h.served.fetchedAt = null;
  h.clock.now = new Date(2026, 9, 15, 10, 0);
  h.answer.next = async () => usage(weeklyWindow(7));
  const { snapshot } = await h.core.buildUsageSnapshot('forced');
  assert.equal(at(at(snapshot.accounts, 0).windows, 0).todayBudget?.usedToday, 5);
});
