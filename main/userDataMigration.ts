// main/userDataMigration.ts — moves the settings store from the folder of the old app
// name to the new one (#200).
//
// Electron derives userData from the app name: up to v0.4.18 it was
// `<appData>/ia-hypermiler`, since the AIpermiler rename it is `<appData>/AIpermiler`.
// Only the electron-store file is carried over (accounts, settings, history): the rest
// of the old folder is Chromium data the app does not need (caches, Local Storage, the
// claude.ai partitions removed in #140), possibly locked, so it is not copied.
//
// Data safety, in order:
// - the copy goes to a temporary name and is renamed, so the store appears whole or not
//   at all, and it never overwrites an existing store;
// - a failed copy is FATAL (the caller stops the app before the store is created):
//   otherwise electron-store would create an empty store, the next start would see a
//   store and the old data would never move;
// - the old folder is deleted only once a marker file confirms the copy, or when it
//   holds no store at all; a failed delete is retried at the next start.
//
// Pure apart from the injected fs, so it is tested on temporary folders.

import path from 'path';

/** electron-store `name` of the settings file (store/index.ts). */
export const STORE_NAME = 'aipermiler-config';
/** userData folder name and store file name before the rename. */
export const LEGACY_DIR_NAME = 'ia-hypermiler';
export const LEGACY_STORE_FILE = 'ia-hypermiler-config.json';
/** Written in the new folder once the old store has been copied. */
export const MIGRATED_MARKER = '.migrated-from-ia-hypermiler';

export interface MigrationFs {
  existsSync(p: string): boolean;
  mkdirSync(p: string, options: { recursive: true }): unknown;
  copyFileSync(from: string, to: string): void;
  renameSync(from: string, to: string): void;
  writeFileSync(p: string, data: string): void;
  rmSync(p: string, options: { recursive?: boolean; force: boolean; maxRetries?: number }): void;
}

export interface MigrationResult {
  /** The old store was copied into the new folder on this run. */
  migrated: boolean;
  /** The old folder was deleted on this run. */
  legacyRemoved: boolean;
  /** The old store exists but could not be copied: the app must not start. */
  fatal: string | null;
  /** What went wrong without stopping the app, for the log. */
  warnings: string[];
}

const message = (err: unknown): string => (err instanceof Error ? err.message : String(err));

/**
 * Runs before the store is created. `defaultUserDataDir` is `<appData>/<app name>`: when
 * userData was set anywhere else (tests, measure-resources.js) nothing is read or
 * deleted, so a test run never touches a real installation.
 */
export function migrateUserData(appDataDir: string, userDataDir: string, defaultUserDataDir: string, fs: MigrationFs): MigrationResult {
  const result: MigrationResult = { migrated: false, legacyRemoved: false, fatal: null, warnings: [] };
  if (path.resolve(userDataDir) !== path.resolve(defaultUserDataDir)) return result;
  const legacyDir = path.join(appDataDir, LEGACY_DIR_NAME);
  // Case-insensitive file systems (Windows, macOS): never treat the new folder as the old one.
  if (path.resolve(legacyDir).toLowerCase() === path.resolve(userDataDir).toLowerCase()) return result;
  if (!fs.existsSync(legacyDir)) return result;

  const store = path.join(userDataDir, `${STORE_NAME}.json`);
  const marker = path.join(userDataDir, MIGRATED_MARKER);
  const legacyStore = path.join(legacyDir, LEGACY_STORE_FILE);
  const legacyHasStore = fs.existsSync(legacyStore);

  if (legacyHasStore && !fs.existsSync(store)) {
    const tmp = `${store}.tmp`;
    try {
      fs.mkdirSync(userDataDir, { recursive: true });
      fs.copyFileSync(legacyStore, tmp);
      fs.renameSync(tmp, store);
      result.migrated = true;
    } catch (err) {
      try { fs.rmSync(tmp, { force: true }); } catch { /* nothing more to do */ }
      result.fatal = message(err);
      return result;
    }
    try {
      fs.writeFileSync(marker, `${legacyStore}\n`);
    } catch (err) {
      result.warnings.push(`userData migration: marker not written, old folder kept (${message(err)})`);
      return result;
    }
  }

  if (legacyHasStore && !fs.existsSync(marker)) {
    // A store already here that did not come from this migration: keep the old data.
    result.warnings.push('userData migration: a store exists in both folders, the old one is kept untouched');
    return result;
  }
  try {
    fs.rmSync(legacyDir, { recursive: true, force: true, maxRetries: 2 });
    result.legacyRemoved = true;
  } catch (err) {
    result.warnings.push(`userData migration: old folder not removed, retried at next start (${message(err)})`);
  }
  return result;
}
