// services/claudeLocalSessions.ts — behavioural insights from LOCAL Claude Code
// sessions (CLI + VS Code extension, same source — see docs/RESEARCH.md §5). Not data of
// the claude.ai account: it reads the session transcripts on this machine through the
// official SDK (@anthropic-ai/claude-agent-sdk) instead of parsing the internal JSONL
// format directly — undocumented and changing between versions (see docs/RESEARCH.md §5.1).
//
// Content discipline: it reads ONLY structural/numeric fields (turn-level usage,
// tool/MCP server names) — NEVER message text (content blocks of type "text"), the
// same rule already applied in services/_shape.ts for format-drift diagnostics.

// @anthropic-ai/claude-agent-sdk ships only as pure ESM (no "require" condition in
// package.json/exports, only "default": "./sdk.mjs") — a static `import` would be
// compiled by tsc into a `require()` (tsconfig.json uses CommonJS) that fails with
// ERR_REQUIRE_ESM in the Node bundled with Electron 31 (verified with `npm start`: the
// system Node tolerates require() of ESM via interop, the one inside Electron does
// not). Fix: a native dynamic import(), see dynamicImport below.
import type { ListSessionsOptions, GetSessionMessagesOptions, SDKSessionInfo, SessionMessage } from '@anthropic-ai/claude-agent-sdk';
import path from 'node:path';
import type { ClaudeLocalInsights, LocalDailyTokens, ToolUsageShare } from '../types/index';
import { localDateKey } from '../budget';

const HIGH_CONTEXT_THRESHOLD = 150_000; // estimated context tokens, same threshold as the VS Code panel that inspired this feature
const LONG_SESSION_HOURS = 8;
const MAX_SESSIONS_SCANNED = 300; // safety margin: listSessions() already returns the most recent first
const TOP_TOOLS_LIMIT = 5;

type PlainRecord = Record<string, unknown>;

