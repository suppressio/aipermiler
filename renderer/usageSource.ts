// renderer/usageSource.ts — Settings → Diagnostics: where each account's last usage read
// came from, the core's service or the CLI with the reason of the fallback (#182).

import { t } from './i18n/index.js';
import type { UsageSourceReason, UsageSourceStatus } from '../types/index';

const REASON_KEYS = {
  not_running: 'settings.diagnostics.reason.notRunning',
  unreachable: 'settings.diagnostics.reason.unreachable',
  unauthorized: 'settings.diagnostics.reason.unauthorized',
  incompatible: 'settings.diagnostics.reason.incompatible',
  unknown_account: 'settings.diagnostics.reason.unknownAccount',
  rejected: 'settings.diagnostics.reason.rejected',
  failed: 'settings.diagnostics.reason.failed',
  malformed: 'settings.diagnostics.reason.malformed',
} as const satisfies Record<UsageSourceReason, string>;

/** "Work: service · Home: CLI (service not running)", or null when no account was read yet. */
export function usageSourceLine(sources: readonly UsageSourceStatus[]): string | null {
  if (sources.length === 0) return null;
  return sources.map((s) => (s.source === 'service' || s.reason === null
    ? t('settings.diagnostics.sourceService', { account: s.label })
    : t('settings.diagnostics.sourceCli', { account: s.label, reason: t(REASON_KEYS[s.reason]) }))).join(' · ');
}
