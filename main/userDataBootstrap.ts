// main/userDataBootstrap.ts — runs the userData migration (main/userDataMigration.ts)
// at import time. It must be the FIRST import of main.ts: store/index.ts creates the
// store when it is imported, and it must find the migrated file already in place.
// The result is kept for main.ts to log once the log buffer is capturing.

import fs from 'fs';
import { app } from 'electron';
import { migrateUserData } from './userDataMigration';

export const userDataMigration = migrateUserData(app.getPath('appData'), app.getPath('userData'), fs);
