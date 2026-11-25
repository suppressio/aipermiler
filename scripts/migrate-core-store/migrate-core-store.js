#!/usr/bin/env node
// migrate-core-store.js - copies AIpermiler's settings and history from the Electron
// app's store (aipermiler-config.json, encrypted by electron-store) into the Go core's
// store.json, once, for the switch to v0.7.0 (#310, #311). docs/UPGRADE-TO-0.7.md.
//
// Run it with the app and the service stopped, with Node.js 22 or with the app itself:
//   node migrate-core-store.js [--yes]
//   ELECTRON_RUN_AS_NODE=1 <AIpermiler binary> migrate-core-store.js [--yes]
//
// What it does:
//   1. reads <config>/AIpermiler/aipermiler-config.json and never changes it;
//   2. refuses while the service runs (api.json present and /api/v1/health answers);
//   3. maps settings, accounts, daily history, recent samples and the last good read
//      onto store.json schema 1 (gocore/internal/store); what does not fit is skipped
//      and named by id, never with its values. Copilot's token is not copied: the OS
//      keyring holds it since #179;
//   4. if store.json already exists, asks first (--yes skips the question) and keeps it
//      as store.json.bak-<date>; writes atomically, owner-only.
// <config> is ~/.config (or $XDG_CONFIG_HOME) on Linux, %APPDATA% on Windows and
// ~/Library/Application Support on macOS. The core's folder is <config>/aipermiler.
// Development overrides (absolute paths): AIPERMILER_ELECTRON_DIR, AIPERMILER_CONFIG_DIR.
//
// Plain JS with Node built-ins only, so it runs from a single downloaded file.
'use strict';

const crypto = require('node:crypto');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const readline = require('node:readline');

// The key the app passes to electron-store (store/index.ts). A development
// placeholder shipped in every package (#251), not a secret.
const APP_KEY = 'dev-only-placeholder-change-before-release';
const ELECTRON_FILE = 'aipermiler-config.json';
const STORE_FILE = 'store.json';
const SCHEMA_VERSION = 1; // gocore/internal/store SchemaVersion
const DEFAULT_SETTINGS = { refreshIntervalMinutes: 30, retentionDays: 90, notificationThresholdPercent: 80 }; // store.Empty
const PROVIDERS = ['claude', 'codex', 'copilot'];
const PERIOD_TYPES = ['rolling-hours', 'rolling-days', 'billing-cycle'];
const UNITS = ['percentage', 'count'];
const WEEKDAYS = ['sun', 'mon', 'tue', 'wed', 'thu', 'fri', 'sat']; // the store's days are Sunday first

class UnsupportedFormatError extends Error {}
class DecryptError extends Error {}

// --- folders ---------------------------------------------------------------------------

function userConfigDir(env, platform, home) {
  switch (platform) {
    case 'win32':
      if (!env.APPDATA) throw new Error('%APPDATA% is not set');
      return env.APPDATA;
    case 'darwin':
      return path.join(home, 'Library', 'Application Support');
    default:
      return env.XDG_CONFIG_HOME || path.join(home, '.config');
  }
}

function overridable(env, name, fallback) {
  const override = env[name];
  if (!override) return fallback();
  if (!path.isAbsolute(override)) throw new Error(`${name} must be an absolute path`);
  return override;
}

/** Electron's userData: <config>/AIpermiler (the app's productName). */
function electronDir(env, platform, home) {
  return overridable(env, 'AIPERMILER_ELECTRON_DIR', () => path.join(userConfigDir(env, platform, home), 'AIpermiler'));
}

/** The core's folder (gocore/internal/config.Dir): <config>/aipermiler. */
function coreDir(env, platform, home) {
  return overridable(env, 'AIPERMILER_CONFIG_DIR', () => path.join(userConfigDir(env, platform, home), 'aipermiler'));
}

// --- decryption --------------------------------------------------------------------------

/**
 * Reads a file written by conf (electron-store) with an encryptionKey: 16-byte IV, ':',
 * AES-256-CBC. The key is PBKDF2-SHA512 with iv.toString() as salt, exactly as conf
 * derives it. Like conf, a file that is plain JSON is read as it is.
 */
