import { test } from 'node:test';
import assert from 'node:assert/strict';
import { windowLabel } from './windowLabel.js';
import { setLocale } from './i18n/index.js';
import type { QuotaWindow } from '../types/index';

function win(id: string, periodType: QuotaWindow['periodType'] = 'rolling-hours', periodLength: number | null = 5): QuotaWindow {
  return { id, label: 'ignored', periodType, periodLength, unit: 'percentage', used: 10, total: 100, resetsAt: null };
}

for (const locale of ['en', 'it'] as const) {
  test(`Codex window labels preserve identity and duration in ${locale}`, () => {
    setLocale(locale);
    const hours = locale === 'en' ? '5-hour limit' : 'Limite 5 ore';
    const days = locale === 'en' ? '2-day limit' : 'Limite 2 giorni';
    assert.equal(windowLabel(win('codex-5h'), 'codex'), hours);
    assert.equal(windowLabel(win('codex-7d', 'rolling-days', 7), 'codex'), locale === 'en' ? 'Weekly limit' : 'Limite settimanale');
    assert.equal(windowLabel(win('codex-review-5h'), 'codex'), `${locale === 'en' ? 'Code review' : 'Revisione codice'}: ${hours}`);
    assert.equal(windowLabel(win('codex-additional-key-fast-model-2d', 'rolling-days', 2), 'codex'), `“fast-model”: ${days}`);
    assert.equal(windowLabel(win('codex-unknown', 'rolling-hours', null), 'codex'), 'codex-unknown');
    assert.equal(windowLabel(win('codex-billing', 'billing-cycle', 30), 'codex'), 'codex-billing');
    const ids = ['codex-5h', 'codex-review-5h', 'codex-additional-array-0-5h', 'codex-additional-array-1-5h', 'codex-additional-key-model-a-5h', 'codex-additional-key-model-b-5h', 'codex-additional-key-Code review-5h', 'codex-additional-key-Additional #1-5h'];
    assert.equal(new Set(ids.map((id) => windowLabel(win(id), 'codex'))).size, ids.length);
    assert.equal(windowLabel(win('codex-additional-array-0-5h'), 'codex'), `${locale === 'en' ? 'Additional' : 'Aggiuntivo'} #1: ${hours}`);
  });
}

for (const locale of ['en', 'it'] as const) {
  test(`Claude core windows have their own labels in ${locale} (#242)`, () => {
    setLocale(locale);
    assert.equal(windowLabel(win('claude-5h'), 'claude'), locale === 'en' ? '5-hour limit' : 'Limite 5 ore');
    assert.equal(windowLabel(win('claude-7d', 'rolling-days', 7), 'claude'), locale === 'en' ? 'Weekly limit' : 'Limite settimanale');
  });
}
