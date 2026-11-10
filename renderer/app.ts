// app.ts — renderer logic of the main widget (no direct access to Node.js).
// Reads/writes only through window.aipermiler, exposed by preload.ts.
// Every visible string goes through t() (renderer/i18n): see CLAUDE.md, i18n rule.

import type {
  AccountSnapshot,
  AccountId,
  AppSettings,
  DailyTip,
  ProviderId,
  QuotaWindow,
  QuotaWindowSnapshot,
  UsageSnapshot,
  WindowVerdict,
  UsageView,
  TrayViewState,
} from '../types/index';
import type { AipermilerBridge } from '../types/ipc';
import { windowLabel } from './windowLabel.js';
import { byId } from './dom.js';
import { applySeverity, severityLevel, todaySeverity } from './severity.js';
import { GAUGE_TARGET_POSITION, GAUGE_TICK_RATIOS, gaugePosition } from './gauge.js';
import { localDateKey, parseDateKey } from './dates.js';
import { showAutonomy, showPeakAvg, showStreak } from './visibility.js';
import { createAlarmPulse, motionState } from './motion.js';
import { finishOdometer, renderOdometer } from './odometer.js';
import { createCompactTransition, createLayoutAnimation, fadeSlide, initDetailsMotion } from './layoutMotion.js';
import { flowSpan, instantFlow, isWorkingDay } from './instantFlow.js';
import {
  applyTranslations,
  formatDate,
  formatDateTime,
  formatNumber,
  formatTime,
  resolveLocale,
  setLocale,
  t,
} from './i18n/index.js';
import type { MessageKey } from './i18n/en.js';

declare global {
  interface Window {
    aipermiler: AipermilerBridge;
  }
}

interface RendererState {
  settings: AppSettings | null;
  latestSnapshot: UsageSnapshot | null;
  view: TrayViewState | null;
  // Auto/Off browsing stays local; activity in the tray must not move the panel.
  // A null window = an account without quota windows (nothing for the badge to show).
  localView: PanelView | null;
  // Manual, but the user opened an account with no window: the panel shows it locally
  // until the selection changes.
  localOverride: boolean;
}

type PanelView = Omit<UsageView, 'windowId'> & { windowId: string | null };

const state: RendererState = {
  settings: null,
  latestSnapshot: null,
  view: null,
  localView: null,
  localOverride: false,
};

const reducedMotion = window.matchMedia('(prefers-reduced-motion: reduce)');
const alarmPulse = createAlarmPulse((active) => { document.body.classList.toggle('alarm-pulse', active); });
let alarmed = false;
let refreshing = false;
let valueContext: string | null = null;
const compactAnimation = createLayoutAnimation(true);
let detailsMotion: { finish(): void } | undefined;
const compactTransition = createCompactTransition({
  isCompact: () => document.body.classList.contains('compact'),
  effects: () => currentMotion().effects,
  animate: (expanding) => compactAnimation.play(
    Array.from(document.querySelectorAll<HTMLElement>('.compact-extra'))
      .filter((element) => element.getClientRects().length > 0)
      .map((element) => fadeSlide(element, expanding)),
  ),
  finish: () => { compactAnimation.finish(); },
  resize: (compact) => window.aipermiler.setCompact(compact),
  apply: (compact) => {
    if (state.settings) state.settings.ui.compact = compact;
    updateCompactButton(compact);
  },
});

function currentMotion(): ReturnType<typeof motionState> {
  return motionState(state.settings?.ui.animations ?? true, reducedMotion.matches, !document.hidden);
}

function syncMotion(): void {
  const motion = currentMotion();
  document.body.classList.toggle('motion-effects', motion.effects);
  document.body.classList.toggle('motion-hidden', document.hidden);
  document.body.classList.toggle('motion-disabled', state.settings?.ui.animations === false);
  // Compact or single-window view: the label carries the alarm, no window rows (style.css).
  document.body.classList.toggle('alarm', alarmed);
  document.body.classList.toggle('refresh-border-active', refreshing && motion.effects);
  document.body.classList.toggle('refresh-rotate', refreshing && motion.rotateRefresh);
  alarmPulse.setActive(alarmed && motion.effects);
  if (!motion.effects) {
    finishOdometer(byId('current-value'));
    detailsMotion?.finish();
    compactAnimation.finish();
  }
  const button = byId('btn-refresh', HTMLButtonElement);
  button.disabled = refreshing;
  button.setAttribute('aria-busy', String(refreshing));
  const key = refreshing ? 'widget.refreshing' : 'widget.refresh';
  button.dataset.i18nTitle = key;
  button.dataset.i18nAriaLabel = key;
  button.title = t(key);
  button.setAttribute('aria-label', t(key));
}

function requestRefresh(): void {
  refreshing = true;
  syncMotion();
  window.aipermiler.requestUsageRefresh();
}

// --- Language ------------------------------------------------------------------

// Applies the configured language (or the system one for 'auto') to the static
// markup and re-renders the dynamic parts with the latest snapshot.
function applyLanguage(settings: AppSettings): void {
  const locale = resolveLocale(settings.ui.language, navigator.language);
  setLocale(locale, navigator.language);
  document.documentElement.lang = locale;
  applyTranslations(document);
  if (state.latestSnapshot) renderSnapshot(state.latestSnapshot);
}

// --- Appearance ----------------------------------------------------------------

function applyWindowStyle(style: AppSettings['ui']['windowStyle']): void {
  document.body.classList.remove('style-filled', 'style-filled-dark', 'style-transparent-digital');
  const cls = style === 'transparent-digital' ? 'style-transparent-digital'
    : style === 'filled-dark' ? 'style-filled-dark'
    : 'style-filled';
  document.body.classList.add(cls);
}

// The accent color (Settings → Appearance) used to be saved but never applied:
// style.css reads --accent from :root, which was never updated (user feedback).
function applyAccentColor(accentColor: string | undefined): void {
  if (!accentColor) return;
  document.documentElement.style.setProperty('--accent', accentColor);
}

// Mirrors the "always on top" state on the title bar pin (same source of truth
// as ui.alwaysOnTop, also settable from Settings/tray).
function updatePinButton(active: boolean): void {
  const btn = byId('btn-pin', HTMLButtonElement);
  btn.classList.toggle('active', active);
  btn.setAttribute('aria-pressed', String(active));
}

