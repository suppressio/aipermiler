// main/serviceCopilot.test.ts — Copilot token and login through the service (#338)
// against a fake service: a loopback HTTP server that checks the bearer token like the
// real one and answers per method and route with the documents of docs/CORE-CLI.md
// ("Copilot account actions", #285). The secrets never leave the request body.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import type { AddressInfo } from 'node:net';
import { CoreOutputError, CoreProviderError, CoreTimeoutError } from './coreClient';
import { ServiceUnusableError } from './serviceClient';
import {
  createCopilotServiceActions,
  loginThroughService,
  type CopilotLoginStatus,
  type CopilotServiceActions,
} from './serviceCopilot';
import { LogBuffer, captureConsole } from '../diagnostics/logBuffer';

// Compiled test: dist/main/ → project root two levels up.
const FIXTURES = path.join(__dirname, '..', '..', 'gocore', 'contract', 'v1');
const fixture = (name: string): string => fs.readFileSync(path.join(FIXTURES, `${name}.json`), 'utf8');
const TOKEN = 'synthetic-service-token';
const PAT = 'github_pat_SYNTHETIC_secret_0123456789';
const CLIENT_SECRET = 'synthetic-client-secret-abcdef';
const AUTHORIZE_URL = 'https://github.com/login/oauth/authorize?client_id=Iv1.synthetic&state=synthetic';

// The CLI's documents for the copilot provider (the shared fixtures use `fake`).
const asCopilot = (name: string): string => fixture(name).replace('"provider":"fake"', '"provider":"copilot"');
const CONNECTED = asCopilot('account-connected');
const DELETED = asCopilot('account-deleted');
const AUTHORIZE = asCopilot('login-authorize');
const errorDoc = (code: string, message: string, reason?: string, provider: string | null = 'copilot'): string =>
  `${JSON.stringify({ schemaVersion: 1, ...(provider === null ? {} : { provider }), error: { code, message, ...(reason ? { reason } : {}) } })}\n`;
const LOGIN_IN_PROGRESS = errorDoc('unavailable', 'A login is already running (account other): cancel it or wait for its end', 'login_in_progress', null);
const LOGIN_CANCELLED = errorDoc('unavailable', 'The login was cancelled', 'login_cancelled');
const NO_ACCOUNT = errorDoc('usage', 'No account with id acc-1', undefined, null);

interface Request { method: string; url: string; authorization: string | undefined; body: string }
type Route = { status: number; body: string } | ((req: Request) => { status: number; body: string });

interface FakeService { dir: string; requests: Request[]; close: () => Promise<void> }

async function fakeService(routes: Record<string, Route>): Promise<FakeService> {
  const requests: Request[] = [];
  const table = new Map(Object.entries(routes));
  const server = http.createServer((req, res) => {
    let body = '';
    req.on('data', (chunk: Buffer) => { body += chunk.toString('utf8'); });
    req.on('end', () => {
      const request: Request = { method: req.method ?? '', url: req.url ?? '', authorization: req.headers.authorization, body };
      requests.push(request);
      if (req.headers.authorization !== `Bearer ${TOKEN}`) {
        res.writeHead(401).end();
        return;
      }
      const route = table.get(`${request.method} ${request.url}`);
      const answer = typeof route === 'function' ? route(request) : route ?? { status: 404, body: NO_ACCOUNT };
      res.writeHead(answer.status, { 'Content-Type': 'application/json' }).end(answer.body);
    });
  });
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const { port } = server.address() as AddressInfo;
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'aipermiler-copilot-'));
  fs.writeFileSync(path.join(dir, 'api.json'), `${JSON.stringify({ schemaVersion: 1, port, token: TOKEN, version: '1.2.3', contractVersion: 1, pid: 1, startedAt: '2026-11-25T08:00:00Z' })}\n`);
  return {
    dir,
    requests,
    close: () => new Promise<void>((resolve) => {
      server.close(() => { resolve(); });
      server.closeAllConnections();
      fs.rmSync(dir, { recursive: true, force: true });
    }),
  };
}

const HEALTH: Route = { status: 200, body: fixture('service/health') };
const TOKEN_ROUTE = '/api/v1/accounts/acc-1/token';
const LOGIN_ROUTE = '/api/v1/accounts/acc-1/login';

async function withService<T>(routes: Record<string, Route>, run: (svc: FakeService, actions: CopilotServiceActions) => Promise<T>): Promise<T> {
  const svc = await fakeService({ 'GET /api/v1/health': HEALTH, ...routes });
  try {
    return await run(svc, createCopilotServiceActions({ configDir: () => svc.dir }));
  } finally {
    await svc.close();
  }
}

const LOGIN_REQUEST = { clientId: 'Iv1.synthetic', clientSecret: CLIENT_SECRET, locale: 'it' as const };

