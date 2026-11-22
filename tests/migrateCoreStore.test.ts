// tests/migrateCoreStore.test.ts — the standalone migration script (#311): Electron's
// encrypted store → the core's store.json. The script is plain JS under scripts/ so users
// can download and run it; the test loads it from the project root.
//
// The expected store.json is gocore/internal/store/testdata/migrated-from-electron.json:
// written by the Go conversion of #308 (an independent implementation of the same rules)
// and opened by a Go test with store.Open, so the format is checked on both sides.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import crypto from 'node:crypto';
import fs from 'node:fs';
import http from 'node:http';
import type { AddressInfo } from 'node:net';
import os from 'node:os';
import path from 'node:path';
import { at } from './support/at';

// Compiled test: dist/tests/ → project root two levels up.
const ROOT = path.join(__dirname, '..', '..');
const SCRIPT = path.join(ROOT, 'scripts', 'migrate-core-store', 'migrate-core-store.js');
const TESTDATA = path.join(ROOT, 'scripts', 'migrate-core-store', 'testdata');
const EXPECTED = path.join(ROOT, 'gocore', 'internal', 'store', 'testdata', 'migrated-from-electron.json');
const APP_KEY = 'dev-only-placeholder-change-before-release';

interface Converted { state: unknown; warnings: string[] }
interface Migrate {
  decrypt(data: Buffer, key: string): unknown;
  convert(settings: unknown): Converted;
  electronDir(env: NodeJS.ProcessEnv, platform: NodeJS.Platform, home: string): string;
  coreDir(env: NodeJS.ProcessEnv, platform: NodeJS.Platform, home: string): string;
  UnsupportedFormatError: new (message: string) => Error;
  DecryptError: new (message: string) => Error;
}

// Day keys are local days: the fixture was written in Europe/Rome (+01:00 in November).
process.env.TZ = 'Europe/Rome';

async function load(): Promise<Migrate> {
  return (await import(SCRIPT)) as Migrate;
}

const settings = (): unknown => JSON.parse(fs.readFileSync(path.join(TESTDATA, 'settings.json'), 'utf8'));

test('decrypt reads files written by the real conf, whatever the IV (salt = iv.toString())', async () => {
  const m = await load();
  for (const name of ['ascii', 'valid-multibyte', 'truncated-3byte', 'all-invalid']) {
    const data = fs.readFileSync(path.join(TESTDATA, `iv-${name}.bin`));
    assert.deepEqual(m.decrypt(data, APP_KEY), settings(), name);
  }
});

test('decrypt: plain JSON is read as conf does, an old createDecipher file is unsupported, a wrong key fails', async () => {
  const m = await load();
  assert.deepEqual(m.decrypt(Buffer.from('{"accounts":[]}'), APP_KEY), { accounts: [] });
  assert.throws(() => m.decrypt(crypto.randomBytes(64), APP_KEY), m.UnsupportedFormatError);
  const data = fs.readFileSync(path.join(TESTDATA, 'iv-ascii.bin'));
  assert.throws(() => m.decrypt(data, 'another key'), m.DecryptError);
  assert.throws(() => m.decrypt(data.subarray(0, 20), APP_KEY), m.DecryptError);
});

test('convert gives the store.json of the Go fixture and reports what it skipped, by id only', async () => {
  const m = await load();
  const { state, warnings } = m.convert(settings());
  assert.deepEqual(state, JSON.parse(fs.readFileSync(EXPECTED, 'utf8')));
  assert.equal(warnings.length, 3);
  assert.ok(warnings.some((w) => w.includes('"gone"')), warnings.join('\n'));
  assert.ok(!JSON.stringify(state).includes('SYNTHETIC-TOKEN'), 'the Copilot token is never copied');
});

