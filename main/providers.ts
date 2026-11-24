// main/providers.ts — provider-specific part of the account registry (issue #4,
// docs/EVOLUTION.md point 3). main.ts works only on generic AccountConfig values and
// delegates here everything that depends on the provider: connection state,
// credentials for the service, disconnection, redaction of secrets towards the
// renderer. Codex (#178) and Copilot (#179) are read by the Go core CLI (main/core.ts):
// Codex through the user's own Codex sign-in, Copilot with the token the core keeps in
// the OS keyring. No provider secret lives in the app any more, except a Copilot token
// of an earlier version until main.ts moves it to the keyring. Claude (#180) is read by
// the core through the user's own Claude Code sign-in (`claude /usage` in a PTY, #147),
// never a claude.ai session (#140). Usage is read from the core's always-on service when
// one runs, the CLI being the fallback for that read (#182, main/serviceClient.ts); the
// source of each account's last read is kept for diagnostics. Adding a provider = a new branch in each function below (the exhaustive
// `switch` on `provider` makes tsc fail when one is forgotten).

import { core, coreService } from './core';
import { requireFresh, type CoreUsageResult } from './coreClient';
import { oneAtATime } from './oneAtATime';
import { readUsageWithFallback, type UsageSourceRecord } from './serviceClient';
import type { AccountRead } from '../core/usage';
import { claudeUsageRequest, codexUsageRequest, copilotAccountFlags, copilotUsageRequest } from './coreRequests';
import { clearClaudePartition } from './claude-auth';
import { PROVIDER_DISPLAY_NAMES } from '../store/migrate';
import type { AccountConfig, AccountId, ProviderId } from '../types/index';

const SECRET_PLACEHOLDER = '••••••••';

export function providerDisplayName(provider: ProviderId): string {
  return PROVIDER_DISPLAY_NAMES[provider];
}

export function isConnected(cfg: AccountConfig): boolean {
  switch (cfg.provider) {
    // Nothing to connect: the core uses the Codex sign-in of this computer (#178).
    case 'codex': return true;
    // Nothing to connect: the core uses the Claude Code sign-in of this computer (#180).
    case 'claude': return true;
    // Connected once the core holds its token (username set at connect), or while a
    // token of an earlier version waits to be moved to the keyring.
    case 'copilot': return !!cfg.credentials.username || !!cfg.credentials.token;
  }
}

/** Label of the Codex source in the diagnostic report. */
export const CODEX_CORE_ENDPOINT_LABEL = 'core: codex app-server account/rateLimits/read';

/**
 * Claude reads one at a time (#180): every read starts a Claude Code session in the
 * core's single probe folder, and the diagnostic report reads outside the refresh queue.
 * Two probes at once would share that folder; the core refuses a non-empty one, but two
 * starting together could both pass the check.
 */
const oneClaudeReadAtATime = oneAtATime();

/** Label of the Claude source in the diagnostic report. */
export const CLAUDE_CORE_ENDPOINT_LABEL = 'core: claude /usage status line rate_limits';


/** The CLI read of the account's usage (the fallback when no service serves it). */
function cliUsage(cfg: AccountConfig): Promise<CoreUsageResult> {
  switch (cfg.provider) {
    case 'codex':
      return core.usage(codexUsageRequest(cfg));
    case 'claude':
      return oneClaudeReadAtATime(() => core.usage(claudeUsageRequest(cfg)));
    case 'copilot':
      return core.usage(copilotUsageRequest(cfg));
  }
}

// Where each account's last usage read came from (memory only): Settings → Diagnostics
// and the diagnostic report.
const usageSources = new Map<AccountId, UsageSourceRecord>();

export function lastUsageSource(accountId: AccountId): UsageSourceRecord | null {
  return usageSources.get(accountId) ?? null;
}

/**
 * The account's usage document from the core's service when one serves it, else from
 * the CLI. While the service serves an account the app never probes it itself (#182 (d)):
 * the service's own errors are the account's answer.
 */
function readUsage(cfg: AccountConfig): Promise<{ result: CoreUsageResult; source: UsageSourceRecord['source'] }> {
  return readUsageWithFallback(coreService, cfg.id, cfg.provider, () => cliUsage(cfg), (source) => { usageSources.set(cfg.id, source); });
}

