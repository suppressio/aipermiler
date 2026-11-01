// main/core.ts — the app's one client of the Go core CLI (main/coreClient.ts, #177).
// Run on demand, nothing to start or stop. The binary is resolved on every call, so a
// missing one is an explicit error of that read (shown with the stale data), never a
// startup failure.

import { app } from 'electron';
import { createCoreClient, resolveCoreBinary } from './coreClient';

export const core = createCoreClient({
  binary: () => resolveCoreBinary({
    env: process.env,
    isPackaged: app.isPackaged,
    resourcesPath: process.resourcesPath,
    appPath: app.getAppPath(),
    platform: process.platform,
  }),
});
