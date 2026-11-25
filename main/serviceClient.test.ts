// main/serviceClient.test.ts — the service client (#182) against a fake service: a real
// loopback HTTP server answering with the service's own fixtures
// (gocore/contract/v1/service/) and the shared usage fixtures, and every case that falls
// back to the CLI.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import type { AddressInfo } from 'node:net';
import { CoreProviderError, parseUsageRun, requireFresh, type CoreUsageResult } from './coreClient';
import {
  NOT_READ_YET,
  ServiceUnusableError,
  coreConfigDir,
  createServiceClient,
  parseServiceUsage,
  readUsageWithFallback,
  type ServiceClient,
  type UsageSourceRecord,
} from './serviceClient';
import { FormatDriftError } from '../services/_shape';

// Compiled test: dist/main/ → project root two levels up.
const FIXTURES = path.join(__dirname, '..', '..', 'gocore', 'contract', 'v1');
const fixture = (name: string): string => fs.readFileSync(path.join(FIXTURES, `${name}.json`), 'utf8');
const TOKEN = 'synthetic-service-token';

interface FakeService {
  dir: string;
  port: number;
  requests: { url: string; authorization: string | undefined }[];
  close: () => Promise<void>;
}

// 'hang': accept the request and never answer it.
type Route = { status: number; body: string; headers?: Record<string, string> } | 'hang';

/** A loopback server that checks the bearer token like the real one, writing api.json in a temp folder. */
async function fakeService(routes: Record<string, Route>, apiFile?: (port: number) => unknown): Promise<FakeService> {
  const requests: FakeService['requests'] = [];
  const server = http.createServer((req, res) => {
    requests.push({ url: req.url ?? '', authorization: req.headers.authorization });
    if (req.headers.authorization !== `Bearer ${TOKEN}`) {
      res.writeHead(401).end();
      return;
    }
    const route = routes[req.url ?? ''];
    if (!route) {
      res.writeHead(404, { 'Content-Type': 'application/json' }).end(fixture('error-usage'));
      return;
    }
    if (route === 'hang') return;
    res.writeHead(route.status, { 'Content-Type': 'application/json', ...route.headers }).end(route.body);
  });
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const { port } = server.address() as AddressInfo;
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'aipermiler-service-'));
  const info = apiFile ? apiFile(port) : { schemaVersion: 1, port, token: TOKEN, version: '1.2.3', contractVersion: 1, pid: 1, startedAt: '2026-10-31T21:00:00Z' };
  fs.writeFileSync(path.join(dir, 'api.json'), `${JSON.stringify(info)}\n`);
  return {
    dir,
    port,
    requests,
    close: () => new Promise<void>((resolve) => {
      server.close(() => { resolve(); });
      server.closeAllConnections();
      fs.rmSync(dir, { recursive: true, force: true });
    }),
  };
}

const HEALTH: Route = { status: 200, body: fixture('service/health') };
const usageRoute = (id: string): string => `/api/v1/accounts/${id}/usage`;

async function withService<T>(routes: Record<string, Route>, run: (svc: FakeService, client: ServiceClient) => Promise<T>, apiFile?: (port: number) => unknown,
  timeouts?: { healthMs: number; usageMs: number }): Promise<T> {
  const svc = await fakeService(routes, apiFile);
  try {
    return await run(svc, createServiceClient({ configDir: () => svc.dir, ...(timeouts ? { timeouts } : {}) }));
  } finally {
    await svc.close();
  }
}

async function unusableReason(promise: Promise<unknown>): Promise<string> {
  try {
    await promise;
  } catch (err) {
    if (err instanceof ServiceUnusableError) return err.reason;
    throw err;
  }
  assert.fail('expected a ServiceUnusableError');
}

test('service fixtures: health, usage list, stale not_read_yet and error not_read_yet are read', () => {
  const health = JSON.parse(fixture('service/health')) as Record<string, unknown>;
  assert.deepEqual(health, { schemaVersion: 1, version: '1.2.3', contractVersion: 1 });

  // The list's documents are usage documents of the same contract.
  const list = JSON.parse(fixture('service/usage-list')) as { schemaVersion: number; accounts: { id: string; document: unknown }[] };
  assert.equal(list.schemaVersion, 1);
  assert.deepEqual(list.accounts.map((a) => a.id), ['a1']);
  const first = list.accounts[0];
  assert.ok(first);
  const fromList = parseServiceUsage(200, JSON.stringify(first.document), 'fake');
  assert.deepEqual(fromList, parseUsageRun({ exitCode: 0, stdout: fixture('usage-ok') }, 'fake'));

  const stale = parseServiceUsage(200, fixture('service/usage-not-read'), 'fake');
  assert.equal(stale.stale, true);
  assert.deepEqual(stale.staleReason, { code: 'unavailable', message: 'Not read yet by the service', reason: NOT_READ_YET });
  assert.throws(() => requireFresh(stale, 'fake'), (err: unknown) => err instanceof CoreProviderError && err.reason === NOT_READ_YET);

  assert.throws(() => parseServiceUsage(503, fixture('service/error-not-read'), 'fake'),
    (err: unknown) => err instanceof CoreProviderError && err.code === 'unavailable' && err.reason === NOT_READ_YET);
});

