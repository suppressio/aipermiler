import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { Script } from 'node:vm';
import type { TrayViewState, UsageView } from '../types/index';

// Exercise the actual panel selection wiring without a browser or Electron.
const source = readFileSync(path.join(path.dirname(fileURLToPath(import.meta.url)), 'app.js'), 'utf8');
const functions = ['panelView', 'applyView', 'choosePanelView', 'selectAccount', 'selectWindowSnapshot'].map(name => {
  const body = source.match(new RegExp(`function ${name}\\([^]*?\\n\\}`))?.[0];
  assert.ok(body, `${name} must exist in the built renderer`);
  return body;
}).join('\n');

const a = { accountId: 'a', criticalWindow: { id: 'weekly' }, windows: [{ window: { id: '5h' } }, { window: { id: 'weekly' } }] };
const b = { ...a, accountId: 'b' };
const view = (mode: 'auto' | 'manual', shown: UsageView): TrayViewState => ({
  selection: { view: mode === 'auto' ? { mode } : { mode, ...shown }, critical: true }, shown, entries: [],
});

test('Auto keeps panel browsing local; Manual follows the main view and its fallback', () => {
  const local = { accountId: 'a', windowId: '5h' };
  const state = { view: view('auto', { accountId: 'b', windowId: 'weekly' }), localView: local,
    latestSnapshot: { accounts: [a, b] } };
  const applyView = new Script(`${functions}\napplyView`).runInNewContext({ state, renderSnapshot: () => undefined }) as (updated: TrayViewState) => void;
  applyView(view('auto', { accountId: 'b', windowId: '5h' }));
  assert.deepEqual(state.localView, local);
  const manual = view('manual', { accountId: 'b', windowId: 'weekly' });
  applyView(manual);
  const fallback = { ...manual, shown: local };
  applyView(fallback);
  assert.equal(state.view.shown, local);
  assert.equal(state.view.selection.view.mode, 'manual');
  applyView(manual);
  applyView(view('auto', local));
  assert.deepEqual({ ...state.localView }, { accountId: 'b', windowId: 'weekly' });
  applyView(view('auto', local));
  assert.deepEqual({ ...state.localView }, { accountId: 'b', windowId: 'weekly' });
});

test('an account tab uses that account critical window instead of another account window id', () => {
  const state = { view: view('manual', { accountId: 'a', windowId: '5h' }), localView: null };
  const select = new Script(`${functions}\nselectWindowSnapshot`).runInNewContext({ state }) as (account: typeof a) => (typeof a.windows)[number];
  assert.equal(select(a).window.id, '5h');
  assert.equal(select(b).window.id, 'weekly');
});

test('a panel click with the badge Off browses locally and leaves the badge Off', () => {
  const off: TrayViewState = { selection: { view: { mode: 'off' }, critical: true }, shown: null, entries: [] };
  const sent: unknown[] = [];
  const state = { view: off, localView: null as UsageView | null, latestSnapshot: { accounts: [a, b] } };
  const context = { state, renderSnapshot: () => undefined, runGuarded: () => undefined,
    window: { aipermiler: { setView: (choice: unknown) => { sent.push(choice); } } } };
  const choose = new Script(`${functions}\nchoosePanelView`).runInNewContext(context) as (view: UsageView) => void;
  choose({ accountId: 'b', windowId: '5h' });
  assert.deepEqual(sent, []);
  assert.deepEqual({ ...state.localView }, { accountId: 'b', windowId: '5h' });
  state.view = { ...off, selection: { view: { mode: 'auto' }, critical: true } };
  choose({ accountId: 'a', windowId: 'weekly' });
  assert.deepEqual(sent, [], 'a panel click never changes the tray selection');
  assert.deepEqual({ ...state.localView }, { accountId: 'a', windowId: 'weekly' });
});

test('Manual shows the selected window even when the badge shows another one', () => {
  // e.g. an unlimited Copilot window: listed and selectable, but no percentage for the badge.
  const selected = { accountId: 'b', windowId: '5h' };
  const state = { view: { ...view('manual', selected), shown: { accountId: 'a', windowId: 'weekly' } },
    localView: null, latestSnapshot: { accounts: [a, b] } };
  const panel = new Script(`${functions}\npanelView`).runInNewContext({ state }) as () => UsageView | null;
  assert.deepEqual({ ...panel() }, selected);
  // Gone from the snapshot (disabled account): the panel follows the shown fallback.
  state.latestSnapshot = { accounts: [a] };
  assert.deepEqual({ ...panel() }, { accountId: 'a', windowId: 'weekly' });
});

test('an account without windows opens locally in Manual until the selection changes', () => {
  const empty = { accountId: 'c', criticalWindow: null, windows: [] };
  const sent: unknown[] = [];
  const state = { view: view('manual', { accountId: 'a', windowId: '5h' }), localView: null, localOverride: false,
    latestSnapshot: { accounts: [a, empty] } };
  const context = { state, renderSnapshot: () => undefined, runGuarded: () => undefined,
    window: { aipermiler: { setView: (choice: unknown) => { sent.push(choice); } } } };
  const run = (name: string) => new Script(`${functions}\n${name}`).runInNewContext(context) as (arg?: unknown) => unknown;
  run('choosePanelView')({ accountId: 'c', windowId: null });
  assert.deepEqual(sent, []);
  assert.equal((run('selectAccount')(state.latestSnapshot) as { accountId: string }).accountId, 'c');
  run('applyView')(view('manual', { accountId: 'a', windowId: '5h' }));
  assert.equal(state.localOverride, true, 'a refresh with the same selection keeps the account open');
  // In Manual a click is a local override until the selection changes; nothing is sent.
  run('choosePanelView')({ accountId: 'a', windowId: '5h' });
  assert.equal(state.localOverride, true);
  assert.deepEqual(sent, []);
  run('applyView')(view('manual', { accountId: 'b', windowId: '5h' }));
  assert.equal(state.localOverride, false);
  run('applyView')(view('manual', { accountId: 'a', windowId: '5h' }));
  run('applyView')(view('manual', { accountId: 'a', windowId: '5h' }));
  assert.equal((run('selectAccount')(state.latestSnapshot) as { accountId: string }).accountId, 'a');
  run('choosePanelView')({ accountId: 'c', windowId: null });
  run('applyView')(view('manual', { accountId: 'a', windowId: 'weekly' }));
  assert.equal(state.localOverride, false);
  assert.equal((run('selectAccount')(state.latestSnapshot) as { accountId: string }).accountId, 'a');
});
