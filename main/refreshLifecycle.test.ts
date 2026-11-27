import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { Script } from 'node:vm';

type ReadMode = 'scheduled' | 'manual' | 'forced' | null;

// Execute the built wiring with injected collaborators, without loading Electron.
const main = readFileSync(path.join(__dirname, '..', 'main.js'), 'utf8');
const source = main.match(/async function refreshOnce\(mode\) \{[\s\S]*?\n\}/)?.[0];
assert.ok(source, 'refreshOnce must exist in the built main process');
const script = new Script(`(${source})`);

function harness(windowState: 'alive' | 'destroyed' | 'absent' = 'alive') {
  const events: { channel: string; payload: unknown }[] = [];
  const modes: ReadMode[] = [];
  let resolveBuild: (value: unknown) => void = () => { assert.fail('build has not started'); };
  let rejectBuild: (error: Error) => void = () => { assert.fail('build has not started'); };
  let retries = 0;
  let viewUpdates = 0;
  let destroyed = windowState === 'destroyed';
  const refreshOnce = script.runInNewContext({
    mainWindow: windowState === 'absent' ? null : { isDestroyed: () => destroyed },
    ipc_1: { sendToRenderer: (_window: unknown, channel: string, payload: unknown) => { events.push({ channel, payload }); } },
    usageReader: {
      read: (mode: ReadMode) => {
        modes.push(mode);
        return new Promise<unknown>((resolve, reject) => { resolveBuild = resolve; rejectBuild = reject; });
      },
    },
    schedulePoll: () => { retries++; },
    console: { error: () => undefined },
    timings: {},
    lastSnapshot: null,
    updateTrayView: () => { viewUpdates++; },
    trayHandle: null,
    maybeNotifyThreshold: () => undefined,
    maybeNotifyPace: () => undefined,
  }) as (mode: ReadMode) => Promise<void>;
  return {
    events, modes, refreshOnce,
    succeed: (snapshot: unknown) => { resolveBuild(snapshot); },
    fail: () => { rejectBuild(new Error('Snapshot failed')); },
    destroy: () => { destroyed = true; },
    retries: () => retries,
    viewUpdates: () => viewUpdates,
  };
}

test('every refresh mode emits start before completion and usage:update on success', async () => {
  for (const mode of ['scheduled', 'manual', 'forced'] as const) {
    const h = harness();
    const running = h.refreshOnce(mode);
    assert.deepEqual(h.events, [{ channel: 'usage:refreshing', payload: true }]);
    assert.deepEqual(h.modes, [mode]);
    const snapshot = { accounts: [] };
    h.succeed(snapshot);
    await running;
    assert.equal(h.viewUpdates(), 1);
    assert.deepEqual(h.events, [
      { channel: 'usage:refreshing', payload: true },
      { channel: 'usage:update', payload: snapshot },
    ]);
  }
});

test('failed refresh clears the indicator without usage:update and schedules the next read', async () => {
  const h = harness();
  const running = h.refreshOnce('scheduled');
  h.fail();
  await running;
  assert.deepEqual(h.events, [
    { channel: 'usage:refreshing', payload: true },
    { channel: 'usage:refreshing', payload: false },
  ]);
  assert.equal(h.retries(), 1);
  assert.equal(h.viewUpdates(), 0);
});

test('the poll (no mode) only reads: no refreshing state, usage:update on success, next poll scheduled', async () => {
  const h = harness();
  const running = h.refreshOnce(null);
  assert.deepEqual(h.events, []);
  assert.deepEqual(h.modes, [null]);
  const snapshot = { accounts: [] };
  h.succeed(snapshot);
  await running;
  assert.deepEqual(h.events, [{ channel: 'usage:update', payload: snapshot }]);
  assert.equal(h.retries(), 1);
  const failing = harness();
  const failed = failing.refreshOnce(null);
  failing.fail();
  await failed;
  assert.deepEqual(failing.events, []);
});

test('refresh IPC skips absent or destroyed windows, including destruction during a run', async () => {
  for (const state of ['absent', 'destroyed', 'alive'] as const) {
    for (const fails of [false, true]) {
      const h = harness(state);
      const running = h.refreshOnce('forced');
      h.destroy();
      if (fails) h.fail();
      else h.succeed({ accounts: [] });
      await running;
      assert.deepEqual(h.events, state === 'alive' ? [{ channel: 'usage:refreshing', payload: true }] : []);
    }
  }
});
