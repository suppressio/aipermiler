# AGENTS.md — AIpermiler

Instructions for every coding agent (Claude Code, Codex, pi, Copilot). Read it in full before touching the code.

---

## What this project is

**AIpermiler** is an Electron desktop app that monitors AI token usage (Claude, GitHub Copilot and Codex) and computes an optimal daily budget so the quota lasts until renewal. It shows an always-visible widget with daily consumption against the ideal budget, end-of-period projection, efficiency metrics and tips derived from real data. UI in English and Italian.

**Languages:** the conversation with the user is in **Italian**. All **code** (identifiers, comments, test names, logs, internal error messages) is in **English**. Human-facing **documents** exist in English (`X.md`, primary) and Italian (`X.it.md`, secondary) with a language switcher at the top; this file is English only.

---

## Stack and dependencies

| Role | Technology |
|---|---|
| Language | TypeScript, `strict: true` + `noUncheckedIndexedAccess`, `exactOptionalPropertyTypes`, `noUnusedLocals/Parameters`, `noImplicitReturns`, `noFallthroughCasesInSwitch`, `noImplicitOverride`. No `any` |
| Desktop shell | Electron (current LTS) |
| UI | Vanilla HTML + CSS + SVG — no framework, no bundler. The renderer compiles to native ES modules (`<script type="module">`) |
| i18n | No library: typed flat dictionaries (`renderer/i18n/`, `main/i18n/`) + `Intl` |
| Persistence | electron-store (with encryptionKey for secrets), normalized at startup (`store/normalize.ts`) |
| Advice agent | `agents/advisor.ts` is a dormant stub, not wired. No Anthropic SDK: #29 will use plain HTTP calls through the accounts the user already connected |
| Local insights (opt-in) | `@anthropic-ai/claude-agent-sdk` — reads local Claude Code sessions via `listSessions`/`getSessionMessages`, never by parsing the internal JSONL directly (see docs/RESEARCH.md §5) |
| Build | `tsc` (three configs: `tsconfig.json` main/preload/core/services/store/budget/agents/tests — CommonJS; `tsconfig.renderer.json` renderer — ES modules; `tsconfig.renderer.test.json` renderer tests only, adds Node types) + `scripts/copy-assets.js` |
| Lint | ESLint 10 + typescript-eslint `strictTypeChecked` (`eslint.config.mjs`) |
| Tests | `node:test` + `node:assert` (no external test library); coverage via Node's built-in `--experimental-test-coverage` |
| Packaging | electron-builder |
| Runtime | Node.js 22 LTS |

