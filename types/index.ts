// types/index.ts — types shared by the main process (Node) and the renderer (browser).
// Type declarations only: no runtime code, so it is safe to import (with
// `import type`) from both contexts without any real coupling. The renderer build
// (tsconfig.renderer.json) compiles this file too, so it must stay runtime-free: its
// emitted ES module would overwrite the CommonJS one of the main build.
// IPC channels and the preload bridge live in ./ipc.ts.

// Id of an account INSTANCE (e.g. 'claude', 'copilot' for accounts migrated from
// the two-slot schema, a generated id for those added later) — no longer the provider
// name: with N accounts (issue #4) two Claude accounts have different ids.
export type AccountId = string;
export type ProviderId = 'claude' | 'copilot' | 'codex';
export type AccountScope = 'personal' | 'organization';
export type DayStatus = 'full' | 'half' | 'off';
export type WindowStyle = 'filled' | 'filled-dark' | 'transparent-digital';
export type ChartRange = 'week' | 'month';
// 'auto' follows the system language (Italian for it-*, English otherwise).
export type LanguageSetting = 'auto' | 'en' | 'it';

export interface RenewalRule {
  type: 'dayOfMonth' | 'rrule';
  day?: number;
  rrule?: string;
}

export interface WorkScheduleDays {
  mon: DayStatus;
  tue: DayStatus;
  wed: DayStatus;
  thu: DayStatus;
  fri: DayStatus;
  sat: DayStatus;
  sun: DayStatus;
}

export interface WorkSchedule {
  // When false, pacing ignores `days` and treats every calendar day as a full
  // working day (see budget.getDayUnit) — meant for a personal account, where limiting
  // to specific days/hours of the week makes no sense, unlike a company account (user
  // feedback).
  enabled: boolean;
  days: WorkScheduleDays;
  // Hours of a full working day: today's elapsed part is the span worked today, from
  // the day's samples, over these hours (budget.todayActivitySpan/todayElapsedUnits),
  // and the sustainable %/h is spread over remaining working hours.
  hoursPerDay: number;
}

/** A single quota window (see docs/ARCHITECTURE.md §0). */
export interface QuotaWindow {
  id: string;
  label: string;
  periodType: 'rolling-hours' | 'rolling-days' | 'billing-cycle';
  // Hours (rolling-hours), days (rolling-days) or months (billing-cycle); null when
  // unknown — such a window gets no pacing (see budget.hasPacing).
  periodLength: number | null;
  unit: 'percentage' | 'count';
  used: number;
  total: number | null;
  resetsAt: Date | string | null;
}

/** Raw usage of an account: contract v1 of the Go core (docs/CORE-CLI.md, main/coreClient.ts). */
export interface RawAccountUsage {
  planTier: string | null;
  subscriptionRenewsAt: Date | string | null;
  quotaWindows: QuotaWindow[];
}

export interface DailyUsagePoint {
  date: string;
  accountId: AccountId;
  windowId: string;
  used: number;
  // Consumption at the start of the day (see budget.updateDailyPoint). Missing on
  // points recorded before it existed.
  dayStartUsed?: number;
  meta?: Record<string, unknown>;
}

/** Today's budget vs today's consumption, quota percentage points (budget.todayBudget). */
export interface TodayBudget {
  budget: number;
  usedToday: number;
}

/**
 * The remaining quota spread over the working units from today on (perUnit, % per
 * full working day) next to the even share of the whole period (idealPerUnit) — see
 * budget.redistributedQuota.
 */
export interface Redistribution {
  perUnit: number;
  idealPerUnit: number;
  unitsLeft: number;
}

/**
 * Raw sample with a precise timestamp (not a calendar date), used only by the
 * instant consumption gauge (see budget.instantaneousRate). Unlike DailyUsagePoint,
 * every successful refresh appends a point here, while DailyUsagePoint keeps one value
 * per day (overwritten on every refresh).
 */
