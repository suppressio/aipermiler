// main/accountNotice.ts — which core errors are account states the user has to act on
// (AccountSnapshot.notice: "sign in", "install Claude Code"…) instead of failures. Pure,
// tested in accountNotice.test.ts; main.ts turns the key into text with t().

import { CoreProviderError } from './coreClient';
import type { MainMessageKey } from './i18n/index';

export function accountNoticeKey(err: unknown): MainMessageKey | null {
  if (!(err instanceof CoreProviderError)) return null;
  if (err.code === 'signed_out') {
    if (err.provider === 'codex') return 'error.codexSignedOut';
    if (err.provider === 'copilot') return 'error.copilotSignedOut';
    if (err.provider === 'claude') return 'error.claudeSignedOut';
    return null;
  }
  if (err.provider !== 'claude') return null;
  // Claude (#180): Claude Code shows a screen of its own, is missing, or is signed in
  // without a subscription or on a usage-credits plan. Reasons from docs/CORE-CLI.md.
  if (err.code === 'not_ready') return 'error.claudeNotReady';
  if (err.code === 'unavailable') {
    if (err.reason === 'not_installed') return 'error.claudeNotInstalled';
    if (err.reason === 'no_subscription') return 'error.claudeNoSubscription';
    if (err.reason === 'usage_credits') return 'error.claudeUsageCredits';
    if (err.reason === 'api_key_question') return 'error.claudeApiKeyQuestion';
  }
  return null;
}
