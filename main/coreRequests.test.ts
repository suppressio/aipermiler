// main/coreRequests.test.ts — the provider flags handed to the core.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import path from 'node:path';
import { codexUsageRequest, copilotAccountFlags, copilotUsageRequest } from './coreRequests';
import { defaultCodexAccount, defaultCopilotAccount } from '../store/migrate';

test('Copilot: account and host on every command; scope, and the manual quota only for the personal scope', () => {
  const personal = { ...defaultCopilotAccount('copilot-1'), host: 'acme.ghe.com', manualQuota: 300 };
  assert.deepEqual(copilotAccountFlags(personal), ['--account', 'copilot-1', '--host', 'acme.ghe.com']);
  assert.deepEqual(copilotUsageRequest({ ...personal, accountScope: 'personal' }), {
    provider: 'copilot',
    flags: ['--account', 'copilot-1', '--host', 'acme.ghe.com', '--scope', 'personal', '--manual-quota', '300'],
  });
  assert.deepEqual(copilotUsageRequest({ ...personal, accountScope: 'organization' }).flags, ['--account', 'copilot-1', '--host', 'acme.ghe.com', '--scope', 'organization']);
});

test('Copilot: a token of an earlier version never reaches the flags', () => {
  const legacy = { ...defaultCopilotAccount('copilot-1'), credentials: { token: 'synthetic-legacy-token', username: 'octo' } };
  assert.ok(!copilotUsageRequest(legacy).flags.some((f) => f.includes('synthetic-legacy-token')));
});

test('Codex: the binary path only when set', () => {
  assert.deepEqual(codexUsageRequest(defaultCodexAccount('cx')), { provider: 'codex', flags: [] });
  const bin = path.resolve('opt', 'codex');
  assert.deepEqual(codexUsageRequest({ ...defaultCodexAccount('cx'), binaryPath: bin }).flags, ['--codex-bin', bin]);
});