function decrypt(data, key) {
  if (data.length > 16 && data[16] === 0x3a) {
    const iv = data.subarray(0, 16);
    let plain;
    try {
      const password = crypto.pbkdf2Sync(key, iv.toString(), 10000, 32, 'sha512');
      const decipher = crypto.createDecipheriv('aes-256-cbc', password, iv);
      plain = Buffer.concat([decipher.update(data.subarray(17)), decipher.final()]).toString('utf8');
      return JSON.parse(plain);
    } catch (error) {
      throw new DecryptError(`cannot decrypt the app's settings (wrong key, truncated or damaged file): ${error.message}`);
    }
  }
  try {
    return JSON.parse(data.toString('utf8'));
  } catch {
    throw new UnsupportedFormatError('the settings file is in an older electron-store format this script does not read');
  }
}

// --- mapping -----------------------------------------------------------------------------

const isObject = (v) => typeof v === 'object' && v !== null && !Array.isArray(v);
const isNumber = (v) => typeof v === 'number' && Number.isFinite(v);
const str = (v) => (typeof v === 'string' ? v : '');

const present = (v) => v !== undefined && v !== null;

/**
 * A global setting within [min, max], the range PUT /api/v1/settings accepts (#284):
 * the default when missing, and a warning naming the setting (never its value) when a
 * present value is replaced or clamped.
 */
function clampedSetting(name, v, min, max, fallback, warn) {
  if (!present(v)) return fallback;
  if (!Number.isInteger(v) || v < 1) {
    warn(`settings.${name} is not a positive integer, set to the default ${fallback}`);
    return fallback;
  }
  const clamped = Math.min(max, Math.max(min, v));
  if (clamped !== v) warn(`settings.${name} out of range, clamped to ${clamped}`);
  return clamped;
}

// gocore/internal/secrets ValidAccount: an id names the keyring entry.
const ACCOUNT_ID = /^[A-Za-z0-9._-]{1,128}$/;
const TENANT_HOST = /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.ghe\.com$/;

/** gocore/internal/github NormalizeHost: "github.com", "<tenant>.ghe.com" or "". */
function normalizeHost(value) {
  let host = value.trim().toLowerCase();
  if (host.startsWith('https://')) host = host.slice('https://'.length);
  const slash = host.indexOf('/');
  if (slash !== -1) host = host.slice(0, slash);
  if (host.startsWith('api.')) host = host.slice('api.'.length);
  return host === 'github.com' || TENANT_HOST.test(host) ? host : '';
}

/**
 * An RFC 3339 time as Go's encoding/json writes it back (RFC3339Nano): the offset kept,
 * "Z" for UTC, trailing zeros of the fraction dropped. null when invalid.
 */
function goTime(value) {
  if (typeof value !== 'string') return null;
  const m = /^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})(?:\.(\d{1,9}))?(Z|[+-]\d{2}:\d{2})$/i.exec(value);
  if (!m || Number.isNaN(Date.parse(value))) return null;
  const [, base, fraction = '', zone] = m;
  const digits = fraction.replace(/0+$/, '');
  const offset = zone.toUpperCase() === 'Z' || zone === '+00:00' || zone === '-00:00' ? 'Z' : zone;
  return `${base}${digits ? `.${digits}` : ''}${offset}`;
}

