import { test } from 'node:test';
import assert from 'node:assert/strict';
import { BADGE_SIZE, badgeText, badgeTone, pickBadge, renderBadge, trackAutoAccount, pickAlerts, badgeEntries, resolveLegacyView } from './trayBadge';
import type { QuotaWindow } from '../types/index';

const window: QuotaWindow = { id: 'any-window', label: 'Limit', periodType: 'rolling-days', periodLength: 7, used: 42, total: 100, unit: 'percentage', resetsAt: null };
const account = (accountId: string, used: number, other = 10, stale = false) => ({
  accountId, criticalWindow: { ...window, used },
  quotaWindows: [{ ...window, used }, { ...window, id: 'other', used: other }], stale,
});
const accounts = [account('a', 62), account('b', 90, 85, true)];
const auto = { view: { mode: 'auto' as const }, critical: true };
const manual = { view: { mode: 'manual' as const, accountId: 'a', windowId: 'other' }, critical: true };

test('badge: startup uses highest utilization, Auto follows activity and stays on idle/reset', () => {
  const initial = trackAutoAccount(null, [], accounts);
  assert.equal(initial, 'b');
  assert.equal(trackAutoAccount(initial, accounts, [account('a', 63), accounts[1]!]), 'a');
  assert.equal(trackAutoAccount('a', accounts, [account('a', 20), account('b', 90)]), 'a');
  assert.equal(pickBadge(accounts, auto, 'a')?.percent, 62);
  assert.equal(pickBadge(accounts, auto, null)?.percent, 90);
  assert.equal(trackAutoAccount('a', accounts, [account('b', 90)]), 'b');
});

test('startup ranks accounts by utilization even when their critical window is chosen for pacing risk', () => {
  const mixed = [account('a', 30, 95), account('b', 80)];
  assert.equal(trackAutoAccount(null, [], mixed), 'a');
  assert.equal(pickBadge(mixed, auto, null)?.percent, 30);
});

test('Auto ignores stale rises, new windows and changed totals; ties are stable', () => {
  assert.equal(trackAutoAccount('a', accounts, [account('a', 62), account('b', 99, 85, true)]), 'a');
  const changed = account('b', 95);
  changed.quotaWindows[0] = { ...window, used: 95, total: 200 };
  assert.equal(trackAutoAccount('a', accounts, [account('a', 62), changed]), 'a');
  assert.equal(trackAutoAccount('a', accounts, [account('a', 63), account('b', 91)]), 'a');
});

test('Auto chooses the most recent rise and only existing windows count as activity', () => {
  const a = { ...account('a', 63), lastUpdatedAt: '2026-10-22T10:00:00Z' };
  const b = { ...account('b', 91), lastUpdatedAt: '2026-10-22T10:00:01Z' };
  assert.equal(trackAutoAccount('a', accounts, [a, b]), 'b');
  const changed = account('b', 90, 85);
  changed.quotaWindows.push({ ...window, id: 'new', used: 99 });
  assert.equal(trackAutoAccount('a', accounts, [account('a', 62), changed]), 'a');
});

test('badge: Manual shows the exact window, unavailable choice falls back without mutation', () => {
  assert.equal(pickBadge(accounts, manual, 'b')?.percent, 10);
  const missing = { ...manual, view: { ...manual.view, windowId: 'missing' } };
  assert.equal(pickBadge(accounts, missing, 'b')?.percent, 90);
  assert.equal(missing.view.windowId, 'missing');
  assert.equal(pickBadge([accounts[1]!], manual, 'b')?.percent, 90);
  assert.equal(pickBadge(accounts, manual, 'b')?.percent, 10);
  assert.equal(pickBadge(accounts, { ...auto, view: { mode: 'off' } }, 'a'), null);
  assert.equal(pickBadge([], auto, null), null);
});