**Do not add dependencies without asking.** Every new library must be justified and agreed before installing it. (Agreed so far: TypeScript, `@types/node`, `@anthropic-ai/claude-agent-sdk`, ESLint + `@eslint/js` + `typescript-eslint` as dev dependencies; in the Go core, `github.com/zalando/go-keyring` for the OS keyring, approved on #179.)

---

## Project structure

```
aipermiler/
├── AGENTS.md                    ← this file (English only)
├── CLAUDE.md                    ← only `@AGENTS.md`, so Claude Code reads the same file
├── LICENSE                      ← MIT
├── README.md / README.it.md     ← user guide only: what the app does, install, how to read every indicator (EN primary, IT secondary)
├── docs/                        ← every other document, EN + .it.md
│   ├── DEVELOPMENT.md           ← build from source, tests, packaging, CI, project structure and status
│   ├── CORE-CLI.md              ← Go CLI and transport-neutral JSON contract v1 (#26)
│   ├── ARCHITECTURE.md          ← app design: data model, windows, widget, tray, updates, i18n
│   ├── PACING.md                ← the pacing model: definitions, edge cases, invariants, state, example tables (source of truth for budget.ts)
│   ├── RESEARCH.md              ← what the Claude/Copilot APIs really expose (Day 1 + addenda)
│   ├── EVOLUTION.md             ← retrospective and future directions with cost/benefit analysis
│   ├── PLAN.md                  ← the original 3-day plan (historical)
│   └── screenshots/             ← README screenshots, synthetic data only
├── tsconfig.json                ← main-process TS config (CommonJS)
├── tsconfig.renderer.json       ← renderer TS config (ES modules, isolated, no Node types)
├── tsconfig.renderer.test.json  ← renderer unit tests (adds Node types; emitted as ES modules)
├── eslint.config.mjs            ← type-aware lint, all three TS projects
├── types/index.ts               ← shared types (AppSettings, AccountConfig, AccountSnapshot, …), runtime-free
├── types/ipc.ts                 ← IPC contract: every channel with its argument/result types + AipermilerBridge (runtime-free)
├── scripts/
│   ├── copy-assets.js           ← copies renderer html/css into dist/renderer/ + writes dist/renderer/package.json {type: module}
│   ├── build-core.js            ← builds the Go core CLI into core-bin/ (gitignored; electron-builder extraResources → <resources>/core/), --target to cross-compile, --dev honours AIPERMILER_CORE_BIN
│   └── generate-icons.js        ← one-off "gauge" icon generator (build/icon.png, build/icons/*, renderer/assets/*.png)
├── build/                       ← electron-builder resources (icons) — TRACKED, not an output (packages go to release/)
├── entry.ts                     ← Electron entry point (package.json main): userData migration (#200), then loads main.ts
├── main.ts                      ← Electron main process: wiring only (lifecycle, windows, tray, IPC, timers, notifications, update checks)
├── preload.ts                   ← contextBridge renderer ↔ main
├── gocore/                      ← Go core module (#145), strict lint + depguard; CI job `go-checks`
│   ├── cmd/aipermiler/          ← one binary, subcommand entry point
│   ├── contract/v1/             ← shared normative JSON fixtures (Go + future TypeScript client)
│   └── internal/
│       ├── buildinfo/           ← binary name and build-time version
│       ├── cli/                 ← command dispatch and exit codes
│       ├── codex/               ← native binary discovery, one-shot app-server probe and quota mapping
│       ├── copilot/             ← Copilot usage (port of services/copilot.ts), token set/delete, OAuth PKCE login, raw response
│       ├── github/              ← allowed GitHub hosts (github.com, <tenant>.ghe.com) and JSON client with a 10 s timeout
│       ├── secrets/             ← OS keyring (go-keyring): the Copilot token, never in argv or documents; reads the pre-rename service `hypermiler` as a fallback, moves it on the next set (#200)
│       ├── config/              ← OS config directory and development override
│       ├── logging/             ← stderr text logger and level selection
│       ├── pacing/              ← pure pacing model
│       ├── provider/            ← provider interface and per-run registry factories
│       └── usage/               ← usage model, JSON contract, errors and text rendering
├── core/
│   └── usage.ts                 ← fetch orchestration + snapshot computation (createUsageCore, computeAccountSnapshot…): store, provider fetch, local insights, clock and error reporting injected, no Electron (+ usage.test.ts)
├── main/
│   ├── ipc.ts                   ← typed handle/listen/sendToRenderer wrappers keyed on types/ipc.ts
│   ├── core.ts                  ← the app's one core client (binary resolved per call)
│   ├── coreRequests.ts          ← provider flags of the core commands from an account config (pure, + test)
│   ├── coreClient.ts            ← runs the bundled core CLI (execFile, no shell, timeout, --json, secrets on stdin) and parses contract v1 against gocore/contract/v1 (+ test)
│   ├── windows.ts               ← main window (skins filled/filled-dark/transparent-digital) and Settings window
│   ├── tray.ts                  ← cross-platform tray; createTray returns { tray, refreshMenu }
│   ├── trayBadge.ts             ← tray usage badge: account choice, tone, icon bitmap (+ test)
│   ├── claude-auth.ts           ← cleanup of claude.ai sessions stored by earlier versions (no Claude sign-in since #140)
│   ├── providers.ts             ← provider-specific part of the account registry (connection, fetch, disconnect, secret redaction)
│   ├── refreshPolicy.ts         ← adaptive refresh pace, per-account backoff, manual-refresh cache (+ test)
│   ├── userDataMigration.ts     ← moves the store from the pre-rename userData folder `ia-hypermiler` (#200, + test)
│   ├── userDataBootstrap.ts     ← runs that migration once at import (entry.ts first, before the store exists)
│   └── i18n/                    ← main-process strings (tray, notifications, dialogs, OAuth page): en.ts, it.ts, index.ts (+ test)
├── renderer/
│   ├── index.html, style.css, app.ts           ← widget
│   ├── settings.html, settings.css, settings.ts ← Settings window (accounts table, language, updates, …)
│   ├── dom.ts                   ← byId(id, ElementType): typed, checked element access
│   ├── schedule.ts              ← one-line summary of an account work schedule (+ test)
│   ├── windowLabel.ts           ← localized quota-window labels, including Codex limits (+ test)
│   ├── gauge.ts                 ← log-scale position of the instant pace against its target (+ test)
│   ├── visibility.ts            ← which indicators are shown (autonomy, peak/average, streak) (+ test)
│   └── i18n/                    ← UI strings: en.ts (reference), it.ts, index.ts (t, applyTranslations, Intl formatting) (+ test)
├── agents/advisor.ts            ← Claude advice agent (stub)
├── services/
│   ├── _http.ts                 ← shared fetch helper (10s timeout, explicit errors)
│   ├── _shape.ts                ← response → structure only (never values), FormatDriftError (+ test)
│   ├── claudeLocalSessions.ts   ← insights from LOCAL Claude Code sessions, opt-in (+ test)
│   ├── claudeLocalSessions.worker.ts ← utility-process entry: the scan runs off the main process
│   ├── updates.ts               ← new-version check via the GitHub Releases list (+ test)
│   └── githubHost.ts            ← github.com or <tenant>.ghe.com: validation + API/OAuth base URLs (+ test)
├── diagnostics/githubIssue.ts   ← pre-filled GitHub issue URLs (format drift, report draft) + response redaction (+ test)
├── diagnostics/report.ts        ← manual diagnostic report file: layout, neutral names (+ test)
├── diagnostics/logBuffer.ts     ← last main-process errors/warnings, memory only
├── store/
│   ├── index.ts                 ← electron-store wrapper: creates the store and normalizes it at startup
│   ├── defaults.ts              ← AppSettings defaults (pure module, usable by tests)
│   ├── merge.ts                 ← deep merge "value on disk over defaults"
│   ├── normalize.ts             ← normalizeSettings: any store on disk → valid AppSettings (+ test)
│   └── migrate.ts               ← per-provider defaults, legacy {claude, copilot} → AccountConfig[] migration (+ test)
├── budget.ts                    ← pure budget/efficiency/projection/verdict/tip/yield logic (+ budget.test.ts)
├── tests/
│   ├── support/at.ts            ← at(array, i) helper for tests under noUncheckedIndexedAccess
├── dist/                        ← tsc output + copied assets (gitignored)
└── package.json
```

---

## Architectural rules — DO NOT deviate

### Electron security
- `nodeIntegration` always `false`, `contextIsolation` always `true`.
- The renderer never has direct access to Node.js: everything goes through `preload.ts` via `contextBridge`.
- No provider secret in the app: the Copilot token lives in the OS keyring, owned by the Go core (#179; it reaches the core on stdin, never argv), Codex keeps nothing secret (#178). The Copilot OAuth client secret is typed in Settings and goes to the core on stdin, never saved. Whatever secret is still in the store (a Copilot token of an earlier version, until moved) stays in the main process — never in the renderer, never in localStorage. `settings:get`/`settings:set` redact secrets towards the renderer (`providers.redactSecrets`) and preserve the real values on write (`providers.preserveSecrets`).
- IPC channels and payload types are declared once in `types/ipc.ts`; main registers through `main/ipc.ts` (`handle`/`listen`/`sendToRenderer`), the preload through typed helpers on the same maps. A new channel = one entry there. The renderer tsconfig has `rootDir: "."` so it can import `types/`; those files must stay runtime-free (the renderer build re-emits them as ES modules over the CommonJS ones).
- Every value coming from the renderer through IPC is `unknown` and is validated in the handler (`requireString`/`requireBoolean`/`requireOneOf` in `main.ts`). `settings:set` accepts only renderer-editable sections (`accounts`, `ui`, `diagnostics`, `updates`) and normalizes the patch before writing.
- URLs opened with `shell.openExternal` come from the main process (store/constants), never from the renderer; update URLs must start with the project repository prefix.

### Cross-platform
- No hard-coded path separators: always `path.join()`/`path.resolve()`.
- No `shell.exec()` or OS-specific commands.
- Think through every path on Windows, macOS and Linux before writing it.

### Service interface
Every provider is read by the Go core CLI since #179 (no TypeScript usage service left): `main/core.ts` runs `aipermiler usage <provider> --json` and gets `RawAccountUsage` (contract v1, docs/CORE-CLI.md; full types in `types/index.ts`, multi-window rationale in docs/ARCHITECTURE.md §0):

```ts
// RawAccountUsage = { planTier, subscriptionRenewsAt, quotaWindows: QuotaWindow[] }
// QuotaWindow    = { id, label, periodType, periodLength, unit: 'percentage'|'count', used, total, resetsAt }
```

A failure is an explicit error (contract error codes → `CoreProviderError`, `FormatDriftError`, `CoreBinaryError`…) — never `null`/`undefined` silently. No `dailyHistory` from providers: the daily history is built locally by `core/usage.ts` in `store.history.dailyUsage` on every successful refresh. Adding a provider = an adapter in `gocore/internal/` (registered in `provider.Default()`, fixtures, docs/CORE-CLI.md) + a branch in every function of `main/providers.ts` (exhaustive switches make tsc fail if one is forgotten) and in `main/coreRequests.ts` + a detail template in `settings.html` + dictionary keys.

### Error handling
- Every network call has an explicit timeout (max 10 seconds) — use `services/_http.ts`.
- If the APIs do not answer, the UI shows the last known data with the timestamp of the last successful update.
- Never a blank screen or a silent crash: no floating promises (`runDetached` in main, `runGuarded`/`guarded` in the renderer); startup failure shows an error dialog and quits.

### Diagnostics: automatic "format drift" report
When a service receives a response whose format no longer matches the expected one (no recognized quota window, key field missing, …) it throws a `FormatDriftError` (`services/_shape.ts`) instead of a generic `Error`. `main.ts` catches it in `fetchAccountOrFallback()` (shared by every account) and calls `maybeReportFormatDrift()`:

- **Never automatic publishing**: only a pre-filled GitHub issue draft opens in the system browser (`shell.openExternal`, via `diagnostics/githubIssue.ts`); the user must always review and submit it by hand. The app has and uses no GitHub token.
- **Never real values in the report**: the issue body contains only the response "shape" (field names + `typeof`, from `extractShape()`), never usage percentages, amounts or real dates. The body is in English (public repository).
- **Deduplicated by structure signature** (`shapeSignature()`): the same shape does not reopen a draft on every refresh. Reported signatures live in `store.diagnostics.reportedSignatures`.
- **Can be disabled** in Settings (`diagnostics.autoReportFormatDrift`, default `true`).

**Manual diagnostic report** (Settings → Diagnostics, "Create diagnostic report"): after an explicit confirmation (native dialog listing what goes in and that nothing is sent), one text file in Downloads (`diagnostics/report.ts`) with, per connected account (enabled or not, named "<Provider> #n"): the raw usage response redacted by `redactResponse()` (`diagnostics/githubIssue.ts` — numbers, booleans, null, short enum-like fields and ISO dates under reset/timing keys kept, other dates and strings redacted, any value under an id key replaced whatever its type), the settings that affect pacing (no labels, host only as github.com/"a .ghe.com tenant"), the app's reading of the data (last snapshot), 7 days of history and today's samples; plus environment, a `system` section (`main.ts reportSystem`: time zone, screens and scaling, widget size, memory/CPU of the app's processes, last refresh and local-insights durations, core count, RAM rounded to GB — never CPU/GPU models, computer/user names, paths or ids) and the last 50 main-process errors/warnings (`diagnostics/logBuffer.ts`, memory only). Free texts go through `neutralize()` (labels/ids → neutral names, single pass) and `shortError()` (no response bodies). The folder is shown and a short GitHub issue draft opens: the user attaches the file by hand, or not. Unlike the automatic drift report it **does** carry usage values; never credentials, ids, names or free text.

### Store
- The store on disk is normalized at startup and after every `settings:set` (`store/normalize.ts`: deep merge with `store/defaults.ts`, wrong types replaced, legacy schemas migrated). Therefore `AppSettings` types are true: read top-level keys with their types (`store.get('history').recentSamples`), **never** dotted paths with casts (`store.get('a.b') as T`) and never `?? []` fallbacks. A new field only needs to be added to `store/defaults.ts`.
- Background: electron-store applies defaults with a SHALLOW merge, so a new nested field used to be `undefined` on existing installations — this caused real bugs before normalization existed.

### Internationalization
- Every user-visible string goes through `t()` (renderer: `renderer/i18n`, main: `main/i18n`) or a `data-i18n*` attribute in HTML (English default text in the markup). A new key goes into `en.ts` **and** `it.ts` (the compiler enforces key parity, the tests also check placeholders).
- Main and renderer own their texts separately (separate TS projects); `resolveLocale` must stay identical in both.
- Tips, verdicts and similar are **data** (`{ key, params }`), composed into sentences by the renderer. Technical error messages from services stay English; `friendlyErrorMessage` frames known cases in the user's language.
- Numbers and dates via the `Intl` helpers in `renderer/i18n/index.ts`, never a hard-coded locale.
- Documents: update both `X.md` and `X.it.md` when changing content. The README is for users only (what each indicator is, how it is computed, how to read it): update it whenever an indicator changes; developer content goes in docs/DEVELOPMENT.md, every other document in `docs/`. Screenshots use synthetic data only.

### Code quality
- `npm run lint` must stay at **zero findings** (it gates CI). Do not turn off a rule to hide a real problem; the only documented exception is the `new Function` dynamic import in `services/claudeLocalSessions.ts`.
- No `!` non-null assertions in production code: `byId()` in the renderer, explicit checks elsewhere.
- Values from outside (IPC, network JSON) are typed `unknown` / `T | null` and narrowed.

### Advisor agent
- The system prompt of `agents/advisor.ts` must ask for **specific, practical** advice, not generic.
- Context passed to the agent: consumption of the last 7 days, current daily budget, services used.
- The result is cached in `electron-store` and regenerated at most once every 24 hours.
- `model` is always `claude-sonnet-4-6`, `max_tokens: 1000`.
- Dormant until #29; it will not use `@anthropic-ai/sdk` (removed from the dependencies in #35).

---

## Core business logic

### Daily budget computation
Implemented in `budget.ts` (do not duplicate it here: update this section only if the model changes). Public API, all pure functions tested in `budget.test.ts`:

- `workingUnitsBetween(start, end, workSchedule)` — working units (1/0.5/0 per day) between two dates; with `workSchedule.enabled === false` every day counts 1
- `todayActivitySpan`, `todayElapsedUnits`, `elapsedWorkingUnits`, `remainingWorkingUnits` — actual working hours: today's span = first to last increase among the day's samples (`history.recentSamples` keeps the whole day), started earlier by today's first local Claude Code session; today's elapsed part = span / `hoursPerDay`, at least 2h, capped at the day unit; calendar fraction of the day with the schedule disabled. Elapsed + remaining = the whole period
- `localDateKey` / `parseDateKey` — LOCAL `YYYY-MM-DD` keys of the history (never `toISOString().slice(0, 10)`, which is the UTC day)
- `updateDailyPoint` — today's history point with its baseline (`dayStartUsed`) and first activity
- `normalizedUtilization(window)` — 0-100 percentage, or `null`
- `pickCriticalWindow(quotaWindows)` — the window with the highest utilization (threshold notification)
- `efficiencyIndex`, `projectedUsage`, `estimatedAutonomyWorkingDays` — pacing on working units, not calendar days; projection and autonomy blend the period average 50/50 with `recentPacePerUnit` (last 3 completed working days); projection NOT capped at 100
- `todayBudget` — today's budget (what was left at the start of the day over the working units from today on) vs today's consumption; `PACE_ALERT_RATIO`
- `redistributedQuota` — what is left NOW over the working units from today on (`perUnit`) next to the even share (`idealPerUnit`): the primary pacing signal, needs no history
- `daysUntilReset` / `workingDaysUntilReset`
- `instantaneousRate`, `sustainableHourlyRate`, `efficiencyRating` — instant gauge (pace over the last hour, anchored on the sample just before it when not older than two hours; sustainable %/h spread over remaining working hours = working units × `hoursPerDay`, also with the schedule disabled; drawn on a log scale of pace/target, `renderer/gauge.ts`) and star rating
- `hourlyOutlook`, `currentPacePerUnit`, `pickCriticalSnapshot` — rolling-hours windows read in hours (time to reset, projection at reset and autonomy at the instant pace; day metrics hidden); the verdict turns at risk when the pace (not preliminary) exceeds `PACE_ALERT_RATIO` × the redistributed quota; the widget opens on an at-risk/exhausted window first, then the most used
- `chartDays` — the chart slots (last 7/30 days + 2/5 days to come): consumption, even share × day unit (thin bar), moving budget (dashed: that morning's budget on past days, `todayBudget` today, redistributed quota ahead; none outside the current period), `overBudget` (over the moving budget, or the share × unit where unknown)
- `dailyDeltas`, `deltaStats`, `windowVerdict` — daily consumption, peak/average/streak on the chart slots (completed days only; the streak breaks on a day `overBudget`, the same flag that colours the bar; days off without use skipped), per-window verdict from the redistribution (at risk < 0.5× / behind < 0.95× / on track / ahead > 1.05× the even share; rolling-hours windows: projection/autonomy)
- `hasPacing`, `repairFirstDayBaseline` — a billing cycle of unknown length has no pacing (also the tie-break of `pickCriticalWindow`); first history point on the period start day gets baseline 0
- `tokenYield`, `consumptionCause` — value per token (Claude local insights; a cause is stated only with a clear signal)
- `generateDailyTip` — `{ key, params }` from explicit conditions on the metrics above, never a generic tip; only tips adding an action or a cause (autonomy shortfall, rating, near reset, consumption cause), none restating a number already on the widget
- `resolveRenewalDate(renewalRule, referenceDate)` — only `{ type: 'dayOfMonth', day }`; `rrule` throws "not supported". Only a fallback: the period end is the window's `resetsAt`, then the provider's `subscriptionRenewsAt`, then the manual day (`main.ts resolvePeriodBounds`); Settings locks the manual field when the provider reports every paced window's reset (`renderer/renewal.ts`)

### Threshold and pace notifications
- System notification when usage passes the configured threshold (default **80%**) of the critical window, at most once per day per account (flag in `store.meta.notifiedToday`).
- Pace notification when today's consumption of any window exceeds `PACE_ALERT_RATIO` (1.5×) today's budget, at most once per day per account (`<account>:pace:<day>` flag). Only today's flags are kept.

### Auto-refresh
- The main process refreshes at the configured interval (default **30 minutes**), also with the window closed (tray only), with one `setTimeout` re-armed after every refresh. After each successful fetch the data is stored and sent to the renderer (`usage:update`).
- Adaptive pace (`main/refreshPolicy.ts`): after a refresh where a freshly fetched window rose, the next one comes after **5 minutes**; the first one without a rise goes back to the interval. Per-account backoff: every consecutive failed fetch doubles that account's minimum gap (5 → 10 → 20 min, capped at the interval), a scheduled tick skips it meanwhile and shows its last data as stale with the error. Refresh modes: `scheduled` (timer, backoff applies), `manual` (button, tray, widget opening: reuses a fetch less than 1 minute old), `forced` (login, disconnect, account changes: always fetches). Only freshly fetched values are recorded as samples.

### Update check
- At startup (packaged app only) and every 24h: GitHub Releases list (not `/releases/latest`, which skips pre-releases; pre-releases are offered only to whoever runs one), one notification per version, download opens in the browser. No electron-updater (unsigned packages).

---

## What NOT to do

- Do not invent API endpoints not documented in `docs/RESEARCH.md`.
- Do not sign in to claude.ai or collect, store or forward claude.ai credentials or session tokens (#140): Anthropic forbids it for third-party apps ([Claude Code legal and compliance](https://code.claude.com/docs/en/legal-and-compliance)). Claude data may come only through Anthropic's own binaries and sign-in (#139).
- Do not sign in to chatgpt.com or store ChatGPT cookies or tokens: Codex is read only through the user's own unmodified `codex` binary and sign-in, by the Go core (#142, #178). Never read or copy `auth.json` or anything under `CODEX_HOME`.
- Animations are allowed but must be switchable off in Settings, low-key, carry state, and off with `prefers-reduced-motion`. Keep them in the visible widget renderer only (never the tray), animate only `opacity`/`transform`, and stop timers when hidden. With animations disabled, only the refresh icon rotates during refresh; reduced motion uses a static refresh state. Not an animation: the tray badge's critical alert (#122) alternates the normal frame with the alert frame every 2 s while another view is over the threshold. It carries information that would otherwise stay hidden, changes state far below the WCAG flash limit (3/s), and does not follow `ui.animations` or reduced motion.
- Do not expose secrets or credentials in any git-tracked file.
- Do not use `any`. If a type is really dynamic use `unknown` with explicit narrowing.
- Do not paste real credentials (sessionKey, PAT/token) in chat, commits, issues or logs — not even for tests.
- Do not fix pacing numbers in isolation until the refactoring (#75) is done: a wrong value found in use becomes an issue and a case in `docs/PACING.md` (with its expected value).
- Do not make unrequested architectural refactors: if you see a structural problem, report it before fixing it.
- Do not write user-visible text outside the i18n dictionaries, and do not write code comments in Italian.

---

## Workflow for agents

1. **New feature:** always use plan mode before writing code — describe what you will do and wait for confirmation.
2. **Bug fix:** describe the bug, the likely cause and the proposed fix before applying it.
3. **Before a release:** `npm run lint`, `npm test`; review `agents/`, `services/` and `main.ts` for calls without timeout, secrets in the renderer, non cross-platform paths, `setInterval` leaks.
4. **UI changes:** verify visually without asking the user for screenshots — offscreen rendering works in this environment: `env -u ELECTRON_RUN_AS_NODE xvfb-run -a npx electron --no-sandbox <script>` with a `BrowserWindow` `show: false` + a fake preload + `webContents.capturePage()`. Check both languages.

### Branches

Same model as every repo managed by agent-ops (full rules: agent-ops `docs/BRANCHES.md`):

- `next` is the **default branch** and the staging line: every change reaches it through a PR merged with
  **Rebase and merge** (linear history). Branch from `next` as `<type>/<n>-slug` (`feat/`, `fix/`, `chore/`, `docs/`…).
- `main` is production: it only receives the release PR from `next` (agent-ops `release`, merge commit) or a
  `hotfix/*` PR, then `release --backport`. Never push, commit or open a PR towards `main` yourself; never force-push.
- In PR bodies write `Refs #n`, not `Closes #n`: the issue closes at release, not when the PR reaches `next`.
- Agents run by the dispatcher work on the branch prepared for them (`agent/<n>-slug`) and do not push: the
  dispatcher pushes and opens the PR.

### Pacing changes: spec, tests, code

Any change to a quantity in `docs/PACING.md` §3 (values, verdict, tips, notification thresholds) goes in this
order, in the same PR:

1. update `PACING.md` and `PACING.it.md` (definition, edge case, invariant, example row);
2. write the tests from the example rows and invariants, naming the ID (e.g. `D-3.20`, `I-3`);
3. change the code until they pass.

Presentation-only changes (layout, wording, colours that don't change a threshold) skip step 1. Review flags a
PR that changes `budget.ts` or the indicators without touching `PACING.md`. Until #75 closes, a test written
from the spec may fail on a known deviation (`PACING.md` §9): say so in the PR, don't bend the test to the code.

---

## Build and test

- `npm run build` — compiles the three TS configs and copies html/css into `dist/renderer/` (`scripts/copy-assets.js`). Output always in `dist/`, never committed.
- `npm start` — build + Go core (`scripts/build-core.js --dev`, needs Go, skipped when `AIPERMILER_CORE_BIN` points to a core) + `electron .`
- `npm run lint` — ESLint, must be zero findings.
- `npm run typecheck` — `tsc --noEmit` on every config.
- `npm test` — build + `cd dist && node --env-file-if-exists=../.env.test --test`. **Not** `node --test dist` from the root: on Node 22 a bare directory argument fails with MODULE_NOT_FOUND. Unit tests mock `fetch`/SDK and never touch the network; renderer tests run as ES modules thanks to `dist/renderer/package.json`.
- `npm run coverage` — tests with Node's built-in coverage (counts only files imported by at least one test).
- `npm run package` — build + Go core (`npm run build:core`) + electron-builder (installers in `release/`, **not** `dist/`). The core ships in `<resources>/core/` (`extraResources`).
- `npm run package:test` — LOCAL test build (`scripts/test-build.js`): lint + tests like CI, then `.deb` and Windows packages in `release/test/`, version `<version>.dev.<timestamp>`, never published. Windows: NSIS `.exe` only with Wine (or on Windows), otherwise a ready-to-run `.zip`. The preferred way to try changes: no pipeline run; the user shares the files over the local network.

### CI — cross-platform build (GitHub Actions)
`.github/workflows/build.yml` builds on macos/windows/ubuntu in parallel:
- **Trigger:** a pushed tag starting with `v` (e.g. `v0.4.0-beta`) → **release** (`-rc` tags excluded); a manual run (`gh workflow run build.yml`, or Actions → Run workflow) → **test build**: version `<package.json version>.dev.<run number>`, packages only as run artifacts (`-test` suffix, 14 days), `--publish=never`, so no release and no update notification. No packaging on every push/PR: `.github/workflows/ci.yml` runs only lint + tests on Linux for every PR to `next` and every push to it. Workflow: work lands on `next` (staging), try test builds on the real machines (preferably local, `npm run package:test`), release `next` → `main` with agent-ops `release` once verified, then tag `main` (see *Branches*).
- **Each job:** setup Go → `npm ci` → `npm run lint` → `npm test` → `npm run build:core` (native, for the runner's OS/arch) → `npx electron-builder --<mac|win|linux> --publish=always`.
- **Output:** packages attached to the GitHub Release of the tag (`contents: write` permission) and uploaded as run artifacts (14 days). Release notes are written by hand (EN + IT) after the run.
- **No code signing** (`CSC_IDENTITY_AUTO_DISCOVERY: false`): Gatekeeper/SmartScreen warn at install time.
- `package.json` `author` must carry an email (Debian "Maintainer"): the GitHub noreply address is used, never a real one.

### Integration tests
None since #179: the provider reads moved to the Go core, whose adapters are tested against fakes. Real accounts are checked by hand with the core binary (`aipermiler usage <provider>`); **never provide credentials in chat**: the assisted environment does not need them.

---

## Milestones

Condensed history; the full session-by-session log is in the git history (CLAUDE.md before 2026-10-01).

| When | Version | Milestone |
|---|---|---|
| Day 1 | — | API research (docs/RESEARCH.md v3), architecture (multi-window quota model), Electron skeleton with mocks |
| Day 2 | — | Real services (Claude internal endpoint + session, Copilot official API), TypeScript migration, unit tests |
| Day 3 | v0.1.0-beta | Notifications, robustness, format-drift diagnostics, first real-account fixes, cross-platform CI |
| post-MVP | v0.1.x | UX rounds (frameless skins, title-bar drag, accent color, Save/Cancel), Copilot OAuth (hypothesis disproved), local Claude Code insights, instant gauge + star rating, data-driven tips, toggleable work schedule, dark skin |
| 2026-09-30 | v0.2.0-beta | N provider-independent accounts with isolated sessions (#4), own chart + window verdicts, value per token, reset time when today (#3) |
| 2026-09-30 | v0.2.1-beta | Fix: hidden window list stayed visible across accounts (`[hidden]` vs `display:flex`) |
| 2026-09-30 | v0.3.0-beta | Update check + browser download (#5) |
| 2026-10-01 | v0.4.0-beta | Robustness: stricter TS, type-aware ESLint gating CI, store normalization, IPC validation, unhandled promises, packaged icon fix; English/Italian UI; code in English; bilingual docs |
| 2026-10-01 | v0.4.1-beta | MIT license (packages metadata included) |
| 2026-10-01 | v0.4.2-beta | Fix: company Claude accounts read the new `spend` object (#6) |
| 2026-10-01 | v0.4.3-beta | Copilot: billing 400 fallback, token-based-billing snapshots (`credits_used`), clear message for enterprise-managed seats (#7) |
| 2026-10-01 | v0.4.4-beta | Copilot: GitHub domain per account — company seats on a `<tenant>.ghe.com` tenant read with a tenant PAT |
| 2026-10-01 | v0.4.5-beta | Copilot: personal scope reads the internal quotas first (billing report as fallback), OAuth no longer experimental; Settings tooltips no longer clipped |
| 2026-10-01 | v0.4.6-beta | Work schedule per account (inside each account detail), the old global one inherited on upgrade; fix: Copilot sync used github.com instead of the account's `.ghe.com` host |
| 2026-10-01 | v0.4.7-beta | Work schedule as the last, collapsed section of each account detail, with a one-line summary |
| 2026-10-01 | v0.4.8-beta | Pacing overhaul: monthly windows (Claude spend, Copilot) finally paced, working day from the day's samples, today's budget + pace notification, recent-pace and uncapped projection, local day keys, renewal date read from the provider |
| 2026-10-02 | v0.4.9-beta | Verdict from the remaining quota redistributed per working day; first-day baseline, phantom activity and running-day fixes; warning colours tied to the notification threshold (#13); "report usage response" issue draft; startup freeze fixed (local insights in a utility process, one store read/write per refresh), Settings loading state |
| 2026-10-02 | v0.4.10-beta | Claude `extra_usage` read as the monthly budget (deduplicated against `spend` on equal amounts), sane values on almost unused quotas, one diagnostic report for every account in Settings → Diagnostics, synthetic-only test data |
| 2026-10-02 | v0.4.11-beta | Diagnostic report file with explicit confirmation (ids redacted, log buffer), rolling-hours windows read in hours, verdict sees the current pace, working-hours gauge target with the schedule disabled, at-risk window shown first, Settings: info tooltips, locked fields visible, plan/Claude scope removed; local insights as their own section |
| 2026-10-02 | v0.4.12-beta | Diagnostic report: `system` section (time zone, screens, app memory/CPU, timings), only reset/timing dates kept; Copilot manual quota only for the personal scope, consumption rounded; local test builds (`npm run package:test`) and CI test builds without releases. First release marked as GitHub "latest" |
| 2026-10-07 | v0.4.13-beta | Absolute value under the percentage for count windows with a total (Copilot AI credits, Claude extra credit in USD; #14, #18, #21); configurable refresh interval (#15); dependencies updated, Electron 31 → 44 (#19); packages without the Claude Agent SDK native binaries, .deb 196 → 97 MB (#34). From here on, work lands through PRs with Claude/Copilot cross-review |
| 2026-10-09 | v0.4.14-beta | Reactive pacing overhaul: adaptive refresh (5 min after a rise), 1h instant pace, moving budget on the chart, leaner widget layout, user-only README (#38, #39, #40, #41); consistent streak/bar colours with the budget (#42); chart: highlight today's slot, thinner share bars (#43); fix: Free tier response no longer triggers a drift report, widget shows "Free plan" label (#44, #45) |
| 2026-10-18 | v0.4.15-beta | Codex provider: ChatGPT login in a per-account partition, usage from the web session (5-hour and weekly windows), Settings, widget labels, diagnostics (#82, #83, #86-#90); tray tooltip with every account's usage (#33); AppStream metadata and readable name for the Linux .deb (#10); shared IPC contract (#22); tray-only footprint measured on Linux (#27) |
| 2026-10-22 | v0.4.16-beta | Compact widget view: percentage and instant gauge only, toggle in the title bar (#60); pacing fixes: today's usage against the baseline at the same precision, reset only on a real drop (#64); Codex accounts without the useless manual renewal day (#95); slimmer packages, only en/it Chromium locales and no unused Anthropic SDK (#35); hardware acceleration off, tray-only PSS ~378 → ~295 MB on Linux (#99); CI lint + tests on every PR to `next`, rc tags never publish (#97, #100); pacing model written down in docs/PACING.md (#74) |
| 2026-10-24 | v0.4.17-beta | Usage badge on the tray icon with an Auto/Manual view shared with the panel and a critical alert (#33, #122); optional window animations: alarm pulse, odometer, neon refresh border (#119); animated push/slow-down arrows on the instant bar pointing to the target (#66, #125); alarm glow and blink on red values (#125); animated collapsible sections and compact/full switch, compact label (#121); renewal day 29-31 clamped to the last day of shorter months (#111); tests guard the IPC contract and channel wiring (#71) |
| 2026-10-25 | v0.4.18-beta | Hotfix: no claude.ai sign-in and stored sessions deleted, Anthropic forbids third-party apps from storing claude.ai session tokens; Claude accounts keep the local insights, quotas return with the Go core (#140, #139) |
| 2026-11-01 | v0.5.0-beta | Renamed to AIpermiler: userData and keyring migrations, in-place upgrade of the Windows installer and the .deb, repository renamed, public roadmap board (#9, #200, #201, #48); Go core CLI with a versioned JSON contract, bundled in the packages, reads Codex (`codex app-server`, no chatgpt.com cookies) and Copilot (token in the OS keyring) (#145, #159, #26, #148, #149, #177, #178, #179); Claude PTY spikes on Linux, Windows, macOS (#141, #183); tray badge cycles every alert (#131), solid skins with visible title bar and styled scrollbar (#133), Electron 45 (#136). macOS package not yet verified (#212) |

### Lessons learned (still relevant)
- **Codex login User-Agent** (historical: the chatgpt.com login route was removed in #178): Google refuses embedded login when a rewritten User-Agent contradicts the client hints. Never change the UA of the Codex login window or its partition.
- **ChatGPT usage authentication** (historical, #178): cookies alone get 401 on `wham/usage`; the Bearer from `/api/auth/session` is required. Node `fetch` works with the partition cookies + UA (probe #86, docs/RESEARCH.md §6); `session.fetch` is the fallback.
- **electron-store shallow merge**: new nested fields were `undefined` on existing stores → solved by `store/normalize.ts`.
- **ESM-only SDK in a CommonJS main process**: `require()` and even a TS-compiled `import()` fail with ERR_REQUIRE_ESM inside Electron → `new Function('s', 'return import(s)')` in `services/claudeLocalSessions.ts`.
- **claude.ai behind Cloudflare**: `sessionKey` alone gets a 403 challenge; the full cookie header (incl. `cf_clearance`) + a browser User-Agent are needed. Usage field names are obfuscated and rotate → windows are recognized by value shape.
- **Check the provider's terms, not only whether it works**: the claude.ai session capture worked for months, but Anthropic forbids third-party apps from collecting or storing claude.ai session tokens. Removed in #140 (stored sessions deleted at startup); before adding a source, read the provider's policy on third-party access.
- **Copilot company seats**: the seat may live on a GitHub Enterprise Cloud tenant with data residency (`<tenant>.ghe.com`, API on `api.<tenant>.ghe.com`), not on github.com — there `copilot_internal/user` has token-based-billing `quota_snapshots` (`credits_used`, `entitlement`), read with VS Code's rules. A github.com account linked to an enterprise (`access_type_sku: enterprise_managed`) has no data at all → `CopilotUsageUnavailableError`, not a drift. Each Copilot account has a validated `host` (`services/githubHost.ts`): the token is only ever sent to github.com or a `.ghe.com` tenant.
- **Logout reused the session**: cookies lived in `session.defaultSession` → one partition per Claude account, cleared on disconnect/remove/before login.
- **`[hidden]` vs author `display`**: a class with `display:flex` overrides `hidden` → global `[hidden] { display: none !important }`.
- **`build/` in .gitignore**: it is electron-builder's resources dir; icons were never committed (fixed — verify the packaged icon on the next release).
- **Optional parameters with a "safe" default hide wiring bugs**: the Copilot host was optional (default github.com) and `main/providers.ts` never passed it, so v0.4.4/0.4.5 sent tenant tokens to api.github.com (401). `CopilotCredentials.host` and `resolveUsername(token, host)` are now required, so the compiler catches a missing host.
- **Release tags** must start with `v` for CI to run.
- **`periodLength: null` silently disabled pacing**: the Claude company `spend` window and every Copilot window had it, so efficiency/projection/autonomy/verdict/tips were always empty there (noticed only after heavy use on day one went without warning). Billing-cycle windows known to be monthly declare `periodLength: 1` (months). Also: the current day only counted once over, so day one of a period had 0 elapsed units.
- **Synchronous work on the main process froze the app** (Settings blank for seconds at the first open): the encrypted store re-reads and decrypts the whole file on every `get` and rewrites it on every `set`, and the local Claude Code scan parsed up to 300 session files in-process. A refresh now reads the history once and writes it once (`HistoryDraft` in `main.ts`), refreshes do not overlap (one queued at most), and the scan runs in an Electron utility process (`services/claudeLocalSessions.worker.ts`), the stale cache shown meanwhile.
- **UTC day keys**: `toISOString().slice(0, 10)` is the UTC day, `new Date('YYYY-MM-DD')` is UTC midnight — both wrong for local working days → `localDateKey`/`parseDateKey`.

### Open items
- Claude quotas are not read since #140; they return through an allowed source with the standalone core in Go (#139, milestone v0.6.0).
- Provider reads live in the Go core (`gocore/`, CLI and JSON contract in docs/CORE-CLI.md); `core/usage.ts` (#23) still owns orchestration and snapshots until pacing moves to Go (#75, #160). New orchestration/snapshot logic goes test-first in `core/` or the Go core, not in `main.ts`.
- **Milestone v0.6.0** (GitHub): Claude quotas through the Go core (#147, #180), migrations of the rename removed (#204), macOS package verified (#212). **Milestone v1.0.0** (out of beta): signed Windows/macOS installers (#11).
- `agents/advisor.ts` is a stub.
- Copilot tab occasionally not clickable right after login (not reproduced; if it happens, open the widget DevTools and check the console).
- Verify with real use the pacing constants (`PACE_ALERT_RATIO` 1.5×, 2h minimum of observed work, 50/50 recent-pace blend, preliminary below 2 working units, redistribution verdict bands 0.5× / ±5%, `NEGLIGIBLE_UTILIZATION` 0.1%) and the `consumptionCause` thresholds, and with the next release the packaged icon and the update notification end to end.
- Tray-only footprint measured on Linux (~351 MB PSS with the widget hidden, idle CPU, GPU process the largest share; docs/EVOLUTION.md): tuning first (#99), point 5 or a system-webview shell only if it is not enough (#12); Windows still to measure (#98).
