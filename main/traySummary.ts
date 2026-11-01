import type { AccountSnapshot } from '../types/index';
import { normalizedUtilization } from '../budget';
import { formatNumber, t } from './i18n/index';

type SummaryAccount = Pick<AccountSnapshot, 'label' | 'criticalWindow' | 'stale'>;

export function trayTooltip(accounts: SummaryAccount[]): string {
  const summary = accounts.map((account) => {
    const used = account.criticalWindow ? normalizedUtilization(account.criticalWindow) : null;
    const usage = `${account.label} ${used === null ? '--' : `${formatNumber(used)}%`}`;
    return account.stale ? t('tray.staleUsage', { usage }) : usage;
  }).join(' · ');
  return summary ? `AIpermiler\n${summary}` : 'AIpermiler';
}
