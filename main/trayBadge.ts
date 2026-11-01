// main/trayBadge.ts — usage badge of the tray icon (issue #33): which percentage it
// shows, its colour and the icon bitmap. Pure (no Electron), tested in trayBadge.test.ts.

import type { AccountSnapshot, ProviderId, QuotaWindow, TrayBadgeChoice, TrayBadgeEntry, TrayBadgeSettings, UsageView } from '../types/index';
import { normalizedUtilization } from '../budget';

type BadgeAccount = Pick<AccountSnapshot, 'accountId' | 'quotaWindows' | 'criticalWindow' | 'stale'> & { lastUpdatedAt?: string };

export interface Badge {
  percent: number;
  stale: boolean;
  view: UsageView;
}

function criticalBadge(account: BadgeAccount): Badge | null {
  const win = account.criticalWindow;
  const percent = win ? normalizedUtilization(win) : null;
  return win && percent !== null ? { percent, stale: account.stale === true, view: { accountId: account.accountId, windowId: win.id } } : null;
}

function autoBadge(accounts: BadgeAccount[], autoAccountId: string | null): Badge | null {
  const known = accounts.flatMap(account => {
    const badge = criticalBadge(account);
    const utilization = Math.max(...account.quotaWindows.map(win => normalizedUtilization(win) ?? -1));
    return badge ? [{ badge, utilization }] : [];
  });
  return (known.find(b => b.badge.view.accountId === autoAccountId)
    ?? known.sort((a, b) => b.utilization - a.utilization)[0])?.badge ?? null;
}

/** Stable on idle/reset. A new account/window establishes a baseline, not activity. */
export function trackAutoAccount(current: string | null, previous: BadgeAccount[], accounts: BadgeAccount[]): string | null {
  const rising = accounts.filter(account => !account.stale && account.quotaWindows.some(win => {
    const before = previous.find(a => a.accountId === account.accountId)?.quotaWindows.find(w => w.id === win.id);
    return before !== undefined && before.total === win.total && before.unit === win.unit && win.used > before.used;
  })).sort((a, b) => (b.lastUpdatedAt ?? '').localeCompare(a.lastUpdatedAt ?? ''));
  return rising[0]?.accountId ?? autoBadge(accounts, current)?.view.accountId ?? null;
}

/** The exact Manual view, or Auto while unavailable; the preference is never mutated. */
export function pickBadge(accounts: BadgeAccount[], choice: TrayBadgeSettings, autoAccountId: string | null): Badge | null {
  const view = choice.view;
  if (view.mode === 'off') return null;
  if (view.mode === 'manual') {
    const account = accounts.find(a => a.accountId === view.accountId);
    const win = account?.quotaWindows.find(w => w.id === view.windowId);
    const percent = win ? normalizedUtilization(win) : null;
    if (account && win && percent !== null) return { percent, stale: account.stale === true, view: { accountId: account.accountId, windowId: win.id } };
  }
  return autoBadge(accounts, autoAccountId);
}

/** Every window at or above the threshold except the shown one, highest first. */
export function pickAlerts(accounts: BadgeAccount[], choice: TrayBadgeSettings, shown: Badge | null, threshold: number): Badge[] {
  if (choice.view.mode === 'off' || !choice.critical) return [];
  return accounts.flatMap(account => account.quotaWindows.flatMap(win => {
    if (shown?.view.accountId === account.accountId && shown.view.windowId === win.id) return [];
    const percent = normalizedUtilization(win);
    return percent !== null && percent >= threshold
      ? [{ percent, stale: account.stale === true, view: { accountId: account.accountId, windowId: win.id } }] : [];
  })).sort((a, b) => b.percent - a.percent);
}

export function sameChoice(a: TrayBadgeChoice, b: TrayBadgeChoice): boolean {
  return a.mode === b.mode && (a.mode !== 'manual' || (b.mode === 'manual' && a.accountId === b.accountId && a.windowId === b.windowId));
}

/** null windowId is reserved for old account-only preferences with no cached usage. */
export function resolveLegacyView(view: TrayBadgeChoice, accounts: BadgeAccount[]): TrayBadgeChoice {
  if (view.mode !== 'manual' || view.windowId !== null) return view;
  const id = accounts.find(a => a.accountId === view.accountId)?.criticalWindow?.id;
  return id === undefined ? view : { ...view, windowId: id };
}

export interface BadgeMenuAccount {
  id: string;
  label: string;
  provider: ProviderId;
  enabled: boolean;
  quotaWindows: QuotaWindow[];
}