test('convert refuses a document that is not the app settings', async () => {
  const m = await load();
  assert.throws(() => m.convert({ ui: {} }), /not the app's settings/);
});

test('convert: a missing dayStartUsed takes the previous day, duplicates keep the last, bad windows are skipped', async () => {
  const m = await load();
  const account = { id: 'a', provider: 'claude', label: 'A', enabled: true, subscription: { renewalRule: { type: 'dayOfMonth', day: 3 } },
    workSchedule: { enabled: false, days: {}, hoursPerDay: 0 }, binaryPath: '' };
  const { state, warnings } = m.convert({
    accounts: [account, account, { id: 'x', provider: 'other' }],
    ui: { refreshIntervalMinutes: 2.5, notificationThresholdPercent: -1 },
    history: {
      retentionDays: 30,
      dailyUsage: [
        { date: '2026-11-02', accountId: 'a', windowId: 'w', used: 5 },
        { date: '2026-11-03', accountId: 'a', windowId: 'w', used: 8 },
        { date: '2026-11-03', accountId: 'a', windowId: 'w', used: 9 },
      ],
      recentSamples: [],
      lastGood: { a: { lastUpdatedAt: '2026-11-03T09:00:00.120Z', planTier: null, subscriptionRenewsAt: null,
        quotaWindows: [{ id: 'w', label: 'W', periodType: 'weird', periodLength: null, unit: 'percentage', used: 1, total: null, resetsAt: null }] } },
    },
  }) as { state: { settings: unknown; accounts: { schedule: unknown; binaryPath?: string; history: Record<string, { dailyUsage: unknown }>; lastGood: unknown }[] }; warnings: string[] };
  assert.deepEqual(state.settings, { refreshIntervalMinutes: 30, retentionDays: 30, notificationThresholdPercent: 80 });
  assert.equal(state.accounts.length, 1);
  const migrated = at(state.accounts, 0);
  assert.deepEqual(migrated.schedule, { enabled: false, days: [0, 0, 0, 0, 0, 0, 0], hoursPerDay: 8 });
  assert.equal(migrated.binaryPath, undefined);
  assert.deepEqual(migrated.history.w?.dailyUsage, [
    { day: '2026-11-02T00:00:00+01:00', used: 5, dayStartUsed: 5 },
    { day: '2026-11-03T00:00:00+01:00', used: 9, dayStartUsed: 5 },
  ]);
  assert.deepEqual(migrated.lastGood, { updatedAt: '2026-11-03T09:00:00.12Z', planTier: null, subscriptionRenewsAt: null, quotaWindows: [] });
  assert.equal(warnings.length, 3); // duplicate account, unknown provider, unknown window type
});

test('convert: day keys match Go where a DST change skips or repeats midnight (time.ParseInLocation)', async () => {
  const m = await load();
  // Values printed by Go's json.Marshal(time.Date(y, m, d, 0, 0, 0, 0, loc)). Go resolves a
  // skipped midnight differently west and east of UTC, and a repeated one to the later
  // offset; the script was also checked against every zone 2015-2030 (4559 such days).
  const cases: [string, string[], string[]][] = [
    ['America/Santiago', ['2026-09-05', '2026-09-06', '2026-09-07'], // 00:00 → 01:00, west
      ['2026-09-05T00:00:00-04:00', '2026-09-05T23:00:00-04:00', '2026-09-07T00:00:00-03:00']],
    ['Africa/Cairo', ['2026-04-24'], ['2026-04-24T01:00:00+03:00']], // 00:00 → 01:00, east
    ['Asia/Amman', ['2015-10-30'], ['2015-10-30T00:00:00+02:00']], // 01:00 → 00:00, midnight twice
  ];
  try {
    for (const [zone, dates, want] of cases) {
      process.env.TZ = zone;
      const { state } = m.convert({
        accounts: [{ id: 'a', provider: 'codex' }],
        history: { dailyUsage: dates.map((date) => ({ date, accountId: 'a', windowId: 'w', used: 1, dayStartUsed: 0 })) },
      }) as { state: { accounts: { history: Record<string, { dailyUsage: { day: string }[] }> }[] } };
      assert.deepEqual(at(state.accounts, 0).history.w?.dailyUsage.map((p) => p.day), want, zone);
    }
  } finally {
    process.env.TZ = 'Europe/Rome';
  }
});

test('convert: a point without baseline after a reset starts from 0, as history.baseline in Go', async () => {
  const m = await load();
  const { state } = m.convert({
    accounts: [{ id: 'a', provider: 'codex' }],
    history: { dailyUsage: [
      { date: '2026-11-02', accountId: 'a', windowId: 'w', used: 80 },
      { date: '2026-11-03', accountId: 'a', windowId: 'w', used: 4 },
      { date: '2026-11-04', accountId: 'a', windowId: 'w', used: 3.5 },
    ] },
  }) as { state: { accounts: { history: Record<string, { dailyUsage: { dayStartUsed: number }[] }> }[] } };
  // 80 → 4 is a reset (drop > 1 point): 0. 4 → 3.5 is noise: the previous value.
  assert.deepEqual(at(state.accounts, 0).history.w?.dailyUsage.map((p) => p.dayStartUsed), [80, 0, 4]);
});

test('folders: Electron userData is AIpermiler, the core folder is aipermiler, overrides must be absolute', async () => {
  const m = await load();
  const home = '/home/u';
  // The script joins with the host's path module (it runs on the OS it migrates), so the expected
  // values do too: on a Windows runner the Linux and macOS cases come out with backslashes.
  assert.equal(m.electronDir({}, 'linux', home), path.join(home, '.config', 'AIpermiler'));
  assert.equal(m.coreDir({ XDG_CONFIG_HOME: '/x' }, 'linux', home), path.join('/x', 'aipermiler'));
  assert.equal(m.electronDir({}, 'darwin', home), path.join(home, 'Library', 'Application Support', 'AIpermiler'));
  assert.equal(m.coreDir({ APPDATA: 'C:\\Users\\u\\AppData\\Roaming' }, 'win32', home), path.join('C:\\Users\\u\\AppData\\Roaming', 'aipermiler'));
  assert.equal(m.electronDir({ AIPERMILER_ELECTRON_DIR: '/e' }, 'linux', home), '/e');
  assert.equal(m.coreDir({ AIPERMILER_CONFIG_DIR: '/c' }, 'linux', home), '/c');
  assert.throws(() => m.coreDir({ AIPERMILER_CONFIG_DIR: 'rel' }, 'linux', home), /absolute/);
});

// --- end to end, as a user runs it ---------------------------------------------------

interface Run { code: number; stdout: string; stderr: string }

function setup(): { electron: string; core: string; env: NodeJS.ProcessEnv } {
  const base = fs.mkdtempSync(path.join(os.tmpdir(), 'migrate-core-store-'));
  const electron = path.join(base, 'electron');
  const core = path.join(base, 'core');
  fs.mkdirSync(electron);
  fs.copyFileSync(path.join(TESTDATA, 'iv-truncated-3byte.bin'), path.join(electron, 'aipermiler-config.json'));
  const env = { ...process.env, TZ: 'Europe/Rome', AIPERMILER_ELECTRON_DIR: electron, AIPERMILER_CONFIG_DIR: core };
  return { electron, core, env };
}

function run(env: NodeJS.ProcessEnv, ...args: string[]): Promise<Run> {
  return new Promise((resolve) => {
    // stdin is not a terminal here, so the script cannot ask: it must refuse without --yes.
    execFile(process.execPath, [SCRIPT, ...args], { env }, (error, stdout, stderr) => {
      resolve({ code: error ? Number(error.code ?? 1) : 0, stdout, stderr });
    });
  });
}

const sha = (file: string): string => crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex');

test('run: writes store.json (0600, folder 0700) and leaves Electron\'s file untouched', async () => {
  const { electron, core, env } = setup();
  const before = sha(path.join(electron, 'aipermiler-config.json'));
  const r = await run(env);
  assert.equal(r.code, 0, r.stderr);
  const written: unknown = JSON.parse(fs.readFileSync(path.join(core, 'store.json'), 'utf8'));
  assert.deepEqual(written, JSON.parse(fs.readFileSync(EXPECTED, 'utf8')));
  assert.equal(sha(path.join(electron, 'aipermiler-config.json')), before);
  assert.deepEqual(fs.readdirSync(core), ['store.json'], 'no temp file left');
  if (process.platform !== 'win32') {
    assert.equal(fs.statSync(path.join(core, 'store.json')).mode & 0o777, 0o600);
    assert.equal(fs.statSync(core).mode & 0o777, 0o700);
  }
  assert.match(r.stdout, /3 accounts/);
});

test('run: an existing store.json is kept unless --yes, then backed up', async () => {
  const { core, env } = setup();
  fs.mkdirSync(core);
  fs.writeFileSync(path.join(core, 'store.json'), '{"schemaVersion":1}');
  const refused = await run(env);
  assert.notEqual(refused.code, 0);
  assert.match(refused.stderr, /--yes/);
  assert.equal(fs.readFileSync(path.join(core, 'store.json'), 'utf8'), '{"schemaVersion":1}');

  const r = await run(env, '--yes');
  assert.equal(r.code, 0, r.stderr);
  const backups = fs.readdirSync(core).filter((f) => f.startsWith('store.json.bak-'));
  assert.equal(backups.length, 1);
  assert.equal(fs.readFileSync(path.join(core, backups[0] ?? ''), 'utf8'), '{"schemaVersion":1}');
});

test('run: any HTTP answer on api.json\'s port means the service is there, even a refusal', async () => {
  const { core, env } = setup();
  fs.mkdirSync(core);
  const server = http.createServer((_req, res) => res.writeHead(401).end());
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const { port } = server.address() as AddressInfo;
  fs.writeFileSync(path.join(core, 'api.json'), JSON.stringify({ schemaVersion: 1, port, token: 'old-token' }));
  try {
    const refused = await run(env, '--yes');
    assert.notEqual(refused.code, 0);
    assert.match(refused.stderr, /aipermiler service stop/);
    assert.ok(!fs.existsSync(path.join(core, 'store.json')));
  } finally {
    await new Promise((resolve) => server.close(resolve));
  }
});

test('run: refuses while the service answers, goes on when api.json is stale', async () => {
  const { core, env } = setup();
  fs.mkdirSync(core);
  const token = 'test-token';
  const server = http.createServer((req, res) => {
    const ok = req.url === '/api/v1/health' && req.headers.authorization === `Bearer ${token}`;
    res.writeHead(ok ? 200 : 401).end(ok ? '{"schemaVersion":1}' : '');
  });
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const { port } = server.address() as AddressInfo;
  fs.writeFileSync(path.join(core, 'api.json'), JSON.stringify({ schemaVersion: 1, port, token }));
  try {
    const refused = await run(env, '--yes');
    assert.notEqual(refused.code, 0);
    assert.match(refused.stderr, /service is running/);
    assert.ok(!fs.existsSync(path.join(core, 'store.json')));
  } finally {
    await new Promise((resolve) => server.close(resolve));
  }
  const r = await run(env); // the same api.json, nobody listening: a leftover
  assert.equal(r.code, 0, r.stderr);
  assert.ok(fs.existsSync(path.join(core, 'store.json')));
});

test('run: no Electron file means nothing to migrate', async () => {
  const { electron, core, env } = setup();
  fs.rmSync(path.join(electron, 'aipermiler-config.json'));
  const r = await run(env);
  assert.notEqual(r.code, 0);
  assert.match(r.stderr, /nothing to migrate/);
  assert.ok(!fs.existsSync(path.join(core, 'store.json')));
});
