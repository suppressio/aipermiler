// renderer/copilotLogin.test.ts — a Copilot sign-in refused because another one runs (#338).

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { isLoginInProgressError } from './copilotLogin.js';

test('login in progress: recognized through the error name main gives it, also wrapped by Electron IPC', () => {
  // What ipcRenderer.invoke rejects with: the main-process error's toString() after a prefix.
  const wrapped = new Error("Error invoking remote method 'accounts:connectCopilotOAuth': CopilotLoginInProgressError: Another GitHub sign-in is already in progress");
  assert.equal(isLoginInProgressError(wrapped), true);
  assert.equal(isLoginInProgressError(new Error("Error invoking remote method 'accounts:connectCopilotOAuth': Error: GitHub sign-in cancelled.")), false);
  assert.equal(isLoginInProgressError('CopilotLoginInProgressError'), false);
});
