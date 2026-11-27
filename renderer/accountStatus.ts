// renderer/accountStatus.ts — the status line under the widget's value: the window
// label, or why the data is not current (a failed sync with its error, a state the user
// has to act on, or the core's service still reading the account after a restart, #182),
// and the service not running (#341).

import { formatDateTime, t } from './i18n/index.js';
import type { AccountSnapshot } from '../types/index';

type AccountStatus = Pick<AccountSnapshot, 'stale' | 'lastUpdatedAt' | 'lastError' | 'notice' | 'refreshing' | 'serviceDown'>;

export function accountStatusLabel(account: AccountStatus, label: string): string {
  // No core service (#341, D1 on #181): the raw values only, and why there is no pace.
  if (account.serviceDown) label = t('widget.serviceDown', { label });
  if (!account.stale) return label;
  if (account.refreshing) {
    // Not a failure: the service has not read the account since it started.
    return account.lastUpdatedAt
      ? t('widget.serviceRefreshing', { label, time: formatDateTime(account.lastUpdatedAt) })
      : t('widget.serviceRefreshingFirst');
  }
  // A state the user has to act on (e.g. sign in with Codex, #178), not a failed sync.
  if (account.notice && !account.lastUpdatedAt) return account.notice;
  // Never a successful sync so far: the account IS connected but the very first
  // fetch failed — not to be confused with "not connected".
  // Without a service and without values, the label already says why (#341).
  const stale = account.lastUpdatedAt
    ? t('widget.stale', { label, time: formatDateTime(account.lastUpdatedAt) })
    : account.serviceDown ? label : t('widget.firstSyncFailed');
  const reason = account.lastError ?? account.notice;
  return reason ? t('widget.withError', { label: stale, error: reason }) : stale;
}
