// main/serviceCopilot.ts — Copilot account actions through the core's always-on service
// (#338, docs/CORE-CLI.md "Copilot account actions", #285): token set and delete, and the
// OAuth login (start, status polling, cancel). Electron no longer runs these CLI commands
// itself: with no usable service the action fails with a ServiceUnusableError, and the
// Settings window disables it with a message (owner decision D1 on #181: no CLI fallback,
// no start button).
//
// Secrets (the token, the OAuth client secret) travel only in the request body, never in
// the route, a header, an error message or a log line. An answer that carries the secret
// (the service withholds those itself) is refused here too, without quoting it.

import { CORE_LOGIN_TIMEOUT_SECONDS, CoreOutputError, CoreProviderError, CoreTimeoutError, providerError } from './coreClient';
import { ServiceUnusableError, connectService, serviceRequest, type FetchLike } from './serviceClient';

const PROVIDER = 'copilot';

export interface CopilotServiceTimeouts {
  healthMs: number;
  // `token set` runs 25 s in the service: a little more here.
  tokenMs: number;
  // The service answers the authorize event within 10 s, or stops the login.
  loginStartMs: number;
  // Status and cancel: the cancel waits for the child to end.
  loginStatusMs: number;
}

const DEFAULT_TIMEOUTS: CopilotServiceTimeouts = { healthMs: 2000, tokenMs: 30000, loginStartMs: 15000, loginStatusMs: 15000 };

export interface CopilotLoginRequest {
  clientId: string;
  clientSecret: string;
  locale: 'en' | 'it';
}

/** A login waiting for the browser (its authorize URL), or ended with the user name. */
export type CopilotLoginStatus = { state: 'waiting'; url: string } | { state: 'connected'; username: string };

export interface CopilotServiceActions {
  /** Whether a usable service runs now (Settings enables the actions only then). */
  available(): Promise<boolean>;
  /** Stores the token in the core's keyring after GitHub checks it; the user name. */
  setToken(accountId: string, token: string): Promise<string>;
  deleteToken(accountId: string): Promise<void>;
  /** Starts the OAuth login; the authorize URL to open in the browser. */
  startLogin(accountId: string, request: CopilotLoginRequest): Promise<string>;
  /** The login's status; its error (timeout, denied, cancelled) is thrown. */
  loginStatus(accountId: string): Promise<CopilotLoginStatus>;
  /** Cancels the account's running login; nothing to cancel is not an error. */
  cancelLogin(accountId: string): Promise<void>;
}

