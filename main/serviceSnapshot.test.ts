// main/serviceSnapshot.test.ts — the pacing snapshot client (#341) against the service's
// own fixtures (gocore/contract/v1/service/snapshot.json, history.json): parsing, the
// mapping to the renderer's types, the account without a service, and the client
// against a loopback fake service.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import type { AddressInfo } from 'node:net';
import { CoreProviderError, parseUsageRun } from './coreClient';
import { ServiceUnusableError } from './serviceClient';
import {
  accountFromSnapshot,
  accountWithoutService,
  ServiceDownError,
  createSnapshotClient,
  lastGoodView,
  parseServiceHistory,
  parseServiceSnapshot,
  type SnapshotClient,
} from './serviceSnapshot';
import { at } from '../tests/support/at';

const FIXTURES = path.join(__dirname, '..', '..', 'gocore', 'contract', 'v1');
const fixture = (name: string): string => fs.readFileSync(path.join(FIXTURES, `${name}.json`), 'utf8');
const IDENTITY = { accountId: 'a1', provider: 'codex' as const, label: 'Work' };

function unusableReason(fn: () => unknown): string {
  try {
    fn();
  } catch (err) {
    if (err instanceof ServiceUnusableError) return err.reason;
    throw err;
  }
  assert.fail('expected a ServiceUnusableError');
}

test('snapshot fixture: usage document as /usage reads it, pacing in the windows order', () => {
  const read = parseServiceSnapshot(200, fixture('service/snapshot'), 'fake');
  assert.equal(read.result.fetchedAt, '2026-11-04T14:55:00Z');
  assert.equal(read.result.stale, false);
  assert.deepEqual(read.result.usage.quotaWindows.map((w) => w.id), ['five-hour', 'weekly', 'premium', 'extra']);
  assert.equal(read.pacing.critical, 1);
  assert.deepEqual(read.pacing.windows.map((w) => w.id), ['five-hour', 'weekly', 'premium', 'extra']);
  assert.equal(at(read.pacing.windows, 0).trend, 'accelerating');
  assert.equal(at(read.pacing.windows, 3).used, null);
});

test('snapshot fixture: mapped to the widget types, exact values (I-4), null stays null (I-5)', () => {
  const account = accountFromSnapshot(IDENTITY, parseServiceSnapshot(200, fixture('service/snapshot'), 'fake'));
  assert.equal(account.accountId, 'a1');
  assert.equal(account.stale, false);
  assert.equal(account.lastUpdatedAt, '2026-11-04T14:55:00Z');
  assert.equal(account.serviceDown, undefined);
  // critical = 1: the weekly window opens first, its fields are on the account.
  assert.equal(account.criticalWindow?.id, 'weekly');
  assert.equal(account.projectedUsage, 81.82692307692307);
  assert.equal(account.workingDaysUntilReset, 3.75);

  const [fiveHour, weekly, premium, extra] = account.windows;
  assert.ok(fiveHour && weekly && premium && extra);
  // Rolling hours: read in hours, no days to reset.
  assert.deepEqual(fiveHour.hourly, { hoursLeft: 2, projectedAtReset: 52, autonomyHours: 6.363636363636363 });
  assert.equal(fiveHour.projectedUsage, 52);
  assert.equal(fiveHour.daysUntilReset, null);
  assert.equal(fiveHour.instantRate, 11);
  // The gauge's reference is the target (D-3.13), not the ceiling.
  assert.equal(fiveHour.sustainableRate, 20);
  // The ceiling stays apart as a limit (#72).
  assert.equal(fiveHour.ceiling, 35);
  assert.deepEqual(fiveHour.verdict, { kind: 'on-track' });
  // The instant trend (D-3.28) drives the gauge arrows (#304).
  assert.equal(fiveHour.trend, 'accelerating');
  assert.deepEqual(fiveHour.chart, []);

  assert.deepEqual(weekly.verdict, { kind: 'on-track', perUnit: 13.714285714285714, idealPerUnit: 14.285714285714286 });
  assert.deepEqual(weekly.redistribution, { perUnit: 13.714285714285714, idealPerUnit: 14.285714285714286, unitsLeft: 4.375 });
  assert.deepEqual(weekly.todayBudget, { budget: 16, usedToday: 10 });
  assert.equal(weekly.efficiencyIndex, 1.1607142857142858);
  assert.equal(weekly.estimatedAutonomyWorkingDays, 5.379310344827586);
  assert.equal(weekly.daysUntilReset, 3.75);
  assert.equal(weekly.preliminary, false);
  assert.deepEqual(weekly.deltaStats, { peak: 15, avg: 10, streakUnderBudget: 3 });
  assert.deepEqual(weekly.efficiencyRating, { stars: 5, avgRatio: 1.746031746031746 });
  assert.deepEqual(weekly.dailyTip, { key: 'rating', params: { average: 1.746031746031746, stars: 5 } });
  assert.equal(weekly.chart.length, 9);
  assert.deepEqual(at(weekly.chart, 4), { date: '2026-11-02', delta: 10, fullShare: 14.285714285714286, dayUnit: 1, budget: 15.833333333333334, upcoming: false, overBudget: false });
  assert.equal(weekly.tokenYield, null);
  assert.equal(weekly.trend, null);

  // No pacing: no tip is "none", the verdict says no pacing.
  assert.deepEqual(premium.verdict, { kind: 'no-pacing' });
  assert.deepEqual(premium.dailyTip, { key: 'none', params: {} });
  assert.equal(premium.todayBudget, null);
  assert.equal(premium.daysUntilReset, 26);
  assert.equal(extra.efficiencyRating, null);
});

