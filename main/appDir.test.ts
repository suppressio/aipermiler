// main/appDir.test.ts — the entry folder in the tsc output and in the esbuild bundle (#36).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'fs';
import path from 'path';
import { APP_DIR, appDirFrom } from './appDir';

test('tsc output: dist/main/ maps to dist/', () => {
  assert.equal(appDirFrom(path.join('/opt', 'app', 'dist', 'main')), path.join('/opt', 'app', 'dist'));
});

test('bundle: dist/ stays dist/', () => {
  assert.equal(appDirFrom(path.join('/opt', 'app', 'dist')), path.join('/opt', 'app', 'dist'));
});

test('APP_DIR is absolute and holds the entry, the preload and the renderer', () => {
  assert.ok(path.isAbsolute(APP_DIR));
  for (const file of ['main.js', 'preload.js', path.join('renderer', 'index.html')]) {
    assert.ok(fs.existsSync(path.join(APP_DIR, file)), `${file} in ${APP_DIR}`);
  }
});
