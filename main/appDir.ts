// main/appDir.ts — the folder of the app's main-process entry (dist/main.js), where the
// renderer files, the preload and the worker live. Modules must not build these paths
// from their own __dirname: in the package the main process is one esbuild bundle at
// dist/main.js (#36, scripts/bundle-main.js), so a module's __dirname is dist/ there and
// dist/main/ in the plain tsc output.

import path from 'path';

// require.main is the entry in both layouts. The fallback (dist/ from dist/main/) only
// covers code loaded without one; under `node --test` require.main is the test file, so
// there APP_DIR is that file's folder (the tests never load files through it).
export const APP_DIR = require.main ? path.dirname(require.main.filename) : path.join(__dirname, '..');