const snapshotDoc = (): Record<string, unknown> => JSON.parse(fixture('service/snapshot')) as Record<string, unknown>;
function withWeekly(change: (w: Record<string, unknown>) => void): string {
  const doc = snapshotDoc();
  const pacing = doc.pacing as { windows: Record<string, unknown>[] };
  change(at(pacing.windows, 1));
  return JSON.stringify(doc);
}

test('verdict numbers follow the rule that decided it (§3.10)', () => {
  const verdictOf = (body: string): unknown => at(accountFromSnapshot(IDENTITY, parseServiceSnapshot(200, body, 'fake')).windows, 1).verdict;
  // Pace: the redistribution plus the current pace.
  assert.deepEqual(verdictOf(withWeekly((w) => { w.judgement = { verdict: 'at-risk', reason: 'pace', autonomyHours: null }; })),
    { kind: 'at-risk', perUnit: 13.714285714285714, idealPerUnit: 14.285714285714286, pacePerUnit: 11.153846153846153 });
  assert.deepEqual(verdictOf(withWeekly((w) => { w.judgement = { verdict: 'at-risk', reason: 'autonomy', autonomyHours: null }; })),
    { kind: 'at-risk', autonomyWorkingDays: 5.379310344827586 });
  assert.deepEqual(verdictOf(withWeekly((w) => { w.judgement = { verdict: 'at-risk', reason: 'projection', autonomyHours: null }; })),
    { kind: 'at-risk', projectedUsage: 81.82692307692307 });
  assert.deepEqual(verdictOf(withWeekly((w) => { w.judgement = { verdict: 'at-risk', reason: 'hourly', autonomyHours: 1.5 }; })),
    { kind: 'at-risk', autonomyHours: 1.5 });
  assert.deepEqual(verdictOf(withWeekly((w) => { w.judgement = { verdict: 'exhausted', reason: null, autonomyHours: null }; })),
    { kind: 'exhausted' });
});

test('local insights of the snapshot reach the window (yield) and the tip (cause)', () => {
  const body = withWeekly((w) => {
    w.tokenYield = { tokensPerPercent: 12000, trendPercent: -4.5, daysCompared: 6 };
    w.tip = { key: 'cause', params: { highDaysHighContextPercent: 70, lowDaysHighContextPercent: 20 } };
  });
  const weekly = at(accountFromSnapshot(IDENTITY, parseServiceSnapshot(200, body, 'fake')).windows, 1);
  assert.deepEqual(weekly.tokenYield, { tokensPerPercent: 12000, trendPercent: -4.5, daysCompared: 6 });
  assert.deepEqual(weekly.dailyTip, { key: 'cause', params: { highDaysHighContextPercent: 70, lowDaysHighContextPercent: 20 } });
});

test('a stale snapshot keeps the last values with the problem of the last read', () => {
  const read = parseServiceSnapshot(200, fixture('service/snapshot'), 'fake');
  const account = accountFromSnapshot(IDENTITY, read, { refreshing: true });
  assert.equal(account.stale, true);
  assert.equal(account.refreshing, true);
  assert.equal(account.windows.length, 4);
});

