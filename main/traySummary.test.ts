import { test } from 'node:test';
import assert from 'node:assert/strict';
import { trayTooltip } from './traySummary';
import { setLocale } from './i18n/index';
import type { QuotaWindow } from '../types/index';
const window: QuotaWindow = { id: 'any-window', label: 'Limit', periodType: 'rolling-days', periodLength: 7, used: 42, total: 100, unit: 'percentage', resetsAt: null };

test('tray summarizes all accounts through the common quota model, without adding percentages', () => {
  setLocale('en');
  assert.equal(trayTooltip([
    { label: 'Claude', criticalWindow: { ...window, used: 62 }, stale: false },
    { label: 'Copilot', criticalWindow: { ...window, unit: 'count', used: 120, total: 300 }, stale: false },
    { label: 'Claude work', criticalWindow: window, stale: false },
  ]), 'AIpermiler\nClaude 62% · Copilot 40% · Claude work 42%');
  assert.equal(trayTooltip([]), 'AIpermiler');
});

test('tray marks stale data and preserves unknown usage', () => {
  setLocale('it');
  assert.match(trayTooltip([{ label: 'Copilot', criticalWindow: window, stale: true }]), /Copilot 42%.*non aggiornati/);
  assert.match(trayTooltip([{ label: 'Copilot', criticalWindow: null, stale: false }]), /Copilot --/);
});
