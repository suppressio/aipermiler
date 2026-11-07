// scripts/bundle-main.js — electron-builder `beforePack` hook (#36): replaces the three
// main-process entry points tsc wrote in dist/ (the app, the preload, the local
// insights worker) with esbuild bundles, so the package carries a few files instead of
// node_modules (date-fns, electron-store and their dependencies were ~30 MB in the
// asar). Runs before every package, from `npm run package`, `npm run package:test` and
// CI alike; `npm test` and `npm start` keep using the plain tsc output.
//
// Kept external:
// - electron: provided by the runtime.
// - @anthropic-ai/claude-agent-sdk: ESM only, loaded through a native dynamic import()
//   hidden from bundlers (services/claudeLocalSessions.ts); it stays a real package in
//   node_modules (dependencies in package.json).
// Each bundle is written where tsc put its entry, so the paths the app builds from
// `__dirname` of the entry (dist/) stay the same (main/appDir.ts).

const path = require('path');
const esbuild = require('esbuild');

const ROOT = path.join(__dirname, '..');
const DIST = path.join(ROOT, 'dist');
const ENTRIES = ['main.js', 'preload.js', path.join('services', 'claudeLocalSessions.worker.js')];
// Electron 45 ships Node 24.
const TARGET = 'node24';

async function bundle() {
  for (const entry of ENTRIES) {
    const file = path.join(DIST, entry);
    await esbuild.build({
      entryPoints: [file],
      outfile: file,
      allowOverwrite: true,
      bundle: true,
      platform: 'node',
      format: 'cjs',
      target: TARGET,
      external: ['electron', '@anthropic-ai/claude-agent-sdk'],
      sourcemap: false,
      legalComments: 'none',
      logLevel: 'warning',
    });
  }
  console.log(`[bundle-main] bundled ${ENTRIES.join(', ')} in ${path.relative(ROOT, DIST)}`);
}

// electron-builder calls the exported function once per platform/arch pack (deb and
// AppImage share one); bundling twice is harmless: the second run re-bundles an already
// self-contained file, valid though not byte-identical.
module.exports = async function beforePack() {
  await bundle();
};

if (require.main === module) {
  bundle().catch((err) => {
    console.error(err);
    process.exit(1);
  });
}
