// main/core.ts — the app's one client of the Go core CLI (main/coreClient.ts, #177) and
// of its always-on service (main/serviceClient.ts, #182). The CLI is run on demand,
// nothing to start or stop; the service is used when it runs, found through its
// api.json on every read. The binary and the folder are resolved on every call, so a
// missing one is an explicit error of that read (shown with the stale data), never a
// startup failure.

import os from 'node:os';
import { app } from 'electron';
import { createCoreClient, resolveCoreBinary } from './coreClient';
import { coreConfigDir, createServiceClient } from './serviceClient';

export const core = createCoreClient({
  binary: () => resolveCoreBinary({
    env: process.env,
    isPackaged: app.isPackaged,
    resourcesPath: process.resourcesPath,
    appPath: app.getAppPath(),
    platform: process.platform,
  }),
});

export const coreService = createServiceClient({
  configDir: () => coreConfigDir(process.env, process.platform, os.homedir()),
});
