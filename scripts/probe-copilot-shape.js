// scripts/probe-copilot-shape.js — prints the STRUCTURE of the copilot_internal/user
// response (field names + types, never values) for the token in .env.test, to check
// whether GitHub exposes AI credits for company seats again (see docs/RESEARCH.md §2.2
// addenda). Run locally, never paste the token anywhere:
//
//   node --env-file=.env.test scripts/probe-copilot-shape.js [--quota-values]
//
// Uses AIPERMILER_TEST_COPILOT_TOKEN, and AIPERMILER_TEST_COPILOT_HOST for a GitHub
// Enterprise Cloud tenant (e.g. acme.ghe.com; default github.com — same rule as
// services/githubHost.ts). Output is safe to share: same rule as services/_shape.ts
// (no numbers, strings or dates), plus the HTTP status.

const HOST = (process.env.AIPERMILER_TEST_COPILOT_HOST || 'github.com').trim().toLowerCase();
const VALID_HOST = /^(github\.com|[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.ghe\.com)$/;
const TIMEOUT_MS = 10_000;
const MAX_DEPTH = 8;
const CREDIT_HINT = /credit|quota|budget|spend|usage|limit|entitlement|remaining|consum/i;

function shapeOf(value, depth = 0) {
  if (depth > MAX_DEPTH) return 'truncated';
  if (value === null) return 'null';
  if (Array.isArray(value)) return value.length > 0 ? [shapeOf(value[0], depth + 1)] : [];
  if (typeof value === 'object') {
    const shape = {};
    for (const key of Object.keys(value).sort()) shape[key] = shapeOf(value[key], depth + 1);
    return shape;
  }
  return typeof value;
}

function hintedPaths(shape, prefix = '') {
  if (Array.isArray(shape)) return shape.length > 0 ? hintedPaths(shape[0], `${prefix}[]`) : [];
  if (shape === null || typeof shape !== 'object') return [];
  return Object.entries(shape).flatMap(([key, child]) => {
    const path = prefix ? `${prefix}.${key}` : key;
    return [...(CREDIT_HINT.test(key) ? [path] : []), ...hintedPaths(child, path)];
  });
}

async function main() {
  const token = process.env.AIPERMILER_TEST_COPILOT_TOKEN;
  if (!token) {
    console.error('Missing AIPERMILER_TEST_COPILOT_TOKEN — fill .env.test and run with --env-file=.env.test');
    process.exitCode = 1;
    return;
  }
  if (!VALID_HOST.test(HOST)) {
    console.error('AIPERMILER_TEST_COPILOT_HOST must be github.com or <tenant>.ghe.com');
    process.exitCode = 1;
    return;
  }
  const response = await fetch(`https://api.${HOST}/copilot_internal/user`, {
    headers: {
      Authorization: `Bearer ${token}`,
      Accept: 'application/vnd.github+json',
      'X-GitHub-Api-Version': '2022-11-28',
    },
    signal: AbortSignal.timeout(TIMEOUT_MS),
  });
  console.log(`HTTP ${response.status}`);
  const body = await response.json().catch(() => null);
  const shape = shapeOf(body);
  console.log(JSON.stringify(shape, null, 2));
  const hints = hintedPaths(shape);
  console.log(`\nFields whose name suggests credits/quota (${hints.length}):`);
  for (const path of hints) console.log(`  ${path}`);
  printSafeSummary(body);
  if (process.argv.includes('--quota-values')) printQuotaValues(body);
}

// Opt-in (--quota-values): the quota numbers of each snapshot, to debug how they are
// read. They are the user's own usage figures — fine to share in a private chat, never
// in a public issue. No identifiers, login, dates or token.
const QUOTA_FIELDS = ['unlimited', 'has_quota', 'entitlement', 'quota_remaining', 'remaining', 'percent_remaining',
  'credits_used', 'overage_count', 'overage_permitted', 'token_based_billing'];

function printQuotaValues(body) {
  const snapshots = body && typeof body === 'object' ? body.quota_snapshots : null;
  if (snapshots === null || typeof snapshots !== 'object') return;
  console.log('\nQuota values (--quota-values, do NOT paste in public issues):');
  for (const [name, snap] of Object.entries(snapshots)) {
    if (snap === null || typeof snap !== 'object') continue;
    const fields = QUOTA_FIELDS.map((key) => `${key}=${JSON.stringify(snap[key])}`).join(' ');
    console.log(`  ${name}: ${fields}`);
  }
}

// Plan names and yes/no flags only: never amounts, percentages, dates or identifiers.
function printSafeSummary(body) {
  if (body === null || typeof body !== 'object') return;
  const label = (value) => (typeof value === 'string' && /^[a-z0-9_.-]{1,40}$/i.test(value) ? value : typeof value);
  const flag = (value) => (typeof value === 'boolean' ? String(value) : typeof value);
  const positive = (value) => (typeof value === 'number' ? String(value > 0) : typeof value);
  console.log('\nSafe summary (plan names and yes/no flags only):');
  console.log(`  copilot_plan: ${label(body.copilot_plan)}`);
  console.log(`  access_type_sku: ${label(body.access_type_sku)}`);
  console.log(`  token_based_billing: ${flag(body.token_based_billing)}`);
  console.log(`  organizations listed: ${Array.isArray(body.organization_list) ? body.organization_list.length > 0 : 'n/a'}`);
  const snapshots = body.quota_snapshots;
  if (snapshots === null || typeof snapshots !== 'object') return;
  for (const [name, snap] of Object.entries(snapshots)) {
    if (snap === null || typeof snap !== 'object') continue;
    console.log(
      `  ${name}: unlimited=${flag(snap.unlimited)} has_quota=${flag(snap.has_quota)} ` +
      `entitlement>0=${positive(snap.entitlement)} credits_used>0=${positive(snap.credits_used)} ` +
      `overage_permitted=${flag(snap.overage_permitted)}`,
    );
  }
}

main().catch((error) => {
  console.error(`Request failed: ${error instanceof Error ? error.name : 'unknown error'}`);
  process.exitCode = 1;
});
