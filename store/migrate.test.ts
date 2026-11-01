// store/migrate.test.ts — migration from the two-slot schema `{ claude, copilot }` to
// the `AccountConfig[]` registry (issue #4). Pure functions, no electron-store.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  claudePartitionFor,
  defaultClaudeAccount,
  defaultCopilotAccount,
  defaultCodexAccount,
  enforceSingleCodexAccount,
  enforceSingleLocalInsights,
  migrateAccounts,
  migrateTrayBadge,
  nextAccountLabel,
  normalizeAccounts,
  purgeClaudeSessions,
} from './migrate';
import { at } from '../tests/support/at';

const legacyClaude = {
  enabled: true,
  accountScope: 'organization',
  authMethod: 'sso',
  session: { sessionKey: 'sk-real', organizationId: 'org-1', capturedAt: '2026-09-01T00:00:00Z', expiresAt: null },
  planTier: 'enterprise',
  subscription: { renewalRule: { type: 'dayOfMonth', day: 15 } },
};

const legacyCopilot = {
  enabled: true,
  accountScope: 'personal',
  authMethod: 'oauth',
  credentials: { token: 'ghp_real', username: 'someone' },
  oauthApp: { clientId: 'Iv1.x' },
  manualQuota: 500,
  planTier: 'pro_plus',
  subscription: { renewalRule: { type: 'dayOfMonth', day: 3 } },
  experimentalWarningAcknowledged: true,
};

test('migrateAccounts: legacy → array, ids kept so the history stays valid', () => {
  const result = migrateAccounts({ claude: legacyClaude, copilot: legacyCopilot }, false);
  assert.deepEqual(result.map((a) => a.id), ['claude', 'copilot']);
  assert.deepEqual(result.map((a) => a.provider), ['claude', 'copilot']);
});

test('migrateAccounts: keeps credentials and configuration, adds the partition', () => {
  const migrated = migrateAccounts({ claude: legacyClaude, copilot: legacyCopilot }, false);
  const claude = at(migrated, 0);
  const copilot = at(migrated, 1);
  assert.equal(claude.provider, 'claude');
  assert.equal(claude.session.sessionKey, 'sk-real');
  assert.equal('planTier' in claude, false); // obsolete field dropped
  assert.equal('accountScope' in claude, false);
  assert.equal(claude.subscription.renewalRule.day, 15);
  assert.equal(claude.partition, claudePartitionFor('claude'));
  assert.equal(claude.label, 'Claude');

  assert.equal(copilot.provider, 'copilot');
  assert.equal(copilot.credentials.token, 'ghp_real');
  assert.equal(copilot.manualQuota, 500);
  assert.equal(copilot.oauthApp.clientId, 'Iv1.x');
});

test('migrateAccounts: the old global localInsights flag moves to the Claude account', () => {
  const claude = at(migrateAccounts({ claude: legacyClaude }, true), 0);
  assert.equal(claude.provider === 'claude' && claude.localInsights, true);
});

test('migrateAccounts: a never-configured legacy slot creates no ghost rows', () => {
  const neverUsed = { claude: { ...legacyClaude, enabled: false, session: { sessionKey: null } }, copilot: { ...legacyCopilot, enabled: false, credentials: { token: null, username: null } } };
  assert.deepEqual(migrateAccounts(neverUsed, false), []);
});

test('migrateAccounts: an incomplete legacy slot (electron-store shallow merge) gets the missing defaults', () => {
  const partial = { claude: { enabled: true, session: { sessionKey: 'sk' } } };
  const claude = at(migrateAccounts(partial, false), 0);
  assert.equal(claude.provider, 'claude');
  assert.equal(claude.session.organizationId, null);
  assert.deepEqual(claude.subscription.renewalRule, { type: 'dayOfMonth', day: 1 });
});

test('normalizeAccounts drops obsolete fields stored by older versions', () => {
  const [claude, copilot] = normalizeAccounts([
    { id: 'c1', provider: 'claude', planTier: 'max_5x', accountScope: 'organization' },
    { id: 'g1', provider: 'copilot', planTier: 'business', accountScope: 'organization' },
  ]);
  assert.equal(claude !== undefined && 'planTier' in claude, false);
  assert.equal(claude !== undefined && 'accountScope' in claude, false);
  assert.equal(copilot !== undefined && 'planTier' in copilot, false);
  assert.equal(copilot?.provider === 'copilot' && copilot.accountScope, 'organization'); // still used by Copilot
});

test('migrateAccounts: idempotent on an already migrated array', () => {
  const already = [defaultClaudeAccount('claude-abc')];
  assert.deepEqual(migrateAccounts(already, true), already);
});

test('migrateAccounts: invalid value → no account', () => {
  assert.deepEqual(migrateAccounts(undefined, false), []);
  assert.deepEqual(migrateAccounts('x', false), []);
});

test('nextAccountLabel: progressive numbering per provider', () => {
  const existing = [defaultClaudeAccount('a'), defaultClaudeAccount('b', 'Claude 2'), defaultCopilotAccount('c')];
  assert.equal(nextAccountLabel('claude', existing), 'Claude 3');
  assert.equal(nextAccountLabel('copilot', existing), 'GitHub Copilot 2');
  assert.equal(nextAccountLabel('claude', []), 'Claude');
});

