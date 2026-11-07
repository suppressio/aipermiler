// services/claudeLocalSessions.test.ts — unit tests for the local insights
// aggregation. No real filesystem/SDK access: listSessions/getSessionMessages are
// injected as fake dependencies (same principle as the `fetch` mock in
// services/claude.test.ts/services/copilot.test.ts).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { computeClaudeLocalInsights, ranIn } from './claudeLocalSessions';
import type { LocalSessionsDeps } from './claudeLocalSessions';
import type { SDKSessionInfo, SessionMessage } from '@anthropic-ai/claude-agent-sdk';
import { at } from '../tests/support/at';
import { localDateKey } from '../budget';

const HOUR = 3600 * 1000;
const DAY = 24 * HOUR;

function session(overrides: Partial<SDKSessionInfo> = {}): SDKSessionInfo {
  return {
    sessionId: 's1',
    summary: 'test',
    lastModified: Date.now(),
    createdAt: Date.now() - HOUR,
    ...overrides,
  };
}

function assistantMessage(usage: Record<string, unknown>, toolNames: string[] = []): SessionMessage {
  const content = toolNames.map((name) => ({ type: 'tool_use', name }));
  return {
    type: 'assistant',
    uuid: 'u1',
    session_id: 's1',
    parent_tool_use_id: null,
    parent_agent_id: null,
    message: { usage, content },
  };
}

function makeDeps(sessions: SDKSessionInfo[], messagesBySession: Record<string, SessionMessage[]>): LocalSessionsDeps {
  return {
    listSessions: (async () => sessions),
    getSessionMessages: (async (sessionId: string) => messagesBySession[sessionId] ?? []),
  };
}

test('computeClaudeLocalInsights returns null when no session is in the window', async () => {
  const deps = makeDeps([session({ lastModified: Date.now() - 10 * DAY })], {});
  const result = await computeClaudeLocalInsights(7, deps);
  assert.equal(result, null);
});

test('computeClaudeLocalInsights excludes sessions outside the window', async () => {
  const inWindow = session({ sessionId: 'in', lastModified: Date.now() - DAY, createdAt: Date.now() - DAY - HOUR });
  const outOfWindow = session({ sessionId: 'out', lastModified: Date.now() - 30 * DAY });
  const deps = makeDeps([inWindow, outOfWindow], {
    in: [assistantMessage({ output_tokens: 100, input_tokens: 10 })],
    out: [assistantMessage({ output_tokens: 9999, input_tokens: 10 })],
  });
  const result = await computeClaudeLocalInsights(7, deps);
  assert.equal(result?.sessionsAnalyzed, 1);
});

test('computeClaudeLocalInsights computes a token-weighted highContextSharePercent', async () => {
  const s = session({ sessionId: 's1' });
  const deps = makeDeps([s], {
    s1: [
      assistantMessage({ output_tokens: 30, input_tokens: 10, cache_read_input_tokens: 0, cache_creation_input_tokens: 0 }), // contesto basso
      assistantMessage({ output_tokens: 70, input_tokens: 10, cache_read_input_tokens: 200_000, cache_creation_input_tokens: 0 }), // contesto alto
    ],
  });
  const result = await computeClaudeLocalInsights(7, deps);
  assert.ok(result);
  assert.equal(result.highContextSharePercent, 70); // 70 of the 100 total output_tokens come from high-context turns
  assert.equal(result.highContextOutputTokens, 70);
  assert.equal(result.totalOutputTokens, 100);
});

test('computeClaudeLocalInsights computes longSessionSharePercent only for 8h+ sessions', async () => {
  const shortSession = session({ sessionId: 'short', createdAt: Date.now() - HOUR, lastModified: Date.now() });
  const longSession = session({ sessionId: 'long', createdAt: Date.now() - 9 * HOUR, lastModified: Date.now() });
  const deps = makeDeps([shortSession, longSession], {
    short: [assistantMessage({ output_tokens: 40, input_tokens: 1 })],
    long: [assistantMessage({ output_tokens: 60, input_tokens: 1 })],
  });
  const result = await computeClaudeLocalInsights(7, deps);
  assert.ok(result);
  assert.equal(result.longSessionSharePercent, 60);
  assert.equal(result.longSessionOutputTokens, 60);
  assert.equal(result.totalOutputTokens, 100);
});

test('computeClaudeLocalInsights truncates topTools to 5 and sorts by frequency', async () => {
  const s = session({ sessionId: 's1' });
  const messages = [
    assistantMessage({ output_tokens: 1, input_tokens: 1 }, ['Bash', 'Bash', 'Read']),
    assistantMessage({ output_tokens: 1, input_tokens: 1 }, ['Bash', 'Edit', 'Write', 'ToolSearch', 'AskUserQuestion', 'EnterPlanMode']),
  ];
  const deps = makeDeps([s], { s1: messages });
  const result = await computeClaudeLocalInsights(7, deps);
  assert.equal(result?.topTools.length, 5);
  assert.equal(at(result.topTools, 0).name, 'Bash'); // 3 occurrences, the most frequent
});

test('computeClaudeLocalInsights skips an unreadable session without stopping the others', async () => {
  const broken = session({ sessionId: 'broken' });
  const ok = session({ sessionId: 'ok' });
  const deps: LocalSessionsDeps = {
    listSessions: (async () => [broken, ok]),
    getSessionMessages: (async (sessionId: string) => {
      if (sessionId === 'broken') throw new Error('corrupted file');
      return [assistantMessage({ output_tokens: 50, input_tokens: 1 })];
    }),
  };
  const result = await computeClaudeLocalInsights(7, deps);
  assert.equal(result?.sessionsAnalyzed, 2); // both counted as "in the window", only one contributes data
  assert.equal(result.highContextSharePercent, 0);
});

