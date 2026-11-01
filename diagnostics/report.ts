// diagnostics/report.ts — the manual diagnostic report file (Settings → Diagnostics):
// one text file with a JSON document the user attaches to a GitHub issue by hand.
// Pure: main.ts gathers the data, this module only redacts and lays it out. Never
// account labels, credentials, ids, tenant names or free text: accounts are
// "<Provider> #n", ids and labels found in texts are replaced by those names.

import { redactResponse, shortError } from './githubIssue';
import type { LogEntry } from './logBuffer';

export interface ReportAccount {
  name: string;
  provider: string;
  enabled: boolean;
  // Original label and id, only to neutralize free texts (neutralize) — never written.
  // `config`, `interpretation` and `history` must already be free of them (main.ts).
  label: string;
  id: string;
  config: Record<string, unknown>;
  endpointLabel: string | null;
  response?: unknown;
  readError?: string;
  interpretation: unknown;
  history: unknown;
}

export interface ReportInput {
  generatedAt: Date;
  environment: Record<string, string>;
  settings: Record<string, unknown>;
  // Machine and app process facts useful for diagnosis (main.ts reportSystem).
  system: Record<string, unknown>;
  accounts: ReportAccount[];
  log: LogEntry[];
}

// Labels/ids not worth hiding, and too generic to replace safely in free text:
// a provider name ("Claude", also the legacy account id "claude") appears in endpoint
// labels and messages.
const GENERIC_TOKENS = new Set(['claude', 'copilot', 'github copilot', 'codex', 'codex (chatgpt)']);

function escapeRegExp(text: string): string {
  return text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

/**
 * Replaces account labels and ids in a FREE TEXT (log line, error message) with the
 * neutral account name, in a single pass (a name just inserted is never replaced
 * again), longest first. Structured data is built without labels/ids by main.ts.
 */
export function neutralize(text: string, accounts: Pick<ReportAccount, 'name' | 'label' | 'id'>[]): string {
  const replacements = new Map<string, string>();
  for (const a of accounts) {
    for (const token of [a.label, a.id]) {
      const trimmed = token.trim();
      if (trimmed.length >= 3 && !GENERIC_TOKENS.has(trimmed.toLowerCase())) replacements.set(trimmed, a.name);
    }
  }
  if (replacements.size === 0) return text;
  const pattern = new RegExp([...replacements.keys()].sort((x, y) => y.length - x.length).map(escapeRegExp).join('|'), 'g');
  return text.replace(pattern, (match) => replacements.get(match) ?? match);
}

export function buildDiagnosticReport(input: ReportInput): string {
  const document = {
    report: 'AIpermiler diagnostic report',
    generatedAt: input.generatedAt.toISOString(),
    environment: input.environment,
    settings: input.settings,
    system: input.system,
    accounts: input.accounts.map((a) => ({
      name: a.name,
      provider: a.provider,
      enabled: a.enabled,
      config: a.config,
      endpoint: a.endpointLabel,
      response: a.readError !== undefined ? undefined : redactResponse(a.response),
      readError: a.readError !== undefined ? neutralize(shortError(a.readError), input.accounts) : undefined,
      interpretation: a.interpretation,
      history: a.history,
    })),
    log: input.log.map((e) => ({ ...e, message: neutralize(e.message, input.accounts) })),
  };
  return [
    '# AIpermiler diagnostic report — real usage values included; names, ids, credentials and free text redacted.',
    JSON.stringify(document, null, 2),
    '',
  ].join('\n');
}