test('enforceSingleLocalInsights: at most one Claude account with local insights', () => {
  const a = { ...defaultClaudeAccount('a'), localInsights: true };
  const b = { ...defaultClaudeAccount('b'), localInsights: true };
  const result = enforceSingleLocalInsights([a, defaultCopilotAccount('c'), b]);
  const flags = result.map((x) => x.provider === 'claude' && x.localInsights);
  assert.deepEqual(flags, [true, false, false]);
});

test('normalizeAccounts gives Copilot accounts a valid GitHub host', () => {
  const [kept, fixed, added] = normalizeAccounts([
    { id: 'a', provider: 'copilot', host: 'https://Acme.ghe.com/' },
    { id: 'b', provider: 'copilot', host: 'evil.com' },
    { id: 'c', provider: 'copilot' },
  ]);
  assert.equal(kept?.provider === 'copilot' ? kept.host : null, 'acme.ghe.com');
  assert.equal(fixed?.provider === 'copilot' ? fixed.host : null, 'github.com');
  assert.equal(added?.provider === 'copilot' ? added.host : null, 'github.com');
});

test('new accounts start from the default Monday–Friday schedule', () => {
  for (const account of [defaultClaudeAccount('x'), defaultCopilotAccount('y')]) {
    assert.equal(account.workSchedule.enabled, true);
    assert.equal(account.workSchedule.days.fri, 'full');
    assert.equal(account.workSchedule.days.sun, 'off');
  }
});


test('migrateTrayBadge: old off/critical choices and per-account current critical window', () => {
  assert.deepEqual(migrateTrayBadge('off', {}), { view: { mode: 'off' }, critical: true });
  assert.deepEqual(migrateTrayBadge('critical', {}), { view: { mode: 'auto' }, critical: true });
  const history = { lastGood: { a: { quotaWindows: [
    { id: '5h', used: 20, total: 100, unit: 'percentage' },
    { id: 'weekly', used: 270, total: 300, unit: 'count' },
  ] } } };
  assert.deepEqual(migrateTrayBadge('a', history), { view: { mode: 'manual', accountId: 'a', windowId: 'weekly' }, critical: true });
  assert.deepEqual(migrateTrayBadge('a', {}), { view: { mode: 'manual', accountId: 'a', windowId: null }, critical: true });
  assert.deepEqual(migrateTrayBadge('a', { lastGood: { a: { quotaWindows: [null, 2, { id: 'bad' }] } } }).view,
    { mode: 'manual', accountId: 'a', windowId: null });
});

test('migrateTrayBadge: new preferences are idempotent, including unavailable views', () => {
  for (const view of [{ mode: 'off' }, { mode: 'auto' }, { mode: 'manual', accountId: 'gone', windowId: 'missing' }]) {
    const setting = { view, critical: false };
    assert.deepEqual(migrateTrayBadge(setting, {}), setting);
  }
});

test('purgeClaudeSessions: Claude accounts lose session and last quotas, the rest stays (#140)', () => {
  const claude = { ...defaultClaudeAccount('c1'), enabled: true, localInsights: true,
    session: { sessionKey: 'sk', organizationId: 'org', capturedAt: '2026-10-01T00:00:00Z', expiresAt: null } };
  const copilot = defaultCopilotAccount('p1');
  const usage = { planTier: null, subscriptionRenewsAt: null, quotaWindows: [], lastUpdatedAt: 'x' };
  const lastGood = { c1: { ...usage, accountId: 'c1' }, p1: { ...usage, accountId: 'p1' } };
  const result = purgeClaudeSessions([claude, copilot], lastGood);
  assert.deepEqual(at(result.accounts, 0), { ...claude, session: { sessionKey: null, organizationId: null, capturedAt: null, expiresAt: null } });
  assert.equal(at(result.accounts, 1), copilot);
  assert.deepEqual(result.lastGood, { p1: lastGood.p1 });
});

test('purgeClaudeSessions: no lastGood stays undefined', () => {
  assert.equal(purgeClaudeSessions([defaultClaudeAccount('c1')], undefined).lastGood, undefined);
});

test('enforceSingleCodexAccount: at most one enabled Codex account, the first kept (#178)', () => {
  const a = defaultCodexAccount('a');
  const b = defaultCodexAccount('b');
  const off = { ...defaultCodexAccount('off'), enabled: false };
  const result = enforceSingleCodexAccount([off, a, defaultCopilotAccount('c'), b]);
  assert.deepEqual(result.map((x) => [x.id, x.enabled]), [['off', false], ['a', true], ['c', false], ['b', false]]);
});

test('normalizeAccounts: two migrated Codex accounts → only the first stays enabled (#178)', () => {
  const result = normalizeAccounts([
    { id: 'x', provider: 'codex', enabled: true, session: { capturedAt: '2026-10-18T10:00:00Z' } },
    { id: 'y', provider: 'codex', enabled: true, session: { capturedAt: '2026-10-19T10:00:00Z' } },
  ]);
  assert.deepEqual(result.map((x) => [x.id, x.enabled]), [['x', true], ['y', false]]);
});
