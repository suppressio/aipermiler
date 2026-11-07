import { test } from 'node:test';
import assert from 'node:assert/strict';
import { accountNoticeKey } from './accountNotice';
import { CoreProviderError } from './coreClient';

test('accountNoticeKey: signed out is a notice for every provider', () => {
  assert.equal(accountNoticeKey(new CoreProviderError('signed_out', 'x', 'codex')), 'error.codexSignedOut');
  assert.equal(accountNoticeKey(new CoreProviderError('signed_out', 'x', 'copilot', 'token_rejected')), 'error.copilotSignedOut');
  assert.equal(accountNoticeKey(new CoreProviderError('signed_out', 'x', 'claude')), 'error.claudeSignedOut');
});

test('accountNoticeKey: Claude states with what to do (#180)', () => {
  assert.equal(accountNoticeKey(new CoreProviderError('not_ready', 'x', 'claude')), 'error.claudeNotReady');
  assert.equal(accountNoticeKey(new CoreProviderError('unavailable', 'x', 'claude', 'not_installed')), 'error.claudeNotInstalled');
  assert.equal(accountNoticeKey(new CoreProviderError('unavailable', 'x', 'claude', 'no_subscription')), 'error.claudeNoSubscription');
  assert.equal(accountNoticeKey(new CoreProviderError('unavailable', 'x', 'claude', 'api_key_question')), 'error.claudeApiKeyQuestion');
});

test('accountNoticeKey: real failures stay errors', () => {
  assert.equal(accountNoticeKey(new CoreProviderError('unavailable', 'x', 'claude')), null);
  assert.equal(accountNoticeKey(new CoreProviderError('unavailable', 'x', 'claude', 'something_new')), null);
  assert.equal(accountNoticeKey(new CoreProviderError('timeout', 'x', 'claude')), null);
  assert.equal(accountNoticeKey(new CoreProviderError('not_ready', 'x', 'codex')), null);
  assert.equal(accountNoticeKey(new Error('x')), null);
});
