// main/userDataMigration.test.ts — store moved from the old app folder (#200), on real
// temporary folders; failures injected by wrapping fs.

import { after, test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'fs';
import os from 'os';
import path from 'path';
import { LEGACY_DIR_NAME, LEGACY_STORE_FILE, MIGRATED_MARKER, STORE_NAME, migrateUserData } from './userDataMigration';
import type { MigrationFs, MigrationResult } from './userDataMigration';

const OLD_STORE = '{"accounts":[{"id":"synthetic"}]}';
const NOTHING: MigrationResult = { migrated: false, legacyRemoved: false, fatal: null, warnings: [] };

const tempDirs: string[] = [];
after(() => { for (const dir of tempDirs) fs.rmSync(dir, { recursive: true, force: true }); });

interface Dirs { appData: string; userData: string; legacy: string; store: string }

function setup(withLegacyStore = true): Dirs {
  const appData = fs.mkdtempSync(path.join(os.tmpdir(), 'userdata-migration-'));
  tempDirs.push(appData);
  const legacy = path.join(appData, LEGACY_DIR_NAME);
  fs.mkdirSync(path.join(legacy, 'Partitions'), { recursive: true });
  if (withLegacyStore) fs.writeFileSync(path.join(legacy, LEGACY_STORE_FILE), OLD_STORE);
  const userData = path.join(appData, 'AIpermiler');
  return { appData, userData, legacy, store: path.join(userData, `${STORE_NAME}.json`) };
}

const run = (d: Dirs, f: MigrationFs = fs): MigrationResult => migrateUserData(d.appData, d.userData, d.userData, f);

test('migrateUserData: first start copies the old store, marks it and removes the old folder', () => {
  const d = setup();
  assert.deepEqual(run(d), { migrated: true, legacyRemoved: true, fatal: null, warnings: [] });
  assert.equal(fs.readFileSync(d.store, 'utf8'), OLD_STORE);
  assert.equal(fs.existsSync(path.join(d.userData, MIGRATED_MARKER)), true);
  assert.equal(fs.existsSync(d.legacy), false);
  assert.equal(fs.existsSync(`${d.store}.tmp`), false);
});

test('migrateUserData: a store not coming from the migration is never overwritten, and the old data is kept', () => {
  const d = setup();
  fs.mkdirSync(d.userData);
  fs.writeFileSync(d.store, '{"new":true}');
  const r = run(d);
  assert.equal(r.migrated, false);
  assert.equal(r.legacyRemoved, false);
  assert.match(r.warnings.join(), /store exists in both folders/);
  assert.equal(fs.readFileSync(d.store, 'utf8'), '{"new":true}');
  assert.equal(fs.readFileSync(path.join(d.legacy, LEGACY_STORE_FILE), 'utf8'), OLD_STORE);
});

test('migrateUserData: no old folder, nothing happens', () => {
  const d = setup();
  fs.rmSync(d.legacy, { recursive: true });
  assert.deepEqual(run(d), NOTHING);
  assert.equal(fs.existsSync(d.userData), false);
});

test('migrateUserData: an old folder without a store is just removed', () => {
  const d = setup(false);
  assert.deepEqual(run(d), { migrated: false, legacyRemoved: true, fatal: null, warnings: [] });
  assert.equal(fs.existsSync(d.store), false);
});

test('migrateUserData: a userData other than the default one never touches the old folder', () => {
  const d = setup();
  // Outside appData, and next to the default folder inside appData.
  for (const custom of [fs.mkdtempSync(path.join(os.tmpdir(), 'userdata-elsewhere-')), path.join(d.appData, 'AIpermiler-test')]) {
    tempDirs.push(custom);
    assert.deepEqual(migrateUserData(d.appData, custom, d.userData, fs), NOTHING);
    assert.equal(fs.existsSync(path.join(custom, `${STORE_NAME}.json`)), false);
  }
  assert.equal(fs.readFileSync(path.join(d.legacy, LEGACY_STORE_FILE), 'utf8'), OLD_STORE);
});

test('migrateUserData: a failed copy is fatal, leaves no store and keeps the old folder for the retry', () => {
  const d = setup();
  const failingRename: MigrationFs = { ...fs, renameSync: () => { throw new Error('disk full'); } };
  const r = run(d, failingRename);
  assert.equal(r.fatal, 'disk full');
  assert.equal(r.migrated, false);
  assert.equal(r.legacyRemoved, false);
  assert.equal(fs.existsSync(d.store), false);
  assert.equal(fs.existsSync(`${d.store}.tmp`), false);
  assert.equal(fs.existsSync(path.join(d.legacy, LEGACY_STORE_FILE)), true);

  assert.deepEqual(run(d), { migrated: true, legacyRemoved: true, fatal: null, warnings: [] });
  assert.equal(fs.readFileSync(d.store, 'utf8'), OLD_STORE);
});

test('migrateUserData: a locked old folder is removed at the next start', () => {
  const d = setup();
  const lockedRm: MigrationFs = {
    ...fs,
    rmSync: (p, o) => { if (p === d.legacy) throw new Error('EBUSY'); fs.rmSync(p, o); },
  };
  const r = run(d, lockedRm);
  assert.equal(r.migrated, true);
  assert.equal(r.legacyRemoved, false);
  assert.match(r.warnings.join(), /old folder not removed.*EBUSY/);
  assert.equal(fs.existsSync(d.legacy), true);

  assert.deepEqual(run(d), { migrated: false, legacyRemoved: true, fatal: null, warnings: [] });
  assert.equal(fs.readFileSync(d.store, 'utf8'), OLD_STORE);
});

test('migrateUserData: without the marker the old folder is kept (store copied, data safe)', () => {
  const d = setup();
  const noMarker: MigrationFs = { ...fs, writeFileSync: () => { throw new Error('read-only'); } };
  const r = run(d, noMarker);
  assert.equal(r.migrated, true);
  assert.equal(r.legacyRemoved, false);
  assert.match(r.warnings.join(), /marker not written/);
  assert.equal(fs.readFileSync(d.store, 'utf8'), OLD_STORE);
  assert.equal(fs.existsSync(path.join(d.legacy, LEGACY_STORE_FILE)), true);
});
