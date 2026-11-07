// store/index.ts — electron-store wrapper
// Schema defined in docs/ARCHITECTURE.md §1. Secrets (sessionKey, PAT/token) stay encrypted
// via encryptionKey and never reach the renderer except through IPC in the main
// process.
//
// SECURITY NOTE: the encryptionKey below is a development placeholder. Before any
// distribution it must be replaced with a securely generated key (e.g. from a secret
// manager or a machine-bound value), NEVER committed.
//
// electron-store/conf NOTE: DEFAULTS are applied with a SHALLOW merge
// (Object.assign(defaults, fileStore) in conf/dist/source/index.js) — on an
// installation with an existing file, a new field added inside a nested object that
// already exists in the file (e.g. `history`, an account) is not merged: the whole
// persisted object overrides the default one, new field excluded. Real bug found when
// adding `history.recentSamples` (see CLAUDE.md, instant consumption gauge):
// `store.get('history.recentSamples')` returned `undefined` on existing installations,
// not `[]`. Fixed at the root by normalizeSettings() at the bottom of this file (deep
// merge at startup): adding the new field to DEFAULTS (store/defaults.ts) is enough.

import Store from 'electron-store';
import type { AppSettings } from '../types/index';
import { normalizeSettings } from './normalize';
import { DEFAULTS } from './defaults';

export { DEFAULTS };

const STORE_NAME = 'aipermiler-config';

const store = new Store<AppSettings>({
  name: STORE_NAME,
  encryptionKey: 'dev-only-placeholder-change-before-release',
  defaults: DEFAULTS,
  // Owner-only (#242): conf's default 0o666 is applied by its atomic write with an
  // explicit chmod, so the umask never narrows it. Every write re-applies the mode,
  // so a file written earlier is fixed on the next save.
  configFileMode: 0o600,
});

// Normalization at startup (store/normalize.ts): brings the file on disk back to the
// AppSettings shape — missing fields (electron-store shallow merge), wrong types,
// legacy `{ claude, copilot }` account schema → `AccountConfig[]` (issue #4, one-off
// migration). From here on the AppSettings types tell the truth and no read needs a
// fallback. The file is rewritten only when something really changed.
const normalized = normalizeSettings(store.store, DEFAULTS);
if (JSON.stringify(normalized) !== JSON.stringify(store.store)) {
  store.store = normalized;
}

export default store;
