// diagnostics/report.test.ts — the manual diagnostic report never carries names, ids,
// credentials or response bodies; values and structure are kept.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildDiagnosticReport, neutralize } from './report';
import { LogBuffer } from './logBuffer';

const ACCOUNTS = [
  { name: 'Claude #1', label: 'Acme Team', id: 'claude-1a2b3c4d' },
  { name: 'Claude #2', label: 'Claude', id: 'claude' }, // default label and legacy id: generic
];

test('neutralize: labels and ids in free text become neutral names, in a single pass', () => {
  assert.equal(
    neutralize('[main] Acme Team unavailable (account claude-1a2b3c4d) on claude.ai', ACCOUNTS),
    '[main] Claude #1 unavailable (account Claude #1) on claude.ai',
  );
  // Generic tokens are left alone: "claude" is a provider name, not a secret.
  assert.equal(neutralize('Claude: missing sessionKey', ACCOUNTS), 'Claude: missing sessionKey');
});

test('buildDiagnosticReport: values kept, ids/strings redacted, errors shortened and neutralized', () => {
  const log = new LogBuffer(5);
  log.push('error', ['[main] Acme Team unavailable:', new Error('usage answered 500 — <html>body</html>')], new Date('2026-07-13T08:00:00Z'));
  const text = buildDiagnosticReport({
    generatedAt: new Date('2026-07-13T09:00:00Z'),
    environment: { app: '0.0.0', platform: 'linux' },
    settings: { notificationThresholdPercent: 80 },
    system: { timeZone: 'Europe/Paris', cpuCores: 8 },
    accounts: [
      {
        ...ACCOUNTS[0]!, provider: 'claude', enabled: true, config: { accountScope: 'organization' },
        endpointLabel: 'claude.ai/api/organizations/{id}/usage',
        response: { spend: { percent: 33.3 }, org_name: 'Acme Corporation', id: 99 },
        interpretation: { windows: [] }, history: { daily: [] },
      },
      {
        ...ACCOUNTS[1]!, provider: 'claude', enabled: false, config: {}, endpointLabel: null,
        readError: 'usage answered 403 Forbidden — <html>challenge</html>', interpretation: null, history: { daily: [] },
      },
    ],
    log: log.list(),
  });
  assert.match(text, /^# AIpermiler diagnostic report/);
  const json: unknown = JSON.parse(text.slice(text.indexOf('\n') + 1));
  const doc = json as { system: unknown; accounts: { name: string; enabled: boolean; response?: unknown; readError?: string }[]; log: { message: string }[] };
  assert.deepEqual(doc.system, { timeZone: 'Europe/Paris', cpuCores: 8 });
  assert.deepEqual(doc.accounts.map((a) => [a.name, a.enabled]), [['Claude #1', true], ['Claude #2', false]]);
  assert.deepEqual(doc.accounts[0]?.response, { id: '<id>', org_name: '<string, 16 chars>', spend: { percent: 33.3 } });
  assert.equal(doc.accounts[1]?.readError, 'usage answered 403 Forbidden');
  assert.equal(doc.log[0]?.message, '[main] Claude #1 unavailable: usage answered 500');
  assert.ok(!text.includes('Acme'));
  assert.ok(!text.includes('claude-1a2b3c4d'));
  assert.ok(!text.includes('<html>'));
});

test('LogBuffer keeps only the last entries', () => {
  const log = new LogBuffer(2);
  for (const n of [1, 2, 3]) log.push('warn', [`w${String(n)}`]);
  assert.deepEqual(log.list().map((e) => e.message), ['w2', 'w3']);
});
