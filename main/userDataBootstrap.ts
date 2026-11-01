// main/userDataBootstrap.ts — runs the userData migration (main/userDataMigration.ts)
// at import time. It must be the FIRST import of main.ts: store/index.ts creates the
// store when it is imported, and it must find the migrated file already in place.
// A failed copy stops the app here, before the store exists (an empty store would
// otherwise hide the old one for good). Warnings are kept for main.ts to log once the
// log buffer is capturing.

import fs from 'fs';
import path from 'path';
import { app, dialog } from 'electron';
import { migrateUserData } from './userDataMigration';
import { resolveLocale, setLocale, t } from './i18n/index';

const appData = app.getPath('appData');
export const userDataMigration = migrateUserData(appData, app.getPath('userData'), path.join(appData, app.getName()), fs);

if (userDataMigration.fatal !== null) {
  setLocale(resolveLocale('auto', app.getPreferredSystemLanguages()[0] ?? ''));
  console.error('[main] userData migration failed:', userDataMigration.fatal);
  dialog.showErrorBox(t('startup.failedTitle'), t('startup.migrationFailed', { detail: userDataMigration.fatal }));
  // Synchronous on purpose: nothing after this import (the store above all) may run.
  process.exit(1);
}
