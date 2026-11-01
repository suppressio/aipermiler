// main/userDataMigration.ts — moves the settings store from the folder of the old app
// name to the new one (#200).
//
// Electron derives userData from the app name: up to v0.4.18 it was
// `<appData>/ia-hypermiler`, since the AIpermiler rename it is `<appData>/AIpermiler`.
// Only the electron-store file is carried over (accounts, settings, history): the rest
// of the old folder is Chromium data the app does not need (caches, Local Storage, the
// claude.ai partitions removed in #140), possibly locked, so it is not copied. Once the
// new store exists the old folder is deleted, best-effort; a failure is retried at the
// next start. Nothing here is fatal: on any error the app starts with what it has and
// the old folder stays for the next try.
//
// Pure apart from the injected fs, so it is tested on temporary folders.

import path from 'path';

/** electron-store `name` of the settings file (store/index.ts). */
export const STORE_NAME = 'aipermiler-config';
/** userData folder name and store file name before the rename. */
export const LEGACY_DIR_NAME = 'ia-hypermiler';
export const LEGACY_STORE_FILE = 'ia-hypermiler-config.json';

export interface MigrationFs {
  existsSync(p: string): boolean;
  mkdirSync(p: string, options: { recursive: true }): unknown;
  copyFileSync(from: string, to: string): void;
  renameSync(from: string, to: string): void;
  rmSync(p: string, options: { recursive?: boolean; force: boolean; maxRetries?: number }): void;
}

export interface MigrationResult {
  /** The old store was copied into the new folder on this run. */
  migrated: boolean;
  /** The old folder was deleted on this run. */
  legacyRemoved: boolean;
  /** What went wrong, for the log; empty when all went well. */
  warnings: string[];
}

const message = (err: unknown): string => (err instanceof Error ? err.message : String(err));

/**
 * Runs before the store is created. Does nothing unless userData is the default
 * `<appData>/<name>` folder: a userData set elsewhere (tests, measure-resources.js) must
 * never read or delete the real old folder.
 */
export function migrateUserData(appDataDir: string, userDataDir: string, fs: MigrationFs): MigrationResult {
  const result: MigrationResult = { migrated: false, legacyRemoved: false, warnings: [] };
  const legacyDir = path.join(appDataDir, LEGACY_DIR_NAME);
  if (path.resolve(path.dirname(userDataDir)) !== path.resolve(appDataDir)) return result;
  // Case-insensitive file systems (Windows, macOS): never treat the new folder as the old one.
  if (path.resolve(legacyDir).toLowerCase() === path.resolve(userDataDir).toLowerCase()) return result;
  if (!fs.existsSync(legacyDir)) return result;

  const store = path.join(userDataDir, `${STORE_NAME}.json`);
  const legacyStore = path.join(legacyDir, LEGACY_STORE_FILE);
  if (!fs.existsSync(store) && fs.existsSync(legacyStore)) {
    // Copy to a temporary name, then rename: the store file appears complete or not at all.
    const tmp = `${store}.tmp`;
    try {
      fs.mkdirSync(userDataDir, { recursive: true });
      fs.copyFileSync(legacyStore, tmp);
      fs.renameSync(tmp, store);
      result.migrated = true;
    } catch (err) {
      result.warnings.push(`userData migration: copy of the old store failed, retried at next start (${message(err)})`);
      try { fs.rmSync(tmp, { force: true }); } catch { /* nothing more to do */ }
      return result;
    }
  }

  // The new store exists, or the old folder never had one: nothing left to take from it.
  try {
    fs.rmSync(legacyDir, { recursive: true, force: true, maxRetries: 2 });
    result.legacyRemoved = true;
  } catch (err) {
    result.warnings.push(`userData migration: old folder not removed, retried at next start (${message(err)})`);
  }
  return result;
}
