// main/usageReader.ts — what the widget, the tray and the notifications show, read from
// the core's service (#341, part of #181). The service reads the providers on its own
// loop, keeps the history and computes the pace; Electron only reads its pacing
// snapshot per account (main/serviceSnapshot.ts) and, for a refresh, asks the loop for
// a run (`POST /api/v1/refresh`). No history, no refresh policy, no pacing in Electron
// (main/refreshPolicy.ts is gone, core/usage.ts is only the parity's reference).
//
// Without a service (owner decision D1 on #181: a message only, autostart is #152) the
// accounts are read through the CLI at the configured interval and shown with their raw
// values, no pacing, and `serviceDown` so the widget says why. A manual refresh reuses a
// CLI read under a minute old; a failed read keeps the last good values, stale. An
// account whose own answer the service cannot give (not served yet, refused, unreadable)
// is read the same way, alone. Pure of Electron: main.ts injects the reads.

import { requireFresh, type CoreUsageResult } from './coreClient';
import { ServiceUnusableError, type UsageSourceRecord } from './serviceClient';
import type { UsageSourceReason } from '../types/index';
import {
  accountFromSnapshot,
  accountWithoutPacing,
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
  /** The CLI read of the account, only when no service serves it. */
  cliUsage(cfg: AccountConfig): Promise<CoreUsageResult>;
  /** How often a plain read runs the CLI again (the configured refresh interval). */
  cliIntervalMs(): number;
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

// Reasons that concern the whole service (connectService): no service for any account.
// The others (`unknown_account`, `rejected`, `failed`, `malformed`) concern one
// account's answer: only that account goes through the CLI (#182 (d)).
const SERVICE_WIDE: readonly UsageSourceReason[] = ['not_running', 'unreachable', 'unauthorized', 'incompatible'];

// A manual refresh within this long of the last CLI read reuses it (the widget asks for
// one every time it is recreated), as the service's own manual cache does.
const MANUAL_CLI_CACHE_MS = 60 * 1000;
// A timer fires a little early: a read right at the interval still reads.
const SCHEDULE_TOLERANCE_MS = 15 * 1000;

interface CliEntry {
  // The last good result, kept to show stale when a later read fails (AGENTS.md).
  good: CoreUsageResult | null;
  problem: AccountProblem | undefined;
  at: number;
}

export function createUsageReader(deps: UsageReaderDeps): UsageReader {
  let down = false;
  const cli = new Map<string, CliEntry>();

  // Whether to run the CLI now or show its last answer: a forced refresh always reads,
  // a manual one unless the last read is a minute old, a plain read once per interval.
  function cliDue(entry: CliEntry | undefined, mode: RefreshRequestMode | null): boolean {
    if (!entry || mode === 'forced') return true;
    const age = deps.now().getTime() - entry.at;
    if (mode === 'manual') return age >= MANUAL_CLI_CACHE_MS;
    return age >= deps.cliIntervalMs() - SCHEDULE_TOLERANCE_MS;
  }

  async function withoutService(cfg: AccountConfig, identity: AccountIdentity, mode: RefreshRequestMode | null, serviceDown: boolean): Promise<AccountSnapshot> {
    const entry = cli.get(cfg.id);
    if (entry && !cliDue(entry, mode)) return accountWithoutPacing(identity, entry.good, entry.problem, serviceDown);
    const at = deps.now().getTime();
    try {
      const result = await deps.cliUsage(cfg);
      const problem = staleProblem(deps, cfg, result);
      cli.set(cfg.id, { good: problem ? entry?.good ?? result : result, problem, at });
      return accountWithoutPacing(identity, problem ? entry?.good ?? result : result, problem, serviceDown);
    } catch (err) {
      const problem = deps.describeProblem(err);
      deps.onFetchError(cfg.provider, err);
      deps.logError(`[main] ${cfg.label} unavailable:`, err instanceof Error ? err.message : err);
      const good = entry?.good ?? null;
      cli.set(cfg.id, { good, problem, at });
      return accountWithoutPacing(identity, good, problem, serviceDown);
    }
  }

  async function readAccount(cfg: AccountConfig, chartDays: 7 | 30, mode: RefreshRequestMode | null, downReason: UsageSourceReason | null): Promise<{ account: AccountSnapshot; downReason: UsageSourceReason | null }> {
    const identity: AccountIdentity = { accountId: cfg.id, provider: cfg.provider, label: cfg.label };
    if (downReason === null) {
      try {
        const read = await deps.snapshot(cfg, chartDays);
        deps.recordSource(cfg.id, { source: 'service', reason: null });
        cli.delete(cfg.id);
        return { account: accountFromSnapshot(identity, read, staleProblem(deps, cfg, read.result)), downReason: null };
      } catch (err) {
        if (err instanceof ServiceUnusableError) {
          deps.recordSource(cfg.id, { source: 'cli', reason: err.reason });
          const serviceDown = SERVICE_WIDE.includes(err.reason);
          // An answer about this account only (not served yet, refused, unreadable):
          // only this one goes through the CLI, without the "service not running" note.
          return { account: await withoutService(cfg, identity, mode, serviceDown), downReason: serviceDown ? err.reason : null };
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
    return { account: await withoutService(cfg, identity, mode, true), downReason };
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
        const read = await readAccount(cfg, chartDays, mode, downReason);
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
