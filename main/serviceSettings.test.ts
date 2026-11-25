// main/serviceSettings.test.ts — Settings through the service (#337): the mapping app
// settings ↔ service document against the service's fixtures
// (gocore/contract/v1/service/settings.json, error-invalid-settings.json), the field of a
// validation error, and the client against a loopback fake of the service.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import type { AddressInfo } from 'node:net';
import type { AccountConfig, CopilotAccountSettings } from '../types/index';
import { defaultAccountFor } from '../store/migrate';
import { ServiceUnusableError } from './serviceClient';
import {
  ServiceSettingsInvalidError,
  accountsFromService,
  createServiceSettingsClient,
  fieldOfInvalidSettings,
  fromServiceAccount,
  invalidSettingsPath,
  keepNoRenewalDay,
  mirrorAccounts,
  parseSettingsAnswer,
  parseSettingsDocument,
  toServiceAccount,
  toServiceDocument,
  withConnectionFields,
} from './serviceSettings';
import { at } from '../tests/support/at';

// Compiled test: dist/main/ → project root two levels up.
const FIXTURES = path.join(__dirname, '..', '..', 'gocore', 'contract', 'v1', 'service');
const fixture = (name: string): string => fs.readFileSync(path.join(FIXTURES, `${name}.json`), 'utf8');
const SETTINGS = fixture('settings');
const INVALID = fixture('error-invalid-settings');

function copilot(account: AccountConfig | undefined): CopilotAccountSettings {
  assert.ok(account?.provider === 'copilot', 'expected a Copilot account');
  return account;
}

test('the service fixture maps to app accounts and back to the same document', () => {
  const doc = parseSettingsDocument(SETTINGS);
  const accounts = accountsFromService(doc, []);
  const back = toServiceDocument({
    accounts,
    refreshIntervalMinutes: doc.settings.refreshIntervalMinutes,
    notificationThresholdPercent: doc.settings.notificationThresholdPercent,
    retentionDays: doc.settings.retentionDays,
  });
  assert.deepEqual(back, JSON.parse(SETTINGS));
});

test('service fields become the app fields: days Sunday first, renewal day 0 as none, Copilot and Claude fields', () => {
  const [claude, company, personal, codex] = accountsFromService(parseSettingsDocument(SETTINGS), []);
  assert.ok(claude?.provider === 'claude');
  assert.deepEqual(claude.workSchedule, {
    enabled: true,
    days: { sun: 'off', mon: 'full', tue: 'full', wed: 'full', thu: 'full', fri: 'half', sat: 'off' },
    hoursPerDay: 8,
  });
  assert.deepEqual(claude.subscription.renewalRule, { type: 'dayOfMonth' });
  assert.equal(claude.binaryPath, '/opt/claude/bin/claude');
  assert.equal(claude.localInsights, true);
  const org = copilot(company);
  assert.equal(org.enabled, false);
  assert.equal(org.host, 'acme.ghe.com');
  assert.equal(org.accountScope, 'organization');
  assert.equal(org.authMethod, 'oauth');
  assert.equal(org.manualQuota, 0);
  assert.deepEqual(org.subscription.renewalRule, { type: 'dayOfMonth', day: 15 });
  const own = copilot(personal);
  assert.equal(own.host, 'github.com');
  assert.equal(own.manualQuota, 300);
  assert.equal(own.label, '');
  assert.ok(codex?.provider === 'codex');
  assert.equal(codex.binaryPath, null);
});

test('fields only Electron keeps come from the local account with the same id', () => {
  const doc = parseSettingsDocument(SETTINGS);
  const local = copilot(defaultAccountFor('copilot', 'copilot-1a2b3c4d'));
  local.credentials = { token: null, username: 'octocat' };
  local.oauthApp = { clientId: 'Iv1.synthetic' };
  local.label = 'Old label';
  const mapped = copilot(accountsFromService(doc, [local])[1]);
  assert.deepEqual(mapped.credentials, { token: null, username: 'octocat' });
  assert.deepEqual(mapped.oauthApp, { clientId: 'Iv1.synthetic' });
  // The service wins on its own fields.
  assert.equal(mapped.label, 'Company');
  assert.equal(mapped.host, 'acme.ghe.com');
});

test('a local account of another provider under the same id is not merged', () => {
  const doc = parseSettingsDocument(SETTINGS);
  const account = doc.accounts[0];
  assert.ok(account);
  const mapped = fromServiceAccount(account, defaultAccountFor('codex', 'claude'));
  assert.equal(mapped.provider, 'claude');
});

