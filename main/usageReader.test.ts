// main/usageReader.test.ts — what the widget reads per account (#341): the service's
// snapshot when one runs, the raw CLI values with serviceDown when none does (D1 on
// #181), the service's own answer when it has no good read, and the refresh request.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { CoreProviderError, parseUsageRun } from './coreClient';
import { ServiceUnusableError, type UsageSourceRecord } from './serviceClient';
import { parseServiceSnapshot, type AccountProblem, type RefreshRequestMode } from './serviceSnapshot';
import { createUsageReader, type UsageReaderDeps } from './usageReader';
import type { AccountConfig, ClaudeLocalInsights } from '../types/index';
import { defaultAccountFor } from '../store/migrate';
import { at } from '../tests/support/at';

const FIXTURES = path.join(__dirname, '..', '..', 'gocore', 'contract', 'v1');
const fixture = (name: string): string => fs.readFileSync(path.join(FIXTURES, `${name}.json`), 'utf8');

const account = (id: string, provider: AccountConfig['provider'] = 'codex'): AccountConfig => ({ ...defaultAccountFor(provider, id, id), enabled: true });

interface Calls {
  refresh: RefreshRequestMode[];
  snapshot: string[];
  cli: string[];
  sources: [string, UsageSourceRecord][];
  fetchErrors: unknown[];
}

function reader(overrides: Partial<UsageReaderDeps>, accounts: AccountConfig[] = [account('a1')]) {
  const calls: Calls = { refresh: [], snapshot: [], cli: [], sources: [], fetchErrors: [] };
  const deps: UsageReaderDeps = {
    accounts: () => accounts,
    isConnected: () => true,
    chartDays: () => 7,
    snapshot: (cfg) => {
      calls.snapshot.push(cfg.id);
      return Promise.resolve(parseServiceSnapshot(200, fixture('service/snapshot'), 'fake'));
    },
    refresh: (mode) => { calls.refresh.push(mode); return Promise.resolve(); },
    cliUsage: (cfg) => { calls.cli.push(cfg.id); return Promise.resolve(parseUsageRun({ exitCode: 0, stdout: fixture('usage-ok') }, 'fake')); },
    recordSource: (id, source) => { calls.sources.push([id, source]); },
    localInsights: () => Promise.resolve(null),
    describeProblem: (err): AccountProblem => (err instanceof CoreProviderError && err.reason === 'not_read_yet'
      ? { refreshing: true }
      : { lastError: err instanceof Error ? err.message : 'error' }),
    onFetchError: (_provider, err) => { calls.fetchErrors.push(err); },
    now: () => new Date('2026-11-04T14:55:00Z'),
    logError: () => undefined,
    ...overrides,
  };
  return { reader: createUsageReader(deps), calls };
}

const down = (reason: 'not_running' | 'unknown_account' | 'unauthorized' = 'not_running') => () => Promise.reject(new ServiceUnusableError(reason, 'no service'));

test('with a service: one snapshot per enabled account, no CLI read, no refresh on a plain read', async () => {
  const disabled = { ...account('off'), enabled: false };
  const { reader: r, calls } = reader({}, [account('a1'), disabled, account('a2')]);
  const snapshot = await r.read(null);
  assert.deepEqual(snapshot.accounts.map((a) => a.accountId), ['a1', 'a2']);
  assert.deepEqual(calls.snapshot, ['a1', 'a2']);
  assert.deepEqual(calls.cli, []);
  assert.deepEqual(calls.refresh, []);
  assert.equal(r.serviceDown(), false);
  assert.equal(at(snapshot.accounts, 0).criticalWindow?.id, 'weekly');
  assert.deepEqual(calls.sources, [['a1', { source: 'service', reason: null }], ['a2', { source: 'service', reason: null }]]);
});

test('a refresh asks the service loop for a run with its mode before reading', async () => {
  const { reader: r, calls } = reader({});
  await r.read('manual');
  await r.read('forced');
  assert.deepEqual(calls.refresh, ['manual', 'forced']);
});

