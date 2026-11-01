// main/coreRequests.ts — the provider flags of the core commands, built from an account
// config (pure: no Electron, tested in coreRequests.test.ts). Account parameters only:
// secrets never go in argv (docs/CORE-CLI.md).

import type { CodexAccountSettings, CopilotAccountSettings } from '../types/index';

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

/** `usage codex`: only the optional binary path. */
export function codexUsageRequest(cfg: CodexAccountSettings): CoreProviderRequest {
  return { provider: 'codex', flags: cfg.binaryPath ? ['--codex-bin', cfg.binaryPath] : [] };
}