export interface RecentUsageSample {
  timestamp: string;
  accountId: AccountId;
  windowId: string;
  used: number;
}

/** Star efficiency rating (see budget.efficiencyRating), over a moving window of days. */
export interface EfficiencyRating {
  stars: number;
  avgRatio: number;
}

/** Consumption of one day vs the ideal share — see budget.dailyDeltas. */
export interface DailyDelta {
  date: string;
  delta: number | null;
  idealShare: number | null;
}

/**
 * One slot of the daily chart — see budget.chartDays:
 * - `delta`: that day's consumption (null: no data, reset in between, or a day to come);
 * - `fullShare`: the even share of a full working day (100 / working units of the
 *   period), null without pacing or outside the current period; `dayUnit` (1/0.5/0) says how much of it is a working
 *   share that day — the thin bar, coloured for the working part, grey for the rest;
 * - `budget`: the moving budget (dashed line) — for a past day what was available that
 *   morning, for today today's budget, for a day to come the remaining quota
 *   redistributed; null when unknown (other period, no pacing);
 * - `upcoming`: a day after today;
 * - `overBudget`: the day used more than its limit — the moving budget, or the even
 *   share of the day where that is unknown (bar colour, streak under budget).
 */
export interface ChartDay {
  date: string;
  delta: number | null;
  fullShare: number | null;
  dayUnit: number;
  budget: number | null;
  upcoming: boolean;
  overBudget: boolean;
}

/** Daily consumption statistics — see budget.deltaStats. */
export interface DeltaStats {
  peak: number | null;
  avg: number | null;
  streakUnderBudget: number | null;
}

/** Short verdict of a window — see budget.windowVerdict. */
export interface WindowVerdict {
  kind: 'exhausted' | 'at-risk' | 'behind' | 'on-track' | 'ahead' | 'no-pacing';
  autonomyWorkingDays?: number;
  projectedUsage?: number;
  // From the redistribution (budget.windowVerdict), when the verdict rests on it.
  perUnit?: number;
  idealPerUnit?: number;
  // At risk because the current pace (% per working day) is well above `perUnit`.
  pacePerUnit?: number;
  // Rolling-hours windows: hours of autonomy at the instant pace (budget.hourlyOutlook).
  autonomyHours?: number;
}

/**
 * A window of a few hours (rolling-hours) read in hours, not working days — see
 * budget.hourlyOutlook.
 */
export interface HourlyOutlook {
  hoursLeft: number;
  projectedAtReset: number;
  // Hours to 100% at the instant pace; null when nothing is being consumed.
  autonomyHours: number | null;
}

/**
 * Daily tip as data (see budget.generateDailyTip): a message key plus the
 * numbers it states; the renderer renders it in the active language.
 */
export interface DailyTip {
  key: 'none' | 'autonomy' | 'rating' | 'nearReset' | 'nearResetToday' | 'cause';
  params: Record<string, number>;
}

/**
 * Metrics computed for ONE quota window (see main.ts, computeWindowSnapshot).
 * An account can have several active windows at the same time (e.g. Claude: standard
 * limit + one-off extra credit) — the widget lists them separately instead of
 * flattening them to a single "critical window".
 */
