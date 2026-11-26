// Parity of the pacing model: budget.ts (through core/usage.ts computeAccountSnapshot) and
// the Go packages (gocore/cmd/pacingparity) run on the same scenarios, outputs compared (#325).
//
//   npm run build
//   node scripts/pacing-parity.js [scenario.json | folder]...
//
// Default: scripts/pacing-parity/scenarios/. Needs Go. Real histories (#323) are scenario
// files of the same format kept outside the repo (it is public): pass their folder.
// Every difference must be listed in the scenario's "expectedDifferences" ({paths, ref})
// with the §9 deviation or §8 row of docs/PACING.md that explains it; paths are leaf
// fields, matched exactly. A difference not listed is a porting error until proven
// otherwise: it fails the run (exit 1), and gets an item of its own; meanwhile list it
// with a ref "porting-error: <what> (<item>)" to keep it visible without failing. A
// listed path that no longer differs fails too (drop it).
const { spawnSync } = require('node:child_process');
const fs = require('node:fs');
const path = require('node:path');

const root = path.join(__dirname, '..');
const EPSILON = 1e-9;
// Decimals the TypeScript path rounds each field to (budget.ts round1/round2, the history
// stored at 0.01 by core/usage.ts); a field not listed is compared exactly.
const PRECISION = {
  efficiencyIndex: 2, projectedUsage: 1, workingDaysUntilReset: 1, autonomyWorkingDays: 1,
  budget: 2, usedToday: 2, perUnit: 2, idealPerUnit: 2, unitsLeft: 1,
  hoursLeft: 2, projectedAtReset: 1, autonomyHours: 1, instantRate: 2, sustainableRate: 2,
  avgRatio: 2, delta: 2, fullShare: 2, peak: 2, avg: 2, used: 2, dayStartUsed: 2,
};
const DAY_NAMES = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun'];

function scenarioFiles(args) {
  const inputs = args.length > 0 ? args : [path.join(__dirname, 'pacing-parity', 'scenarios')];
  const files = [];
  for (const input of inputs) {
    if (fs.statSync(input).isDirectory()) {
      files.push(...fs.readdirSync(input).filter((f) => f.endsWith('.json')).sort().map((f) => path.join(input, f)));
    } else {
      files.push(input);
    }
  }
  return files;
}

// Local day key of the process time zone (set per scenario).
function dayKey(date) {
  const p = (n) => String(n).padStart(2, '0');
  return `${date.getFullYear()}-${p(date.getMonth() + 1)}-${p(date.getDate())}`;
}

function runTypeScript(budget, usage, s) {
  process.env.TZ = s.timeZone;
  const now = new Date(s.now);
  const accountId = 'parity';
  const days = {};
  DAY_NAMES.forEach((name) => {
    const w = s.schedule.days[name] ?? 0;
    days[name] = w === 1 ? 'full' : w === 0.5 ? 'half' : 'off';
  });
  const cfg = {
    id: accountId, provider: 'claude', label: 'parity', enabled: true,
    subscription: { renewalRule: { type: 'dayOfMonth', day: s.renewalDay || 1 } },
    workSchedule: { enabled: s.schedule.enabled, days, hoursPerDay: s.schedule.hoursPerDay },
  };
  const quotaWindows = s.windows.map((w) => ({
    id: w.id, label: w.id, periodType: w.periodType, periodLength: w.periodLength || null,
    unit: w.unit, used: w.used, total: w.total || null, resetsAt: w.resetsAt ?? null,
  }));
  const draft = { dailyUsage: [], recentSamples: [], chartDays: s.chartDays || 7 };
  for (const [windowId, h] of Object.entries(s.history ?? {})) {
    for (const d of h.daily ?? []) {
      draft.dailyUsage.push({ date: d.date, accountId, windowId, used: d.used, ...(d.dayStartUsed == null ? {} : { dayStartUsed: d.dayStartUsed }) });
    }
    for (const r of h.samples ?? []) {
      draft.recentSamples.push({ timestamp: new Date(r.at).toISOString(), accountId, windowId, used: r.used });
    }
  }
  const insights = s.firstSession
    ? { firstSessionStartByDay: { [dayKey(now)]: s.firstSession }, daily: [] }
    : null;
  const raw = {
    accountId, planTier: null, subscriptionRenewsAt: s.subscriptionRenewsAt ?? null, quotaWindows,
    ...(s.fresh ? { lastUpdatedAt: s.readAt ?? s.now } : {}),
  };
  const account = usage.computeAccountSnapshot(draft, raw, cfg, now, insights, Boolean(s.fresh));
  const verdictKind = (v) => v.kind;
  return {
    name: s.name,
    critical: account.criticalWindow ? account.windows.findIndex((w) => w.window === account.criticalWindow) : -1,
    windows: account.windows.map((w) => ({
      id: w.window.id,
      efficiencyIndex: w.efficiencyIndex,
      projectedUsage: w.projectedUsage,
      daysUntilReset: w.daysUntilReset,
      workingDaysUntilReset: w.workingDaysUntilReset,
      autonomyWorkingDays: w.estimatedAutonomyWorkingDays,
      todayBudget: w.todayBudget,
      redistribution: w.redistribution,
      hourly: w.hourly,
      preliminary: w.preliminary,
      instantRate: w.instantRate,
      sustainableRate: w.sustainableRate,
      rating: w.efficiencyRating,
      verdict: verdictKind(w.verdict),
      chart: w.chart.map((c) => ({ date: c.date, delta: c.delta, fullShare: c.fullShare, dayUnit: c.dayUnit, budget: c.budget, upcoming: c.upcoming, overBudget: c.overBudget })),
      stats: { peak: w.deltaStats.peak, avg: w.deltaStats.avg, streak: w.deltaStats.streakUnderBudget },
      history: {
        daily: draft.dailyUsage.filter((h) => h.windowId === w.window.id).sort((a, b) => a.date.localeCompare(b.date))
          .map((h) => ({ date: h.date, used: h.used, dayStartUsed: h.dayStartUsed ?? null })),
        samples: draft.recentSamples.filter((r) => r.windowId === w.window.id).sort((a, b) => a.timestamp.localeCompare(b.timestamp))
          .map((r) => ({ at: r.timestamp.replace(/\.\d+Z$/, 'Z'), used: r.used })),
      },
    })),
  };
}

