// diagnostics/githubIssue.ts — builds the URL of a pre-filled GitHub issue when a
// service detects that an endpoint format changed (see services/_shape.ts, main.ts).
// Pure function, no Electron dependency: actually opening the browser
// (shell.openExternal) stays in main.ts. The issue goes to a public repository, so its
// text is in English regardless of the UI language.
//
// It deliberately does NOT automate issue creation through the GitHub API: it only
// opens a pre-filled draft that the user must review and submit — an explicit choice
// never to publish real data without a confirming human click (see CLAUDE.md).

import type { ProviderId } from '../types/index';

const REPO_OWNER = 'suppressio';
const REPO_NAME = 'aipermiler';

export interface FormatDriftIssueParams {
  provider: ProviderId;
  endpointLabel: string;
  shape: unknown;
}

export function buildFormatDriftIssueUrl({ provider, endpointLabel, shape }: FormatDriftIssueParams): string {
  const serviceName = { claude: 'Claude', copilot: 'Copilot', codex: 'Codex' }[provider];
  const title = `Response format changed: ${serviceName} (${endpointLabel})`;

  const body = [
    `Detected automatically by AIpermiler on ${new Date().toISOString()}.`,
    '',
    `Service: **${serviceName}**`,
    `Endpoint: \`${endpointLabel}\``,
    '',
    'Structure of the received response — field names and types only, **never real values** ' +
      '(usage percentages, amounts and renewal dates are never included here, see CLAUDE.md):',
    '',
    '```json',
    JSON.stringify(shape, null, 2),
    '```',
    '',
    `Previously expected format: see \`docs/RESEARCH.md\` and \`services/${provider}.ts\`.`,
    '',
    '_Draft generated automatically — review the content before submitting._',
  ].join('\n');

  const url = new URL(`https://github.com/${REPO_OWNER}/${REPO_NAME}/issues/new`);
  url.searchParams.set('title', title);
  url.searchParams.set('body', body);
  url.searchParams.set('labels', 'api-drift,auto-generated');
  return url.toString();
}

// ---------------------------------------------------------------------------
// Manual diagnostic report (Settings → Diagnostics): unlike the automatic format-drift
// draft above, the report FILE (diagnostics/report.ts) carries the response VALUES,
// because deciding how to read a field (e.g. two objects that may report the same
// budget) needs them. The issue draft only lists the file, which the user attaches by
// hand — or never: nothing is sent by the app. Strings are redacted (names, free
// text) except dates and a few enum-like fields; ids are redacted whatever their type.
// ---------------------------------------------------------------------------

// Enum-like string fields whose value says what a field means, not who the user is.
const KEPT_STRING_KEYS = new Set([
  'currency', 'severity', 'type', 'status', 'kind', 'unit', 'period', 'interval',
  'access_type_sku', 'copilot_plan', 'quota_id',
]);
const MAX_KEPT_STRING_LENGTH = 32;
const ISO_DATE = /^\d{4}-\d{2}-\d{2}(T[\d:.]+(Z|[+-]\d{2}:\d{2})?)?$/;
// Dates are kept only under keys about resets and timing (resets_at, quota_reset_date,
// timestamp_utc, as_of, window_started_at…): they are what pacing reads. Any other
// date (e.g. when a seat was assigned) says something about the user, not the usage.
const TIME_KEY = /reset|time|^as_of$|_at$/i;
// `id`, `user_id`, `enterpriseId`…: a numeric id identifies an account as well as a
// string one (a GitHub user id leads to the login).
const ID_KEY = /^id$|_id$|[a-z]Id$/;
const MAX_REDACT_DEPTH = 8;

/**
 * Copy of a response safe to share: numbers, booleans, null and reset/timing ISO dates kept;
 * strings kept only under KEPT_STRING_KEYS and when short; any other string replaced
 * by its length; any value under an id key replaced. Field names are kept (they are
 * the format).
 */
export function redactResponse(value: unknown, key = '', depth = 0): unknown {
  if (depth > MAX_REDACT_DEPTH) return '<truncated>';
  if (value === null) return null;
  // An enum-like value first: `quota_id` names a quota ("chat"), it is not an id.
  if (typeof value === 'string' && KEPT_STRING_KEYS.has(key) && value.length <= MAX_KEPT_STRING_LENGTH) return value;
  if (ID_KEY.test(key) && typeof value !== 'object') return '<id>';
  if (typeof value === 'number' || typeof value === 'boolean') return value;
  if (typeof value === 'string') {
    if (ISO_DATE.test(value)) return TIME_KEY.test(key) ? value : '<date>';
    return `<string, ${String(value.length)} chars>`;
  }
  if (Array.isArray(value)) return value.map((item) => redactResponse(item, key, depth + 1));
  if (typeof value === 'object') {
    const record = value as Record<string, unknown>;
    return Object.fromEntries(Object.keys(record).sort().map((k) => [k, redactResponse(record[k], k, depth + 1)]));
  }
  return `<${typeof value}>`;
}

/**
 * Only the first part of an error message: _http.ts appends the response body after
 * " — ", which may be anything (e.g. a Cloudflare page).
 */
export function shortError(message: string, maxLength = 160): string {
  return (message.split(' — ')[0] ?? message).slice(0, maxLength);
}

export interface ReportIssueParams {
  appVersion: string;
  fileName: string;
  // Neutral account names ("Claude #1"), as in the report file.
  accounts: string[];
}

/** Short issue draft pointing to the report file the user attaches by hand. */
export function buildReportIssueUrl({ appVersion, fileName, accounts }: ReportIssueParams): string {
  const body = [
    `Diagnostic report from AIpermiler ${appVersion}, accounts: ${accounts.join(', ') || 'none'}.`,
    '',
    `**Attach the file \`${fileName}\`** (saved in your Downloads folder) by dragging it here.`,
    '',
    'It contains real usage values (percentages, amounts, reset dates) and the settings that affect pacing; ' +
      'names, ids, credentials and free text are redacted. Review it before submitting, or do not submit it at all.',
  ].join('\n');
  const url = new URL(`https://github.com/${REPO_OWNER}/${REPO_NAME}/issues/new`);
  url.searchParams.set('title', `Diagnostic report (${appVersion})`);
  url.searchParams.set('body', body);
  url.searchParams.set('labels', 'response-report');
  return url.toString();
}