/** A local 'YYYY-MM-DD' day as Go writes time.ParseInLocation(DateOnly, …, Local). */
function localDay(value) {
  const m = typeof value === 'string' ? /^(\d{4})-(\d{2})-(\d{2})$/.exec(value) : null;
  if (!m) return null;
  const [y, mo, d] = [Number(m[1]), Number(m[2]), Number(m[3])];
  const check = new Date(Date.UTC(y, mo - 1, d));
  if (check.getUTCFullYear() !== y || check.getUTCMonth() !== mo - 1 || check.getUTCDate() !== d) return null;
  // Midnight as Go's time.Date resolves it, or the core's history would not recognize
  // the point as that day's where a DST change skips or repeats midnight: take the
  // offset in effect at the wall time read as UTC, and if the instant it gives falls in
  // another zone period, the offset there (zoneinfo lookup in time.Date). JS's own
  // Date(y, m, d) disagrees on skipped and repeated midnights.
  const wall = check.getTime();
  const offsetAt = (ms) => -new Date(ms).getTimezoneOffset() * 60000;
  let offsetMs = offsetAt(wall);
  const other = offsetAt(wall - offsetMs);
  if (other !== offsetMs) offsetMs = other;
  const date = new Date(wall - offsetMs);
  const off = -date.getTimezoneOffset();
  const pad = (n) => String(n).padStart(2, '0');
  const zone = off === 0 ? 'Z' : `${off > 0 ? '+' : '-'}${pad(Math.floor(Math.abs(off) / 60))}:${pad(Math.abs(off) % 60)}`;
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}:00${zone}`;
}

const RESET_THRESHOLD = 1 + 1e-9; // history.resetThreshold + thresholdTolerance

function baseline(previous, point) {
  if (!previous) return point.used;
  return previous.used - point.used > RESET_THRESHOLD ? 0 : previous.used;
}

function convertAccount(src, accounts, skip) {
  if (!isObject(src) || typeof src.id !== 'string' || src.id === '') {
    skip('an account is malformed or has no id');
    return;
  }
  if (!PROVIDERS.includes(src.provider)) {
    skip(`account ${JSON.stringify(src.id)} has an unknown provider`);
    return;
  }
  if (accounts.some((a) => a.id === src.id)) {
    skip(`account ${JSON.stringify(src.id)} appears twice`);
    return;
  }
  if (!ACCOUNT_ID.test(src.id)) {
    skip(`account ${JSON.stringify(src.id)} has an id the core cannot use`);
    return;
  }
  // The Copilot token goes only to the host the user set: never defaulted to another.
  const hasHost = src.provider === 'copilot' && present(src.host) && src.host !== '';
  const host = hasHost && typeof src.host === 'string' ? normalizeHost(src.host) : '';
  if (hasHost && !host) {
    skip(`account ${JSON.stringify(src.id)} has a GitHub host that is neither github.com nor a .ghe.com tenant`);
    return;
  }
  // A present value changed or dropped is listed by account and field, never with the value.
  const changed = (field, what) => skip(`account ${JSON.stringify(src.id)}: ${field} ${what}`);
  const rule = isObject(src.subscription) && isObject(src.subscription.renewalRule) ? src.subscription.renewalRule : {};
  const work = isObject(src.workSchedule) ? src.workSchedule : {};
  const days = isObject(work.days) ? work.days : {};
  const weight = (status) => (status === 'full' ? 1 : status === 'half' ? 0.5 : 0);
  // Out-of-range values get their default (a renewal day: none), so the service's
  // PUT /api/v1/settings accepts the result (#284).
  let renewalDay = 0;
  if (rule.type === 'dayOfMonth' && present(rule.day)) {
    if (Number.isInteger(rule.day) && rule.day >= 1 && rule.day <= 31) renewalDay = rule.day;
    else changed('renewalDay', 'is not a day 1-31, set to none');
  }
  let hoursPerDay = 8;
  if (present(work.hoursPerDay)) {
    if (isNumber(work.hoursPerDay) && work.hoursPerDay > 0 && work.hoursPerDay <= 24) hoursPerDay = work.hoursPerDay;
    else changed('schedule.hoursPerDay', 'is not over 0 and at most 24, set to 8');
  }
  const account = {
    id: src.id,
    provider: src.provider,
    label: str(src.label),
    enabled: src.enabled === true,
    renewalDay,
    schedule: {
      enabled: work.enabled === true,
      days: WEEKDAYS.map((name) => weight(days[name])),
      hoursPerDay,
    },
  };
  // Go omits empty host/scope/manualQuota/binaryPath/localInsights/authMethod (omitempty).
  if (src.provider === 'copilot') {
    if (host) account.host = host;
    if (src.accountScope === 'personal' || src.accountScope === 'organization') account.scope = src.accountScope;
    else if (present(src.accountScope) && src.accountScope !== '') changed('scope', 'is not personal or organization, dropped');
    if (isNumber(src.manualQuota) && src.manualQuota > 0) account.manualQuota = src.manualQuota;
    else if (present(src.manualQuota) && src.manualQuota !== 0) changed('manualQuota', 'is not 0 or more, dropped');
    if (src.authMethod === 'pat' || src.authMethod === 'oauth') account.authMethod = src.authMethod;
    else if (present(src.authMethod) && src.authMethod !== '') changed('authMethod', 'is not pat or oauth, dropped');
  } else if (str(src.binaryPath) && path.isAbsolute(src.binaryPath)) {
    account.binaryPath = src.binaryPath;
  } else if (present(src.binaryPath) && src.binaryPath !== '') {
    // The core then finds the binary itself.
    changed('binaryPath', 'is not absolute, dropped');
  }
  // The service accepts local insights on one Claude account only (#336); the app
  // already keeps at most one, so a second is only possible in a hand-edited file.
  if (src.provider === 'claude' && src.localInsights === true) {
    if (accounts.some((a) => a.localInsights)) changed('localInsights', 'is already on another Claude account, dropped');
    else account.localInsights = true;
  }
  account.history = {};
  account.lastGood = null;
  accounts.push(account);
}

function convertWindow(w) {
  if (!isObject(w) || !str(w.id) || !PERIOD_TYPES.includes(w.periodType) || !UNITS.includes(w.unit)) return null;
  const resetsAt = w.resetsAt === null || w.resetsAt === undefined ? null : goTime(w.resetsAt);
  if (resetsAt === null && w.resetsAt !== null && w.resetsAt !== undefined) return null;
  return {
    id: w.id,
    label: str(w.label),
    periodType: w.periodType,
    periodLength: isNumber(w.periodLength) ? w.periodLength : null,
    unit: w.unit,
    used: isNumber(w.used) ? w.used : 0,
    total: isNumber(w.total) ? w.total : null,
    resetsAt,
  };
}

function convertHistory(history, accounts, skip) {
  const byId = new Map(accounts.map((a) => [a.id, a]));
  const series = (account, windowId) => {
    account.history[windowId] ??= { dailyUsage: [], recentSamples: [] };
    return account.history[windowId];
  };

  // Daily points: one per local day (the last stored), sorted. A point without its own
  // baseline starts from the previous day's value, as Electron's budget does, or from 0
  // after a reset (a drop of more than one point, history.baseline in Go); the first one
  // starts from its own value.
  const daily = new Map();
  for (const p of Array.isArray(history.dailyUsage) ? history.dailyUsage : []) {
    if (!isObject(p)) { skip('a daily point is malformed'); continue; }
    const day = localDay(p.date);
    if (day === null || !isNumber(p.used) || p.used < 0) {
      skip(`a daily point of ${JSON.stringify(str(p.accountId))} has an invalid date or value`);
      continue;
    }
    const account = byId.get(p.accountId);
    if (!account || !str(p.windowId)) {
      skip(`a daily point of unknown account ${JSON.stringify(str(p.accountId))}`);
      continue;
    }
    const key = `${account.id}\u0000${p.windowId}`;
    if (!daily.has(key)) daily.set(key, { account, windowId: p.windowId, days: new Map() });
    daily.get(key).days.set(p.date, { day, used: p.used, dayStartUsed: isNumber(p.dayStartUsed) ? p.dayStartUsed : null });
  }
  for (const { account, windowId, days } of daily.values()) {
    const points = [...days.entries()].sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0)).map(([, v]) => v);
    series(account, windowId).dailyUsage = points.map((p, i) => ({
      day: p.day,
      used: p.used,
      dayStartUsed: p.dayStartUsed ?? baseline(points[i - 1], p),
    }));
  }

  // Recent samples: sorted by time, one per timestamp (the first).
  const samples = new Map();
  for (const s of Array.isArray(history.recentSamples) ? history.recentSamples : []) {
    const at = isObject(s) ? goTime(s.timestamp) : null;
    if (at === null || !isNumber(s.used) || s.used < 0) { skip('a recent sample is malformed'); continue; }
    const account = byId.get(s.accountId);
    if (!account || !str(s.windowId)) {
      skip(`a recent sample of unknown account ${JSON.stringify(str(s.accountId))}`);
      continue;
    }
    const key = `${account.id}\u0000${s.windowId}`;
    if (!samples.has(key)) samples.set(key, { account, windowId: s.windowId, list: [] });
    samples.get(key).list.push({ at, ms: Date.parse(at), used: s.used });
  }
  for (const { account, windowId, list } of samples.values()) {
    list.sort((a, b) => a.ms - b.ms);
    series(account, windowId).recentSamples = list
      .filter((r, i) => i === 0 || r.ms !== list[i - 1].ms)
      .map(({ at, used }) => ({ at, used }));
  }

  // Last good read per account.
  for (const [id, lg] of Object.entries(isObject(history.lastGood) ? history.lastGood : {})) {
    const account = byId.get(id);
    if (!account) { skip(`last good read of unknown account ${JSON.stringify(id)}`); continue; }
    const updatedAt = isObject(lg) ? goTime(lg.lastUpdatedAt) : null;
    const renews = isObject(lg) && lg.subscriptionRenewsAt != null ? goTime(lg.subscriptionRenewsAt) : null;
    if (updatedAt === null || (lg.subscriptionRenewsAt != null && renews === null)) {
      skip(`last good read of account ${JSON.stringify(id)} is malformed`);
      continue;
    }
    const windows = [];
    for (const w of Array.isArray(lg.quotaWindows) ? lg.quotaWindows : []) {
      const window = convertWindow(w);
      if (window) windows.push(window);
      else skip(`window ${JSON.stringify(isObject(w) ? str(w.id) : '')} of account ${JSON.stringify(id)} is malformed or has an unknown type`);
    }
    account.lastGood = {
      updatedAt,
      planTier: typeof lg.planTier === 'string' ? lg.planTier : null,
      subscriptionRenewsAt: renews,
      quotaWindows: windows,
    };
  }
}

/**
 * Maps Electron's AppSettings (types/index.ts) onto store.json schema 1. Daily points
 * are local days of the time zone this runs in, the one the app ran in.
 * Returns { state, warnings }: warnings name what was skipped by id, never by value.
 */
function convert(settings) {
  if (!isObject(settings) || !Array.isArray(settings.accounts)) {
    throw new Error("no accounts array: not the app's settings");
  }
  const warnings = [];
  const skip = (what) => warnings.push(what);
  const ui = isObject(settings.ui) ? settings.ui : {};
  const history = isObject(settings.history) ? settings.history : {};
  const accounts = [];
  for (const src of settings.accounts) convertAccount(src, accounts, skip);
  convertHistory(history, accounts, skip);
  const state = {
    schemaVersion: SCHEMA_VERSION,
    settings: {
      // The ranges of PUT /api/v1/settings (#284), so a GET → PUT of the result is accepted.
      refreshIntervalMinutes: clampedSetting('refreshIntervalMinutes', ui.refreshIntervalMinutes, 5, 1440, DEFAULT_SETTINGS.refreshIntervalMinutes, skip),
      retentionDays: clampedSetting('retentionDays', history.retentionDays, 1, 3650, DEFAULT_SETTINGS.retentionDays, skip),
      notificationThresholdPercent: clampedSetting('notificationThresholdPercent', ui.notificationThresholdPercent, 1, 100, DEFAULT_SETTINGS.notificationThresholdPercent, skip),
    },
    accounts,
  };
  return { state, warnings };
}

// --- running -----------------------------------------------------------------------------

/**
 * True when api.json names a port where something answers. Any HTTP answer counts,
 * even a refusal: only a connection that fails means a leftover api.json. The core's
 * own test is its lock (service/stop.go), which Node cannot probe; the window this
 * leaves (a service starting, its lock taken, api.json not written yet) is closed by
 * asking the user to stop the service first and by checking again just before writing.
 */
async function serviceRunning(dir) {
  let info;
  try {
    info = JSON.parse(fs.readFileSync(path.join(dir, 'api.json'), 'utf8'));
  } catch {
    return false; // no api.json (or an unreadable leftover): no service
  }
  if (!Number.isInteger(info.port) || typeof info.token !== 'string') return false;
  try {
    const res = await fetch(`http://127.0.0.1:${info.port}/api/v1/health`, {
      headers: { Authorization: `Bearer ${info.token}` },
      signal: AbortSignal.timeout(2000),
    });
    void res.body?.cancel();
    return true;
  } catch {
    return false; // nobody listening: a leftover of a service that did not stop cleanly
  }
}

