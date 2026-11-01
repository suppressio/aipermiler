// diagnostics/githubIssue.test.ts — checks that the automatically generated GitHub
// issue draft never contains real values, only the response structure (see
// services/_shape.ts, CLAUDE.md). No network call: buildFormatDriftIssueUrl is a pure
// function that only builds the URL, opening the browser stays in main.ts.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { buildFormatDriftIssueUrl, buildReportIssueUrl, redactResponse, shortError } from './githubIssue';
import { extractShape } from '../services/_shape';

test('buildFormatDriftIssueUrl points to the right repo with new issue', () => {
  const url = buildFormatDriftIssueUrl({ provider: 'claude', endpointLabel: 'x', shape: {} });
  assert.match(url, /^https:\/\/github\.com\/suppressio\/aipermiler\/issues\/new\?/);
});

test('buildFormatDriftIssueUrl includes a title with the readable service name', () => {
  const urlClaude = buildFormatDriftIssueUrl({ provider: 'claude', endpointLabel: 'ep', shape: {} });
  const urlCopilot = buildFormatDriftIssueUrl({ provider: 'copilot', endpointLabel: 'ep', shape: {} });
  const titleClaude = new URL(urlClaude).searchParams.get('title') || '';
  const titleCopilot = new URL(urlCopilot).searchParams.get('title') || '';
  assert.match(titleClaude, /Claude/);
  assert.match(titleCopilot, /Copilot/);
});

test('buildFormatDriftIssueUrl sets the expected labels', () => {
  const url = buildFormatDriftIssueUrl({ provider: 'claude', endpointLabel: 'ep', shape: {} });
  assert.equal(new URL(url).searchParams.get('labels'), 'api-drift,auto-generated');
});

test('buildFormatDriftIssueUrl never contains real values, only the reduced shape', () => {
  const shape = extractShape({ used_dollars: 389.19, limit_dollars: 1000, resets_at: '2026-09-13T14:38:47.107149+00:00' });
  const url = buildFormatDriftIssueUrl({
    provider: 'claude',
    endpointLabel: 'claude.ai/api/organizations/{id}/usage',
    shape,
  });
  const body = new URL(url).searchParams.get('body') || '';
  assert.ok(!body.includes('389.19'));
  assert.ok(!body.includes('1000'));
  assert.ok(!body.includes('2026-09-13'));
  assert.ok(body.includes('"used_dollars": "number"'));
});

test('buildFormatDriftIssueUrl includes a manual review reminder in the body', () => {
  const url = buildFormatDriftIssueUrl({ provider: 'copilot', endpointLabel: 'ep', shape: {} });
  const body = new URL(url).searchParams.get('body') || '';
  assert.match(body, /review the content before submitting/i);
});

// ---------------------------------------------------------------------------
// Manual response report: values kept, strings redacted.
// ---------------------------------------------------------------------------

test('redactResponse keeps numbers, booleans, null, dates and enum-like fields; redacts other strings', () => {
  const response = {
    extra_usage: { utilization: 33.3, is_enabled: true, monthly_limit: null, resets_at: '2026-11-01T00:00:00+00:00' },
    spend: { percent: 33.3, used: { amount_minor: 3330, currency: 'USD', exponent: 2 }, severity: 'normal', disclaimer: 'Some long free text' },
    organization_uuid: '0b8c1d2e-aaaa-bbbb-cccc-1234567890ab',
    limits: [{ name: 'Jane Doe', value: 3 }],
  };
  assert.deepEqual(redactResponse(response), {
    extra_usage: { is_enabled: true, monthly_limit: null, resets_at: '2026-11-01T00:00:00+00:00', utilization: 33.3 },
    limits: [{ name: '<string, 8 chars>', value: 3 }],
    organization_uuid: '<string, 36 chars>',
    spend: {
      disclaimer: '<string, 19 chars>',
      percent: 33.3,
      severity: 'normal',
      used: { amount_minor: 3330, currency: 'USD', exponent: 2 },
    },
  });
  // An enum-like key with a long value is redacted all the same.
  assert.equal(redactResponse('x'.repeat(40), 'type'), '<string, 40 chars>');
});

test('redactResponse redacts ids whatever their type, keeps diagnostic enums', () => {
  const response = {
    id: 1234567,
    enterprise_list: [{ id: 7 }],
    analytics_tracking_id: 'abc',
    organizationId: 'org-1',
    access_type_sku: 'free_limited_copilot',
    copilot_plan: 'business',
    quota_snapshots: { premium_interactions: { quota_id: 'premium_interactions', entitlement: 300, credits_used: 12 } },
    endpoints: { api: 'https://api.example.test' },
    nothing: null,
  };
  assert.deepEqual(redactResponse(response), {
    access_type_sku: 'free_limited_copilot',
    analytics_tracking_id: '<id>',
    copilot_plan: 'business',
    endpoints: { api: '<string, 24 chars>' },
    enterprise_list: [{ id: '<id>' }],
    id: '<id>',
    nothing: null,
    organizationId: '<id>',
    quota_snapshots: { premium_interactions: { credits_used: 12, entitlement: 300, quota_id: 'premium_interactions' } },
  });
});

test('redactResponse keeps reset/timing dates only', () => {
  assert.deepEqual(redactResponse({
    resets_at: '2026-07-20T00:00:00Z', quota_reset_date: '2026-08-01', timestamp_utc: '2026-07-13T10:00:00.000Z',
    as_of: '2026-07-13T10:00:00Z', window_started_at: '2026-07-10T00:00:00Z', assigned_date: '2026-01-15T09:00:00+01:00',
  }), {
    as_of: '2026-07-13T10:00:00Z', assigned_date: '<date>', quota_reset_date: '2026-08-01', resets_at: '2026-07-20T00:00:00Z',
    timestamp_utc: '2026-07-13T10:00:00.000Z', window_started_at: '2026-07-10T00:00:00Z',
  });
});

test('shortError drops the response body appended after " — "', () => {
  assert.equal(shortError('x answered 401 Unauthorized — <html>secret</html>'), 'x answered 401 Unauthorized');
});

test('buildReportIssueUrl: short draft naming the file to attach and the neutral accounts', () => {
  const url = buildReportIssueUrl({ appVersion: '0.4.11-beta', fileName: 'aipermiler-report-x.txt', accounts: ['Claude #1', 'GitHub Copilot #1'] });
  assert.match(url, /^https:\/\/github\.com\/suppressio\/aipermiler\/issues\/new\?/);
  const params = new URL(url).searchParams;
  const body = params.get('body') ?? '';
  assert.ok(body.includes('aipermiler-report-x.txt'));
  assert.ok(body.includes('Claude #1, GitHub Copilot #1'));
  assert.match(body, /real usage values/);
  assert.equal(params.get('labels'), 'response-report');
  assert.ok(url.length < 2000);
});

test('redactResponse removes Codex wham usage identity fields', () => {
  assert.deepEqual(redactResponse({ email: 'synthetic@example.invalid', user_id: 'synthetic-user', account_id: 12345, rate_limit: { primary_window: { used_percent: 25, reset_at: 1792320000 } } }), {
    email: '<string, 25 chars>', user_id: '<id>', account_id: '<id>',
    rate_limit: { primary_window: { used_percent: 25, reset_at: 1792320000 } },
  });
});
