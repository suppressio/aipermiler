// main/userDataBootstrap.ts — runs the userData migration (main/userDataMigration.ts)
// once, at import time. entry.ts imports it first and loads main.ts only when it did
// not fail, so the store (created when store/index.ts is imported) finds the migrated
// file in place. main.ts imports it again (same module, not re-run) to log the warnings
// once the log buffer is capturing.

import fs from 'fs';
import path from 'path';
import { app } from 'electron';
import { migrateUserData } from './userDataMigration';

const appData = app.getPath('appData');
export const userDataMigration = migrateUserData(appData, app.getPath('userData'), path.join(appData, app.getName()), fs);