// The pacing snapshot (#326) is the usage document plus `pacing`: today's parser
// reads it as the usage document, unknown fields ignored. Reading the pacing
// itself is the client's next step (#181).
test('service fixture snapshot: read as a usage document, with pacing per window', () => {
  const doc = JSON.parse(fixture('service/snapshot')) as { pacing: { critical: number; windows: { id: string; trend: string | null }[] } };
  const usage = parseServiceUsage(200, fixture('service/snapshot'), 'fake');
  assert.equal(usage.stale, false);
  assert.deepEqual(usage.usage.quotaWindows.map((w) => w.id), doc.pacing.windows.map((w) => w.id));
  assert.equal(doc.pacing.critical, 1);
  assert.deepEqual(doc.pacing.windows.map((w) => w.trend), ['accelerating', null, null, null]);
});

test('a running service: api.json, /health with the token, then the account document', async () => {
  await withService({ '/api/v1/health': HEALTH, [usageRoute('a1')]: { status: 200, body: fixture('usage-ok') } }, async (svc, client) => {
    const result = await client.usage('a1', 'fake');
    assert.equal(result.fetchedAt, '2026-10-31T21:10:00Z');
    assert.equal(result.usage.quotaWindows.length, 2);
    assert.deepEqual(svc.requests.map((r) => r.url), ['/api/v1/health', usageRoute('a1')]);
    assert.ok(svc.requests.every((r) => r.authorization === `Bearer ${TOKEN}`));
  });
});

test('the account id is escaped in the route', async () => {
  await withService({ '/api/v1/health': HEALTH, [usageRoute('a%2Fb')]: { status: 200, body: fixture('usage-ok') } }, async (svc, client) => {
    await client.usage('a/b', 'fake');
    assert.equal(svc.requests.at(-1)?.url, usageRoute('a%2Fb'));
  });
});

test('a provider error served by the service (502) is the account answer, not a fallback', async () => {
  await withService({ '/api/v1/health': HEALTH, [usageRoute('a1')]: { status: 502, body: fixture('error-signed-out') } }, async (_svc, client) => {
    await assert.rejects(client.usage('a1', 'fake'), (err: unknown) => err instanceof CoreProviderError && err.code === 'signed_out');
  });
  await withService({ '/api/v1/health': HEALTH, [usageRoute('a1')]: { status: 502, body: fixture('error-format-drift') } }, async (_svc, client) => {
    await assert.rejects(client.usage('a1', 'fake'), FormatDriftError);
  });
});

test('fallback: no api.json → not_running', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'aipermiler-service-'));
  try {
    assert.equal(await unusableReason(createServiceClient({ configDir: () => dir }).usage('a1', 'fake')), 'not_running');
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
  // A config folder that cannot be resolved is no service either.
  assert.equal(await unusableReason(createServiceClient({ configDir: () => { throw new Error('no folder'); } }).usage('a1', 'fake')), 'not_running');
});

