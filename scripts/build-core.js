#!/usr/bin/env node
// scripts/build-core.js — builds the Go core CLI (gocore/cmd/aipermiler) into core-bin/,
// where electron-builder `extraResources` picks it up (<resources>/core/ in the package)
// and the app looks for it in development (main/coreClient.ts resolveCoreBinary).
//
// Usage:
//   node scripts/build-core.js                  core for this machine
//   node scripts/build-core.js --target win32-x64   cross-compile (Node platform-arch names)
//   node scripts/build-core.js --dev            as above, but skipped when AIPERMILER_CORE_BIN
//                                               points to a core built elsewhere (npm start)
// The version stamped in the binary is package.json's, or AIPERMILER_CORE_VERSION (CI test
// builds use "<version>.dev.<run>", like the app). Needs Go (version in gocore/go.mod).

const { spawnSync } = require('child_process');
const fs = require('fs');
const path = require('path');

const root = path.join(__dirname, '..');
const outDir = path.join(root, 'core-bin');
const args = process.argv.slice(2);

if (args.includes('--dev') && process.env.AIPERMILER_CORE_BIN) {
  console.log(`build-core: using AIPERMILER_CORE_BIN=${process.env.AIPERMILER_CORE_BIN}, nothing to build`);
  process.exit(0);
}

const GOOS = { linux: 'linux', darwin: 'darwin', win32: 'windows' };
const GOARCH = { x64: 'amd64', arm64: 'arm64' };

const targetIndex = args.indexOf('--target');
const target = targetIndex >= 0 ? args[targetIndex + 1] : `${process.platform}-${process.arch}`;
const [platform, arch] = (target ?? '').split('-');
const goos = GOOS[platform];
const goarch = GOARCH[arch];
if (!goos || !goarch) {
  console.error(`build-core: unsupported target "${target}" (expected linux|darwin|win32 - x64|arm64)`);
  process.exit(2);
}

const version = process.env.AIPERMILER_CORE_VERSION || require(path.join(root, 'package.json')).version;
const binary = path.join(outDir, platform === 'win32' ? 'aipermiler.exe' : 'aipermiler');

// One binary at a time in core-bin/: a Linux core must never ship in a Windows package.
fs.rmSync(outDir, { recursive: true, force: true });
fs.mkdirSync(outDir, { recursive: true });

const result = spawnSync('go', [
  'build', '-trimpath',
  '-ldflags', `-s -w -X github.com/suppressio/aipermiler/gocore/internal/buildinfo.Version=${version}`,
  '-o', binary,
  './cmd/aipermiler',
], {
  cwd: path.join(root, 'gocore'),
  stdio: 'inherit',
  env: { ...process.env, GOOS: goos, GOARCH: goarch, CGO_ENABLED: '0' },
});

if (result.error) {
  console.error(`build-core: cannot run Go (${result.error.message}). Install Go (version in gocore/go.mod), or set AIPERMILER_CORE_BIN to a core built elsewhere.`);
  process.exit(1);
}
if (result.status !== 0) {
  console.error('build-core: go build failed');
  process.exit(result.status ?? 1);
}
console.log(`build-core: ${path.relative(root, binary)} (${goos}/${goarch}, ${version})`);