function isPlainRecord(value: unknown): value is PlainRecord {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function readNumber(record: PlainRecord, key: string): number {
  const value = record[key];
  return typeof value === 'number' && Number.isFinite(value) ? value : 0;
}

/** Estimated context of a turn: tokens read from cache + written to cache + direct input. */
function turnContextTokens(usage: PlainRecord): number {
  return readNumber(usage, 'cache_read_input_tokens') + readNumber(usage, 'cache_creation_input_tokens') + readNumber(usage, 'input_tokens');
}

// Injectable in tests (services/claudeLocalSessions.test.ts) to avoid real
// filesystem/SDK access — same principle as the `fetch` mock in
// services/claude.test.ts and services/copilot.test.ts.
export interface LocalSessionsDeps {
  listSessions: (options?: ListSessionsOptions) => Promise<SDKSessionInfo[]>;
  getSessionMessages: (sessionId: string, options?: GetSessionMessagesOptions) => Promise<SessionMessage[]>;
}

// tsc, with tsconfig.json at "module": "CommonJS", turns EVEN a dynamic `import()`
// into `Promise.resolve().then(() => require(...))` — the same ERR_REQUIRE_ESM as the
// static require() (verified with a real build). The only way to get a native
// import() from a file compiled to CommonJS is to hide it from the compiler's static
// analysis through a Function built at runtime — a known pattern for this exact
// scenario (loading an ESM-only package from CJS code), not an accidental workaround.
// Verified both in plain Node and inside Electron 31 (with `npm start`).
// eslint-disable-next-line @typescript-eslint/no-implied-eval -- the only way to get a native import() from code compiled to CommonJS, see the comment above
const dynamicImport = new Function('specifier', 'return import(specifier)') as (
  specifier: string,
) => Promise<typeof import('@anthropic-ai/claude-agent-sdk')>;

let sdkDepsPromise: Promise<LocalSessionsDeps> | null = null;

function loadSdkDeps(): Promise<LocalSessionsDeps> {
  if (!sdkDepsPromise) {
    sdkDepsPromise = dynamicImport('@anthropic-ai/claude-agent-sdk').then((mod) => ({
      listSessions: mod.listSessions,
      getSessionMessages: mod.getSessionMessages,
    }));
  }
  return sdkDepsPromise;
}

/**
 * Whether a session ran in `folder` (the core's Claude probe folder, #180): compared as
 * resolved paths, case-insensitively on Windows and macOS (their default file systems).
 */
export function ranIn(sessionCwd: string | undefined, folder: string | null, platform: NodeJS.Platform = process.platform): boolean {
  if (folder === null || sessionCwd === undefined || sessionCwd === '') return false;
  const p = platform === 'win32' ? path.win32 : path.posix;
  const a = p.resolve(sessionCwd);
  const b = p.resolve(folder);
  return platform === 'win32' || platform === 'darwin' ? a.toLowerCase() === b.toLowerCase() : a === b;
}

/**
 * Aggregates behavioural insights over the local Claude Code sessions of the last
 * `windowDays` days. Shares are weighted by output_tokens volume (the usage that
 * really consumes credit), not by turn count. Returns null when there are no sessions
 * in the window — not an error, just nothing to show.
 */
export async function computeClaudeLocalInsights(
  windowDays: number,
  deps?: LocalSessionsDeps,
  // The core's Claude probe folder (#180): its sessions are the core's /usage reads,
  // not the user's work. Otherwise each probe would count as the day's first session
  // (firstSessionStartByDay moves the working-day start in pacing).
  skipCwd: string | null = null,
): Promise<ClaudeLocalInsights | null> {
  const resolvedDeps = deps ?? (await loadSdkDeps());
  let sessions: SDKSessionInfo[];
  try {
    sessions = await resolvedDeps.listSessions({ limit: MAX_SESSIONS_SCANNED });
  } catch (err) {
    console.error('[services/claudeLocalSessions] listSessions failed:', (err as Error).message);
    return null;
  }

  const cutoff = Date.now() - windowDays * 24 * 3600 * 1000;
  const inWindow = sessions.filter((s) => s.lastModified >= cutoff && !ranIn(s.cwd, skipCwd));
  if (inWindow.length === 0) return null;

  let totalOutputTokens = 0;
  let highContextOutputTokens = 0;
  let longSessionOutputTokens = 0;
  const toolCounts = new Map<string, number>();
  let totalToolCalls = 0;
  // Tokens per day, to cross with quota consumption (budget.tokenYield /
  // budget.consumptionCause). The SDK exposes no per-message timestamp (SessionMessage
  // has none), so the whole session is attributed to the day it was last modified: an
  // approximation stated in the UI. Dates as LOCAL YYYY-MM-DD, the same convention as
  // history.dailyUsage (main.ts, budget.localDateKey).
  const daily = new Map<string, LocalDailyTokens>();
  // Earliest session start per local day: the start of the working day when the app
  // was opened after work had begun (budget.todayActivitySpan).
  const firstSessionStartByDay: Record<string, string> = {};

  for (const session of inWindow) {
    const durationMs = typeof session.createdAt === 'number' ? session.lastModified - session.createdAt : null;
    if (typeof session.createdAt === 'number') {
      const startDay = localDateKey(new Date(session.createdAt));
      const known = firstSessionStartByDay[startDay];
      if (known === undefined || session.createdAt < new Date(known).getTime()) {
        firstSessionStartByDay[startDay] = new Date(session.createdAt).toISOString();
      }
    }
    const day = localDateKey(new Date(session.lastModified));
    const dayBucket = daily.get(day) ?? { date: day, outputTokens: 0, highContextOutputTokens: 0 };
    daily.set(day, dayBucket);
    const isLongSession = durationMs !== null && durationMs >= LONG_SESSION_HOURS * 3600 * 1000;

    let messages: SessionMessage[];
    try {
      messages = await resolvedDeps.getSessionMessages(session.sessionId);
    } catch (err) {
      // An unreadable session (corrupted file, changed internal format) must not stop the
      // aggregation of the others — the same resilience as the rest of the app (never a
      // crash because of a single missing datum).
      console.error(`[services/claudeLocalSessions] session ${session.sessionId} unreadable, skipped:`, (err as Error).message);
      continue;
    }

    for (const entry of messages) {
      if (entry.type !== 'assistant' || !isPlainRecord(entry.message)) continue;
      const usage = entry.message.usage;
      if (!isPlainRecord(usage)) continue;

      const outputTokens = readNumber(usage, 'output_tokens');
      if (outputTokens <= 0) continue;

      totalOutputTokens += outputTokens;
      dayBucket.outputTokens += outputTokens;
      if (turnContextTokens(usage) > HIGH_CONTEXT_THRESHOLD) {
        highContextOutputTokens += outputTokens;
        dayBucket.highContextOutputTokens += outputTokens;
      }
      if (isLongSession) longSessionOutputTokens += outputTokens;

      // Only the name of the invoked tool/MCP server — never the call parameters nor other
      // content blocks (e.g. type "text", the real message text).
      const content = entry.message.content;
      if (Array.isArray(content)) {
        for (const block of content) {
          if (isPlainRecord(block) && block.type === 'tool_use' && typeof block.name === 'string') {
            toolCounts.set(block.name, (toolCounts.get(block.name) ?? 0) + 1);
            totalToolCalls += 1;
          }
        }
      }
    }
  }

  if (totalOutputTokens === 0) return null;

  const topTools: ToolUsageShare[] = [...toolCounts.entries()]
    .sort((a, b) => b[1] - a[1])
    .slice(0, TOP_TOOLS_LIMIT)
    .map(([name, count]) => ({ name, sharePercent: Math.round((count / totalToolCalls) * 1000) / 10 }));

  return {
    computedAt: new Date().toISOString(),
    windowDays,
    sessionsAnalyzed: inWindow.length,
    totalOutputTokens,
    highContextOutputTokens,
    longSessionOutputTokens,
    highContextSharePercent: Math.round((highContextOutputTokens / totalOutputTokens) * 1000) / 10,
    longSessionSharePercent: Math.round((longSessionOutputTokens / totalOutputTokens) * 1000) / 10,
    topTools,
    daily: [...daily.values()].filter((d) => d.outputTokens > 0).sort((a, b) => a.date.localeCompare(b.date)),
    firstSessionStartByDay,
  };
}