test('computeClaudeLocalInsights returns null when listSessions fails', async () => {
  const deps: LocalSessionsDeps = {
    listSessions: (async () => { throw new Error('errore SDK'); }),
    getSessionMessages: (async () => []),
  };
  const result = await computeClaudeLocalInsights(7, deps);
  assert.equal(result, null);
});

test('computeClaudeLocalInsights groups tokens by the day the session was last modified', async () => {
  const now = Date.now();
  const today = localDateKey(new Date(now));
  const yesterday = localDateKey(new Date(now - DAY));
  const deps = makeDeps(
    [session({ sessionId: 'a', lastModified: now }), session({ sessionId: 'b', lastModified: now - DAY, createdAt: now - DAY - HOUR })],
    {
      a: [assistantMessage({ output_tokens: 100, cache_read_input_tokens: 200_000 }), assistantMessage({ output_tokens: 50, input_tokens: 10 })],
      b: [assistantMessage({ output_tokens: 30, input_tokens: 10 })],
    },
  );
  const result = await computeClaudeLocalInsights(7, deps);
  assert.deepEqual(result?.daily, [
    { date: yesterday, outputTokens: 30, highContextOutputTokens: 0 },
    { date: today, outputTokens: 150, highContextOutputTokens: 100 },
  ]);
});

test('computeClaudeLocalInsights records the earliest session start of each day (start of the working day)', async () => {
  const day = new Date(2026, 6, 13);
  const at9 = new Date(2026, 6, 13, 9, 0).getTime();
  const at11 = new Date(2026, 6, 13, 11, 0).getTime();
  const now = new Date(2026, 6, 13, 18, 0).getTime();
  const deps = makeDeps(
    [
      session({ sessionId: 'late', createdAt: at11, lastModified: now }),
      session({ sessionId: 'early', createdAt: at9, lastModified: at11 }),
      { sessionId: 'unknown', summary: 'test', lastModified: now }, // no createdAt: ignored
    ],
    { late: [assistantMessage({ output_tokens: 10 })], early: [assistantMessage({ output_tokens: 10 })], unknown: [assistantMessage({ output_tokens: 10 })] },
  );
  const original = Date.now;
  Date.now = () => now; // sessions of 13 July within the window
  try {
    const result = await computeClaudeLocalInsights(7, deps);
    assert.deepEqual(result?.firstSessionStartByDay, { [localDateKey(day)]: new Date(at9).toISOString() });
  } finally {
    Date.now = original;
  }
});

// #180: the core's Claude probe sessions are not the user's work.
test('computeClaudeLocalInsights skips the sessions that ran in the probe folder', async () => {
  const probe = '/synthetic/cache/aipermiler/claude-probe';
  const early = Date.now() - 3 * HOUR;
  const work = session({ sessionId: 'work', cwd: '/synthetic/project', createdAt: Date.now() - HOUR });
  const probeRun = session({ sessionId: 'probe', cwd: probe, createdAt: early, lastModified: early + 5000 });
  const deps = makeDeps([work, probeRun], {
    work: [assistantMessage({ output_tokens: 100, input_tokens: 10 })],
    probe: [assistantMessage({ output_tokens: 1, input_tokens: 10 })],
  });
  const result = await computeClaudeLocalInsights(7, deps, probe);
  assert.equal(result?.sessionsAnalyzed, 1);
  assert.equal(result.totalOutputTokens, 100);
  // The probe would otherwise move the day's first session start earlier.
  assert.equal(result.firstSessionStartByDay?.[localDateKey(new Date(work.createdAt ?? 0))], new Date(work.createdAt ?? 0).toISOString());

  const all = await computeClaudeLocalInsights(7, deps, null);
  assert.equal(all?.sessionsAnalyzed, 2);
});

test('ranIn: resolved paths, case-insensitive on Windows and macOS only', () => {
  assert.equal(ranIn('/c/aipermiler/claude-probe/', '/c/aipermiler/claude-probe', 'linux'), true);
  assert.equal(ranIn('/c/aipermiler/x/../claude-probe', '/c/aipermiler/claude-probe', 'linux'), true);
  assert.equal(ranIn('/c/Aipermiler/claude-probe', '/c/aipermiler/claude-probe', 'linux'), false);
  assert.equal(ranIn('/c/Aipermiler/claude-probe', '/c/aipermiler/claude-probe', 'darwin'), true);
  assert.equal(ranIn('C:\\Users\\X\\AppData\\Local\\aipermiler\\claude-probe', 'c:\\users\\x\\appdata\\local\\aipermiler\\claude-probe', 'win32'), true);
  assert.equal(ranIn('/c/aipermiler/claude-probe/sub', '/c/aipermiler/claude-probe', 'linux'), false);
  assert.equal(ranIn(undefined, '/c/aipermiler/claude-probe', 'linux'), false);
  assert.equal(ranIn('/c/aipermiler/claude-probe', null, 'linux'), false);
  // A linked cache folder: the probe folder named through the link is still matched.
  const links = (value: string) => value.replace('/home/u/.cache', '/data/cache');
  assert.equal(ranIn('/data/cache/aipermiler/claude-probe', '/home/u/.cache/aipermiler/claude-probe', 'linux', links), true);
});
