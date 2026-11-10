// scripts/sync-metainfo.js — keeps the <release> list of the AppStream metainfo in step
// with package.json (#206). Without arguments it adds the entry of the current version
// (date: today, local) when missing; `--check` only verifies and exits 1 when it is missing.
// Wired to the npm `version` lifecycle (the entry is added by `npm version`, before the
// release commit) and to CI (`--check`, followed by `appstreamcli validate`).

const fs = require('fs');
const path = require('path');

const ROOT = path.join(__dirname, '..');
const FILE = path.join(ROOT, 'build', 'linux', 'com.suppressio.aipermiler.metainfo.xml');
const { version } = require(path.join(ROOT, 'package.json'));

function localDate() {
  const now = new Date();
  const pad = (n) => String(n).padStart(2, '0');
  return `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`;
}

const xml = fs.readFileSync(FILE, 'utf8');
const present = xml.includes(`<release version="${version}"`);

if (process.argv.includes('--check')) {
  if (!present) {
    console.error(`${path.relative(ROOT, FILE)} has no <release> for ${version}; run "npm run metainfo:sync".`);
    process.exit(1);
  }
  console.log(`metainfo lists ${version}`);
  process.exit(0);
}

if (present) {
  console.log(`metainfo already lists ${version}`);
  process.exit(0);
}

// A pre-release (0.6.1-beta, 1.0.0-rc.1) is "development" in AppStream.
const type = version.includes('-') ? 'development' : 'stable';
// A Windows checkout with core.autocrlf has CRLF endings: match either and keep the file's.
const marker = /( *)<releases>(\r?\n)/;
const match = xml.match(marker);
if (!match) {
  console.error('No <releases> element in the metainfo');
  process.exit(1);
}
const [, indent, eol] = match;
const entry = `${indent}  <release version="${version}" date="${localDate()}" type="${type}"/>${eol}`;
fs.writeFileSync(FILE, xml.replace(marker, (m) => m + entry));
console.log(`metainfo: added ${version}`);
