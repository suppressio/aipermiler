// main/tray.ts — cross-platform system tray (see docs/ARCHITECTURE.md §4)

import { Tray, Menu, nativeImage, app, BrowserWindow, MenuItem } from 'electron';
import path from 'path';
import type Store from 'electron-store';
import { trayTooltip } from './traySummary';
import { BADGE_SIZE, badgeText, badgeTone, pickBadge, pickAlerts, renderBadge } from './trayBadge';
import { windowLabel } from './windowLabel';
import type { AppSettings, UsageSnapshot, TrayBadgeChoice, TrayViewState } from '../types/index';
import { t } from './i18n/index';

const ICON_PATH = path.join(__dirname, '..', 'renderer', 'assets', 'tray-icon.png');

export interface CreateTrayDeps {
  getMainWindow: () => BrowserWindow | null;
  openSettings: () => void;
  refreshNow: () => void;
  store: Store<AppSettings>;
  getViewState: () => TrayViewState;
  selectView: (choice: TrayBadgeChoice) => void;
}

/** Creates the tray icon with its context menu. */
export interface TrayHandle {
  tray: Tray;
  // Rebuilds the menu (e.g. after an update check or a language change): Electron
  // does not refresh a context menu that is already set.
  refreshMenu: () => void;
  updateUsage: (snapshot: UsageSnapshot) => void;
}

export function createTray({ getMainWindow, openSettings, refreshNow, store, getViewState, selectView }: CreateTrayDeps): TrayHandle {
  // If the custom icon does not exist (missing asset), an empty fallback icon is used:
  // Electron does not crash, but a real cross-platform asset must ship in builds (see
  // scripts/generate-icons.js).
  let icon;
  try {
    icon = nativeImage.createFromPath(ICON_PATH);
    if (icon.isEmpty()) icon = nativeImage.createEmpty();
  } catch {
    icon = nativeImage.createEmpty();
  }

  const baseIcon = icon;
  const tray = new Tray(icon);
  tray.setToolTip('AIpermiler');

  let latestSnapshot: UsageSnapshot | null = null;
  let shownBadge = '';
  let alertTimer: ReturnType<typeof setInterval> | null = null;
  const stopAlert = () => {
    if (alertTimer) clearInterval(alertTimer);
    alertTimer = null;
    alertIndex = -1;
  };
  app.once('before-quit', stopAlert);

  let normal: ReturnType<typeof pickBadge> = null;
  let alerts: ReturnType<typeof pickAlerts> = [];
  // Position in the cycle normal → alert 0 → alert 1 → … → normal; -1 = the normal view.
  let alertIndex = -1;
  let threshold = 80;
  const renderFrame = () => {
    const alert = alerts[alertIndex];
    const badge = alert ?? normal;
    const isAlert = alert !== undefined;
    const tone = badge ? isAlert ? 'warning' : badgeTone(badge, threshold) : null;
    const key = badge && tone ? `${badgeText(badge.percent, isAlert)}:${tone}:${String(isAlert)}:${String(badge.stale)}` : '';
    if (key === shownBadge) return;
    shownBadge = key;
    if (process.platform === 'darwin') {
      tray.setTitle(badge ? `${isAlert ? '!' : ''}${badgeText(badge.percent)}%${badge.stale ? '*' : ''}` : '');
      return;
    }
    if (!badge || !tone) {
      tray.setImage(baseIcon);
      return;
    }
    // Electron wants BGRA; the pure renderer draws RGBA.
    const rgba = renderBadge(badge.percent, tone, isAlert);
    const bgra = Buffer.alloc(rgba.length);
    for (let i = 0; i < rgba.length; i += 4) {
      bgra[i] = rgba[i + 2] ?? 0;
      bgra[i + 1] = rgba[i + 1] ?? 0;
      bgra[i + 2] = rgba[i] ?? 0;
      bgra[i + 3] = rgba[i + 3] ?? 0;
    }
    tray.setImage(nativeImage.createFromBitmap(bgra, { width: BADGE_SIZE, height: BADGE_SIZE }));
  };

  const applyBadge = () => {
    const ui = store.get('ui');
    const state = getViewState();
    const accounts = latestSnapshot?.accounts ?? [];
    threshold = ui.notificationThresholdPercent;
    normal = pickBadge(accounts, state.selection, state.shown?.accountId ?? null);
    alerts = pickAlerts(accounts, state.selection, normal, threshold);
    if (alertIndex >= alerts.length) alertIndex = -1;
    if (alerts.length > 0 && !alertTimer) {
      alertTimer = setInterval(() => {
        if (tray.isDestroyed()) { stopAlert(); return; }
        alertIndex = alertIndex + 1 >= alerts.length ? -1 : alertIndex + 1;
        renderFrame();
      }, 2000);
    } else if (alerts.length === 0) stopAlert();
    renderFrame();
  };

  const toggleMainWindow = () => {
    const win = getMainWindow();
    if (!win || win.isDestroyed()) return;
    if (win.isVisible()) win.hide();
    else win.show();
  };

  const buildMenu = () => {
    const ui = store.get('ui');
    const available = store.get('updates').available;
    return Menu.buildFromTemplate([
      ...(available
        ? [{ label: t('tray.updateAvailable', { version: available.version }), click: openSettings }, { type: 'separator' as const }]
        : []),
      { label: t('tray.toggle'), click: toggleMainWindow },
      { label: t('tray.settings'), click: openSettings },
      { label: t('tray.refresh'), click: refreshNow },
      { type: 'separator' },
      ...getViewState().entries.map(entry => ({
        type: 'radio' as const,
        label: entry.view.mode === 'off' ? t('tray.badgeOff') : entry.view.mode === 'auto' ? t('tray.badgeAuto')
          : entry.window && entry.provider ? `${entry.accountLabel ?? ''} · ${windowLabel(entry.window, entry.provider)}`
          : t('tray.badgeUnavailable', { account: entry.accountLabel ?? '', window: entry.view.windowId ?? t('tray.badgePending') }),
        checked: entry.checked,
        enabled: entry.enabled,
        click: () => { selectView(entry.view); },
      })),
      { type: 'separator' },
      {
        label: t('tray.alwaysOnTop'),
        type: 'checkbox',
        checked: ui.alwaysOnTop,
        click: (menuItem: MenuItem) => {
          store.set('ui.alwaysOnTop', menuItem.checked);
          const win = getMainWindow();
          if (win && !win.isDestroyed()) win.setAlwaysOnTop(menuItem.checked, 'floating');
        },
      },
      { type: 'separator' },
      { label: t('tray.quit'), click: () => { app.quit(); } },
    ]);
  };

  tray.setContextMenu(buildMenu());

  // Left click: toggle on Windows/Linux. On macOS a left click usually opens the
  // native tray menu: its "Show/Hide" entry covers that case, giving the same behaviour
  // on every platform.
  tray.on('click', toggleMainWindow);

  return {
    tray,
    refreshMenu: () => { tray.setContextMenu(buildMenu()); applyBadge(); },
    updateUsage: (snapshot) => {
      latestSnapshot = snapshot;
      tray.setToolTip(trayTooltip(snapshot.accounts));
      tray.setContextMenu(buildMenu());
      applyBadge();
    },
  };
}
