// main/windows.ts — window creation and management (see docs/ARCHITECTURE.md §2, §6)
// Kept apart from main.ts so it is not weighed down by the skin logic.

import { BrowserWindow, screen } from 'electron';
import path from 'path';
import type Store from 'electron-store';
import type { AppSettings } from '../types/index';
import { APP_DIR } from './appDir';

const ROOT = APP_DIR;
// renderer/assets/ (not build/, which is not copied into dist/ — see scripts/generate-icons.js)
// so the same app icon reaches an unpackaged `npm start` too.
const ICON_PATH = path.join(ROOT, 'renderer', 'assets', 'app-icon.png');

// Compact view: only the percentage and the instant gauge. Fixed size, not resizable.
export const COMPACT_SIZE = { width: 260, height: 132 } as const;
const FULL_MIN_SIZE = { width: 260, height: 320 } as const;

/**
 * Switches the main window between the full and the compact view. The top-left corner
 * stays where it is (clamped to the screen when the full view would spill out); the
 * full size lives in `ui.bounds` and is never overwritten by the compact one.
 */
export function applyCompact(win: BrowserWindow, store: Store<AppSettings>, compact: boolean): void {
  const full = store.get('ui').bounds;
  const { x, y } = win.getBounds();
  if (compact) {
    // Save the full size now: a resize still waiting for the persistBounds debounce
    // would keep the older stored size, since by then the flag already says compact.
    // Only the full view is resizable, so an already compact window is never saved.
    if (win.isResizable()) store.set('ui.bounds', win.getBounds());
    win.setMinimumSize(0, 0);
    win.setResizable(true);
    win.setBounds({ x, y, ...COMPACT_SIZE });
    win.setResizable(false);
    return;
  }
  const area = screen.getDisplayMatching({ x, y, ...COMPACT_SIZE }).workArea;
  const width = Math.min(full.width, area.width);
  const height = Math.min(full.height, area.height);
  win.setResizable(true);
  win.setMinimumSize(FULL_MIN_SIZE.width, FULL_MIN_SIZE.height);
  win.setBounds({
    x: Math.max(area.x, Math.min(x, area.x + area.width - width)),
    y: Math.max(area.y, Math.min(y, area.y + area.height - height)),
    width,
    height,
  });
}

/** Creates the main window (widget) in the style chosen by the user. */
export function createMainWindow(store: Store<AppSettings>): BrowserWindow {
  const ui = store.get('ui');
  const isTransparent = ui.windowStyle === 'transparent-digital';

  const win = new BrowserWindow({
    width: ui.compact ? COMPACT_SIZE.width : ui.bounds.width,
    height: ui.compact ? COMPACT_SIZE.height : ui.bounds.height,
    resizable: !ui.compact,
    // Position only if ever saved: without it, Electron centers the window.
    ...(ui.bounds.x !== undefined && ui.bounds.y !== undefined ? { x: ui.bounds.x, y: ui.bounds.y } : {}),
    minWidth: ui.compact ? 0 : FULL_MIN_SIZE.width,
    minHeight: ui.compact ? 0 : FULL_MIN_SIZE.height,
    // Always without a native frame, in every skin (user feedback — with frame:true the
    // "filled" skin showed the OS native title bar above the app's custom title bar,
    // duplicated and with the menu bar). Window controls are always the custom ones in
    // renderer/index.html, with dragging handled via CSS -webkit-app-region (see
    // renderer/style.css).
    frame: false,
    transparent: isTransparent,
    // Initial window color before the CSS is applied (avoids a flash in the wrong
    // color) — for the "filled-dark" skin it must be the same hex as --bg-filled-dark in
    // renderer/style.css.
    backgroundColor: isTransparent ? '#00000000' : ui.windowStyle === 'filled-dark' ? '#1a1a1a' : '#fafafa',
    alwaysOnTop: ui.alwaysOnTop,
    // macOS: accept the first mouse-down as a content click even when the window
    // is not focused, preventing the "focus-click" issue (issue #25).
    acceptFirstMouse: true,
    show: true,
    icon: ICON_PATH,
    webPreferences: {
      preload: path.join(ROOT, 'preload.js'),
      nodeIntegration: false,
      contextIsolation: true,
    },
  });

  // Per-window defence in addition to the global Menu.setApplicationMenu(null) in
  // main.ts: without it, the "filled" skin (frame: true) showed Electron's default menu
  // bar (File/Edit/View/Window/Help) above the widget (user feedback).
  win.setMenuBarVisibility(false);
  // A failure here is a blank window: at least it leaves a trace in the log.
  win.loadFile(path.join(ROOT, 'renderer', 'index.html')).catch((err: unknown) => {
    console.error('[windows] widget load failed:', err);
  });

  // Position/size persistence (simple debounce so it is not written on every pixel)
  let saveBoundsTimer: NodeJS.Timeout | null = null;
  const persistBounds = () => {
    if (saveBoundsTimer) clearTimeout(saveBoundsTimer);
    saveBoundsTimer = setTimeout(() => {
      if (win.isDestroyed()) return;
      const bounds = win.getBounds();
      // Compact: only the position follows, the full size is kept for the way back.
      if (store.get('ui').compact) {
        const { width, height } = store.get('ui').bounds;
        store.set('ui.bounds', { x: bounds.x, y: bounds.y, width, height });
      } else {
        store.set('ui.bounds', bounds);
      }
    }, 400);
  };
  win.on('move', persistBounds);
  win.on('resize', persistBounds);

  return win;
}

/** Creates (or brings to front) the Settings window. */
export function createSettingsWindow(existing?: BrowserWindow | null): BrowserWindow {
  if (existing && !existing.isDestroyed()) {
    existing.focus();
    return existing;
  }

  const win = new BrowserWindow({
    // Wider than before for the accounts table (issue #4).
    width: 620,
    height: 680,
    resizable: true,
    frame: true,
    transparent: false,
    // Same as the page background (settings.css): the window is shown at once, before
    // the page paints, and must not flash white while the main process is busy.
    backgroundColor: '#fafafa',
    icon: ICON_PATH,
    webPreferences: {
      preload: path.join(ROOT, 'preload.js'),
      nodeIntegration: false,
      contextIsolation: true,
    },
  });

  win.setMenuBarVisibility(false);
  win.loadFile(path.join(ROOT, 'renderer', 'settings.html')).catch((err: unknown) => {
    console.error('[windows] Settings load failed:', err);
  });

  return win;
}
