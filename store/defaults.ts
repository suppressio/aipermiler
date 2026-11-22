// store/defaults.ts — default values of AppSettings (schema in docs/ARCHITECTURE.md §1).
// Pure module (no electron/electron-store import): usable from tests too.

import type { AppSettings, WorkSchedule } from '../types/index';

// Work schedule a new account starts from (each account has its own since 0.4.6 —
// it used to be one global setting, migrated by store/normalize.ts).
export const DEFAULT_WORK_SCHEDULE: WorkSchedule = {
  // Default true: keeps the behaviour already in use (pacing on the working days
  // configured below). Can be disabled in Settings for a personal account, where pacing
  // on specific days/hours makes no sense.
  enabled: true,
  days: {
    mon: 'full',
    tue: 'full',
    wed: 'full',
    thu: 'full',
    fri: 'full',
    sat: 'off',
    sun: 'off',
  },
  // Hours of a full working day: today's elapsed part = time since the first activity
  // of the day over these hours (budget.todayElapsedUnits). A single number, not a
  // start/end range (user feedback): the start of the day is inferred from the data.
  hoursPerDay: 8,
};

export const DEFAULTS: AppSettings = {
  // Account registry (issue #4): starts empty, accounts are added from the table in
  // Settings. A legacy `{ claude, copilot }` store is converted by migrateAccounts() —
  // see store/migrate.ts.
  accounts: [],

  ui: {
    language: 'auto',
    windowStyle: 'filled',
    alwaysOnTop: false,
    // Compact view of the widget (percentage + instant gauge only).
    compact: false,
    animations: true,
    accentColor: '#2563eb',
    // x/y missing until the user moves the window (see main/windows.ts).
    bounds: { width: 360, height: 480 },
    chartRange: 'week',
    refreshIntervalMinutes: 30,
    notificationThresholdPercent: 80,
    trayBadge: { view: { mode: 'auto' }, critical: true },
  },

  history: {
    dailyUsage: [],
    retentionDays: 90,
    recentSamples: [],
  },

  advisorCache: { generatedAt: null, adviceText: null },

  meta: { notifiedToday: {} },

  diagnostics: {
    // When an endpoint changes format, opens a pre-filled GitHub issue draft (structure
    // only, never real values) instead of failing silently — see services/_shape.ts and
    // diagnostics/githubIssue.ts. On by default: it publishes nothing on its own, it
    // always needs a manual confirmation in the browser.
    autoReportFormatDrift: true,
    reportedSignatures: {},
  },

  localInsightsCache: { claudeCode: null, countedByCore: false },

  updates: {
    // New-version check at startup and every 24h (issue #5, services/updates.ts): only
    // reads the GitHub Releases list, no automatic download.
    autoCheck: true,
    lastCheckedAt: null,
    lastError: null,
    available: null,
    notifiedVersion: null,
  },
};