test('alerts list every other view at or above the threshold, highest first', () => {
  const shown = pickBadge(accounts, auto, 'b');
  assert.deepEqual(pickAlerts(accounts, auto, shown, 85).map(a => a.percent), [85]);
  assert.deepEqual(pickAlerts(accounts, auto, shown, 86), []);
  assert.deepEqual(pickAlerts(accounts, { ...auto, critical: false }, shown, 80), []);
  assert.deepEqual(pickAlerts(accounts, { ...auto, view: { mode: 'off' } }, null, 80), []);
  const single = [account('a', 80, 0)];
  assert.deepEqual(pickAlerts(single, auto, pickBadge(single, auto, 'a'), 80), []);
  const many = [account('a', 84, 0), account('b', 97, 0, true), account('c', 3, 0)];
  const alerts = pickAlerts(many, auto, pickBadge(many, auto, 'c'), 80);
  assert.deepEqual(alerts.map(a => [a.view.accountId, a.percent, a.stale]), [['b', 97, true], ['a', 84, false]]);
});

test('menu entries keep exactly one checked unavailable Manual view and restore it', () => {
  const registry = [{ id: 'a', label: 'Claude', provider: 'claude' as const, enabled: false, quotaWindows: accounts[0]!.quotaWindows }];
  const entries = badgeEntries(registry, [], manual.view);
  assert.equal(entries.filter(e => e.checked).length, 1);
  assert.equal(entries.find(e => e.checked)?.enabled, false);
  assert.equal(badgeEntries([{ ...registry[0]!, enabled: true }], accounts, manual.view).find(e => e.checked)?.enabled, true);
  const missing = badgeEntries([], [], manual.view);
  assert.equal(missing.filter(e => e.checked).length, 1);
  assert.equal(missing.find(e => e.checked)?.enabled, false);
});

test('pending legacy Manual view resolves once, missing explicit views are never replaced', () => {
  const pending = { ...manual.view, windowId: null };
  assert.deepEqual(resolveLegacyView(pending, accounts), { ...manual.view, windowId: 'any-window' });
  assert.deepEqual(resolveLegacyView(manual.view, accounts), manual.view);
  assert.deepEqual(resolveLegacyView(pending, []), pending);
});

test('badge tone follows the notification threshold, stale wins', () => {
  assert.equal(badgeTone({ percent: 74, stale: false }, 80), 'ok');
  assert.equal(badgeTone({ percent: 75, stale: false }, 80), 'caution');
  assert.equal(badgeTone({ percent: 80, stale: false }, 80), 'warning');
  assert.equal(badgeTone({ percent: 99, stale: true }, 80), 'stale');
});

test('badge text is a rounded 0-100 integer', () => {
  assert.equal(badgeText(61.6), '62');
  assert.equal(badgeText(130), '100');
  assert.equal(badgeText(-3), '0');
  assert.equal(badgeText(95, true), '!95');
  assert.equal(badgeText(100, true), '100');
  assert.notDeepEqual(renderBadge(95, 'warning', true), renderBadge(95, 'warning'));
});

test('badge bitmap: right size, transparent corners, white digits inside', () => {
  const lit = (pixels: Uint8Array, x: number, y: number) => pixels.slice((y * BADGE_SIZE + x) * 4, (y * BADGE_SIZE + x) * 4 + 4).join(',');
  for (const percent of [0, 7, 62, 100]) {
    const pixels = renderBadge(percent, 'ok');
    assert.equal(pixels.length, BADGE_SIZE * BADGE_SIZE * 4);
    assert.equal(lit(pixels, 0, 0), '0,0,0,0');
    assert.equal(lit(pixels, 16, 1), '22,163,74,255');
    let white = 0;
    for (let i = 0; i < pixels.length; i += 4) if (pixels[i] === 255 && pixels[i + 1] === 255 && pixels[i + 2] === 255) white++;
    assert.ok(white > 30, `digits drawn for ${String(percent)}`);
  }
  assert.notDeepEqual(renderBadge(62, 'ok'), renderBadge(63, 'ok'));
});