test('a snapshot error document is the account\'s answer; a malformed pacing means no usable service', () => {
  assert.throws(() => parseServiceSnapshot(503, fixture('service/error-not-read'), 'fake'), CoreProviderError);
  assert.equal(unusableReason(() => parseServiceSnapshot(404, fixture('error-usage'), 'fake')), 'unknown_account');
  const broken = [
    withWeekly((w) => { w.target = '1'; }),
    withWeekly((w) => { w.judgement = { verdict: 7, reason: null, autonomyHours: null }; }),
    withWeekly((w) => { w.id = 'monthly'; }),
    withWeekly((w) => { w.chart = null; }),
    JSON.stringify({ ...snapshotDoc(), pacing: { critical: 4, windows: (snapshotDoc().pacing as { windows: unknown[] }).windows } }),
    JSON.stringify({ ...snapshotDoc(), pacing: undefined }),
  ];
  for (const body of broken) assert.equal(unusableReason(() => parseServiceSnapshot(200, body, 'fake')), 'malformed', body.slice(0, 80));
});

test('enum values this client does not know read as the neutral value, not a broken snapshot', () => {
  const body = withWeekly((w) => {
    w.judgement = { verdict: 'cruising', reason: 'vibes', autonomyHours: null };
    w.trend = 'wobbling';
    w.tip = { key: 'newTip', params: { x: 1 } };
  });
  const read = parseServiceSnapshot(200, body, 'fake');
  const w = at(read.pacing.windows, 1);
  assert.deepEqual(w.judgement, { verdict: 'no-pacing', reason: null, autonomyHours: null });
  assert.equal(w.trend, null);
  assert.equal(w.tip, null);
});

test('Settings reads each account\'s last values from the last snapshot (renewal lock)', () => {
  const account = accountFromSnapshot(IDENTITY, parseServiceSnapshot(200, fixture('service/snapshot'), 'fake'));
  const empty = accountWithoutService({ ...IDENTITY, accountId: 'a2' }, null, { lastError: 'boom' });
  const view = lastGoodView({ generatedAt: '2026-11-04T14:55:00Z', accounts: [account, empty] });
  assert.deepEqual(Object.keys(view), ['a1']);
  const a1 = view.a1;
  assert.ok(a1);
  assert.equal(a1.lastUpdatedAt, '2026-11-04T14:55:00Z');
  assert.equal(a1.quotaWindows.length, 4);
  assert.deepEqual(lastGoodView(null), {});
});

test('unknown fields of the snapshot are ignored (additive v1)', () => {
  const body = withWeekly((w) => { w.futureField = { a: 1 }; });
  assert.equal(parseServiceSnapshot(200, body, 'fake').pacing.windows.length, 4);
});

test('without a service: raw values, no pacing, most used window first, serviceDown', () => {
  const result = parseUsageRun({ exitCode: 0, stdout: fixture('usage-ok') }, 'fake');
  const account = accountWithoutService(IDENTITY, result);
  assert.equal(account.serviceDown, true);
  assert.equal(account.stale, false);
  assert.equal(account.lastUpdatedAt, result.fetchedAt);
  assert.equal(account.windows.length, result.usage.quotaWindows.length);
  for (const w of account.windows) {
    assert.equal(w.instantRate, null);
    assert.equal(w.todayBudget, null);
    assert.deepEqual(w.dailyTip, { key: 'none', params: {} });
    assert.ok(w.verdict.kind === 'no-pacing' || w.verdict.kind === 'exhausted');
  }
  const ratio = (w: { used: number; total: number | null }): number => (w.total ? w.used / w.total : w.used / 100);
  const mostUsed = [...result.usage.quotaWindows].sort((a, b) => ratio(b) - ratio(a))[0];
  assert.equal(account.criticalWindow?.id, mostUsed?.id);

  // A failed CLI read too: no values, the problem, still serviceDown.
  const empty = accountWithoutService(IDENTITY, null, { lastError: 'boom' });
  assert.equal(empty.serviceDown, true);
  assert.equal(empty.stale, true);
  assert.equal(empty.lastError, 'boom');
  assert.equal(empty.criticalWindow, null);
  assert.deepEqual(empty.windows, []);
});

test('history fixture: daily points and today\'s samples per window', () => {
  const history = parseServiceHistory(200, fixture('service/history'), 'fake');
  assert.equal(history.days, 7);
  const five = at(history.windows, 0);
  assert.equal(five.id, 'five-hour');
  assert.deepEqual(at(five.daily, 0), { day: '2026-11-04', used: 30, dayStartUsed: 10 });
  assert.deepEqual(at(five.samplesToday, 0), { at: '2026-11-04T13:40:00Z', used: 18 });
  assert.equal(unusableReason(() => parseServiceHistory(200, fixture('service/history').replace('"provider":"fake"', '"provider":"codex"'), 'fake')), 'malformed');
  assert.equal(unusableReason(() => parseServiceHistory(404, fixture('error-usage'), 'fake')), 'unknown_account');
  assert.equal(unusableReason(() => parseServiceHistory(500, '', 'fake')), 'failed');
});

