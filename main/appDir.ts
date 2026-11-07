// main/appDir.ts — the folder of the app's main-process entry (dist/main.js), where the
// renderer files, the preload and the worker live. Modules must not build these paths
// from their own __dirname: in the package the main process is one esbuild bundle at
// dist/main.js (#36, scripts/bundle-main.js), so a module's __dirname is dist/ there and
// dist/main/ in the plain tsc output.
//
// Derived from this module's own __dirname, never from require.main: under Electron
// require.main.filename is "electron" (its internal init), which made APP_DIR "." and
// broke the preload and the windows.

import path from 'path';

/** The entry's folder seen from the folder this module was loaded from. */
export function appDirFrom(moduleDir: string): string {
  return path.basename(moduleDir) === 'main' ? path.dirname(moduleDir) : moduleDir;
}

export const APP_DIR = appDirFrom(__dirname);
