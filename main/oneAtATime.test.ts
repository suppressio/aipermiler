import { test } from 'node:test';
import assert from 'node:assert/strict';
import { oneAtATime } from './oneAtATime';

const delay = (ms: number): Promise<void> => new Promise((resolve) => { setTimeout(resolve, ms); });

test('oneAtATime: tasks never overlap and run in call order', async () => {
  const queue = oneAtATime();
  const events: string[] = [];
  const task = (name: string, ms: number) => async (): Promise<string> => {
    events.push(`start ${name}`);
    await delay(ms);
    events.push(`end ${name}`);
    return name;
  };
  const results = await Promise.all([queue(task('a', 30)), queue(task('b', 5)), queue(task('c', 1))]);
  assert.deepEqual(results, ['a', 'b', 'c']);
  assert.deepEqual(events, ['start a', 'end a', 'start b', 'end b', 'start c', 'end c']);
});

test('oneAtATime: a rejected task reaches its caller and lets the next one run', async () => {
  const queue = oneAtATime();
  const failed = queue(async () => { await delay(5); throw new Error('probe failed'); });
  const after = queue(async () => 'next');
  await assert.rejects(failed, /probe failed/);
  assert.equal(await after, 'next');
});