test('without a service: raw CLI values with serviceDown, the service looked for once', async () => {
  const { reader: r, calls } = reader({ snapshot: (cfg) => { calls.snapshot.push(cfg.id); return down()(); }, refresh: down() }, [account('a1'), account('a2')]);
  const snapshot = await r.read('manual');
  assert.equal(r.serviceDown(), true);
  assert.deepEqual(calls.snapshot, ['a1']);
  assert.deepEqual(calls.cli, ['a1', 'a2']);
  for (const a of snapshot.accounts) {
    assert.equal(a.serviceDown, true);
    assert.equal(a.stale, false);
    assert.ok(a.windows.length > 0);
    assert.ok(a.windows.every((w) => w.todayBudget === null && w.instantRate === null));
  }
  assert.deepEqual(calls.sources.map(([, s]) => s), [{ source: 'cli', reason: 'not_running' }, { source: 'cli', reason: 'not_running' }]);
});

test('an account the service does not serve goes through the CLI alone; the others keep the service', async () => {
  const { reader: r, calls } = reader({
    snapshot: (cfg) => {
      calls.snapshot.push(cfg.id);
      return cfg.id === 'new' ? down('unknown_account')() : Promise.resolve(parseServiceSnapshot(200, fixture('service/snapshot'), 'fake'));
    },
  }, [account('new'), account('a1')]);
  const snapshot = await r.read(null);
  assert.equal(r.serviceDown(), false);
  assert.deepEqual(calls.snapshot, ['new', 'a1']);
  assert.deepEqual(calls.cli, ['new']);
  assert.equal(at(snapshot.accounts, 1).serviceDown, undefined);
});

test('a failed CLI read without a service: no values, the error, serviceDown', async () => {
  const { reader: r, calls } = reader({ snapshot: down(), cliUsage: () => Promise.reject(new Error('core missing')) });
  const a = at((await r.read(null)).accounts, 0);
  assert.equal(a.serviceDown, true);
  assert.equal(a.stale, true);
  assert.equal(a.lastError, 'core missing');
  assert.equal(calls.fetchErrors.length, 1);
});

test('the service has no good read yet: its error is the account\'s answer, not a CLI read', async () => {
  const { reader: r, calls } = reader({ snapshot: () => Promise.resolve(parseServiceSnapshot(503, fixture('service/error-not-read'), 'fake')) });
  const a = at((await r.read(null)).accounts, 0);
  assert.equal(a.refreshing, true);
  assert.equal(a.stale, true);
  assert.equal(a.serviceDown, undefined);
  assert.deepEqual(calls.cli, []);
  // Not read yet is not a failure: no format drift check.
  assert.deepEqual(calls.fetchErrors, []);
});

test('a stale snapshot keeps the last values with the read\'s problem', async () => {
  const stale = JSON.parse(fixture('service/snapshot')) as Record<string, unknown>;
  stale.stale = true;
  stale.staleReason = { code: 'timeout', message: 'Claude did not answer' };
  const { reader: r, calls } = reader({ snapshot: () => Promise.resolve(parseServiceSnapshot(200, JSON.stringify(stale), 'fake')) }, [account('a1', 'claude')]);
  const a = at((await r.read(null)).accounts, 0);
  assert.equal(a.stale, true);
  assert.equal(a.windows.length, 4);
  assert.ok(a.lastError);
  assert.equal(calls.fetchErrors.length, 1);
});

test('local insights stay on the opted-in Claude account (account-level panel)', async () => {
  const insights = { computedAt: '2026-11-04T14:00:00Z' } as ClaudeLocalInsights;
  const claude = { ...account('c1', 'claude'), localInsights: true } as AccountConfig;
  const { reader: r } = reader({ localInsights: () => Promise.resolve(insights) }, [claude, account('a1')]);
  const snapshot = await r.read(null);
  assert.equal(at(snapshot.accounts, 0).localInsights, insights);
  assert.equal(at(snapshot.accounts, 1).localInsights, undefined);
});

test('a refresh that fails for another reason than no service propagates', async () => {
  const { reader: r } = reader({ refresh: () => Promise.reject(new Error('boom')) });
  await assert.rejects(r.read('manual'), /boom/);
});

