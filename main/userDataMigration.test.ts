// main/userDataMigration.test.ts — store moved from the old app folder (#200), on real
// temporary folders; failures injected by wrapping fs.

import { after, test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'fs';
import os from 'os';
import path from 'path';
import { LEGACY_DIR_NAME, LEGACY_STORE_FILE, STORE_NAME, migrateUserData } from './userDataMigration';
import type { MigrationFs } from './userDataMigration';

const OLD_STORE = '{"accounts":[{"id":"synthetic"}]}';

const tempDirs: string[] = [];
const tempDir = (prefix: string): string => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), prefix));
  tempDirs.push(dir);
  return dir;
};
after(() => { for (const dir of tempDirs) fs.rmSync(dir, { recursive: true, force: true }); });

function setup(withLegacyStore = true): { appData: string; userData: string; legacy: string; store: string } {
  const appData = tempDir('userdata-migration-');
  const legacy = path.join(appData, LEGACY_DIR_NAME);
  fs.mkdirSync(path.join(legacy, 'Partitions'), { recursive: true });
  if (withLegacyStore) fs.writeFileSync(path.join(legacy, LEGACY_STORE_FILE), OLD_STORE);
  const userData = path.join(appData, 'AIpermiler');
  return { appData, userData, legacy, store: path.join(userData, `${STORE_NAME}.json`) };
}

test('migrateUserData: first start copies the old store and removes the old folder', () => {
  const d = setup();
  const r = migrateUserData(d.appData, d.userData, fs);
  assert.deepEqual(r, { migrated: true, legacyRemoved: true, warnings: [] });
  assert.equal(fs.readFileSync(d.store, 'utf8'), OLD_STORE);
  assert.equal(fs.existsSync(d.legacy), false);
  assert.equal(fs.existsSync(`${d.store}.tmp`), false);
});

test('migrateUserData: an existing new store is never overwritten', () => {
  const d = setup();
  fs.mkdirSync(d.userData);
  fs.writeFileSync(d.store, '{"new":true}');
  const r = migrateUserData(d.appData, d.userData, fs);
  assert.equal(r.migrated, false);
  assert.equal(r.legacyRemoved, true);
  assert.equal(fs.readFileSync(d.store, 'utf8'), '{"new":true}');
});

test('migrateUserData: no old folder, nothing happens', () => {
  const d = setup();
  fs.rmSync(d.legacy, { recursive: true });
  assert.deepEqual(migrateUserData(d.appData, d.userData, fs), { migrated: false, legacyRemoved: false, warnings: [] });
  assert.equal(fs.existsSync(d.userData), false);
});

test('migrateUserData: an old folder without a store is just removed', () => {
  const d = setup(false);
  const r = migrateUserData(d.appData, d.userData, fs);
  assert.deepEqual(r, { migrated: false, legacyRemoved: true, warnings: [] });
  assert.equal(fs.existsSync(d.store), false);
});

test('migrateUserData: a userData outside appData (tests, measurements) never touches the old folder', () => {
  const d = setup();
  const elsewhere = tempDir('userdata-elsewhere-');
  assert.deepEqual(migrateUserData(d.appData, elsewhere, fs), { migrated: false, legacyRemoved: false, warnings: [] });
  assert.equal(fs.existsSync(path.join(d.legacy, LEGACY_STORE_FILE)), true);
});

test('migrateUserData: a failed copy leaves no store, keeps the old folder and succeeds on retry', () => {
  const d = setup();
  const failingRename: MigrationFs = { ...fs, renameSync: () => { throw new Error('disk full'); } };
  const r = migrateUserData(d.appData, d.userData, failingRename);
  assert.equal(r.migrated, false);
  assert.equal(r.legacyRemoved, false);
  assert.match(r.warnings.join(), /copy of the old store failed.*disk full/);
  assert.equal(fs.existsSync(d.store), false);
  assert.equal(fs.existsSync(`${d.store}.tmp`), false);
  assert.equal(fs.existsSync(path.join(d.legacy, LEGACY_STORE_FILE)), true);

  assert.deepEqual(migrateUserData(d.appData, d.userData, fs), { migrated: true, legacyRemoved: true, warnings: [] });
  assert.equal(fs.readFileSync(d.store, 'utf8'), OLD_STORE);
});

test('migrateUserData: a locked old folder is removed at the next start', () => {
  const d = setup();
  const lockedRm: MigrationFs = {
    ...fs,
    rmSync: (p, o) => { if (p === d.legacy) throw new Error('EBUSY'); fs.rmSync(p, o); },
  };
  const r = migrateUserData(d.appData, d.userData, lockedRm);
  assert.equal(r.migrated, true);
  assert.equal(r.legacyRemoved, false);
  assert.match(r.warnings.join(), /old folder not removed.*EBUSY/);
  assert.equal(fs.existsSync(d.legacy), true);

  const retry = migrateUserData(d.appData, d.userData, fs);
  assert.deepEqual(retry, { migrated: false, legacyRemoved: true, warnings: [] });
  assert.equal(fs.readFileSync(d.store, 'utf8'), OLD_STORE);
});
