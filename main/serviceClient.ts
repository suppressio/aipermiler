// main/serviceClient.ts — the Electron side of the core's always-on service (#182,
// docs/CORE-CLI.md "Service"). When a service runs, an account's usage is read from it
// over the loopback API instead of starting the CLI: the client reads `api.json` in the
// core's config folder, calls `/api/v1/health` with the token, checks the contract
// version, then reads the account's document, which is byte for byte what
// `usage <provider> --json` prints (parsed by main/coreClient.ts).
//
// Any sign of no usable service (no api.json, nobody listening, 401, another contract
// version, an account the service does not serve, a 5xx other than a provider error,
// a malformed answer) is a ServiceUnusableError with its reason: the caller falls back
// to the CLI for that read (readUsageWithFallback) and shows the reason in diagnostics.
// A provider error the service serves (502), or `not_read_yet` (503, the service has
// not read the account since it started), is the account's answer, not a fallback:
// while the service serves an account, the app does not probe it itself (#182 (d)).

import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { CORE_SCHEMA_VERSION, CoreOutputError, parseUsageRun, type CoreUsageResult } from './coreClient';
import type { UsageSourceReason } from '../types/index';

/** `staleReason.reason` / error `reason` of an account the service has not read since it started. */
export const NOT_READ_YET = 'not_read_yet';

const HEALTH_TIMEOUT_MS = 2000;
// The account document is what the service already holds: it answers at once.
const USAGE_TIMEOUT_MS = 10000;

/** No usable service for this read: the caller reads through the CLI instead. */
export class ServiceUnusableError extends Error {
  readonly reason: UsageSourceReason;

  constructor(reason: UsageSourceReason, message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = 'ServiceUnusableError';
    this.reason = reason;
  }
}

/**
 * The core's config folder, as the Go core's config.Dir(): AIPERMILER_CONFIG_DIR
 * (absolute) wins, else os.UserConfigDir()/aipermiler (%APPDATA% on Windows,
 * ~/Library/Application Support on macOS, $XDG_CONFIG_HOME or ~/.config elsewhere).
 */
