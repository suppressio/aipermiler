// main/coreRequests.ts — the provider flags of the core commands, built from an account
// config (pure: no Electron, tested in coreRequests.test.ts). Account parameters only:
// secrets never go in argv (docs/CORE-CLI.md).

import type { AccountConfig, ClaudeAccountSettings, CodexAccountSettings, CopilotAccountSettings } from '../types/index';

export interface CoreProviderRequest {
  provider: string;
  flags: string[];
}

/** The account flags of every Copilot command: the keyring entry and the GitHub host. */
export function copilotAccountFlags(cfg: Pick<CopilotAccountSettings, 'id' | 'host'>): string[] {
  return ['--account', cfg.id, '--host', cfg.host];
}

/**
 * `usage copilot`: account flags, the scope and, for the personal scope, the manual
 * quota (the total of the billing-report fallback; a company seat never reads it).
 */
export function copilotUsageRequest(cfg: CopilotAccountSettings): CoreProviderRequest {
  const flags = [...copilotAccountFlags(cfg), '--scope', cfg.accountScope];
  if (cfg.accountScope === 'personal') flags.push('--manual-quota', String(cfg.manualQuota));
  return { provider: 'copilot', flags };
}

/** `usage claude`: only the optional binary path (#180). */
export function claudeUsageRequest(cfg: ClaudeAccountSettings): CoreProviderRequest {
  return { provider: 'claude', flags: cfg.binaryPath ? ['--claude-bin', cfg.binaryPath] : [] };
}

/** `usage codex`: only the optional binary path. */
export function codexUsageRequest(cfg: CodexAccountSettings): CoreProviderRequest {
  return { provider: 'codex', flags: cfg.binaryPath ? ['--codex-bin', cfg.binaryPath] : [] };
}

/**
 * Whether an account kept across a settings save changed its Claude Code or Codex binary
 * path (#242): its last result, often a "not found" error, no longer applies, so the
 * app re-reads at once instead of waiting for the next tick or the manual-refresh cache.
 */
export function binaryPathChanged(before: readonly AccountConfig[], after: readonly AccountConfig[]): boolean {
  const previous = new Map<string, string | null>();
  for (const a of before) if (a.provider !== 'copilot') previous.set(a.id, a.binaryPath);
  return after.some((a) => a.provider !== 'copilot' && previous.has(a.id) && previous.get(a.id) !== a.binaryPath);
}