// Compact view: the triangle points up when compact, down when full; rotating it
// by 180 degrees follows the body class that switches the layout (style.css).
function updateCompactButton(compact: boolean): void {
  const btn = byId('btn-compact', HTMLButtonElement);
  const label = t(compact ? 'widget.expand' : 'widget.compact');
  document.body.classList.toggle('compact', compact);
  btn.setAttribute('aria-pressed', String(compact));
  btn.title = label;
  btn.setAttribute('aria-label', label);
}

// Reveals buttons/drag handle while hovering the WHOLE window. DOM mouse events
// were abandoned (mouseenter/leave, then mouseover/out): the
// -webkit-app-region:drag strip is treated by the OS as a non-client area, so
// regular document mouse events are unreliable exactly there — the bar vanished
// while hovering it (repeated user feedback). The reliable fix computes hover in
// the main process with screen.getCursorScreenPoint() (see main.ts,
// startWindowHoverPolling) and receives it here via IPC.
function initHoverReveal(): void {
  window.aipermiler.onWindowHoverChanged((isHovering) => {
    document.body.classList.toggle('window-hover', isHovering);
  });
}

// --- Formatting ------------------------------------------------------------------

// Small local copy of budget.normalizedUtilization: the renderer cannot
// require()/import budget.ts (nodeIntegration is disabled on purpose).
function budgetNormalizedUtilization(win: QuotaWindow): number | null {
  if (win.unit === 'percentage') return win.used;
  if (typeof win.total === 'number' && win.total > 0) {
    return Math.round((win.used / win.total) * 1000) / 10;
  }
  return null;
}

// Red threshold of percent-of-quota values (issue #13): the notification threshold, so
// colour and notification agree; the default before settings are loaded.
function warningThreshold(): number {
  return state.settings?.ui.notificationThresholdPercent ?? 80;
}

function formatPercent(value: number | null | undefined): string {
  if (value === null || value === undefined || Number.isNaN(value)) return '--%';
  return `${formatNumber(value, 1)}%`;
}

// Same as budget.NEGLIGIBLE_UTILIZATION (the renderer tsconfig is isolated).
const NEGLIGIBLE_UTILIZATION = 0.1;

function formatEfficiency(value: number | null | undefined): string {
  if (value === null || value === undefined) return '--';
  return formatNumber(value, 2);
}

// The raw efficiencyIndex value (e.g. "1.75") is not self-explanatory (user
// feedback). >= 1 = consuming less than the ideal pace (good), < 1 = more than
// sustainable (risk) — see budget.efficiencyIndex.
function formatEfficiencyHint(value: number | null | undefined): string {
  if (value === null || value === undefined) return '';
  return value >= 1 ? t('widget.metric.efficiencyBelow') : t('widget.metric.efficiencyAbove');
}

// Hours and minutes ("1 h 36 min"), for windows of a few hours (budget.hourlyOutlook).
function formatHours(hours: number): string {
  const totalMinutes = Math.max(0, Math.round(hours * 60));
  return t('unit.hoursMinutes', { h: Math.floor(totalMinutes / 60), m: totalMinutes % 60 });
}

function formatDays(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return '—';
  return t('unit.days', { n: formatNumber(value, 1) });
}

// Issue #3: when the reset falls today the date says nothing useful — the time
// does. Compared on the LOCAL calendar day, not UTC.
function formatResetMoment(resetsAt: Date | string): string {
  const d = new Date(resetsAt);
  const now = new Date();
  const isToday = d.getFullYear() === now.getFullYear() && d.getMonth() === now.getMonth() && d.getDate() === now.getDate();
  return isToday ? t('widget.resetAtTime', { time: formatTime(d) }) : formatDate(d);
}

function formatRate(value: number | null | undefined): string {
  if (value === null || value === undefined) return '--';
  return t('unit.ratePerHour', { n: formatNumber(value, 2) });
}

// Tip of the day: budget.generateDailyTip returns a key + numbers, the sentence
// is composed here so it follows the active language without a refresh.
function formatTip(tip: DailyTip, win: QuotaWindow, provider: ProviderId): string {
  const window = windowLabel(win, provider);
  const reset = win.resetsAt ? formatDate(win.resetsAt) : t('tips.nextReset');
  // Numbers in the active locale ("6,5" in Italian, not "6.5"): the tip carries raw values.
  const numbers = Object.fromEntries(Object.entries(tip.params).map(([k, v]) => [k, formatNumber(v, 2)]));
  const params = { ...numbers, window, reset };
  switch (tip.key) {
    case 'none': return '';
    case 'autonomy': return t('tips.autonomy', params);
    case 'rating': return t('tips.rating', params);
    case 'nearReset': return t('tips.nearReset', params);
    case 'nearResetToday': return t('tips.nearResetToday', params);
    case 'cause': return t('tips.cause', params);
  }
}

function formatVerdict(verdict: WindowVerdict, win: QuotaWindow): string {
  const moment = win.resetsAt ? formatResetMoment(win.resetsAt) : null;
  // Verdicts resting on the redistribution (budget.windowVerdict) carry the quota per
  // working day left and the even share.
  const quota = verdict.perUnit !== undefined && verdict.idealPerUnit !== undefined
    ? { perUnit: formatPercent(verdict.perUnit), ideal: formatPercent(verdict.idealPerUnit) }
    : null;
  switch (verdict.kind) {
    case 'exhausted': return moment ? t('verdict.exhaustedReset', { moment }) : t('verdict.exhausted');
    case 'at-risk':
      if (quota && verdict.pacePerUnit !== undefined) return t('verdict.atRiskPace', { ...quota, pace: formatPercent(verdict.pacePerUnit) });
      if (quota) return t('verdict.atRiskQuota', quota);
      if (verdict.autonomyHours !== undefined) return t('verdict.atRiskHours', { time: formatHours(verdict.autonomyHours) });
      if (verdict.autonomyWorkingDays !== undefined) return t('verdict.atRiskAutonomy', { days: formatDays(verdict.autonomyWorkingDays) });
      return t('verdict.atRiskProjection', { value: formatPercent(verdict.projectedUsage) });
    case 'behind': return quota ? t('verdict.behind', quota) : t('verdict.onTrack');
    case 'on-track': return quota ? t('verdict.onTrackQuota', quota) : t('verdict.onTrack');
    case 'ahead': return quota ? t('verdict.ahead', quota) : t('verdict.onTrack');
    case 'no-pacing': return moment ? t('verdict.noPacingReset', { moment }) : t('verdict.noPacing');
  }
}