export interface CopilotServiceOptions {
  // Resolved on every call: a service started or stopped later is found then.
  configDir: () => string;
  fetch?: FetchLike;
  timeouts?: CopilotServiceTimeouts;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function carriesSecret(body: string, secret: string | null): boolean {
  if (secret === null || secret === '') return false;
  // As it is, or JSON-escaped inside a document.
  return body.includes(secret) || body.includes(JSON.stringify(secret).slice(1, -1));
}

interface Answer {
  status: number;
  doc: Record<string, unknown>;
}

/**
 * Reads an answer of the account actions. An error document is thrown: a `usage` error
 * for an unknown account (404) is a ServiceUnusableError (the service does not serve the
 * account), any other `usage` error a CoreOutputError (a request this client built
 * wrong), any other code the provider's error (CoreProviderError with its reason, e.g.
 * `login_in_progress`; the service's own errors carry no provider and count as Copilot's).
 */
export function parseActionAnswer(status: number, body: string, secret: string | null): Answer {
  if (carriesSecret(body, secret)) throw new CoreOutputError('Service answer withheld: it carried the secret');
  if (status === 401) throw new ServiceUnusableError('unauthorized', 'Service refused the token');
  let doc: unknown;
  try {
    doc = JSON.parse(body);
  } catch {
    doc = undefined;
  }
  if (!isRecord(doc) || doc.schemaVersion !== 1) throw new ServiceUnusableError('malformed', `Service answered ${String(status)} without a v1 document`);
  if (doc.provider !== undefined && doc.provider !== PROVIDER) throw new ServiceUnusableError('malformed', 'Service document is for another provider');
  const error = doc.error;
  if (isRecord(error)) {
    if (error.code === 'usage') {
      if (status === 404) throw new ServiceUnusableError('unknown_account', 'Account not known to the service');
      const message = typeof error.message === 'string' ? `: ${error.message}` : '';
      throw new CoreOutputError(`Service rejected the request${message}`);
    }
    throw providerError(doc, PROVIDER);
  }
  return { status, doc };
}

function unexpected(answer: Answer): never {
  throw new ServiceUnusableError('malformed', `Service answered ${String(answer.status)} with an unexpected document`);
}

function usernameOf(answer: Answer): string | null {
  return answer.status === 200 && typeof answer.doc.username === 'string' ? answer.doc.username : null;
}

function authorizeUrlOf(answer: Answer): string | null {
  return answer.status === 202 && answer.doc.event === 'authorize' && typeof answer.doc.url === 'string' ? answer.doc.url : null;
}

export function createCopilotServiceActions(options: CopilotServiceOptions): CopilotServiceActions {
  const fetchImpl: FetchLike = options.fetch ?? ((url, init) => fetch(url, init));
  const timeouts = options.timeouts ?? DEFAULT_TIMEOUTS;
  const route = (accountId: string, what: 'token' | 'login'): string => `/api/v1/accounts/${encodeURIComponent(accountId)}/${what}`;

  async function call(method: string, path: string, timeoutMs: number, body?: unknown, secret: string | null = null): Promise<Answer> {
    const endpoint = await connectService(fetchImpl, options.configDir, timeouts.healthMs);
    const answer = await serviceRequest(fetchImpl, endpoint, method, path, timeoutMs, body);
    return parseActionAnswer(answer.status, answer.body, secret);
  }

  return {
    async available() {
      try {
        await connectService(fetchImpl, options.configDir, timeouts.healthMs);
        return true;
      } catch (err) {
        if (err instanceof ServiceUnusableError) return false;
        throw err;
      }
    },
    async setToken(accountId, token) {
      const answer = await call('PUT', route(accountId, 'token'), timeouts.tokenMs, { token }, token);
      return usernameOf(answer) ?? unexpected(answer);
    },
    async deleteToken(accountId) {
      const answer = await call('DELETE', route(accountId, 'token'), timeouts.tokenMs);
      if (answer.status !== 200 || answer.doc.deleted !== true) unexpected(answer);
    },
    async startLogin(accountId, request) {
      const answer = await call('POST', route(accountId, 'login'), timeouts.loginStartMs, { ...request }, request.clientSecret);
      return authorizeUrlOf(answer) ?? unexpected(answer);
    },
    async loginStatus(accountId) {
      const answer = await call('GET', route(accountId, 'login'), timeouts.loginStatusMs);
      const url = authorizeUrlOf(answer);
      if (url !== null) return { state: 'waiting', url };
      const username = usernameOf(answer);
      if (username !== null) return { state: 'connected', username };
      return unexpected(answer);
    },
    async cancelLogin(accountId) {
      try {
        await call('DELETE', route(accountId, 'login'), timeouts.loginStatusMs);
      } catch (err) {
        // The answer is the login's end (`login_cancelled`, or the result it already had),
        // or no login of this account at all: either way nothing runs any more.
        if (err instanceof CoreProviderError) return;
        if (err instanceof ServiceUnusableError && err.reason === 'unknown_account') return;
        throw err;
      }
    },
  };
}

export interface LoginPolling {
  onAuthorize: (url: string) => void;
  // Interval between two status reads.
  pollMs?: number;
  // Past the service's own login timeout: a status that never ends is cancelled.
  deadlineMs?: number;
  sleep?: (ms: number) => Promise<void>;
  now?: () => number;
}

const DEFAULT_POLL_MS = 1000;
const DEFAULT_LOGIN_DEADLINE_MS = (CORE_LOGIN_TIMEOUT_SECONDS + 60) * 1000;

/**
 * The whole OAuth login through the service: start, open the authorize URL, poll the
 * status until the user name or an error (timeout, denied, cancelled from Settings:
 * `cancelLogin` makes the next status read throw `login_cancelled`).
 */
export async function loginThroughService(actions: CopilotServiceActions, accountId: string, request: CopilotLoginRequest, polling: LoginPolling): Promise<string> {
  const sleep = polling.sleep ?? ((ms: number) => new Promise<void>((resolve) => { setTimeout(resolve, ms); }));
  const now = polling.now ?? Date.now;
  const deadline = now() + (polling.deadlineMs ?? DEFAULT_LOGIN_DEADLINE_MS);
  polling.onAuthorize(await actions.startLogin(accountId, request));
  for (;;) {
    await sleep(polling.pollMs ?? DEFAULT_POLL_MS);
    const status = await actions.loginStatus(accountId);
    if (status.state === 'connected') return status.username;
    if (now() >= deadline) {
      await actions.cancelLogin(accountId);
      throw new CoreTimeoutError('The sign-in did not end in time');
    }
  }
}