// ---------------------------------------------------------------------------
// Client against a loopback fake service
// ---------------------------------------------------------------------------

const TOKEN = 'synthetic-service-token';

async function withService(routes: Record<string, { status: number; body: string }>, run: (client: SnapshotClient, requests: { method: string; url: string; body: string }[]) => Promise<void>): Promise<void> {
  const requests: { method: string; url: string; body: string }[] = [];
  const server = http.createServer((req, res) => {
    let body = '';
    req.on('data', (chunk: Buffer) => { body += chunk.toString(); });
    req.on('end', () => {
      requests.push({ method: req.method ?? '', url: req.url ?? '', body });
      if (req.headers.authorization !== `Bearer ${TOKEN}`) {
        res.writeHead(401).end();
        return;
      }
      const route = routes[`${req.method ?? ''} ${req.url ?? ''}`];
      if (!route) {
        res.writeHead(404, { 'Content-Type': 'application/json' }).end(fixture('error-usage'));
        return;
      }
      res.writeHead(route.status, { 'Content-Type': 'application/json' }).end(route.body);
    });
  });
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const { port } = server.address() as AddressInfo;
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'aipermiler-snapshot-'));
  fs.writeFileSync(path.join(dir, 'api.json'), `${JSON.stringify({ schemaVersion: 1, port, token: TOKEN, version: '1.2.3', contractVersion: 1, pid: 1, startedAt: '2026-11-04T14:00:00Z' })}\n`);
  try {
    await run(createSnapshotClient({ configDir: () => dir }), requests);
  } finally {
    await new Promise<void>((resolve) => { server.close(() => { resolve(); }); server.closeAllConnections(); });
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

const HEALTH = { status: 200, body: fixture('service/health') };

test('client: snapshot with the chart span, refresh with its mode, history with its days', async () => {
  await withService({
    'GET /api/v1/health': HEALTH,
    'GET /api/v1/accounts/a%201/snapshot?chartDays=30': { status: 200, body: fixture('service/snapshot') },
    'POST /api/v1/refresh': { status: 200, body: fixture('service/usage-list') },
    'GET /api/v1/accounts/a%201/history?days=7': { status: 200, body: fixture('service/history') },
  }, async (client, requests) => {
    const read = await client.snapshot('a 1', 'fake', 30);
    assert.equal(read.pacing.critical, 1);
    await client.refresh('manual');
    const refresh = requests.find((r) => r.method === 'POST');
    assert.deepEqual(JSON.parse(refresh?.body ?? ''), { mode: 'manual' });
    const history = await client.history('a 1', 'fake', 7);
    assert.equal(history.windows.length > 0, true);
  });
});

test('client: a failing /health or the service\'s own 503 is about the whole service; a provider 503 is the account\'s', async () => {
  await withService({ 'GET /api/v1/health': { status: 500, body: '' } }, async (c) => {
    await assert.rejects(c.snapshot('a1', 'fake', 7), (err) => err instanceof ServiceDownError && err.reason === 'failed');
  });
  const stopping = JSON.stringify({ schemaVersion: 1, error: { code: 'unavailable', message: 'stopping' } });
  await withService({ 'GET /api/v1/health': HEALTH, 'GET /api/v1/accounts/a1/snapshot?chartDays=7': { status: 503, body: stopping } }, async (c) => {
    await assert.rejects(c.snapshot('a1', 'fake', 7), (err) => err instanceof ServiceDownError);
  });
  await withService({ 'GET /api/v1/health': HEALTH, 'GET /api/v1/accounts/a1/snapshot?chartDays=7': { status: 503, body: fixture('service/error-not-read') } }, async (c) => {
    await assert.rejects(c.snapshot('a1', 'fake', 7), CoreProviderError);
  });
});

test('client: no api.json is no service; a refused refresh is unusable', async () => {
  const client = createSnapshotClient({ configDir: () => path.join(os.tmpdir(), 'aipermiler-no-such-dir') });
  await assert.rejects(client.snapshot('a1', 'fake', 7), (err) => err instanceof ServiceUnusableError && err.reason === 'not_running');
  await withService({ 'GET /api/v1/health': HEALTH, 'POST /api/v1/refresh': { status: 503, body: fixture('service/error-not-read') } }, async (c) => {
    await assert.rejects(c.refresh('manual'), (err) => err instanceof ServiceUnusableError && err.reason === 'failed');
  });
});