test('token set: PUT with the token in the body only, answers the user name', async () => {
  await withService({ [`PUT ${TOKEN_ROUTE}`]: { status: 200, body: CONNECTED } }, async (svc, actions) => {
    assert.equal(await actions.setToken('acc-1', PAT), 'octo');
    const put = svc.requests.find((r) => r.method === 'PUT');
    assert.ok(put);
    assert.deepEqual(JSON.parse(put.body), { token: PAT });
    assert.equal(put.authorization, `Bearer ${TOKEN}`);
    assert.ok(!put.url.includes(PAT));
  });
});

test('token set: a rejected token is the service error (signed_out, token_rejected), shown as such', async () => {
  await withService({ [`PUT ${TOKEN_ROUTE}`]: { status: 502, body: errorDoc('signed_out', 'GitHub rejected the token', 'token_rejected') } }, async (_svc, actions) => {
    await assert.rejects(actions.setToken('acc-1', PAT), (err: unknown) =>
      err instanceof CoreProviderError && err.code === 'signed_out' && err.reason === 'token_rejected' && err.provider === 'copilot');
  });
});

test('token delete: DELETE answers deleted', async () => {
  await withService({ [`DELETE ${TOKEN_ROUTE}`]: { status: 200, body: DELETED } }, async (svc, actions) => {
    await actions.deleteToken('acc-1');
    assert.ok(svc.requests.some((r) => r.method === 'DELETE' && r.url === TOKEN_ROUTE && r.body === ''));
  });
});

test('an account the service does not know (404 usage error) is a ServiceUnusableError unknown_account', async () => {
  await withService({}, async (_svc, actions) => {
    await assert.rejects(actions.setToken('acc-1', PAT), (err: unknown) => err instanceof ServiceUnusableError && err.reason === 'unknown_account');
    await assert.rejects(actions.deleteToken('acc-1'), (err: unknown) => err instanceof ServiceUnusableError && err.reason === 'unknown_account');
  });
});

test('a bad request (400 usage error) is a client bug, not a provider error', async () => {
  await withService({ [`PUT ${TOKEN_ROUTE}`]: { status: 400, body: errorDoc('usage', 'token: a non-empty string of at most 64 KiB is required', undefined, null) } }, async (_svc, actions) => {
    await assert.rejects(actions.setToken('acc-1', PAT), CoreOutputError);
  });
});