export interface QuotaWindowSnapshot {
  window: QuotaWindow;
  dailyHistory: DailyUsagePoint[];
  // Daily consumption vs budget (widget chart, budget.chartDays) and related statistics.
  chart: ChartDay[];
  deltaStats: DeltaStats;
  verdict: WindowVerdict;
  efficiencyIndex: number | null;
  projectedUsage: number | null;
  daysUntilReset: number | null;
  workingDaysUntilReset: number | null;
  estimatedAutonomyWorkingDays: number | null;
  // Today's budget and consumption (budget.todayBudget), null without pacing.
  todayBudget: TodayBudget | null;
  // The remaining quota per working day vs the even share (budget.redistributedQuota),
  // null without pacing or on rolling-hours windows. Drives the verdict.
  redistribution: Redistribution | null;
  // Rolling-hours windows only (null otherwise): the reading in hours that replaces the
  // working-day metrics there (budget.hourlyOutlook).
  hourly: HourlyOutlook | null;
  // Fewer than 2 working units elapsed in the period: projection and autonomy rest on
  // too little data and are shown as a preliminary estimate.
  preliminary: boolean;
  // Instant consumption (%/h, from recent samples) and sustainable hourly pace to
  // reach exactly 100% at the reset — see budget.instantaneousRate /
  // budget.sustainableHourlyRate.
  instantRate: number | null;
  sustainableRate: number | null;
  // Star efficiency rating over the last days (see budget.efficiencyRating).
  efficiencyRating: EfficiencyRating | null;
  // "Tip of the day": derived from real facts about this window's data, never a
  // generic sentence — see budget.generateDailyTip.
  dailyTip: DailyTip;
  // Only for the Claude account with local insights enabled (null otherwise) — see budget.tokenYield.
  tokenYield: TokenYield | null;
}

/** Usage share (by token volume) of a single tool/MCP server — see services/claudeLocalSessions.ts. */
export interface ToolUsageShare {
  name: string;
  sharePercent: number;
}

/**
 * Behavioural insights computed from LOCAL Claude Code sessions (CLI/VS Code
 * extension, same source — see docs/RESEARCH.md §5), not from the claude.ai account.
 * Claude only: Copilot has no equivalent local source. Cross-window (not tied to a
 * specific QuotaWindow): lives on AccountSnapshot, not on QuotaWindowSnapshot. Shares
 * are weighted by token volume (output_tokens), not by turn count — consistent with
 * the "% of your usage" of the VS Code panel that inspired it. Never computed from real
 * message content, only from structural fields (usage, tool names) — see
 * services/claudeLocalSessions.ts.
 */
export interface ClaudeLocalInsights {
  computedAt: string;
  windowDays: number;
  sessionsAnalyzed: number;
  totalOutputTokens: number;
  highContextOutputTokens: number;
  longSessionOutputTokens: number;
  highContextSharePercent: number | null;
  longSessionSharePercent: number | null;
  topTools: ToolUsageShare[];
  // Output tokens per day (a session is attributed to the day it was last modified).
  daily: LocalDailyTokens[];
  // Earliest session start (ISO) per local day — start of the working day. Optional:
  // a cache written before it existed lacks it and is recomputed (main.ts).
  firstSessionStartByDay?: Record<string, string>;
}

export interface LocalDailyTokens {
  date: string;
  outputTokens: number;
  highContextOutputTokens: number;
}

/** "Yield": output tokens per 1% of quota used — see budget.tokenYield. */
export interface TokenYield {
  tokensPerPercent: number;
  // % change of the yield between the second and the first half of the compared days (null when not computable).
  trendPercent: number | null;
  daysCompared: number;
}

/** Observed link between high-consumption days and large context — see budget.consumptionCause. */
export interface ConsumptionCause {
  highDaysHighContextPercent: number;
  lowDaysHighContextPercent: number;
  daysCompared: number;
}

/** Enriched snapshot sent to the renderer via IPC (see main.ts). */
export interface AccountSnapshot extends RawAccountUsage {
  accountId: AccountId;
  provider: ProviderId;
  label: string;
  windows: QuotaWindowSnapshot[];
  // Present only on the Claude account with `localInsights: true` in Settings (at most
  // one) — see main.ts/computeLocalInsightsIfNeeded.
  localInsights?: ClaudeLocalInsights | null;
  // Fields derived from the most critical window (budget.pickCriticalWindow), kept for
  // compatibility (80% threshold notification, default widget view) — see also
  // QuotaWindowSnapshot in `windows` for the account's other windows.
  criticalWindow: QuotaWindow | null;
  dailyHistory: DailyUsagePoint[];
  efficiencyIndex: number | null;
  projectedUsage: number | null;
  daysUntilReset: number | null;
  workingDaysUntilReset: number | null;
  estimatedAutonomyWorkingDays: number | null;
  lastUpdatedAt?: string;
  stale?: boolean;
  lastError?: string;
  // A state of the account the user has to act on (e.g. "sign in with Codex", #178),
  // shown in place of an error; never set together with lastError.
  notice?: string;
}