// Today's budget (budget.todayBudget): today's consumption against the share of the
// quota today can use, with what is left or how far over. Red over budget, orange
// near it (renderer/severity.ts todaySeverity). The hint also carries the remaining quota redistributed per
// working day (budget.redistributedQuota), also on a non-working day.
function renderTodayBudget(winSnap: QuotaWindowSnapshot | undefined): void {
  const valueEl = byId('metric-today', HTMLElement);
  const hintEl = byId('metric-today-hint', HTMLElement);
  const today = winSnap?.todayBudget ?? null;
  const redistribution = winSnap?.redistribution ?? null;
  const hints: string[] = [];
  if (redistribution) {
    hints.push(t('widget.metric.todayQuota', {
      perUnit: formatPercent(redistribution.perUnit),
      ideal: formatPercent(redistribution.idealPerUnit),
    }));
  }
  applySeverity(valueEl, 'none');
  if (!today) {
    valueEl.textContent = '--';
    hintEl.textContent = hints.join(' · ');
    return;
  }
  valueEl.textContent = `${formatPercent(today.usedToday)} / ${formatPercent(today.budget)}`;
  // Red only over budget, orange from the alert threshold of it (renderer/severity.ts).
  applySeverity(valueEl, todaySeverity(today.usedToday, today.budget, warningThreshold()));
  const diff = today.budget - today.usedToday;
  if (diff < 0) {
    hints.push(t('widget.metric.todayOver', { value: formatPercent(-diff) }));
  } else {
    hints.push(t('widget.metric.todayLeft', { value: formatPercent(diff) }));
  }
  hintEl.textContent = hints.join(' · ');
}

// --- Daily chart -------------------------------------------------------------------

// "Daily consumption vs budget" chart (docs/EVOLUTION.md point 1), one slot per day from
// winSnap.chart (budget.chartDays: the last 7 or 30 days, then 2 or 5 days to come):
// - wide bar: the consumption of THAT day (not the cumulative % of the provider
//   dashboard), --warning above that day's budget; faint when there is no data;
// - thin bar beside it: the even share of a full working day, coloured for the part
//   that is working time that day (all of it, half, none) and grey for the rest;
// - dashed line: the moving budget — what was available that morning on a past day,
//   today's budget today, the remaining quota redistributed on the days to come.
function renderChart(winSnap: QuotaWindowSnapshot | undefined): void {
  const container = byId('chart', HTMLDivElement);
  const idealLabel = byId('chart-ideal', HTMLSpanElement);
  container.innerHTML = '';
  idealLabel.textContent = '';

  if (winSnap?.window.periodType === 'rolling-hours') {
    // Same criterion as the star rating: on a window of a few hours a "daily"
    // consumption spans several resets and measures nothing.
    const note = document.createElement('div');
    note.className = 'chart-note';
    note.textContent = t('widget.chart.notApplicable');
    container.appendChild(note);
    return;
  }

  const series = winSnap?.chart ?? [];
  if (series.length === 0) return;
  const fullShare = Math.max(0, ...series.map((d) => d.fullShare ?? 0));
  if (fullShare > 0) idealLabel.textContent = t('widget.chart.ideal', { value: formatPercent(fullShare) });

  const width = container.clientWidth || 300;
  const height = 90;
  const max = Math.max(0.1, ...series.map((d) => Math.max(d.delta ?? 0, d.fullShare ?? 0, d.budget ?? 0)));
  const slotWidth = width / series.length;
  // Thin share bar of about 2.5 px right beside the consumption bar (user feedback:
  // a few pixels are enough), narrower only when 30 slots leave no room.
  const shareWidth = Math.min(2.5, slotWidth * 0.2);
  const shareGap = Math.min(2, slotWidth * 0.1);
  const barWidth = Math.max(1, slotWidth * 0.85 - shareWidth - shareGap - 1);
  const shareX = 1 + barWidth + shareGap;
  const scale = (value: number) => (value / max) * (height - 4);

  const svgNs = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(svgNs, 'svg');
  svg.setAttribute('viewBox', `0 0 ${width} ${height}`);
  svg.setAttribute('width', '100%');
  svg.setAttribute('height', String(height));
  const addRect = (x: number, y: number, w: number, h: number, fill: string, opacity: string): SVGRectElement => {
    const rect = document.createElementNS(svgNs, 'rect');
    rect.setAttribute('x', String(x));
    rect.setAttribute('y', String(y));
    rect.setAttribute('width', String(w));
    rect.setAttribute('height', String(h));
    rect.setAttribute('fill', fill);
    rect.setAttribute('opacity', opacity);
    svg.appendChild(rect);
    return rect;
  };

  // Today stands out from the past and the days to come: a faint band behind its slot
  // (the last one that is not upcoming), drawn first so bars and lines stay on top.
  const todayIndex = series.filter((d) => !d.upcoming).length - 1;
  if (todayIndex >= 0) {
    const band = addRect(todayIndex * slotWidth, 0, slotWidth, height, 'currentColor', '0.1');
    band.setAttribute('rx', '3');
    const title = document.createElementNS(svgNs, 'title');
    title.textContent = t('widget.chart.today');
    band.appendChild(title);
  }

  series.forEach((point, i) => {
    const x = i * slotWidth;
    const date = formatDate(parseDateKey(point.date));
    const delta = point.delta;

    if (!point.upcoming) {
      const barHeight = Math.max(2, scale(delta ?? 0));
      const bar = addRect(x + 1, height - barHeight, barWidth, barHeight, point.overBudget ? 'var(--warning)' : 'var(--accent)', delta !== null ? '0.85' : '0.15');
      const title = document.createElementNS(svgNs, 'title');
      title.textContent = delta === null
        ? t('widget.chart.noData', { date })
        : point.budget !== null
          ? t('widget.chart.pointWithBudget', { date, value: formatPercent(delta), budget: formatPercent(point.budget) })
          : t('widget.chart.point', { date, value: formatPercent(delta) });
      bar.appendChild(title);
    }

    if (point.fullShare !== null && point.fullShare > 0) {
      const working = scale(point.fullShare * point.dayUnit);
      const rest = scale(point.fullShare * (1 - point.dayUnit));
      if (working > 0) addRect(x + shareX, height - working, shareWidth, working, 'var(--accent)', '0.75');
      if (rest > 0) addRect(x + shareX, height - working - rest, shareWidth, rest, 'currentColor', '0.25');
    }

    if (point.budget !== null && point.budget > 0) {
      const y = height - scale(point.budget);
      const line = document.createElementNS(svgNs, 'line');
      line.setAttribute('x1', String(x));
      line.setAttribute('x2', String(x + slotWidth));
      line.setAttribute('y1', String(y));
      line.setAttribute('y2', String(y));
      line.setAttribute('stroke', 'currentColor');
      line.setAttribute('stroke-width', '1.5');
      line.setAttribute('stroke-dasharray', '4 2');
      line.setAttribute('opacity', point.upcoming ? '0.6' : '0.95');
      if (point.upcoming) {
        const title = document.createElementNS(svgNs, 'title');
        title.textContent = t('widget.chart.upcoming', { date, budget: formatPercent(point.budget) });
        line.appendChild(title);
      }
      svg.appendChild(line);
    }
  });

  container.appendChild(svg);
}