export function coreConfigDir(env: NodeJS.ProcessEnv, platform: NodeJS.Platform, home: string): string {
  const override = env.AIPERMILER_CONFIG_DIR;
  if (override) {
    if (!path.isAbsolute(override)) throw new Error('AIPERMILER_CONFIG_DIR must be an absolute path');
    return override;
  }
  let base: string;
  switch (platform) {
    case 'win32':
      if (!env.APPDATA) throw new Error('%APPDATA% is not set');
      base = env.APPDATA;
      break;
    case 'darwin':
      base = path.join(home, 'Library', 'Application Support');
      break;
    default: {
      // Go's os.UserConfigDir refuses a relative XDG_CONFIG_HOME: so does this client.
      const xdg = env.XDG_CONFIG_HOME;
      if (xdg && !path.isAbsolute(xdg)) throw new Error('XDG_CONFIG_HOME must be an absolute path');
      base = xdg || path.join(home, '.config');
    }
  }
  return path.join(base, 'aipermiler');
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function parseJson(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}

interface ServiceEndpoint {
  baseUrl: string;
  token: string;
}

/** What a client needs from api.json; the host is always 127.0.0.1, never taken from the file. */
export async function readApiFile(dir: string): Promise<ServiceEndpoint> {
  let text: string;
  try {
    // Node opens files with delete sharing on Windows: the service can still replace it.
    text = await readFile(path.join(dir, 'api.json'), 'utf8');
  } catch (err) {
    throw new ServiceUnusableError('not_running', 'No service: api.json not found', { cause: err });
  }
  const info = parseJson(text);
  if (!isRecord(info) || !Number.isInteger(info.port) || typeof info.port !== 'number' || info.port < 1 || info.port > 65535
    || typeof info.token !== 'string' || info.token === '') {
    throw new ServiceUnusableError('malformed', 'api.json is not readable');
  }
  return { baseUrl: `http://127.0.0.1:${String(info.port)}`, token: info.token };
}

export type FetchLike = (url: string, init: { headers: Record<string, string>; signal: AbortSignal }) => Promise<Response>;

async function get(fetchImpl: FetchLike, endpoint: ServiceEndpoint, route: string, timeoutMs: number): Promise<{ status: number; body: string }> {
  try {
    const response = await fetchImpl(`${endpoint.baseUrl}${route}`, {
      headers: { Authorization: `Bearer ${endpoint.token}` },
      signal: AbortSignal.timeout(timeoutMs),
    });
    return { status: response.status, body: await response.text() };
  } catch (err) {
    // A leftover api.json of a service that did not stop cleanly, or one that hangs.
    throw new ServiceUnusableError('unreachable', 'Service not reachable', { cause: err });
  }
}

export interface ServiceHealth {
  version: string;
  contractVersion: number;
}

/** `/api/v1/health`: the service answers with the token and speaks this client's contract. */
export async function checkHealth(fetchImpl: FetchLike, endpoint: ServiceEndpoint): Promise<ServiceHealth> {
  const { status, body } = await get(fetchImpl, endpoint, '/api/v1/health', HEALTH_TIMEOUT_MS);
  if (status === 401) throw new ServiceUnusableError('unauthorized', 'Service refused the token');
  if (status !== 200) throw new ServiceUnusableError('failed', `Service health answered ${String(status)}`);
  const doc = parseJson(body);
  if (!isRecord(doc) || doc.schemaVersion !== 1 || typeof doc.version !== 'string') {
    throw new ServiceUnusableError('malformed', 'Service health document is not readable');
  }
  // A lower contract version, or one this client does not know, is an older or
  // incompatible service: unusable (docs/CORE-CLI.md, /health).
  if (doc.contractVersion !== CORE_SCHEMA_VERSION) {
    throw new ServiceUnusableError('incompatible', `Service speaks contract ${String(doc.contractVersion)}, this app ${String(CORE_SCHEMA_VERSION)}`);
  }
  return { version: doc.version, contractVersion: doc.contractVersion };
}

function isNotReadYet(body: string): boolean {
  const doc = parseJson(body);
  return isRecord(doc) && isRecord(doc.error) && doc.error.reason === NOT_READ_YET;
}

/**
 * Reads an account's document from the service's answer. 200 is a usage document
 * (stale included), 502 a provider error and 503 `not_read_yet` the service's own
 * "not read yet": these are the account's answer (thrown as the CLI's would be).
 * Every other answer means no usable service for this read.
 */
export function parseServiceUsage(status: number, body: string, provider: string): CoreUsageResult {
  let exitCode: number;
  if (status === 200) exitCode = 0;
  else if (status === 502 || (status === 503 && isNotReadYet(body))) exitCode = 1;
  else if (status === 401) throw new ServiceUnusableError('unauthorized', 'Service refused the token');
  else if (status === 404) throw new ServiceUnusableError('unknown_account', 'Account not served by the service');
  else throw new ServiceUnusableError('failed', `Service answered ${String(status)}`);
  try {
    return parseUsageRun({ exitCode, stdout: body }, provider);
  } catch (err) {
    if (err instanceof CoreOutputError) throw new ServiceUnusableError('malformed', `Service document: ${err.message}`, { cause: err });
    throw err;
  }
}

export interface ServiceClient {
  /** The account's usage document from the service; ServiceUnusableError when there is no usable one. */
  usage(accountId: string, provider: string): Promise<CoreUsageResult>;
}

export interface ServiceClientOptions {
  // Resolved on every read: a service started or stopped later is found then.
  configDir: () => string;
  fetch?: FetchLike;
}

export function createServiceClient(options: ServiceClientOptions): ServiceClient {
  const fetchImpl: FetchLike = options.fetch ?? ((url, init) => fetch(url, init));
  return {
    async usage(accountId, provider) {
      let dir: string;
      try {
        dir = options.configDir();
      } catch (err) {
        throw new ServiceUnusableError('not_running', 'Core config folder unknown', { cause: err });
      }
      // api.json and /health on every read: a restarted service has a new port and token.
      const endpoint = await readApiFile(dir);
      await checkHealth(fetchImpl, endpoint);
      const { status, body } = await get(fetchImpl, endpoint, `/api/v1/accounts/${encodeURIComponent(accountId)}/usage`, USAGE_TIMEOUT_MS);
      return parseServiceUsage(status, body, provider);
    },
  };
}

/** Where the last read of an account came from: the service, or the CLI and why. */
export type UsageSourceRecord = { source: 'service'; reason: null } | { source: 'cli'; reason: UsageSourceReason };

/**
 * The service's document when there is a usable service, else the CLI's (`cli`). The
 * source of the read is passed to `record` (diagnostics). Errors of the service's own
 * answer (a provider error, `not_read_yet`) are thrown, never retried through the CLI.
 */
export async function readUsageWithFallback(
  service: ServiceClient,
  accountId: string,
  provider: string,
  cli: () => Promise<CoreUsageResult>,
  record: (source: UsageSourceRecord) => void,
): Promise<{ result: CoreUsageResult; source: UsageSourceRecord['source'] }> {
  try {
    const result = await service.usage(accountId, provider);
    record({ source: 'service', reason: null });
    return { result, source: 'service' };
  } catch (err) {
    if (!(err instanceof ServiceUnusableError)) {
      record({ source: 'service', reason: null });
      throw err;
    }
    record({ source: 'cli', reason: err.reason });
    return { result: await cli(), source: 'cli' };
  }
}