async function confirm(question) {
  const rl = readline.createInterface({ input: process.stdin, output: process.stdout });
  try {
    const answer = await new Promise((resolve) => rl.question(question, resolve));
    return /^(y|yes)$/i.test(answer.trim());
  } finally {
    rl.close();
  }
}

function writeAtomic(file, content) {
  const tmp = `${file}.${crypto.randomBytes(6).toString('hex')}.tmp`;
  try {
    const fd = fs.openSync(tmp, 'wx', 0o600);
    try {
      fs.writeFileSync(fd, content);
      fs.fsyncSync(fd);
    } finally {
      fs.closeSync(fd);
    }
    fs.renameSync(tmp, file);
  } catch (error) {
    fs.rmSync(tmp, { force: true });
    throw error;
  }
  // The rename survives a power loss only once the folder is synced, as in the core's
  // store. Windows cannot open a folder for that.
  // The file is in place either way: a failed folder sync (some network or FUSE file
  // systems refuse it) is a warning, as ErrNotSynced is in the core.
  if (process.platform !== 'win32') {
    try {
      const dir = fs.openSync(path.dirname(file), 'r');
      try {
        fs.fsyncSync(dir);
      } finally {
        fs.closeSync(dir);
      }
    } catch (error) {
      console.warn(`Warning: ${STORE_FILE} is written, but its folder could not be synced (${error.message}): a power loss right now could undo it.`);
    }
  }
}