export interface UsageSnapshot {
  generatedAt: string;
  // One item per enabled and connected account, in the same order as
  // AppSettings.accounts.
  accounts: AccountSnapshot[];
}

/**
 * Part common to every account, independent of the provider (issue #4): the
 * provider-specific part lives in the interfaces extending it, discriminated on
 * `provider`.
 */
export interface AccountConfigBase {
  id: AccountId;
  provider: ProviderId;
  label: string;
  enabled: boolean;
  subscription: { renewalRule: RenewalRule };
  // Per account (it used to be one global setting): e.g. a personal account without
  // constraints next to a company one paced on working days.
  workSchedule: WorkSchedule;
}

export interface ClaudeAccountSettings extends AccountConfigBase {
  provider: 'claude';
  // authMethod and session are kept so stores of earlier versions stay valid; since
  // #140 the app no longer signs in to claude.ai and the session is always empty.
  authMethod: 'password' | 'google' | 'sso';
  session: {
    sessionKey: string | null;
    organizationId: string | null;
    capturedAt: string | null;
    expiresAt: string | null;
  };
  // Dedicated Electron partition (`persist:account-<id>`) that held the claude.ai
  // cookies before #140; still cleared on disconnect/removal.
  partition: string;
  // Claude Code sessions of THIS machine attributed to this account (opt-in, at most
  // one Claude account at a time) — see services/claudeLocalSessions.ts.
  localInsights: boolean;
}

export interface CopilotAccountSettings extends AccountConfigBase {
  provider: 'copilot';
  // Chooses which connection panel to show in Settings (PAT vs OAuth) and is updated
  // automatically from the method used for the last successful connection — see
  // renderer/settings.ts (applyAccountDetailState) and main.ts (accounts:connectCopilot*).
  authMethod: 'pat' | 'oauth';
  // Personal plan or company (organization) seat: chooses which endpoints are read
  // (`aipermiler usage copilot --scope`, gocore/internal/copilot).
  accountScope: AccountScope;
  // "github.com" or a GitHub Enterprise Cloud tenant with data residency
  // ("<tenant>.ghe.com"), where company seats live — see services/githubHost.ts.
  host: string;
  // Since #179 the token lives in the core's OS keyring (entry copilot/<id>), never in
  // the store: `token` only holds one of an earlier version until main.ts
  // (moveCopilotTokensToKeyring) hands it over; `username` marks a connected account.
  credentials: { token: string | null; username: string | null };
  // Client ID of a GitHub OAuth App registered by the user (not a secret — see
  // renderer/settings.ts): alternative to the PAT, registered on the account's host.
  oauthApp: { clientId: string | null };
  manualQuota: number;
  experimentalWarningAcknowledged: boolean;
}

export interface CodexAccountSettings extends AccountConfigBase {
  provider: 'codex';
  // Electron partition (`persist:account-<id>`) that held the chatgpt.com cookies
  // before #178; cleared once at startup (main.ts purgeStoredCodexSessions).
  partition: string;
  // Absolute path of the codex binary, or null to let the core find it (CLI on PATH,
  // npm global, VS Code extension; docs/CORE-CLI.md). Usage is read through the
  // user's own Codex sign-in by the core (#148), never through a stored session.
  binaryPath: string | null;
}

export type AccountConfig = ClaudeAccountSettings | CopilotAccountSettings | CodexAccountSettings;

