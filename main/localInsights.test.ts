// main/localInsights.test.ts — which cached local insights the app may still show (#299).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { usableInsightsCache } from './localInsights';
import type { ClaudeLocalInsights } from '../types/index';

const insights: ClaudeLocalInsights = {
  computedAt: '2026-11-20T10:00:00Z',
  windowDays: 8,
  sessionsAnalyzed: 2,
  totalOutputTokens: 1000,
  highContextOutputTokens: 0,
  longSessionOutputTokens: 0,
  highContextSharePercent: 0,
  longSessionSharePercent: 0,
  topTools: [],
  daily: [{ date: '2026-11-20', outputTokens: 1000, highContextOutputTokens: 0 }],
  firstSessionStartByDay: {},
};

test('a cache counted by the core for the same window is usable', () => {
  assert.equal(usableInsightsCache({ claudeCode: insights, countedByCore: true }, 8), insights);
});

test('a cache written by the Agent SDK is never shown: its tokens are inflated (#301)', () => {
  assert.equal(usableInsightsCache({ claudeCode: insights, countedByCore: false }, 8), null);
});

test('a cache for another window, or no cache, is not usable', () => {
  assert.equal(usableInsightsCache({ claudeCode: insights, countedByCore: true }, 31), null);
  assert.equal(usableInsightsCache({ claudeCode: null, countedByCore: true }, 8), null);
});