test('unset values are omitted as the service writes them', () => {
  const pat = copilot(defaultAccountFor('copilot', 'c1'));
  const doc = toServiceAccount({ ...pat, manualQuota: 0 });
  assert.equal('host' in doc, false, 'github.com is the core default');
  assert.equal('manualQuota' in doc, false);
  assert.equal(doc.scope, 'personal');
  assert.equal(doc.authMethod, 'pat');
  const claude = toServiceAccount(defaultAccountFor('claude', 'k'));
  assert.equal('binaryPath' in claude, false);
  assert.equal('localInsights' in claude, false);
  assert.equal(claude.renewalDay, 1);
  assert.deepEqual(claude.schedule.days, [0, 1, 1, 1, 1, 1, 0]);
});

test('mirror: service accounts updated or added, an account only Electron has is kept', () => {
  const doc = parseSettingsDocument(SETTINGS);
  const onlyLocal = defaultAccountFor('copilot', 'legacy');
  const stale = { ...defaultAccountFor('codex', 'codex'), label: 'Stale' };
  const fromService = accountsFromService(doc, [onlyLocal, stale]);
  const mirrored = mirrorAccounts([onlyLocal, stale], fromService);
  assert.deepEqual(mirrored.map((a) => a.id), ['legacy', 'codex', 'claude', 'copilot-1a2b3c4d', 'copilot']);
  assert.equal(mirrored.find((a) => a.id === 'codex')?.label, 'Codex');
  // The store's normal form: a renewal day of none falls back to the store default.
  assert.deepEqual(mirrored.find((a) => a.id === 'claude')?.subscription.renewalRule, { type: 'dayOfMonth', day: 1 });
});

test('invalid settings: the fixture names the field of the account sent at that index', () => {
  assert.throws(() => parseSettingsAnswer(400, INVALID), (err: unknown) => {
    assert.ok(err instanceof ServiceSettingsInvalidError);
    assert.equal(err.path, 'accounts[1].renewalDay');
    assert.equal(err.message, 'accounts[1].renewalDay: must be 0 (none) or 1-31');
    assert.deepEqual(fieldOfInvalidSettings(err.path, [{ id: 'a' }, { id: 'b' }]), { accountId: 'b', path: 'subscription.renewalRule.day' });
    return true;
  });
});

test('invalid settings: global fields, schedule fields, and paths with no input', () => {
  const sent = [{ id: 'a' }];
  assert.deepEqual(fieldOfInvalidSettings('settings.refreshIntervalMinutes', sent), { accountId: null, path: 'ui.refreshIntervalMinutes' });
  assert.deepEqual(fieldOfInvalidSettings('settings.notificationThresholdPercent', sent), { accountId: null, path: 'ui.notificationThresholdPercent' });
  assert.deepEqual(fieldOfInvalidSettings('accounts[0].schedule.hoursPerDay', sent), { accountId: 'a', path: 'workSchedule.hoursPerDay' });
  assert.deepEqual(fieldOfInvalidSettings('accounts[0].scope', sent), { accountId: 'a', path: 'accountScope' });
  // No such account sent, no input for the field, no index (Go 1.26 path of a wrong
  // type), no path at all: a general message.
  assert.equal(fieldOfInvalidSettings('accounts[3].label', sent), null);
  assert.equal(fieldOfInvalidSettings('accounts[0].id', sent), null);
  assert.equal(fieldOfInvalidSettings('accounts.label', sent), null);
  assert.equal(fieldOfInvalidSettings('settings.retentionDays', sent), null);
  assert.equal(fieldOfInvalidSettings(null, sent), null);
});

test('invalidSettingsPath reads only a leading JSON path', () => {
  assert.equal(invalidSettingsPath('accounts[12].schedule.hoursPerDay: must be over 0'), 'accounts[12].schedule.hoursPerDay');
  assert.equal(invalidSettingsPath('settings: missing'), 'settings');
  assert.equal(invalidSettingsPath('request body is not JSON'), null);
});

test('other answers: no usable service, or a failure to show', () => {
  const reasonOf = (status: number, body: string): string => {
    try {
      parseSettingsAnswer(status, body);
    } catch (err) {
      assert.ok(err instanceof ServiceUnusableError);
      return err.reason;
    }
    return 'none';
  };
  assert.equal(reasonOf(401, ''), 'unauthorized');
  assert.equal(reasonOf(503, '{"schemaVersion":1,"error":{"code":"unavailable","message":"x"}}'), 'failed');
  assert.equal(reasonOf(400, '{"schemaVersion":1,"error":{"code":"usage","message":"x"}}'), 'rejected');
  assert.equal(reasonOf(200, '{"schemaVersion":2}'), 'malformed');
  assert.equal(reasonOf(200, '{"schemaVersion":1,"settings":{"refreshIntervalMinutes":5,"retentionDays":1,"notificationThresholdPercent":1},"accounts":[{"id":"x"}]}'), 'malformed');
});

// --- client against a loopback fake ---------------------------------------------------

