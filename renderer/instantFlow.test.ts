// renderer/instantFlow.test.ts — the gauge arrows follow the core's trend alone (#304).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { FLOW_DURATION_SECONDS, trendFlow } from './instantFlow.js';

test('arrows: right while accelerating, left while slowing down, at one steady speed', () => {
  assert.deepEqual(trendFlow('accelerating'), { direction: 'right', durationSeconds: FLOW_DURATION_SECONDS });
  assert.deepEqual(trendFlow('slowing-down'), { direction: 'left', durationSeconds: FLOW_DURATION_SECONDS });
});

test('arrows: none when steady or when the trend cannot be measured', () => {
  assert.equal(trendFlow('steady'), null);
  assert.equal(trendFlow(null), null);
});
