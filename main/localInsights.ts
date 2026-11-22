// main/localInsights.ts — which cached Claude local insights the app may still show.
// The insights are counted by the Go core (`aipermiler insights claude`, #298, #299);
// a cache written by the earlier Agent SDK reader counted output tokens 2-2.5 times
// (#301), so it is recomputed once instead of being shown.

import type { AppSettings, ClaudeLocalInsights } from '../types/index';

export function usableInsightsCache(cache: AppSettings['localInsightsCache'], windowDays: number): ClaudeLocalInsights | null {
  const cached = cache.claudeCode;
  if (!cache.countedByCore || cached === null || cached.windowDays !== windowDays) return null;
  return cached;
}
