// renderer/usageSource.test.ts — the "usage source" line of Settings → Diagnostics (#182).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { usageSourceLine } from './usageSource.js';
import { setLocale } from './i18n/index.js';

test('one entry per account: service, or CLI with the reason', () => {
  setLocale('en');
  assert.equal(usageSourceLine([
    { label: 'Work', source: 'service', reason: null },
    { label: 'Home', source: 'cli', reason: 'not_running' },
    { label: 'New', source: 'cli', reason: 'unknown_account' },
  ]), 'Work: service · Home: CLI (service not running) · New: CLI (account not known to the service)');
});

test('every fallback reason has a text in both languages', () => {
  for (const locale of ['en', 'it'] as const) {
    setLocale(locale);
    for (const reason of ['not_running', 'unreachable', 'unauthorized', 'incompatible', 'unknown_account', 'failed', 'malformed'] as const) {
      const line = usageSourceLine([{ label: 'A', source: 'cli', reason }]);
      assert.ok(!line?.includes('settings.'), `${locale} ${reason}: ${String(line)}`);
    }
  }
});

test('no account read yet: no line', () => {
  setLocale('en');
  assert.equal(usageSourceLine([]), null);
});