// The TypeScript path rounds what it stores or hands to the widget, the Go packages keep
// every value exact (I-4, docs/PACING.md §6): a TS value is the same number when it is the
// Go value rounded as TypeScript rounds that field (PRECISION).
function sameNumber(ts, go, field) {
  const close = (a, b) => Math.abs(a - b) <= EPSILON * Math.max(1, Math.abs(a), Math.abs(b));
  if (close(ts, go)) return true;
  const d = PRECISION[field];
  return d !== undefined && close(ts, Math.round(go * 10 ** d) / 10 ** d);
}

// The field name of a path: its last key, without indices.
const fieldOf = (p) => p.replace(/\[\d+\]/g, '').split('.').pop();

// Field paths of every difference between two normalized outputs.
function diff(a, b, at, out) {
  if (typeof a === 'number' && typeof b === 'number') {
    if (!sameNumber(a, b, fieldOf(at))) out.push({ path: at, ts: a, go: b });
  } else if (Array.isArray(a) && Array.isArray(b)) {
    if (a.length !== b.length) out.push({ path: `${at}.length`, ts: a.length, go: b.length });
    for (let i = 0; i < Math.min(a.length, b.length); i++) diff(a[i], b[i], `${at}[${i}]`, out);
  } else if (a && b && typeof a === 'object' && typeof b === 'object') {
    for (const key of new Set([...Object.keys(a), ...Object.keys(b)])) diff(a[key] ?? null, b[key] ?? null, at ? `${at}.${key}` : key, out);
  } else if (a !== b) {
    out.push({ path: at, ts: a, go: b });
  }
}

// Differences of one scenario against its list: unexplained, tracked, stale paths.
function compare(s, ts, go) {
  const diffs = [];
  diff(ts, go, '', diffs);
  const expected = (s.expectedDifferences ?? []).flatMap((e) => e.paths.map((p) => ({ path: p, ref: e.ref })));
  const find = (d) => expected.find((e) => e.path === d.path);
  const unexplained = diffs.filter((d) => !find(d));
  // Each listed path on its own: one that no longer differs would excuse a future error.
  const stale = expected.filter((e) => !diffs.some((d) => d.path === e.path));
  const tracked = diffs.filter((d) => find(d)?.ref.startsWith('porting-error'));
  return { diffs, find, unexplained, stale, tracked };
}

function main() {
  const files = scenarioFiles(process.argv.slice(2));
  if (files.length === 0) {
    console.error('No scenario (*.json) found in the given folders.');
    process.exit(2);
  }
  const scenarios = files.map((f) => JSON.parse(fs.readFileSync(f, 'utf8')));
  const go = spawnSync('go', ['run', './cmd/pacingparity', ...files.map((f) => path.resolve(f))], {
    cwd: path.join(root, 'gocore'), encoding: 'utf8', maxBuffer: 256 * 1024 * 1024,
  });
  if (go.status !== 0) {
    process.stderr.write(go.stderr);
    process.exit(2);
  }
  const goOutputs = JSON.parse(go.stdout);
  const budget = require(path.join(root, 'dist', 'budget.js'));
  const usage = require(path.join(root, 'dist', 'core', 'usage.js'));

  let failed = 0;
  let trackedTotal = 0;
  scenarios.forEach((s, i) => {
    const ts = runTypeScript(budget, usage, s);
    const { diffs, find, unexplained, stale, tracked } = compare(s, ts, goOutputs[i]);
    const status = unexplained.length === 0 && stale.length === 0 ? (tracked.length > 0 ? 'warn' : 'ok  ') : 'FAIL';
    console.log(`${status} ${s.name} (${diffs.length} differences, ${diffs.length - unexplained.length - tracked.length} explained, ${tracked.length} tracked porting errors)`);
    for (const d of diffs) {
      const e = find(d);
      console.log(`     ${e ? `[${e.ref}]` : '[UNEXPLAINED]'} ${d.path}: ts=${JSON.stringify(d.ts)} go=${JSON.stringify(d.go)}`);
    }
    for (const e of stale) console.log(`     [STALE] ${e.path} (${e.ref}) no longer differs: drop it`);
    trackedTotal += tracked.length;
    if (unexplained.length > 0 || stale.length > 0) failed++;
  });
  console.log(`${scenarios.length} scenarios, ${failed} failing, ${trackedTotal} differences tracked as porting errors`);
  process.exit(failed === 0 ? 0 : 1);
}

if (require.main === module) main();

module.exports = { compare, diff, sameNumber, runTypeScript, scenarioFiles };