test('no service running: every action fails with not_running (no CLI fallback), available() is false', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'aipermiler-copilot-none-'));
  try {
    const actions = createCopilotServiceActions({ configDir: () => dir });
    assert.equal(await actions.available(), false);
    const notRunning = (err: unknown): boolean => err instanceof ServiceUnusableError && err.reason === 'not_running';
    await assert.rejects(actions.setToken('acc-1', PAT), notRunning);
    await assert.rejects(actions.deleteToken('acc-1'), notRunning);
    await assert.rejects(actions.startLogin('acc-1', LOGIN_REQUEST), notRunning);
    await assert.rejects(actions.loginStatus('acc-1'), notRunning);
    await assert.rejects(actions.cancelLogin('acc-1'), notRunning);
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('available(): true with a running service', async () => {
  await withService({}, async (_svc, actions) => {
    assert.equal(await actions.available(), true);
  });
});

test('login start: POST with client id, secret and locale in the body, answers the authorize URL (202)', async () => {
  await withService({ [`POST ${LOGIN_ROUTE}`]: { status: 202, body: AUTHORIZE } }, async (svc, actions) => {
    assert.equal(await actions.startLogin('acc-1', LOGIN_REQUEST), AUTHORIZE_URL);
    const post = svc.requests.find((r) => r.method === 'POST');
    assert.ok(post);
    assert.deepEqual(JSON.parse(post.body), { clientId: 'Iv1.synthetic', clientSecret: CLIENT_SECRET, locale: 'it' });
    assert.ok(!post.url.includes(CLIENT_SECRET));
  });
});

test('login start: another login running (409) is login_in_progress', async () => {
  await withService({ [`POST ${LOGIN_ROUTE}`]: { status: 409, body: LOGIN_IN_PROGRESS } }, async (_svc, actions) => {
    await assert.rejects(actions.startLogin('acc-1', LOGIN_REQUEST), (err: unknown) =>
      err instanceof CoreProviderError && err.code === 'unavailable' && err.reason === 'login_in_progress');
  });
});

test('login status: waiting (202), connected (200), error documents thrown', async () => {
  const answers = [{ status: 202, body: AUTHORIZE }, { status: 200, body: CONNECTED }, { status: 504, body: errorDoc('timeout', 'No authorization within 5m0s') }];
  await withService({ [`GET ${LOGIN_ROUTE}`]: () => answers.shift() ?? { status: 500, body: '' } }, async (_svc, actions) => {
    assert.deepEqual(await actions.loginStatus('acc-1'), { state: 'waiting', url: AUTHORIZE_URL });
    assert.deepEqual(await actions.loginStatus('acc-1'), { state: 'connected', username: 'octo' });
    await assert.rejects(actions.loginStatus('acc-1'), (err: unknown) => err instanceof CoreProviderError && err.code === 'timeout');
  });
});

test('login cancel: DELETE, the cancelled answer or no login at all both resolve', async () => {
  await withService({ [`DELETE ${LOGIN_ROUTE}`]: { status: 502, body: LOGIN_CANCELLED } }, async (svc, actions) => {
    await actions.cancelLogin('acc-1');
    assert.ok(svc.requests.some((r) => r.method === 'DELETE' && r.url === LOGIN_ROUTE));
  });
  await withService({}, async (_svc, actions) => {
    await actions.cancelLogin('acc-1');
  });
});

function fakeActions(statuses: (CopilotLoginStatus | Error)[]): CopilotServiceActions & { cancelled: number } {
  const fake = {
    cancelled: 0,
    available: () => Promise.resolve(true),
    setToken: () => Promise.reject(new Error('not used')),
    deleteToken: () => Promise.reject(new Error('not used')),
    startLogin: () => Promise.resolve(AUTHORIZE_URL),
    loginStatus: () => {
      const next = statuses.shift() ?? { state: 'waiting', url: AUTHORIZE_URL };
      return next instanceof Error ? Promise.reject(next) : Promise.resolve(next);
    },
    cancelLogin: () => { fake.cancelled += 1; return Promise.resolve(); },
  };
  return fake;
}

test('loginThroughService: opens the URL, polls until the user name', async () => {
  const actions = fakeActions([{ state: 'waiting', url: AUTHORIZE_URL }, { state: 'waiting', url: AUTHORIZE_URL }, { state: 'connected', username: 'octo' }]);
  const opened: string[] = [];
  let sleeps = 0;
  const username = await loginThroughService(actions, 'acc-1', LOGIN_REQUEST, { onAuthorize: (url) => opened.push(url), sleep: () => { sleeps += 1; return Promise.resolve(); } });
  assert.equal(username, 'octo');
  assert.deepEqual(opened, [AUTHORIZE_URL]);
  assert.equal(sleeps, 3);
});

test('loginThroughService: an error status (cancelled from Settings, denied, timeout) ends it', async () => {
  const cancelled = new CoreProviderError('unavailable', 'The login was cancelled', 'copilot', 'login_cancelled');
  const actions = fakeActions([{ state: 'waiting', url: AUTHORIZE_URL }, cancelled]);
  await assert.rejects(loginThroughService(actions, 'acc-1', LOGIN_REQUEST, { onAuthorize: () => undefined, sleep: () => Promise.resolve() }), cancelled);
});

test('loginThroughService: a status that never ends is cancelled at the deadline', async () => {
  const actions = fakeActions([]);
  let clock = 0;
  await assert.rejects(loginThroughService(actions, 'acc-1', LOGIN_REQUEST, {
    onAuthorize: () => undefined,
    deadlineMs: 5000,
    now: () => clock,
    sleep: (ms) => { clock += ms; return Promise.resolve(); },
  }), CoreTimeoutError);
  assert.equal(actions.cancelled, 1);
});

test('secrets never appear in errors, logs or the diagnostic log buffer, even when an answer echoes them', async () => {
  const echo = (req: Request): { status: number; body: string } => ({ status: 502, body: errorDoc('unavailable', `bad: ${req.body}`) });
  const buffer = new LogBuffer(100);
  const { error, warn } = console;
  const printed: string[] = [];
  console.error = (...args: unknown[]) => { printed.push(args.map(String).join(' ')); };
  console.warn = console.error;
  captureConsole(buffer);
  const failures: unknown[] = [];
  try {
    const cases: Record<string, Route>[] = [
      { [`PUT ${TOKEN_ROUTE}`]: echo, [`POST ${LOGIN_ROUTE}`]: echo },
      { [`PUT ${TOKEN_ROUTE}`]: { status: 502, body: errorDoc('signed_out', 'GitHub rejected the token', 'token_rejected') } },
      { [`PUT ${TOKEN_ROUTE}`]: { status: 500, body: 'not json' }, [`POST ${LOGIN_ROUTE}`]: { status: 409, body: LOGIN_IN_PROGRESS } },
      {},
    ];
    for (const routes of cases) {
      await withService(routes, async (_svc, actions) => {
        for (const run of [() => actions.setToken('acc-1', PAT), () => actions.startLogin('acc-1', LOGIN_REQUEST)]) {
          try {
            await run();
          } catch (err) {
            failures.push(err);
            // What main.ts does with a failed action.
            console.error('[main] Copilot action failed:', err);
          }
        }
      });
    }
  } finally {
    console.error = error;
    console.warn = warn;
  }
  assert.equal(failures.length, 8);
  const echoed = failures[0];
  assert.ok(echoed instanceof CoreOutputError && /withheld/.test(echoed.message));
  for (const err of failures) {
    const text = `${String(err)} ${err instanceof Error ? `${err.stack ?? ''} ${String(err.cause)}` : ''}`;
    for (const secret of [PAT, CLIENT_SECRET]) assert.ok(!text.includes(secret), `secret in error: ${text}`);
  }
  for (const entry of [...buffer.list().map((e) => e.message), ...printed]) {
    for (const secret of [PAT, CLIENT_SECRET]) assert.ok(!entry.includes(secret), `secret in log: ${entry}`);
  }
});