export interface UsageView {
  accountId: AccountId;
  windowId: string;
}

/** A null Manual window exists only until an uncached legacy account preference resolves. */
export type TrayBadgeChoice = { mode: 'off' } | { mode: 'auto' }
  | { mode: 'manual'; accountId: AccountId; windowId: string | null };

export interface TrayBadgeSettings {
  view: TrayBadgeChoice;
  critical: boolean;
}

export interface TrayBadgeEntry {
  view: TrayBadgeChoice;
  checked: boolean;
  enabled: boolean;
  accountLabel?: string;
  provider?: ProviderId;
  window?: QuotaWindow;
}

export interface TrayViewState {
  selection: TrayBadgeSettings;
  shown: UsageView | null;
  entries: TrayBadgeEntry[];
}

export interface UiSettings {
  language: LanguageSetting;
  windowStyle: WindowStyle;
  alwaysOnTop: boolean;
  compact: boolean;
  animations: boolean;
  accentColor: string;
  bounds: { x?: number; y?: number; width: number; height: number };
  chartRange: ChartRange;
  refreshIntervalMinutes: number;
  notificationThresholdPercent: number;
  trayBadge: TrayBadgeSettings;
}

export interface HistorySettings {
  dailyUsage: DailyUsagePoint[];
  retentionDays: number;
  // Buffer of closely spaced samples (append-only, pruned by age, not by days) used
  // only by the instant consumption gauge — see RecentUsageSample above.
  recentSamples: RecentUsageSample[];
  // Chiave = AccountId.
  lastGood?: Record<AccountId, RawAccountUsage & { accountId: AccountId; lastUpdatedAt: string }>;
}

/**
 * Automatic "format drift" report (see services/_shape.ts, main.ts,
 * diagnostics/githubIssue.ts): when a service no longer recognizes an endpoint
 * format, the app opens a pre-filled GitHub issue draft (never real values, only
 * structure) and deduplicates it by signature so it is not reopened on every refresh.
 */
export interface DiagnosticsSettings {
  autoReportFormatDrift: boolean;
  reportedSignatures: Record<string, string>; // signature -> ISO timestamp of the first report
}

/** New version available — see services/updates.ts (issue #5). */
export interface UpdateInfo {
  version: string;
  publishedAt: string | null;
  releaseUrl: string;
  // Package for the current OS; when none fits (e.g. Intel Mac), the release page.
  downloadUrl: string;
  assetName: string | null;
}

export interface UpdateSettings {
  autoCheck: boolean;
  lastCheckedAt: string | null;
  lastError: string | null;
  available: UpdateInfo | null;
  // Version already announced with a system notification: not repeated on every check.
  notifiedVersion: string | null;
}

export interface AppSettings {
  // Registry of N accounts (issue #4) — previously two fixed slots `{ claude, copilot }`,
  // converted at startup by store/normalize.ts → store/migrate.ts.
  accounts: AccountConfig[];
  ui: UiSettings;
  history: HistorySettings;
  advisorCache: { generatedAt: string | null; adviceText: string | null };
  meta: {
    notifiedToday: Record<string, boolean>;
    // One-off removal of the claude.ai sessions stored by earlier versions (#140, see
    // main.ts, purgeStoredClaudeSessions).
    claudeSessionsPurged?: boolean;
    // One-off removal of the chatgpt.com cookies stored before Codex moved to the core
    // (#178, main.ts purgeStoredCodexSessions).
    codexSessionsPurged?: boolean;
  };
  diagnostics: DiagnosticsSettings;
  updates: UpdateSettings;
  // App-managed cache (not a user setting), same pattern as advisorCache: avoids
  // rescanning all local Claude Code sessions on every 30-minute refresh — see main.ts
  // LOCAL_INSIGHTS_RECOMPUTE_INTERVAL_MS.
  localInsightsCache: { claudeCode: ClaudeLocalInsights | null };
}
