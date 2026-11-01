#!/usr/bin/env node
// scripts/test-build.js — local TEST build, the same steps as the CI workflow
// (.github/workflows/build.yml) without spending a pipeline run: lint, tests, then
// packages for Linux (.deb) and Windows, versioned "<package.json version>.dev.<timestamp>"
// and never published (no release, no update notification). Output: release/test/.
//
// Windows: the NSIS installer (.exe) can be built from Linux/macOS only with Wine;
// without it a .zip of the ready-to-run app is built instead (extract and start
// "AIpermiler.exe"). Usage: `npm run package:test` (add `-- --linux` or `-- --win`
// for one platform only).

const { spawnSync } = require('child_process');
const fs = require('fs');
const path = require('path');

const root = path.join(__dirname, '..');
const outDir = path.join(root, 'release', 'test');
const npx = process.platform === 'win32' ? 'npx.cmd' : 'npx';
const npm = process.platform === 'win32' ? 'npm.cmd' : 'npm';

function run(command, args) {
  console.log(`\n> ${command} ${args.join(' ')}`);
  // AIPERMILER_CORE_VERSION: the core built for each package carries the same test version.
  const result = spawnSync(command, args, { cwd: root, stdio: 'inherit', env: { ...process.env, CSC_IDENTITY_AUTO_DISCOVERY: 'false', AIPERMILER_CORE_VERSION: version } });
  if (result.status !== 0) {
    console.error(`\nTest build stopped: "${command} ${args.join(' ')}" failed.`);
    process.exit(result.status ?? 1);
  }
}

function hasCommand(name) {
  const probe = process.platform === 'win32' ? spawnSync('where', [name]) : spawnSync('sh', ['-c', `command -v ${name}`]);
  return probe.status === 0;
}

const only = process.argv.slice(2);
const wantLinux = only.length === 0 || only.includes('--linux');
const wantWin = only.length === 0 || only.includes('--win');

const pad = (n) => String(n).padStart(2, '0');
const now = new Date();
const stamp = `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}${pad(now.getHours())}${pad(now.getMinutes())}`;
const version = `${require(path.join(root, 'package.json')).version}.dev.${stamp}`;

// Same checks as the CI job: a lint finding or a failing test stops the build.
run(npm, ['run', 'lint']);
run(npm, ['test']);

fs.rmSync(outDir, { recursive: true, force: true });
const common = ['--x64', '--publish=never', `-c.extraMetadata.version=${version}`, `-c.directories.output=${path.relative(root, outDir)}`];

// The Go core (scripts/build-core.js) is cross-compiled for each package right before it:
// core-bin/ holds one binary at a time.
const buildCore = (target) => run(process.execPath, [path.join(__dirname, 'build-core.js'), '--target', target]);

if (wantLinux) {
  buildCore('linux-x64');
  run(npx, ['electron-builder', '--linux', 'deb', ...common]);
}
if (wantWin) {
  // Without Wine (non-Windows host) neither NSIS nor the icon/version edit of the .exe
  // (rcedit) can run.
  const native = process.platform === 'win32' || hasCommand('wine');
  buildCore('win32-x64');
  run(npx, ['electron-builder', '--win', native ? 'nsis' : 'zip', ...common, ...(native ? [] : ['-c.win.signAndEditExecutable=false'])]);
  if (!native) console.log('\nNo Wine on this machine: Windows build as .zip (extract it and start "AIpermiler.exe"). Install Wine for the .exe installer.');
}

const files = fs.existsSync(outDir) ? fs.readdirSync(outDir).filter((f) => /\.(deb|exe|zip)$/.test(f)) : [];
console.log(`\nTest build ${version}:`);
for (const f of files) console.log(`  ${path.join(outDir, f)}`);