// --- Instant consumption gauge ------------------------------------------------------

// A bar filled by the recent pace (winSnap.instantRate, %/h) against the sustainable
// pace that reaches exactly 100% at the reset over the remaining working hours
// (winSnap.sustainableRate, the "target"), on a logarithmic scale of their ratio
// (renderer/gauge.ts): the target marker stays in the middle, faint ticks mark ½× and
// 2×. Without a target there is no scale to read the pace on: only the number.
function renderInstantGauge(winSnap: QuotaWindowSnapshot | undefined, accountId?: AccountId): void {
  const container = byId('instant-gauge', HTMLDivElement);
  const label = byId('gauge-label', HTMLSpanElement);
  container.innerHTML = '';

  const instant = winSnap?.instantRate ?? null;
  const sustainable = winSnap?.sustainableRate ?? null;

  if (instant === null) {
    label.textContent = t('widget.gauge.waiting');
    return;
  }
  label.textContent = sustainable !== null
    ? t('widget.gauge.withTarget', { rate: formatRate(instant), target: formatRate(sustainable) })
    : formatRate(instant);

  const width = container.clientWidth || 300;
  const height = 20;
  const svgNs = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(svgNs, 'svg');
  svg.setAttribute('viewBox', `0 0 ${width} ${height}`);
  svg.setAttribute('width', '100%');
  svg.setAttribute('height', String(height));

  const track = document.createElementNS(svgNs, 'rect');
  track.setAttribute('x', '0');
  track.setAttribute('y', '0');
  track.setAttribute('width', String(width));
  track.setAttribute('height', String(height));
  track.setAttribute('rx', '4');
  track.setAttribute('fill', 'currentColor');
  track.setAttribute('opacity', '0.1');
  svg.appendChild(track);

  if (sustainable !== null) {
    const overBudget = instant > sustainable;
    const fill = document.createElementNS(svgNs, 'rect');
    fill.setAttribute('x', '0');
    fill.setAttribute('y', '0');
    fill.setAttribute('width', String(gaugePosition(instant, sustainable) * width));
    fill.setAttribute('height', String(height));
    fill.setAttribute('rx', '4');
    fill.setAttribute('fill', overBudget ? 'var(--warning)' : 'var(--accent)');
    svg.appendChild(fill);

    for (const ratio of GAUGE_TICK_RATIOS) {
      const tick = document.createElementNS(svgNs, 'rect');
      tick.setAttribute('x', String(gaugePosition(ratio, 1) * width - 0.5));
      tick.setAttribute('y', String(height * 0.6));
      tick.setAttribute('width', '1');
      tick.setAttribute('height', String(height * 0.4));
      tick.setAttribute('fill', 'currentColor');
      tick.setAttribute('opacity', '0.35');
      svg.appendChild(tick);
    }

    const marker = document.createElementNS(svgNs, 'rect');
    marker.setAttribute('x', String(GAUGE_TARGET_POSITION * width - 1));
    marker.setAttribute('y', '0');
    marker.setAttribute('width', '2');
    marker.setAttribute('height', String(height));
    marker.setAttribute('fill', 'currentColor');
    const title = document.createElementNS(svgNs, 'title');
    title.textContent = t('widget.gauge.sustainable', { rate: formatRate(sustainable) });
    marker.appendChild(title);
    svg.appendChild(marker);
  }

  container.appendChild(svg);

  const schedule = state.settings?.accounts.find((account) => account.id === accountId)?.workSchedule;
  // instantRate is already the last-hour average computed by budget.instantaneousRate.
  const flow = instantFlow(instant, sustainable, schedule !== undefined && isWorkingDay(schedule, new Date()));
  if (flow && sustainable !== null) {
    // The arrows fill only the gap between the bar end and the target marker.
    const span = flowSpan(gaugePosition(instant, sustainable), GAUGE_TARGET_POSITION);
    const gap = document.createElement('div');
    gap.className = 'gauge-flow-gap';
    gap.style.left = `${span.left * 100}%`;
    gap.style.width = `${span.width * 100}%`;
    const strip = document.createElementNS(svgNs, 'svg');
    strip.setAttribute('class', 'gauge-flow');
    strip.dataset.direction = flow.direction;
    strip.setAttribute('aria-hidden', 'true');
    strip.style.setProperty('--flow-duration', `${flow.durationSeconds}s`);
    const pattern = document.createElementNS(svgNs, 'pattern');
    pattern.id = 'instant-flow-pattern';
    pattern.setAttribute('width', '48');
    pattern.setAttribute('height', '20');
    pattern.setAttribute('patternUnits', 'userSpaceOnUse');
    const arrows = document.createElementNS(svgNs, 'path');
    arrows.setAttribute('d', flow.direction === 'right'
      // Chevrons at the start of each 48 px period: with the gap's 24 px minimum width
      // (style.css) three whole arrows show even when static.
      ? 'M2 6l4 4-4 4m8-8 4 4-4 4m8-8 4 4-4 4'
      : 'M6 6l-4 4 4 4m8-8-4 4 4 4m8-8-4 4 4 4');
    arrows.setAttribute('fill', 'none');
    arrows.setAttribute('stroke', 'currentColor');
    arrows.setAttribute('stroke-width', '2');
    pattern.append(arrows);
    const repeat = document.createElementNS(svgNs, 'rect');
    repeat.setAttribute('width', '100%');
    repeat.setAttribute('height', '20');
    repeat.setAttribute('fill', 'url(#instant-flow-pattern)');
    strip.append(pattern, repeat);
    gap.append(strip);
    container.append(gap);
  }
}