test('fallback: stopped service (leftover api.json, nobody listening) → unreachable', async () => {
  const svc = await fakeService({ '/api/v1/health': HEALTH });
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'aipermiler-service-'));
  fs.copyFileSync(path.join(svc.dir, 'api.json'), path.join(dir, 'api.json'));
  await svc.close();
  try {
    assert.equal(await unusableReason(createServiceClient({ configDir: () => dir }).usage('a1', 'fake')), 'unreachable');
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('fallback: unreadable api.json → malformed', async () => {
  for (const info of [{ port: 'x', token: TOKEN }, { port: 0, token: TOKEN }, { token: TOKEN }, (port: number) => ({ port, token: '' })]) {
    const reason: string = await withService({ '/api/v1/health': HEALTH }, (_svc, client) => unusableReason(client.usage('a1', 'fake')),
      (port) => (typeof info === 'function' ? info(port) : info));
    assert.equal(reason, 'malformed');
  }
});

test('fallback: the token is refused (401) → unauthorized', async () => {
  const reason = await withService({ '/api/v1/health': HEALTH }, (_svc, client) => unusableReason(client.usage('a1', 'fake')),
    (port) => ({ schemaVersion: 1, port, token: 'token-of-a-dead-instance', version: '1.2.3', contractVersion: 1 }));
  assert.equal(reason, 'unauthorized');
});

test('fallback: a fake older service (lower or unknown contractVersion) is not used', async () => {
  for (const contractVersion of [0, 2, '1', null]) {
    const health: Route = { status: 200, body: JSON.stringify({ schemaVersion: 1, version: '0.0.9', contractVersion }) };
    await withService({ '/api/v1/health': health, [usageRoute('a1')]: { status: 200, body: fixture('usage-ok') } }, async (svc, client) => {
      assert.equal(await unusableReason(client.usage('a1', 'fake')), 'incompatible');
      assert.deepEqual(svc.requests.map((r) => r.url), ['/api/v1/health'], 'no account read from an older service');
    });
  }
  // A health document without contractVersion at all: an older service too.
  await withService({ '/api/v1/health': { status: 200, body: '{"schemaVersion":1,"version":"0.0.1"}' } }, async (_svc, client) => {
    assert.equal(await unusableReason(client.usage('a1', 'fake')), 'incompatible');
  });
});

test('fallback: an account the service does not serve (404) → unknown_account', async () => {
  await withService({ '/api/v1/health': HEALTH }, async (_svc, client) => {
    assert.equal(await unusableReason(client.usage('added-after-migration', 'fake')), 'unknown_account');
  });
});

test('fallback: a failing service (5xx other than a provider error, failed health) → failed', async () => {
  // The service's own 503s (stopping, no refresh loop) carry no provider.
  const stopping = '{"schemaVersion":1,"error":{"code":"unavailable","message":"The service is stopping"}}';
  await withService({ '/api/v1/health': HEALTH, [usageRoute('a1')]: { status: 503, body: stopping } }, async (_svc, client) => {
    assert.equal(await unusableReason(client.usage('a1', 'fake')), 'failed');
  });
  // A 502 whose body is not an error document.
  await withService({ '/api/v1/health': HEALTH, [usageRoute('a1')]: { status: 502, body: fixture('usage-ok') } }, async (_svc, client) => {
    assert.equal(await unusableReason(client.usage('a1', 'fake')), 'failed');
  });
  await withService({ '/api/v1/health': HEALTH, [usageRoute('a1')]: { status: 500, body: '' } }, async (_svc, client) => {
    assert.equal(await unusableReason(client.usage('a1', 'fake')), 'failed');
  });
  await withService({ '/api/v1/health': { status: 500, body: '' } }, async (_svc, client) => {
    assert.equal(await unusableReason(client.usage('a1', 'fake')), 'failed');
  });
});

test('fallback: malformed answers → malformed', async () => {
  const cases: Record<string, Route>[] = [
    { '/api/v1/health': { status: 200, body: 'not json' } },
    { '/api/v1/health': HEALTH, [usageRoute('a1')]: { status: 200, body: '{"schemaVersion":1' } },
    { '/api/v1/health': HEALTH, [usageRoute('a1')]: { status: 200, body: fixture('usage-ok').replace('"schemaVersion":1', '"schemaVersion":2') } },
    { '/api/v1/health': HEALTH, [usageRoute('a1')]: { status: 200, body: fixture('error-unavailable') } },
    // A provider error document without its message.
    { '/api/v1/health': HEALTH, [usageRoute('a1')]: { status: 502, body: '{"schemaVersion":1,"provider":"fake","error":{"code":"timeout"}}' } },
  ];
  for (const routes of cases) {
    await withService(routes, async (_svc, client) => {
      assert.equal(await unusableReason(client.usage('a1', 'fake')), 'malformed');
    });
  }
  // A document for another provider is malformed too.
  await withService({ '/api/v1/health': HEALTH, [usageRoute('a1')]: { status: 200, body: fixture('usage-ok') } }, async (_svc, client) => {
    assert.equal(await unusableReason(client.usage('a1', 'codex')), 'malformed');
  });
});

test('readUsageWithFallback: service answer, CLI on an unusable service, service errors not retried', async () => {
  const ok = parseUsageRun({ exitCode: 0, stdout: fixture('usage-ok') }, 'fake');
  const cliResult: CoreUsageResult = { ...ok, fetchedAt: 'cli' };
  let cliCalls = 0;
  const cli = (): Promise<CoreUsageResult> => { cliCalls += 1; return Promise.resolve(cliResult); };
  const sources: UsageSourceRecord[] = [];
  const record = (s: UsageSourceRecord): void => { sources.push(s); };

  const serving: ServiceClient = { usage: () => Promise.resolve(ok) };
  assert.deepEqual(await readUsageWithFallback(serving, 'a1', 'fake', cli, record), { result: ok, source: 'service' });
  assert.equal(cliCalls, 0, 'no own probe while the service serves the account');

  const older: ServiceClient = { usage: () => Promise.reject(new ServiceUnusableError('incompatible', 'older')) };
  assert.deepEqual(await readUsageWithFallback(older, 'a1', 'fake', cli, record), { result: cliResult, source: 'cli' });
  assert.equal(cliCalls, 1);

  const signedOut: ServiceClient = { usage: () => Promise.reject(new CoreProviderError('signed_out', 'Signed out', 'fake')) };
  await assert.rejects(readUsageWithFallback(signedOut, 'a1', 'fake', cli, record), CoreProviderError);
  assert.equal(cliCalls, 1, 'a provider error from the service is not read again through the CLI');

  assert.deepEqual(sources, [
    { source: 'service', reason: null },
    { source: 'cli', reason: 'incompatible' },
    { source: 'service', reason: null },
  ]);
});

test('coreConfigDir: the Go core folder on each OS, with the override', () => {
  const home = path.join(path.sep, 'home', 'u');
  assert.equal(coreConfigDir({}, 'linux', home), path.join(home, '.config', 'aipermiler'));
  const xdg = path.join(path.sep, 'xdg');
  assert.equal(coreConfigDir({ XDG_CONFIG_HOME: xdg }, 'linux', home), path.join(xdg, 'aipermiler'));
  assert.throws(() => coreConfigDir({ XDG_CONFIG_HOME: 'relative' }, 'linux', home), /absolute/);
  assert.equal(coreConfigDir({}, 'darwin', home), path.join(home, 'Library', 'Application Support', 'aipermiler'));
  const appData = path.join(path.sep, 'AppData', 'Roaming');
  assert.equal(coreConfigDir({ APPDATA: appData }, 'win32', home), path.join(appData, 'aipermiler'));
  assert.throws(() => coreConfigDir({}, 'win32', home), /APPDATA/);
  const override = path.join(path.sep, 'dev', 'core');
  assert.equal(coreConfigDir({ AIPERMILER_CONFIG_DIR: override }, 'win32', home), override);
  assert.throws(() => coreConfigDir({ AIPERMILER_CONFIG_DIR: 'rel' }, 'linux', home), /absolute/);
});

test('a 503 error document for the provider (not_ready) is the account answer: no CLI probe next to the service (#182 (d))', async () => {
  await withService({ '/api/v1/health': HEALTH, [usageRoute('a1')]: { status: 503, body: fixture('error-not-ready') } }, async (_svc, client) => {
    let cliCalls = 0;
    const cli = (): Promise<CoreUsageResult> => { cliCalls += 1; return Promise.reject(new Error('no CLI expected')); };
    await assert.rejects(readUsageWithFallback(client, 'a1', 'fake', cli, () => undefined),
      (err: unknown) => err instanceof CoreProviderError && err.code === 'not_ready');
    assert.equal(cliCalls, 0);
  });
});

test('fallback: a read the service refused (400 usage error) → rejected', async () => {
  await withService({ '/api/v1/health': HEALTH, [usageRoute('a1')]: { status: 400, body: fixture('error-usage') } }, async (_svc, client) => {
    assert.equal(await unusableReason(client.usage('a1', 'fake')), 'rejected');
  });
});

test('fallback: a hanging service (connection accepted, no answer) → unreachable within the timeout', async () => {
  const timeouts = { healthMs: 200, usageMs: 200 };
  for (const routes of [{ '/api/v1/health': 'hang' as const }, { '/api/v1/health': HEALTH, [usageRoute('a1')]: 'hang' as const }]) {
    const started = Date.now();
    const reason = await withService(routes, (_svc, client) => unusableReason(client.usage('a1', 'fake')), undefined, timeouts);
    assert.equal(reason, 'unreachable');
    assert.ok(Date.now() - started < 5000, 'bounded by the client timeout');
  }
});

test('a redirect is never followed: the token stays on the service port', async () => {
  const routes: Record<string, Route> = {
    '/api/v1/health': { status: 302, body: '', headers: { Location: '/elsewhere' } },
    '/elsewhere': HEALTH,
  };
  await withService(routes, async (svc, client) => {
    assert.equal(await unusableReason(client.usage('a1', 'fake')), 'unreachable');
    assert.deepEqual(svc.requests.map((r) => r.url), ['/api/v1/health']);
  });
});