function stamp(date) {
  const pad = (n) => String(n).padStart(2, '0');
  return `${date.getFullYear()}${pad(date.getMonth() + 1)}${pad(date.getDate())}-${pad(date.getHours())}${pad(date.getMinutes())}${pad(date.getSeconds())}`;
}

class Failure extends Error {}

const STILL_RUNNING = 'the AIpermiler service is running. Quit the app, stop the service (`aipermiler service stop`, the core binary in the app\'s resources/core folder) and run this script again. If no service runs, delete the leftover api.json in the core\'s folder.';

async function main(argv) {
  const args = new Set(argv);
  if (args.has('--help') || args.has('-h')) {
    console.log('Usage: migrate-core-store.js [--yes]\nCopies the settings and history of AIpermiler\'s Electron app into the core\'s store.json. See docs/UPGRADE-TO-0.7.md.');
    return;
  }
  const home = os.homedir();
  const fromDir = electronDir(process.env, process.platform, home);
  const toDir = coreDir(process.env, process.platform, home);
  const source = path.join(fromDir, ELECTRON_FILE);
  const target = path.join(toDir, STORE_FILE);

  if (!fs.existsSync(source)) throw new Failure(`no AIpermiler settings found in ${fromDir}: nothing to migrate.`);
  if (await serviceRunning(toDir)) {
    throw new Failure(STILL_RUNNING);
  }

  const { state, warnings } = convert(decrypt(fs.readFileSync(source), APP_KEY));

  if (fs.existsSync(target)) {
    const replace = args.has('--yes') || (process.stdin.isTTY && await confirm(
      `The core already has a store in ${toDir}.\nIt will be replaced and kept as a backup. Continue? [y/N] `));
    if (!replace) {
      throw new Failure(`${target} already exists and was left as it is. Run again with --yes to replace it (the old one is kept as a backup).`);
    }
  }

  // Checked again: the question above can wait for minutes while the service starts.
  if (await serviceRunning(toDir)) throw new Failure(STILL_RUNNING);

  fs.mkdirSync(toDir, { recursive: true, mode: 0o700 });
  if (fs.existsSync(target)) {
    let backup = `${target}.bak-${stamp(new Date())}`;
    for (let n = 2; fs.existsSync(backup); n++) backup = `${target}.bak-${stamp(new Date())}-${n}`;
    fs.copyFileSync(target, backup, fs.constants.COPYFILE_EXCL);
    console.log(`Previous store kept as ${backup}`);
  }
  writeAtomic(target, `${JSON.stringify(state, null, 2)}\n`);

  for (const w of warnings) console.log(`Left out or changed: ${w}`);
  console.log(`Migrated ${state.accounts.length} accounts into ${target} (${warnings.length} entries left out or changed).`);
  console.log(`The app's own file was not changed: ${source}`);
}

module.exports = { decrypt, convert, electronDir, coreDir, UnsupportedFormatError, DecryptError };

if (require.main === module) {
  main(process.argv.slice(2)).catch((error) => {
    console.error(`Error: ${error instanceof Error ? error.message : String(error)}`);
    process.exitCode = 1;
  });
}