// --- Efficiency rating ------------------------------------------------------------

function starIcon(filled: boolean): SVGSVGElement {
  const svgNs = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(svgNs, 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('fill', filled ? 'var(--accent)' : 'none');
  svg.setAttribute('stroke', 'currentColor');
  svg.setAttribute('stroke-width', '1.5');
  const path = document.createElementNS(svgNs, 'path');
  path.setAttribute('d', 'M12 2l2.9 6.3 6.9.6-5.2 4.6 1.6 6.8L12 16.9l-6.2 3.4 1.6-6.8L2.2 8.9l6.9-.6L12 2z');
  svg.appendChild(path);
  return svg;
}

// Star rating (winSnap.efficiencyRating): how consistently usage stayed close to
// the ideal pace over `chartDays` (7 or 30, the same view as the chart — see
// budget.efficiencyRating). Kept next to "Efficiency index" in index.html: same
// concept on a different horizon (user feedback).
function renderEfficiencyRating(winSnap: QuotaWindowSnapshot | undefined, chartDays: number): void {
  const starsContainer = byId('efficiency-rating-stars', HTMLDivElement);
  const labelEl = byId('efficiency-rating-label', HTMLSpanElement);
  starsContainer.innerHTML = '';
  starsContainer.removeAttribute('title');

  const rating = winSnap?.efficiencyRating ?? null;
  const isRollingHours = winSnap?.window.periodType === 'rolling-hours';

  if (!rating) {
    // On a window renewing every few hours (e.g. Claude's 5-hour limit) a daily
    // rating is not missing data: the concept does not apply at that time scale —
    // an explicit message instead of empty stars (which would read as "terrible").
    labelEl.textContent = isRollingHours ? t('widget.rating.notApplicable') : t('widget.rating.insufficient');
    for (let i = 1; i <= 5; i++) starsContainer.appendChild(starIcon(false));
    return;
  }

  labelEl.textContent = t('widget.rating.label', { days: chartDays });
  starsContainer.title = t('widget.rating.avgRatio', { ratio: formatNumber(rating.avgRatio, 2) });
  for (let i = 1; i <= 5; i++) starsContainer.appendChild(starIcon(i <= rating.stars));
}

// --- Local insights (Claude Code sessions on this machine) ---------------------------

// Yield (docs/EVOLUTION.md point 4, budget.tokenYield): tokens produced per 1% of quota
// of the selected window, with the trend between the second and first half.
function renderTokenYield(winSnap: QuotaWindowSnapshot | undefined): void {
  const valueEl = byId('local-insights-yield', HTMLElement);
  const hintEl = byId('local-insights-yield-hint', HTMLElement);
  const y = winSnap?.tokenYield ?? null;
  if (!y) {
    valueEl.textContent = '--';
    hintEl.textContent = winSnap?.window.periodType === 'rolling-hours'
      ? t('widget.insights.yieldNotApplicable')
      : t('widget.insights.yieldInsufficient');
    return;
  }
  valueEl.textContent = y.tokensPerPercent >= 1000 ? `${formatNumber(y.tokensPerPercent / 1000, 1)}k` : formatNumber(y.tokensPerPercent, 0);
  const over = t('widget.insights.yieldOver', { days: formatDays(y.daysCompared) });
  hintEl.textContent = y.trendPercent === null
    ? over
    : t('widget.insights.yieldTrend', {
      over,
      arrow: y.trendPercent >= 0 ? '▲' : '▼',
      percent: formatNumber(Math.abs(y.trendPercent), 1),
    });
}

// Behavioural insights from LOCAL Claude Code sessions (opt-in, see docs/RESEARCH.md
// §5) — a sub-section inside the "Tip of the day" <details>, shown only when
// enabled and available. Present only on the Claude account the user attributed
// local sessions to (per-account flag in Settings).
function renderLocalInsights(account: AccountSnapshot | undefined): void {
  const details = byId('local-insights', HTMLDetailsElement);
  const insights = account?.localInsights ?? null;
  if (!insights) {
    details.hidden = true;
    return;
  }
  details.hidden = false;
  byId('local-insights-context').textContent = formatPercent(insights.highContextSharePercent);
  byId('local-insights-duration').textContent = formatPercent(insights.longSessionSharePercent);
  byId('local-insights-context-count').textContent = t('widget.insights.tokenCount', {
    count: formatNumber(insights.highContextOutputTokens, 0),
  });
  byId('local-insights-duration-count').textContent = t('widget.insights.tokenCount', {
    count: formatNumber(insights.longSessionOutputTokens, 0),
  });

  const list = byId('local-insights-tools', HTMLUListElement);
  list.innerHTML = '';
  insights.topTools.forEach((tool) => {
    const li = document.createElement('li');
    const name = document.createElement('span');
    name.textContent = tool.name;
    const share = document.createElement('span');
    share.textContent = formatPercent(tool.sharePercent);
    li.appendChild(name);
    li.appendChild(share);
    list.appendChild(li);
  });
}

// Absolute "used / total" under the main value, only for count windows with a total:
// Copilot AI credits and Claude extra credit (USD). A Claude percentage window has no
// token total behind it (the endpoint gives utilization and reset only), and a count
// window without a total already shows its used value as the main value.
function absoluteCountLabel(win: QuotaWindow | null, provider: ProviderId): string | null {
  if (!win || win.unit !== 'count' || typeof win.total !== 'number' || win.total <= 0) return null;
  const params = { used: formatNumber(win.used, 2), total: formatNumber(win.total, 2) };
  return t(provider === 'claude' ? 'widget.counts.extraCreditRatio' : 'widget.counts.creditRatio', params);
}

// --- Accounts and quota windows ------------------------------------------------------

// One tab per account, in the order of the Settings table (issue #4: N accounts,
// possibly two of the same provider — hence the account label, not the provider name).
// In Manual the panel shows the selected view itself, even when the badge cannot draw
// it (e.g. an unlimited window with no percentage); only when it is gone (disabled
// account, missing window) does it follow the view the badge shows instead.
function panelView(): PanelView | null {
  const selected = state.view?.selection.view;
  if (selected?.mode !== 'manual' || state.localOverride) return state.localView;
  const exists = state.latestSnapshot?.accounts.some((a) => a.accountId === selected.accountId
    && a.windows.some((w) => w.window.id === selected.windowId)) === true;
  return exists ? { accountId: selected.accountId, windowId: selected.windowId } : state.view?.shown ?? null;
}

function applyView(updated: TrayViewState): void {
  // Freeze the panel's current view when switching away from Manual.
  if (state.latestSnapshot && state.view?.selection.view.mode === 'manual' && updated.selection.view.mode !== 'manual') {
    const account = selectAccount(state.latestSnapshot);
    const win = account ? selectWindowSnapshot(account) : undefined;
    if (account && win) state.localView = { accountId: account.accountId, windowId: win.window.id };
  }
  if (JSON.stringify(state.view?.selection.view) !== JSON.stringify(updated.selection.view)) state.localOverride = false;
  state.view = updated;
  if (state.latestSnapshot) renderSnapshot(state.latestSnapshot);
}

// A panel click only moves the panel's local view, in every mode: the tray selection
// (Off/Auto/Manual and the Manual view) changes only from the tray menu or Settings. In
// Manual the override holds until the selection changes.
function choosePanelView(view: PanelView): void {
  state.localView = view;
  state.localOverride = state.view?.selection.view.mode === 'manual';
  if (state.latestSnapshot) renderSnapshot(state.latestSnapshot);
}

function selectAccount(snapshot: UsageSnapshot): AccountSnapshot | undefined {
  const accounts = snapshot.accounts;
  const view = panelView();
  return accounts.find((a) => a.accountId === view?.accountId) ?? accounts[0];
}

function renderAccountTabs(snapshot: UsageSnapshot): void {
  const nav = byId('account-tabs', HTMLElement);
  const available = snapshot.accounts;
  if (available.length <= 1) {
    nav.innerHTML = '';
    nav.hidden = true;
    return;
  }
  const activeId = selectAccount(snapshot)?.accountId;
  nav.hidden = false;
  nav.innerHTML = '';
  available.forEach((account) => {
    const id = account.accountId;
    const utilization = account.criticalWindow ? budgetNormalizedUtilization(account.criticalWindow) : null;
    const btn = document.createElement('button');
    btn.textContent = `${account.label} ${utilization === null ? '--%' : formatPercent(utilization)}`;
    btn.title = `${account.label} (${utilization === null ? '--%' : formatPercent(utilization)})`;
    btn.className = id === activeId ? 'active' : '';
    // A failing account must not look current: the reason is in the label (#266).
    if (account.stale === true) {
      btn.classList.add('stale');
      btn.title = `${btn.title} — ${t('widget.staleTab')}`;
    }
    btn.addEventListener('click', () => {
      // Diagnostic for issue #25 (tab occasionally not clickable right after login):
      // if this fires but the widget stays unchanged, check state.view and
      // state.latestSnapshot in the DevTools console.
      console.debug('[widget] tab click id=%s mode=%s accounts=%d', id, state.view?.selection.view.mode, state.latestSnapshot?.accounts.length ?? 0);
      choosePanelView({ accountId: id, windowId: selectWindowSnapshot(account)?.window.id ?? null });
    });
    nav.appendChild(btn);
  });
}

// Quota windows of the active account (docs/EVOLUTION.md point 1), replacing tabs that
// only lined up the provider's metrics: one row per window with a verdict computed
// by the app (budget.windowVerdict), the critical one first. Clicking a row shows
// that window in the rest of the widget. Hidden when the account has one window.
function renderWindowList(account: AccountSnapshot): void {
  const list = byId('window-list', HTMLElement);
  // Always emptied, even when hidden: no row of another account may stay in the
  // DOM (see [hidden] in style.css, 0.2.0 bug).
  list.innerHTML = '';
  // Without rows, the label under the value is the only place for the alarm (style.css).
  document.body.classList.toggle('single-window', account.windows.length <= 1);
  if (account.windows.length <= 1) {
    list.hidden = true;
    return;
  }
  list.hidden = false;
  const selected = selectWindowSnapshot(account);
  const criticalId = account.criticalWindow?.id;
  const ordered = [...account.windows].sort((a, b) => Number(b.window.id === criticalId) - Number(a.window.id === criticalId));

  ordered.forEach((winSnap) => {
    const utilization = budgetNormalizedUtilization(winSnap.window);
    const labelText = windowLabel(winSnap.window, account.provider);
    const row = document.createElement('button');
    row.type = 'button';
    row.className = `window-row verdict-${winSnap.verdict.kind}${winSnap.window.id === selected?.window.id ? ' active' : ''}`;
    row.title = labelText;

    const dot = document.createElement('span');
    dot.className = 'window-row-dot';
    const label = document.createElement('span');
    label.className = 'window-row-label';
    label.textContent = labelText;
    const bar = document.createElement('span');
    bar.className = 'window-row-bar';
    const fill = document.createElement('span');
    fill.style.width = `${Math.max(0, Math.min(100, utilization ?? 0))}%`;
    // An at-risk/exhausted verdict keeps its red bar even below the threshold.
    const alarmed = winSnap.verdict.kind === 'at-risk' || winSnap.verdict.kind === 'exhausted';
    applySeverity(fill, alarmed ? 'warning' : severityLevel(utilization, warningThreshold()));
    bar.appendChild(fill);
    const pct = document.createElement('span');
    pct.className = 'window-row-pct';
    pct.textContent = formatPercent(utilization);
    const verdict = document.createElement('span');
    verdict.className = 'window-row-verdict';
    const verdictText = formatVerdict(winSnap.verdict, winSnap.window);
    // A verdict resting on projection/autonomy (rolling hours) is flagged while they
    // are preliminary; one resting on the redistribution needs no history.
    const projectionVerdict = (winSnap.verdict.kind === 'at-risk' || winSnap.verdict.kind === 'on-track')
      && winSnap.verdict.perUnit === undefined;
    verdict.textContent = projectionVerdict && winSnap.preliminary
      ? t('verdict.preliminary', { verdict: verdictText })
      : verdictText;

    row.append(dot, label, bar, pct, verdict);
    row.addEventListener('click', () => {
      choosePanelView({ accountId: account.accountId, windowId: winSnap.window.id });
    });
    list.appendChild(row);
  });
}

function selectWindowSnapshot(account: AccountSnapshot): QuotaWindowSnapshot | undefined {
  if (!account.windows.length) return undefined;
  const view = panelView();
  const byActive = view?.accountId === account.accountId
    ? account.windows.find((w) => w.window.id === view.windowId)
    : undefined;
  if (byActive) return byActive;
  const criticalId = account.criticalWindow?.id;
  const critical = criticalId !== undefined
    ? account.windows.find((w) => w.window.id === criticalId)
    : undefined;
  return critical ?? account.windows[0];
}

// Projection, time to reset and autonomy. A window of a few hours (winSnap.hourly,
// budget.hourlyOutlook) is read in hours, and the metrics that only make sense per
// working day (today's budget, efficiency, peak/average, streak) are hidden there
// instead of showing "--" or "0 days".
function setMetricVisible(id: string, visible: boolean): void {
  const block = byId(id).closest<HTMLElement>('.metric');
  if (block) block.hidden = !visible;
}

function renderPeriodMetrics(winSnap: QuotaWindowSnapshot | undefined): void {
  const hourly = winSnap?.hourly ?? null;
  for (const id of ['metric-today', 'metric-efficiency']) setMetricVisible(id, hourly === null);
  // Peak/average and streak only once they say something (renderer/visibility.ts).
  const stats = winSnap?.deltaStats;
  const todayKey = localDateKey(new Date());
  setMetricVisible('metric-peak-avg', hourly === null && showPeakAvg(winSnap?.chart ?? [], todayKey));
  setMetricVisible('metric-streak', hourly === null && showStreak(stats?.streakUnderBudget ?? null));
  byId('metric-peak-avg').textContent =
    stats?.peak == null ? '--' : `${formatPercent(stats.peak)} / ${formatPercent(stats.avg)}`;
  byId('metric-streak').textContent =
    stats?.streakUnderBudget == null ? '--' : formatDays(stats.streakUnderBudget);
  const setLabel = (id: string, key: MessageKey): void => {
    const el = byId(id);
    el.dataset.i18n = key;
    el.textContent = t(key);
  };
  const projected = byId('metric-projected');
  const autonomyEl = byId('metric-autonomy');
  if (hourly) {
    setLabel('metric-projected-label', 'widget.metric.projectedAtReset');
    setLabel('metric-days-left-label', 'widget.metric.resetIn');
    projected.textContent = formatPercent(hourly.projectedAtReset);
    applySeverity(projected, severityLevel(hourly.projectedAtReset, warningThreshold()));
    byId('metric-days-left').textContent = formatHours(hourly.hoursLeft);
    // Autonomy only when it runs out before the reset (renderer/visibility.ts).
    setMetricVisible('metric-autonomy', showAutonomy(hourly.autonomyHours, hourly.hoursLeft));
    autonomyEl.textContent = hourly.autonomyHours === null ? '--' : formatHours(hourly.autonomyHours);
    byId('metric-projected-hint').textContent = '';
    byId('metric-autonomy-hint').textContent = '';
    return;
  }
  setLabel('metric-projected-label', 'widget.metric.projected');
  setLabel('metric-days-left-label', 'widget.metric.daysLeft');
  projected.textContent = formatPercent(winSnap?.projectedUsage ?? null);
  // No warning colour on a preliminary projection: it extrapolates the first day(s) to
  // the whole period and would outshout the verdict (the value stays visible).
  applySeverity(projected, winSnap?.preliminary ? 'none' : severityLevel(winSnap?.projectedUsage, warningThreshold()));
  // Projection and autonomy extrapolated from less than two working days.
  const preliminaryHint = winSnap?.preliminary ? t('widget.metric.preliminary') : '';
  byId('metric-projected-hint').textContent = preliminaryHint;
  byId('metric-autonomy-hint').textContent = preliminaryHint;
  byId('metric-days-left').textContent = t('widget.metric.daysLeftValue', {
    days: winSnap?.daysUntilReset ?? '--',
    working: formatDays(winSnap?.workingDaysUntilReset ?? null),
  });
  // Autonomy past the renewal says nothing more than the projection under 100%: shown
  // only when the quota would run out first (renderer/visibility.ts).
  const autonomy = winSnap?.estimatedAutonomyWorkingDays ?? null;
  setMetricVisible('metric-autonomy', showAutonomy(autonomy, winSnap?.workingDaysUntilReset ?? null));
  autonomyEl.textContent = formatDays(autonomy);
}

// --- Main render ------------------------------------------------------------------

function renderSnapshot(snapshot: UsageSnapshot): void {
  if (state.view?.selection.view.mode !== 'manual') {
    const account = selectAccount(snapshot);
    const win = account ? selectWindowSnapshot(account) : undefined;
    if (account && win) state.localView = { accountId: account.accountId, windowId: win.window.id };
  }
  detailsMotion?.finish();
  state.latestSnapshot = snapshot;
  renderAccountTabs(snapshot);
  // Same window (7/30 days) as the chart view, also used by the star rating.
  const chartDays = state.settings?.ui.chartRange === 'month' ? 30 : 7;

  const account = selectAccount(snapshot);
  if (!account) {
    alarmed = false;
    valueContext = null;
    syncMotion();
    byId('window-list').hidden = true;
    renderOdometer(byId('current-value'), '--', false);
    byId('current-count').hidden = true;
    byId('current-label').textContent = t('widget.noAccount');
    delete byId('current-label').dataset.account;
    renderInstantGauge(undefined);
    renderEfficiencyRating(undefined, chartDays);
    renderLocalInsights(undefined);
    setTipsVisible(true);
    byId('tips-text').textContent = t('widget.tips.waiting');
    return;
  }

  renderWindowList(account);
  const winSnap = selectWindowSnapshot(account);
  const win = winSnap?.window ?? null;
  const utilization = win ? budgetNormalizedUtilization(win) : null;

  const context = `${account.accountId}:${win?.id ?? ''}:${document.documentElement.lang}`;
  const value = utilization !== null ? formatPercent(utilization) : (win ? `${win.used}${win.total ? `/${win.total}` : ''}` : '--');
  renderOdometer(byId('current-value'), value, currentMotion().effects && context === valueContext && utilization !== null,
    formatNumber(1.1).replace(/\d/g, ''));
  valueContext = context;
  byId('current-value').classList.toggle('stale', account.stale === true);
  alarmed = account.windows.some((entry) => entry.verdict.kind === 'at-risk' || entry.verdict.kind === 'exhausted');
  syncMotion();
  applySeverity(byId('current-value'), severityLevel(utilization, warningThreshold()));
  const countLabel = byId('current-count', HTMLDivElement);
  const absoluteCount = absoluteCountLabel(win, account.provider);
  countLabel.textContent = absoluteCount ?? '';
  countLabel.hidden = absoluteCount === null;

  let label = t('widget.waiting');
  if (win) {
    const name = windowLabel(win, account.provider);
    label = win.resetsAt ? t('widget.windowWithReset', { window: name, moment: formatResetMoment(win.resetsAt) }) : name;
  } else if (account.planTier === 'free') {
    label = t('widget.freePlan');
  }
  if (account.stale && account.notice && !account.lastUpdatedAt) {
    // A state the user has to act on (e.g. sign in with Codex, #178), not a failed sync.
    label = account.notice;
  } else if (account.stale) {
    // Never a successful sync so far: the account IS connected but the very first
    // fetch failed — not to be confused with "not connected".
    label = account.lastUpdatedAt
      ? t('widget.stale', { label, time: formatDateTime(account.lastUpdatedAt) })
      : t('widget.firstSyncFailed');
    const reason = account.lastError ?? account.notice;
    if (reason) label = t('widget.withError', { label, error: reason });
  }
  byId('current-label').textContent = label;
  // The compact view hides the account tabs: the label carries the account name there (style.css).
  byId('current-label').dataset.account = account.label;

  renderInstantGauge(winSnap, account.accountId);
  renderEfficiencyRating(winSnap, chartDays);
  renderLocalInsights(account);
  renderTokenYield(winSnap);

  byId('metric-efficiency').textContent = formatEfficiency(winSnap?.efficiencyIndex ?? null);
  // budget.efficiencyIndex is null below NEGLIGIBLE_UTILIZATION on a paced window: say why.
  const negligible = winSnap?.redistribution != null && utilization !== null && utilization < NEGLIGIBLE_UTILIZATION;
  byId('metric-efficiency-hint').textContent = negligible
    ? t('widget.metric.efficiencyNegligible')
    : formatEfficiencyHint(winSnap?.efficiencyIndex ?? null);
  renderTodayBudget(winSnap);
  renderPeriodMetrics(winSnap);

  byId('chart-title').textContent = t('widget.chart.title', { days: chartDays });
  renderChart(winSnap);

  // No tip section when there is nothing to add (budget.generateDailyTip: `none`).
  const tip = winSnap?.dailyTip ?? null;
  setTipsVisible(tip === null || tip.key !== 'none');
  byId('tips-text').textContent = winSnap && win && tip
    ? formatTip(tip, win, account.provider)
    : t('widget.tips.waiting');
}

function setTipsVisible(visible: boolean): void {
  const section = byId('tips-text').closest<HTMLElement>('details');
  if (section) section.hidden = !visible;
}

// --- Startup ----------------------------------------------------------------------

async function togglePin(): Promise<void> {
  const next = !(state.settings?.ui.alwaysOnTop ?? false);
  await window.aipermiler.setAlwaysOnTop(next);
  if (state.settings) state.settings.ui.alwaysOnTop = next;
  updatePinButton(next);
}

async function toggleCompact(): Promise<void> {
  detailsMotion?.finish();
  await compactTransition.toggle();
}

async function init(): Promise<void> {
  const settings = await window.aipermiler.getSettings();
  state.settings = settings;
  window.aipermiler.onViewUpdate(applyView);
  state.view = await window.aipermiler.getView();
  applyLanguage(settings);
  applyWindowStyle(settings.ui.windowStyle);
  applyAccentColor(settings.ui.accentColor);
  updatePinButton(settings.ui.alwaysOnTop);
  updateCompactButton(settings.ui.compact);
  detailsMotion = initDetailsMotion(() => currentMotion().effects);
  initHoverReveal();
  reducedMotion.addEventListener('change', syncMotion);
  document.addEventListener('visibilitychange', syncMotion);
  window.addEventListener('pagehide', () => {
    alarmPulse.setActive(false);
    detailsMotion?.finish();
    compactAnimation.finish();
  });
  syncMotion();

  byId('btn-settings').addEventListener('click', () => {
    window.aipermiler.openSettingsWindow();
  });
  byId('btn-minimize').addEventListener('click', () => {
    window.aipermiler.minimizeWindow();
  });
  byId('btn-close').addEventListener('click', () => {
    window.aipermiler.closeWindow();
  });
  byId('btn-compact').addEventListener('click', () => { runGuarded(toggleCompact()); });
  byId('btn-pin').addEventListener('click', () => { runGuarded(togglePin()); });
  byId('btn-refresh').addEventListener('click', () => {
    // requestUsageRefresh() is fire-and-forget (ipcRenderer.send): the result
    // arrives through onUsageUpdate below; onUsageRefreshing also clears failures.
    requestRefresh();
  });

  window.aipermiler.onUsageRefreshing((active) => {
    refreshing = active;
    syncMotion();
  });
  window.aipermiler.onUsageUpdate((snapshot) => {
    refreshing = false;
    try { renderSnapshot(snapshot); }
    finally { syncMotion(); }
  });
  // Applies Settings changes live while the widget is open (accent color, always
  // on top from Settings/tray, language) — see preload.ts/main.ts, settings:update.
  window.aipermiler.onSettingsUpdate((updated) => {
    const languageChanged = updated.ui.language !== state.settings?.ui.language;
    const thresholdChanged = updated.ui.notificationThresholdPercent !== state.settings?.ui.notificationThresholdPercent;
    state.settings = updated;
    syncMotion();
    const account = state.latestSnapshot ? selectAccount(state.latestSnapshot) : undefined;
    renderInstantGauge(account ? selectWindowSnapshot(account) : undefined, account?.accountId);
    applyAccentColor(updated.ui.accentColor);
    updatePinButton(updated.ui.alwaysOnTop);
    if (languageChanged) {
      applyLanguage(updated);
      updateCompactButton(document.body.classList.contains('compact'));
    }
    // The warning colours follow the notification threshold (renderer/severity.ts).
    else if (thresholdChanged && state.latestSnapshot) renderSnapshot(state.latestSnapshot);
  });
  requestRefresh();
}

// An error at startup or on a button must not leave the widget silent: it is
// shown in the label under the main value.
function runGuarded(task: Promise<unknown>): void {
  task.catch((err: unknown) => {
    console.error('[widget]', err);
    const label = document.getElementById('current-label');
    if (label) label.textContent = t('widget.error', { message: err instanceof Error ? err.message : String(err) });
  });
}

document.addEventListener('DOMContentLoaded', () => { runGuarded(init()); });