/**
 * The account's fresh usage. A service document carries the time its loop read it
 * (core/usage.ts records a sample only when that is newer, #182 (a)); a CLI read is now.
 */
export async function fetchUsage(cfg: AccountConfig): Promise<AccountRead> {
  const { result, source } = await readUsage(cfg);
  return { usage: requireFresh(result, cfg.provider), fetchedAt: source === 'service' ? result.fetchedAt : null };
}

/**
 * The raw usage response of the account, for the manual "report response" diagnostic
 * (main.ts, diagnostics/githubIssue.ts). Copilot reads several endpoints with
 * fallbacks: its main source, copilot_internal/user, is reported.
 */
export async function fetchRawResponse(cfg: AccountConfig): Promise<{ endpointLabel: string; response: unknown }> {
  switch (cfg.provider) {
    case 'codex':
      // The core's reading (contract v1 document), not the raw app-server answer: the
      // core never forwards it. Redacted like any response by the report. From the
      // service when it serves the account, so the report never starts a probe of its own.
      return { endpointLabel: CODEX_CORE_ENDPOINT_LABEL, response: (await readUsage(cfg)).result };
    case 'claude':
      // The core's reading too: the status line JSON stays inside the core.
      return { endpointLabel: CLAUDE_CORE_ENDPOINT_LABEL, response: (await readUsage(cfg)).result };
    case 'copilot':
      // Through the CLI: a GitHub API call, no probe to share with the service.
      // copilot_internal/user, the main source of both scopes, raw (redacted by the report).
      return core.raw({ provider: 'copilot', flags: copilotAccountFlags(cfg) });
  }
}

/**
 * Removes credentials and, for Claude, the partition cookies — not only the
 * sessionKey in the store (cause of issue #4). Returns the updated, disabled config: a
 * disconnected account stays disconnected until "Connect" is done again.
 */
export async function disconnect(cfg: AccountConfig): Promise<AccountConfig> {
  switch (cfg.provider) {
    // Nothing stored to remove: signing out of Codex is done in Codex itself.
    case 'codex':
      return { ...cfg, enabled: false };
    case 'claude':
      await clearClaudePartition(cfg.partition);
      return { ...cfg, enabled: false, session: { sessionKey: null, organizationId: null, capturedAt: null, expiresAt: null } };
    case 'copilot':
      // The token lives in the core's keyring: removed there. A failure (keyring not
      // reachable) is thrown, so the account does not look disconnected while its token
      // stays stored. oauthApp.clientId is kept: not a secret, handy to reconnect.
      // A never-connected account has nothing to remove (no core call needed to delete it).
      if (cfg.credentials.username || cfg.credentials.token) {
        await core.tokenDelete({ provider: 'copilot', flags: copilotAccountFlags(cfg) });
      }
      return { ...cfg, enabled: false, credentials: { token: null, username: null } };
  }
}

/** Replaces secrets with a placeholder before crossing the IPC boundary towards the renderer. */
export function redactSecrets(cfg: AccountConfig): AccountConfig {
  switch (cfg.provider) {
    case 'codex': return cfg;
    case 'claude':
      return { ...cfg, session: { ...cfg.session, sessionKey: cfg.session.sessionKey ? SECRET_PLACEHOLDER : null } };
    case 'copilot':
      return { ...cfg, credentials: { ...cfg.credentials, token: cfg.credentials.token ? SECRET_PLACEHOLDER : null } };
  }
}

/**
 * Secrets change only through the dedicated flows (connect/disconnect), never through
 * the generic Settings save: the config coming from the renderer contains the
 * placeholder, replaced here by the current real value of the same account (by id).
 * `partition` is also owned by the main process only. An unknown account (id not
 * present) is dropped: accounts are created only via accounts:add, never invented in
 * a patch.
 */
export function preserveSecrets(incoming: AccountConfig, current: AccountConfig | undefined): AccountConfig | null {
  if (!current || current.provider !== incoming.provider) return null;
  switch (incoming.provider) {
    case 'codex': {
      const cur = current as typeof incoming;
      return { ...incoming, partition: cur.partition };
    }
    case 'claude': {
      const cur = current as typeof incoming;
      return { ...incoming, partition: cur.partition, session: { ...cur.session } };
    }
    case 'copilot': {
      const cur = current as typeof incoming;
      return { ...incoming, credentials: { ...cur.credentials } };
    }
  }
}
