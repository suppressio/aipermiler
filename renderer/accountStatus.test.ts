// renderer/accountStatus.test.ts — the status line of the widget: stale data, errors,
// notices and the service's neutral "refreshing" state (#182).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { accountStatusLabel } from './accountStatus.js';
import { formatDateTime, setLocale } from './i18n/index.js';

const AT = '2026-10-31T21:10:00Z';

test('fresh data: the window label as it is', () => {
  setLocale('en');
  assert.equal(accountStatusLabel({ stale: false }, 'Weekly'), 'Weekly');
});

test('stale data with an error, and a first sync that failed', () => {
  setLocale('en');
  assert.equal(accountStatusLabel({ stale: true, lastUpdatedAt: AT, lastError: 'boom' }, 'Weekly'),
    `Weekly — data not up to date (last successful update: ${formatDateTime(AT)}) (boom)`);
  assert.equal(accountStatusLabel({ stale: true, lastError: 'boom' }, 'Weekly'), 'Account connected — first sync failed (boom)');
});

test('a notice without data replaces the label', () => {
  setLocale('en');
  assert.equal(accountStatusLabel({ stale: true, notice: 'Sign in with Codex' }, 'Weekly'), 'Sign in with Codex');
});

test('service not running (#341): the label says so, also with a failed read, in both languages', () => {
  setLocale('en');
  assert.equal(accountStatusLabel({ stale: false, serviceDown: true }, 'Weekly'),
    'Weekly — the AIpermiler service is not running: usage only, no pacing');
  assert.equal(accountStatusLabel({ stale: true, serviceDown: true, lastError: 'boom' }, 'Weekly'),
    'Weekly — the AIpermiler service is not running: usage only, no pacing (boom)');
  setLocale('it');
  assert.equal(accountStatusLabel({ stale: false, serviceDown: true }, 'Settimanale'),
    'Settimanale — il servizio AIpermiler non è attivo: solo consumi, niente ritmo');
});

test('refreshing (#182): neutral, never an error or a failed sync, in both languages', () => {
  setLocale('en');
  const withData = accountStatusLabel({ stale: true, refreshing: true, lastUpdatedAt: AT }, 'Weekly');
  assert.equal(withData, `Weekly — refreshing (last update: ${formatDateTime(AT)})`);
  assert.equal(accountStatusLabel({ stale: true, refreshing: true }, 'Weekly'), 'Refreshing…');
  setLocale('it');
  assert.equal(accountStatusLabel({ stale: true, refreshing: true }, 'Settimanale'), 'Aggiornamento in corso…');
  assert.ok(!accountStatusLabel({ stale: true, refreshing: true, lastUpdatedAt: AT }, 'Settimanale').includes('non aggiornato'));
});