const TOKEN = 'synthetic-service-token';

interface Seen { method: string; url: string; contentType: string | undefined; body: string }

async function withFake(answer: (seen: Seen) => { status: number; body: string }, run: (dir: string, seen: Seen[]) => Promise<void>): Promise<void> {
  const seen: Seen[] = [];
  const server = http.createServer((req, res) => {
    let body = '';
    req.on('data', (chunk: Buffer) => { body += chunk.toString('utf8'); });
    req.on('end', () => {
      if (req.headers.authorization !== `Bearer ${TOKEN}`) {
        res.writeHead(401).end();
        return;
      }
      if (req.url === '/api/v1/health') {
        res.writeHead(200).end(fixture('health'));
        return;
      }
      const s = { method: req.method ?? '', url: req.url ?? '', contentType: req.headers['content-type'], body };
      seen.push(s);
      const a = answer(s);
      res.writeHead(a.status, { 'Content-Type': 'application/json' }).end(a.body);
    });
  });
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const { port } = server.address() as AddressInfo;
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'aipermiler-settings-'));
  fs.writeFileSync(path.join(dir, 'api.json'), JSON.stringify({ schemaVersion: 1, port, token: TOKEN, version: '1.2.3', contractVersion: 1, pid: 1, startedAt: '2026-11-25T08:00:00Z' }));
  try {
    await run(dir, seen);
  } finally {
    await new Promise<void>((resolve) => { server.close(() => { resolve(); }); server.closeAllConnections(); });
    fs.rmSync(dir, { recursive: true, force: true });
  }
}

test('client: GET reads the document, PUT sends it as JSON and reads the answer', async () => {
  await withFake((s) => ({ status: 200, body: s.method === 'PUT' ? s.body : SETTINGS }), async (dir, seen) => {
    const client = createServiceSettingsClient({ configDir: () => dir });
    const doc = await client.get();
    assert.equal(doc.accounts.length, 4);
    const saved = await client.put({ ...doc, settings: { ...doc.settings, refreshIntervalMinutes: 20 } });
    assert.equal(saved.settings.refreshIntervalMinutes, 20);
    assert.deepEqual(seen.map((s) => `${s.method} ${s.url}`), ['GET /api/v1/settings', 'PUT /api/v1/settings']);
    assert.equal(seen[1]?.contentType, 'application/json');
  });
});

test('client: a refused PUT is a ServiceSettingsInvalidError, nothing else is tried', async () => {
  await withFake(() => ({ status: 400, body: INVALID }), async (dir, seen) => {
    const client = createServiceSettingsClient({ configDir: () => dir });
    await assert.rejects(client.put(parseSettingsDocument(SETTINGS)), ServiceSettingsInvalidError);
    assert.equal(seen.length, 1);
  });
});

test('client: no api.json is a service not running', async () => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'aipermiler-settings-'));
  try {
    const client = createServiceSettingsClient({ configDir: () => dir });
    await assert.rejects(client.get(), (err: unknown) => err instanceof ServiceUnusableError && err.reason === 'not_running');
  } finally {
    fs.rmSync(dir, { recursive: true, force: true });
  }
});

test('keepNoRenewalDay: an empty renewal day in the form is saved as none, not as the store default', () => {
  const raw = [
    { id: 'a', provider: 'claude', subscription: { renewalRule: { type: 'dayOfMonth', day: null } } },
    { id: 'b', provider: 'claude', subscription: { renewalRule: { type: 'dayOfMonth', day: 12 } } },
  ];
  const twelve = defaultAccountFor('claude', 'b');
  twelve.subscription = { renewalRule: { type: 'dayOfMonth', day: 12 } };
  const kept = keepNoRenewalDay(raw, [defaultAccountFor('claude', 'a'), twelve]);
  assert.equal(toServiceAccount(at(kept, 0)).renewalDay, 0);
  assert.equal(toServiceAccount(at(kept, 1)).renewalDay, 12);
});

test('withConnectionFields: enabled, Copilot authentication and host from Electron\'s copy', () => {
  const doc = parseSettingsDocument(SETTINGS);
  const company = at(doc.accounts, 1);
  const cfg = { ...copilot(defaultAccountFor('copilot', company.id)), enabled: true, authMethod: 'pat' as const, host: 'github.com' };
  const out = withConnectionFields(company, cfg);
  assert.equal(out.enabled, true);
  assert.equal(out.authMethod, 'pat');
  assert.equal('host' in out, false);
  assert.equal(out.label, 'Company', 'other fields stay the service\'s');
  const codex = at(doc.accounts, 3);
  assert.deepEqual(withConnectionFields(codex, { ...defaultAccountFor('codex', 'codex'), enabled: false }), { ...codex, enabled: false });
});
