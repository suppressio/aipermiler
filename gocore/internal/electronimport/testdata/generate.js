// Regenerates the iv-*.bin fixtures with the real `conf` package, the one electron-store
// uses. From the repository root: node gocore/internal/electronimport/testdata/generate.js
// Each fixture is settings.json encrypted with the app's key and a chosen IV, so the
// IVs that are not valid UTF-8 (the salt is the IV decoded as UTF-8 by Node) are covered.
'use strict';
const crypto = require('node:crypto');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const Conf = require('../../../../node_modules/conf').default ?? require('../../../../node_modules/conf');

const KEY = 'dev-only-placeholder-change-before-release';
const ivs = {
  ascii: Buffer.from('0123456789abcdef'),
  'valid-multibyte': Buffer.concat([Buffer.from('ab'), Buffer.from('€', 'utf8'), Buffer.from('😀', 'utf8'), Buffer.from('xyzw123')]),
  'lone-continuation': Buffer.from([0x80, 0x41, 0x42, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48, 0x49, 0x4a, 0x4b, 0x4c, 0x4d, 0x4e, 0x4f]),
  'invalid-ff': Buffer.from([0xff, 0xfe, 0x41, 0x42, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48, 0x49, 0x4a, 0x4b, 0x4c, 0x4d, 0x4e]),
  // A truncated sequence is ONE U+FFFD in Node (maximal subpart), not one per byte.
  'truncated-3byte': Buffer.from([0xe2, 0x82, 0x41, 0x42, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48, 0x49, 0x4a, 0x4b, 0x4c, 0x4d, 0x4e]),
  'truncated-4byte': Buffer.from([0xf0, 0x9f, 0x98, 0x41, 0x42, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48, 0x49, 0x4a, 0x4b, 0x4c, 0x4d]),
  'truncated-at-end': Buffer.from([0x41, 0x42, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48, 0x49, 0x4a, 0x4b, 0x4c, 0x4d, 0x4e, 0xe2, 0x82]),
  surrogate: Buffer.from([0xed, 0xa0, 0x80, 0x41, 0x42, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48, 0x49, 0x4a, 0x4b, 0x4c, 0x4d]),
  overlong: Buffer.from([0xc0, 0x80, 0xe0, 0x80, 0x80, 0x41, 0x42, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48, 0x49, 0x4a, 0x4b]),
  'above-max': Buffer.from([0xf4, 0x90, 0x80, 0x80, 0xf5, 0x80, 0x41, 0x42, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48, 0x49, 0x4a]),
  'all-invalid': Buffer.alloc(16, 0xff),
};

const dir = __dirname;
const settings = JSON.parse(fs.readFileSync(path.join(dir, 'settings.json'), 'utf8'));
const realRandomBytes = crypto.randomBytes;
for (const [name, iv] of Object.entries(ivs)) {
  if (iv.length !== 16) throw new Error(`${name}: IV is ${iv.length} bytes`);
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'conf-'));
  crypto.randomBytes = (n) => (n === 16 ? Buffer.from(iv) : realRandomBytes(n));
  const conf = new Conf({ cwd: tmp, configName: 'aipermiler-config', encryptionKey: KEY, fileExtension: 'json' });
  for (const [k, v] of Object.entries(settings)) conf.set(k, v);
  crypto.randomBytes = realRandomBytes;
  const file = fs.readFileSync(path.join(tmp, 'aipermiler-config.json'));
  if (!file.subarray(0, 16).equals(iv)) throw new Error(`${name}: the file does not start with the chosen IV`);
  fs.writeFileSync(path.join(dir, `iv-${name}.bin`), file);
  fs.rmSync(tmp, { recursive: true });
}
// The plaintext conf wrote, to compare after decryption (conf serializes with tabs).
const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'conf-'));
const plain = new Conf({ cwd: tmp, configName: 'plain', fileExtension: 'json' });
for (const [k, v] of Object.entries(settings)) plain.set(k, v);
fs.copyFileSync(path.join(tmp, 'plain.json'), path.join(dir, 'plaintext.json'));
fs.rmSync(tmp, { recursive: true });