export function badgeEntries(registry: BadgeMenuAccount[], accounts: BadgeAccount[], choice: TrayBadgeChoice): TrayBadgeEntry[] {
  const entries: TrayBadgeEntry[] = ['off', 'auto'].map(mode => ({
    view: { mode: mode === 'off' ? 'off' : 'auto' }, checked: choice.mode === mode, enabled: true,
  }));
  for (const account of registry) {
    const live = accounts.find(a => a.accountId === account.id);
    for (const win of live?.quotaWindows ?? account.quotaWindows) {
      const view: TrayBadgeChoice = { mode: 'manual', accountId: account.id, windowId: win.id };
      entries.push({ view, checked: sameChoice(choice, view), enabled: account.enabled && live?.quotaWindows.some(w => w.id === win.id) === true,
        accountLabel: account.label, provider: account.provider, window: win });
    }
  }
  if (choice.mode === 'manual' && !entries.some(e => e.checked)) {
    const account = registry.find(a => a.id === choice.accountId);
    entries.push({ view: choice, checked: true, enabled: false, accountLabel: account?.label ?? choice.accountId });
  }
  return entries;
}

export type BadgeTone = 'ok' | 'caution' | 'warning' | 'stale';

const CAUTION_POINTS = 5;

/** Same bands as the widget (renderer/severity.ts): red from the notification threshold. */
export function badgeTone(badge: Pick<Badge, 'percent' | 'stale'>, threshold: number): BadgeTone {
  if (badge.stale) return 'stale';
  if (badge.percent >= threshold) return 'warning';
  if (badge.percent >= threshold - CAUTION_POINTS) return 'caution';
  return 'ok';
}

const TONE_RGB: Record<BadgeTone, [number, number, number]> = {
  ok: [22, 163, 74],
  caution: [217, 119, 6],
  warning: [220, 38, 38],
  stale: [107, 114, 128],
};

// 3×5 digit glyphs, one string per row ('#' = lit).
const GLYPHS: Record<string, readonly string[]> = {
  '!': [' # ', ' # ', ' # ', '   ', ' # '],
  '0': ['###', '# #', '# #', '# #', '###'],
  '1': [' # ', '## ', ' # ', ' # ', '###'],
  '2': ['###', '  #', '###', '#  ', '###'],
  '3': ['###', '  #', '###', '  #', '###'],
  '4': ['# #', '# #', '###', '  #', '  #'],
  '5': ['###', '#  ', '###', '  #', '###'],
  '6': ['###', '#  ', '###', '# #', '###'],
  '7': ['###', '  #', '  #', '  #', '  #'],
  '8': ['###', '# #', '###', '# #', '###'],
  '9': ['###', '# #', '###', '  #', '###'],
};

export const BADGE_SIZE = 32;
const CORNER = 6;

export function badgeText(percent: number, alert = false): string {
  const value = Math.min(100, Math.max(0, Math.round(percent)));
  return `${alert && value < 100 ? '!' : ''}${String(value)}`;
}

/** RGBA bitmap (BADGE_SIZE², row-major): a rounded square in the tone colour with white digits. */
export function renderBadge(percent: number, tone: BadgeTone, alert = false): Uint8Array {
  const size = BADGE_SIZE;
  const pixels = new Uint8Array(size * size * 4);
  const [r, g, b] = TONE_RGB[tone];
  const inside = (x: number, y: number): boolean => {
    const dx = Math.max(CORNER - x, x - (size - 1 - CORNER), 0);
    const dy = Math.max(CORNER - y, y - (size - 1 - CORNER), 0);
    return dx * dx + dy * dy <= CORNER * CORNER;
  };
  const put = (x: number, y: number, rgb: readonly [number, number, number]): void => {
    const i = (y * size + x) * 4;
    pixels.set([rgb[0], rgb[1], rgb[2], 255], i);
  };
  for (let y = 0; y < size; y++) for (let x = 0; x < size; x++) if (inside(x, y)) put(x, y, [r, g, b]);

  const text = badgeText(percent, alert);
  const scale = text.length >= 3 ? 3 : 4;
  const gap = text.length >= 3 ? 1 : 2;
  const width = text.length * 3 * scale + (text.length - 1) * gap;
  const left = Math.floor((size - width) / 2);
  const top = Math.floor((size - 5 * scale) / 2);
  text.split('').forEach((char, index) => {
    const rows = GLYPHS[char] ?? [];
    const originX = left + index * (3 * scale + gap);
    rows.forEach((row, ry) => {
      row.split('').forEach((cell, rx) => {
        if (cell !== '#') return;
        for (let dy = 0; dy < scale; dy++) for (let dx = 0; dx < scale; dx++) put(originX + rx * scale + dx, top + ry * scale + dy, [255, 255, 255]);
      });
    });
  });
  return pixels;
}
