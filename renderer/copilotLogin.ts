// renderer/copilotLogin.ts — a Copilot sign-in refused because another one runs (#338).
// IPC keeps only the main-process error's text ("<name>: <message>" after Electron's
// prefix), so main names that error and the name is looked for here.

import type { CopilotLoginInProgressErrorName } from '../types/ipc';

const LOGIN_IN_PROGRESS: CopilotLoginInProgressErrorName = 'CopilotLoginInProgressError';

export function isLoginInProgressError(err: unknown): boolean {
  return err instanceof Error && err.message.includes(`${LOGIN_IN_PROGRESS}:`);
}
