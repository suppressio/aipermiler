// main/usageReader.ts — what the widget, the tray and the notifications show, read from
// the core's service (#341, part of #181). The service reads the providers on its own
// loop, keeps the history and computes the pace; Electron only reads its pacing
// snapshot per account (main/serviceSnapshot.ts) and, for a refresh, asks the loop for
// a run (`POST /api/v1/refresh`). No history, no refresh policy, no pacing in Electron:
// core/usage.ts and main/refreshPolicy.ts are gone.
//
// Without a service (owner decision D1 on #181: a message only, autostart is #152) each
// account is read once through the CLI and shown with its raw values, no pacing, and
// `serviceDown` so the widget says why. Pure of Electron: main.ts injects the reads.

import { requireFresh, type CoreUsageResult } from './coreClient';
import { ServiceUnusableError, type UsageSourceRecord } from './serviceClient';
import type { UsageSourceReason } from '../types/index';
import {
  accountFromSnapshot,
  accountWithoutPacing,
  accountWithoutService,
  type AccountIdentity,
  type AccountProblem,
  type RefreshRequestMode,
  type ServiceSnapshotRead,
} from './serviceSnapshot';
import type { AccountConfig, AccountSnapshot, ClaudeLocalInsights, ProviderId, UsageSnapshot } from '../types/index';

export interface UsageReaderDeps {
  accounts(): AccountConfig[];
  isConnected(cfg: AccountConfig): boolean;
  chartDays(): 7 | 30;
  /** The account's pacing snapshot from the service (ServiceUnusableError without one). */
  snapshot(cfg: AccountConfig, chartDays: 7 | 30): Promise<ServiceSnapshotRead>;
  /** Asks the service's loop for a run and waits for it (ServiceUnusableError without one). */
  refresh(mode: RefreshRequestMode): Promise<void>;
  /** The CLI read of the account, only when no service runs. */
  cliUsage(cfg: AccountConfig): Promise<CoreUsageResult>;
  /** Where the account's last read came from (diagnostics). */
  recordSource(accountId: string, source: UsageSourceRecord): void;
  /** Local Claude Code insights for the account-level panel (context share, top tools). */
  localInsights(): Promise<ClaudeLocalInsights | null>;
  /** An error, a state the user acts on, or the service still reading the account. */
  describeProblem(err: unknown): AccountProblem;
  /** Called on every failed or stale read (format drift report). */
  onFetchError(provider: ProviderId, err: unknown): void;
  now(): Date;
  logError(message: string, ...details: unknown[]): void;
}

export interface UsageReader {
  /**
   * Reads every enabled, connected account. With a mode, the service's loop runs first
   * (the refresh button, the tray, a login or a settings change); without one it is a
   * plain read of what the service holds.
   */
  read(mode: RefreshRequestMode | null): Promise<UsageSnapshot>;
  /** Whether the last read found no service. */
  serviceDown(): boolean;
}

// The problem of a stale document (the last read failed, or the service has not read
// the account yet): the same error the CLI's stale answer stands for.
function staleProblem(deps: UsageReaderDeps, cfg: AccountConfig, result: CoreUsageResult): AccountProblem | undefined {
  try {
    requireFresh(result, cfg.provider);
    return undefined;
  } catch (err) {
    const problem = deps.describeProblem(err);
    if (!('refreshing' in problem)) deps.onFetchError(cfg.provider, err);
    return problem;
  }
}

export function createUsageReader(deps: UsageReaderDeps): UsageReader {
  let down = false;

  async function withoutService(cfg: AccountConfig, identity: AccountIdentity): Promise<AccountSnapshot> {
    try {
      const result = await deps.cliUsage(cfg);
      return accountWithoutService(identity, result, staleProblem(deps, cfg, result));
    } catch (err) {
      const problem = deps.describeProblem(err);
      deps.onFetchError(cfg.provider, err);
      deps.logError(`[main] ${cfg.label} unavailable:`, err instanceof Error ? err.message : err);
      return accountWithoutService(identity, null, problem);
    }
  }

  async function readAccount(cfg: AccountConfig, chartDays: 7 | 30, downReason: UsageSourceReason | null): Promise<{ account: AccountSnapshot; downReason: UsageSourceReason | null }> {
    const identity: AccountIdentity = { accountId: cfg.id, provider: cfg.provider, label: cfg.label };
    if (downReason === null) {
      try {
        const read = await deps.snapshot(cfg, chartDays);
        deps.recordSource(cfg.id, { source: 'service', reason: null });
        return { account: accountFromSnapshot(identity, read, staleProblem(deps, cfg, read.result)), downReason: null };
      } catch (err) {
        if (err instanceof ServiceUnusableError) {
          deps.recordSource(cfg.id, { source: 'cli', reason: err.reason });
          // An account the service does not serve (just added, not saved yet): only
          // that one goes through the CLI.
          return { account: await withoutService(cfg, identity), downReason: err.reason === 'unknown_account' ? null : err.reason };
        }
        // The service's own answer for the account (no good read yet): shown as is.
        deps.recordSource(cfg.id, { source: 'service', reason: null });
        const problem = deps.describeProblem(err);
        if (!('refreshing' in problem)) {
          deps.onFetchError(cfg.provider, err);
          deps.logError(`[main] ${cfg.label} unavailable and no previous data:`, err instanceof Error ? err.message : err);
        }
        return { account: accountWithoutPacing(identity, null, problem, false), downReason: null };
      }
    }
    deps.recordSource(cfg.id, { source: 'cli', reason: downReason });
    return { account: await withoutService(cfg, identity), downReason };
  }

  return {
    async read(mode) {
      if (mode !== null) {
        try {
          await deps.refresh(mode);
        } catch (err) {
          // No service: the accounts are read through the CLI below.
          if (!(err instanceof ServiceUnusableError)) throw err;
        }
      }
      const snapshot: UsageSnapshot = { generatedAt: deps.now().toISOString(), accounts: [] };
      const chartDays = deps.chartDays();
      let downReason: UsageSourceReason | null = null;
      for (const cfg of deps.accounts()) {
        if (!cfg.enabled || !deps.isConnected(cfg)) continue;
        // Once the service is found missing, the other accounts do not look for it again.
        const read = await readAccount(cfg, chartDays, downReason);
        downReason = downReason ?? read.downReason;
        const { account } = read;
        if (cfg.provider === 'claude' && cfg.localInsights) {
          const insights = await deps.localInsights();
          if (insights) account.localInsights = insights;
        }
        snapshot.accounts.push(account);
      }
      down = downReason !== null;
      return snapshot;
    },
    serviceDown: () => down,
  };
}
