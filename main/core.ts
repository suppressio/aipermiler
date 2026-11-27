// main/core.ts — the app's one client of the Go core CLI (main/coreClient.ts, #177) and
// of its always-on service (main/serviceClient.ts, #182). The CLI is run on demand,
// nothing to start or stop; the service is used when it runs, found through its
// api.json on every read. The binary and the folder are resolved on every call, so a
// missing one is an explicit error of that read (shown with the stale data), never a
// startup failure. The Copilot account actions (token, OAuth login) go only through the
// service (#338): without one they fail, and Settings disables them.

import os from 'node:os';
import { app } from 'electron';
import { createCoreClient, resolveCoreBinary } from './coreClient';
import { coreConfigDir, createServiceClient } from './serviceClient';
import { createCopilotServiceActions } from './serviceCopilot';
import { createServiceSettingsClient } from './serviceSettings';
import { createSnapshotClient } from './serviceSnapshot';

export const core = createCoreClient({
  binary: () => resolveCoreBinary({
    env: process.env,
    isPackaged: app.isPackaged,
    resourcesPath: process.resourcesPath,
    appPath: app.getAppPath(),
    platform: process.platform,
  }),
});

const configDir = (): string => coreConfigDir(process.env, process.platform, os.homedir());

export const coreService = createServiceClient({ configDir });

export const copilotService = createCopilotServiceActions({ configDir });

// Settings through the service (#337): the only writer of the core's store.json.
export const settingsService = createServiceSettingsClient({ configDir });

// The pacing snapshot, the refresh request and the history (#341): what the widget, the
// tray and the diagnostic report read now that the service keeps the history and the pace.
export const snapshotService = createSnapshotClient({ configDir });
