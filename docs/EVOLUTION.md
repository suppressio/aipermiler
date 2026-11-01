🌐 **English** | [Italiano](EVOLUTION.it.md)

# EVOLUTION.md — Future directions and cost/benefit analysis

> A tracking document, not a design one. Unlike `ARCHITECTURE.md` (what is being built) and `RESEARCH.md` (real API constraints), this file collects a conceptual retrospective opened by the user after the MVP: evolution directions for the project **not yet decided nor scheduled**, with a first cost/benefit evaluation to guide priorities. It is updated when new considerations emerge or when a direction moves from "evaluated" to "decided" (at that point the decided part moves into `ARCHITECTURE.md`/`CLAUDE.md` like any other feature).

---

## Baseline frame (assumptions confirmed by the user, 2026-08-31)

These assumptions correct/narrow the scope compared with a first, more "open" reading of the directions below — they must be kept in mind in every later evaluation:

- **The user is and remains the developer**, owner of their own credit budget (bought, obtained with a free plan, or assigned by their company). There is no multi-user audience to serve.
- **An agent or an external CLI reading the app's data acts on behalf of the developer**, it is not a foreign consumer — an extension of the same user, not a third-party audience owed a contract that stays stable over time. This lowers the perceived cost/risk of a local API (point 2 below), but does not remove it: an unauthenticated local process can still be reached by any other process on the same machine, not only by the developer's tools.
- **A large transformation is fine**, not just an internal refactor — the user explicitly calls it an **evolution**, not a problem to minimize.
- **Inherited technical debt is to be paid off along the way**, not all at once (e.g. the open bug of the Copilot tab after login, `workSchedule.hoursPerDay` not used yet by `budget.ts`) — no urgency stated on these, they stay in the `CLAUDE.md` log.
- **The interface is considered relatively mature** (skins, dragging, tabs, icons — recent work judged enough for now). The most felt gap is not the UI itself but that the content shown stays too close to what Anthropic already offers in its own dashboard.

---

## The 5 directions

1. **Move away from the provider's view.** The goal is not to show the same numbers/charts Claude or GitHub already expose in their dashboards, but to decide independently which data really helps efficiency. The existing derived metrics (`efficiencyIndex`, instant/sustainable gauge, star rating, `generateDailyTip`) already go this way; still a direct copy of the provider UI are the "Weekly trend" chart (the same bar-chart-per-day as claude.ai) and the per-quota-window tabs (a plain side-by-side of the two metrics Anthropic exposes).

2. **Separate data/backend from the UI, with an internal API.** `main.ts` today mixes fetch orchestration, snapshot computation (`computeAccountSnapshot`/`computeWindowSnapshot`) and IPC/broadcast in the same file, with the snapshot computation written against the Electron store (not pure like `budget.ts`). The goal is for data to come before the interface — the UI is a "courtesy", not the product. The app must be able to live in the system tray only (the main process already runs headless with the window closed — that part exists), exposing the same data outside its own renderer too: an agent building its own MCP on top, or an independent client written by others (e.g. a Go CLI), both always on behalf of the developer who owns the data.

3. **Multi-provider genericity (OpenAI, z.ai, others).** The contract already imposed by CLAUDE.md (`fetchUsage(credentials): Promise<RawAccountUsage>` with a normalized `QuotaWindow`) is already a suitable base to add new providers at the single-service level. The real bottleneck is higher up: `AppSettings.accounts` has only two named slots (`claude`/`copilot`), not a dynamic registry — and so do the Settings panels and the account tabs in the widget.

4. **Redefine "efficiency" from pacing to value per token.** `efficiencyIndex`/`efficiencyRating` measure today how much the real consumption pace deviates from an ideal pace that avoids running out of quota — not whether that consumption is well spent. The signal for a value-per-token analysis already exists but is isolated: `services/claudeLocalSessions.ts` reads context size, session length, tool/MCP usage, but today it is an informational panel disconnected from the quota consumption numbers (and it covers only Claude, not Copilot). The leap in value would be crossing the two sources for actionable advice ("you consume fast *because* X") instead of just a thermometer.

5. **Electron's resource cost, even with the window closed.** Even in tray-only mode the main process and much of the Chromium/GPU runtime stay resident to keep the icon and polling alive — a conceptually simple workload pays the overhead of a full browser. It ties into point 2: if the core is extracted as a standalone Node process, the always-on piece might no longer be Electron. A trade-off not to underestimate: the tray icon and auto-start at login are provided for free and cross-platform by Electron today; removing it from the always-on piece means reimplementing them per platform.

---

## Points of attention from the "outside" analysis

- The 5 points are not independent initiatives: they are the same direction (from "widget" to "personal data platform about AI usage, with the UI as one client among many") seen from 5 angles.
- The structural irony of point 2: the app already has a whole diagnostics discipline for when *a provider's API* changes format under it (`FormatDriftError`). If the app itself becomes an API consulted by other developer tools, it will face the same problem in reverse (stability of its own contract over time) — there is no thinking on how yet. Answered in #26: a versioned contract with shared fixtures, see [CORE-CLI](CORE-CLI.md).
- A non-trivial tension between points 1/3 (parity between providers) and point 4 (the richest insight exists only for Claude via local files): the more is invested in deep analysis, the more asymmetric the product becomes in favour of Claude.
- Point 5 is an unmeasured hypothesis today: nobody has verified how much RAM/CPU the app really uses in tray-only mode on a real machine — before investing in a solution (expensive: independent daemon + packaging/auto-start per platform), the problem is worth quantifying.
- There is a low-cost alternative for much of the need in point 2: an on-demand headless CLI command (e.g. `--json`/export) instead of an always-on server with network authentication — see row "2b" in the table below.

---

## Cost/benefit evaluation

| # | Direction | Benefit | Cost | Risk | Dependencies |
|---|---|---|---|---|---|
| **1** | Move away from the provider's view (own chart/tabs, not a copy of Anthropic) | High — really sets the product apart from the Anthropic dashboard; the derived metrics already exist, the right visual hierarchy is missing | Low — no new infrastructure, mainly deciding what to promote and what to demote in the UI | Low, reversible | None — can start right away |
| **2a** | Extract a pure core (fetch+snapshot) from `main.ts` | Medium directly, high as a prerequisite — unlocks 2b/2c, testability, reuse | Medium — a real refactor, but covered by 91+ existing tests, low regression risk | Low | None |
| **2b** | On-demand headless CLI (`--json`/export) invoked by the developer or their agent | High for its cost — covers "the agent does the developer's bidding" with no network, ports, daemons | Low, builds on 2a | Low — no new network surface | 2a |
| **2c** | Always-on local HTTP server (continuous/remote access) | High but only if continuous access is really needed, not on demand | High — local authentication still needed, lifecycle management, unresolved whether it lives inside or outside Electron | Medium — new resident surface | 2a, and only if 2b proves insufficient |
| **3** | Multi-provider genericity (schema/UI) | Low today — no second real provider connected yet | Medium if done now on its own; much lower if done together with the 2a refactor | Risk of over-engineering for a hypothetical need | Better done during 2a, not as a separate intervention |
| **4** | Redefine efficiency: pacing → value per token | High — the closest to the app's stated purpose | Medium-high — crossing two sources separated today, risk of weak correlations if rushed | Medium — a bad insight undermines trust more than no insight | Pays off more after point 1 |
| **5** | Reduce Electron's cost in tray-only mode | Unknown — nobody has measured how much it really weighs today | High if really solved (independent daemon + packaging/auto-start for 3 OSes) — the most expensive item on the list | High: risk of investing heavily in a problem that may be more theoretical than real | None for the measurement; 2a if it is then decided to act |

---

## Suggested sequence

1. **Point 1** — low cost, high benefit, no dependency.
2. **2a**, together with **point 3** — touch the account schema once, not twice.
3. **2b** — almost free once 2a is done.
4. **Point 4** — after point 1, so as not to repeat the mistake of the disconnected panel.
5. **Point 5** — measure real tray consumption first, then possibly act (the most expensive of all).
6. **2c** — only if, after using 2b for a while, a real need for continuous/remote access emerges.

---

## Status (updated 2026-09-30)

The user's choice: evolve the app "along with my usage", **without upending its architecture** — hence points 1, 3 and 4, feasible within the current structure (Electron + main.ts + IPC + store). 2a (core extraction) was not done: point 3 was achieved without it, at a contained cost. Sequence executed: issue #3 → point 3 → point 1 → point 4, one commit per phase.

| # | Status | What was done |
|---|---|---|
| **1** | ✅ done | "Daily consumption vs budget" chart (delta per day + ideal share, `budget.dailyDeltas`) instead of the cumulative %; window list with a verdict computed by the app (`budget.windowVerdict`) instead of tabs; peak/average and streak computed on deltas (on the cumulative value they were effectively wrong). |
| **3** | ✅ done (with issue #4) | Registry of N provider-independent accounts (`AccountConfig[]`, common part + specific part), `main/providers.ts` for everything provider-dependent, accounts table in Settings, automatic migration from the two-slot schema (`store/migrate.ts`). One Electron partition per Claude account: fixes the logout bug that did not clear the session. Adding a new provider (e.g. OpenAI) = a service with `fetchUsage` + a branch in each function of `main/providers.ts` + a detail template in `settings.html`. |
| **4** | ✅ done (first version) | "Yield" (output tokens per 1% of quota, with trend) and a causal tip about large context, shown only with a clear signal. The **tension** noted above remains: it exists only for Claude (the only local source). Known limit: a session is attributed to the day it was last modified (the SDK does not expose message times). |
| 2a/2b/2c, 5 | ⏸ not addressed | As in the suggested sequence; point 5 must be **measured** before any intervention. |

## Open questions / next step

- Check with real usage whether the `consumptionCause` thresholds (≥5 days, ≥1.5×, ≥20 points) are too strict (the sentence never appears) or too loose.
- Measure point 5 (RAM/CPU in tray-only mode) before deciding whether to tackle 2a/5.

## Update (2026-10-01): 2a becomes the next step

The user's next request — making the app more robust and moving to test-driven development — changed the priority of **2a**: without extracting from `main.ts` a pure core (with the store injected), the orchestration logic cannot be tested without starting Electron, so test-first work is not possible. Done meanwhile: stricter TypeScript, type-aware ESLint at zero findings and gating in CI, store normalization, IPC input validation. Agreed order: first a single IPC contract (channels + types shared by main, preload and renderer), then the core extraction (2a), with tests written first to pin the current behaviour. 2b stays "almost free" once 2a is done.

## Update (2026-10-16): measuring point 5 — tool ready, numbers pending

`scripts/measure-resources.js` loads the built app in a throw-away profile (no accounts, no network) and samples `app.getAppMetrics()` first with the widget open, then after closing it (tray-only), printing a CSV and an average/maximum summary of total RAM (working set) and CPU over all app processes. It is cross-platform, so the same run works on Linux, Windows and macOS:

```
npm run build
env -u ELECTRON_RUN_AS_NODE npx electron scripts/measure-resources.js 5 240 30   # minutes open, minutes tray-only, seconds between samples
```

**Result: not measured yet.** The environment where this was prepared cannot start Electron (no GTK libraries), so no figure is claimed here. To do on real machines (Linux, Windows): run the command above for a few hours, add the summary lines to this section, and compare with a similar always-on tray app measured the same way (e.g. a chat client or a sync tool: Task Manager "Memory (active private working set)" / `ps` RSS summed over the process tree).

Decision rule proposed, to be confirmed with the figures: tray-only total below ~250 MB and average CPU below ~0.5% → **nothing** (point 5 closed); noticeably above, but dominated by one process (GPU/renderer kept alive) → **tuning** first (e.g. destroy the window instead of hiding it, `app.disableHardwareAcceleration()`); only if tuning is not enough → point 5 (after 2a).

Observation from reading the code: the 120 ms cursor poll (`startWindowHoverPolling`) keeps running in tray-only mode whenever `mainWindow` is hidden rather than destroyed; it is the first candidate if CPU turns out not to be idle.

## Update (2026-10-18): point 5 measured on Linux

Linux (Debian, KDE Plasma on X11, NVIDIA driver, 24 cores, 62 GB RAM), build from `next` after v0.4.14-beta, `measure-resources.js 5 60 30` on the real desktop (throw-away profile, no accounts). The script hides the window like the widget close button (`window:close` in `main.ts`), so the renderer and the 120 ms cursor poll stay alive as in real use:

```
SUMMARY open: processes=4 avgMB=712 maxMB=712 avgCPU=0.02% maxCPU=0.0% samples=6
SUMMARY tray: processes=4 avgMB=711 maxMB=712 avgCPU=0.01% maxCPU=0.0% samples=117
```

The summed working set counts the pages shared between processes (Chromium libraries, driver mappings) once per process, so it overstates the real footprint. The PSS (`/proc/<pid>/smaps_rollup`, shared pages split among their users) of the whole process tree, read in tray-only mode, was **~351 MB**: GPU process 136 MB, browser 86 MB, renderer 55 MB, network service 26 MB, zygotes and broker the rest. A first run that destroyed the window instead of hiding it gave ~320 MB: the renderer was gone, the GPU process was not.

Reading against the rule above: CPU is idle (0.01% average, the cursor poll does not show), memory is above ~250 MB and the GPU process is the largest share → **tuning** first (#99): `app.disableHardwareAcceleration()` (the widget is plain HTML/SVG, no animations), then destroying the window on close (~30-55 MB). Hypothesis, to be checked after #99: tuning may bring it to ~180-220 MB, below the ~250 MB rule. If it does not, or if a native tray app measured the same way is far lighter, point 5 or a system-webview shell goes to the architecture review (#12) with these figures. Not done: the comparison with another tray app, and Windows (#98).

**#99, 2026-10-22** (same Linux machine, X11/KDE, throw-away profile, tray-only PSS of the app's process tree): `app.disableHardwareAcceleration()` took it from ~378 to ~295 MB (−83 MB, −22%), almost all from the GPU process (172 → 97 MB: Chromium keeps a software GPU process). CPU still idle. The three skins render correctly, transparent-digital included (screenshots on the real display). Kept: noticeably lighter with no visible regression, though still above the ~250 MB rule; destroying the window on close is the next tuning step. Windows not checked yet (#98).

**#98, 2026-10-25** (Windows, installed v0.4.17-beta, so hardware acceleration already off; **real profile** with accounts and network, not the throw-away one; tray-only for 60 min, 30 s samples, first 3 skipped; summed working set and CPU over all `ia-hypermiler` processes, measured with PowerShell `Get-Process` instead of `measure-resources.js` so no source checkout was needed; CPU % = delta CPU seconds / 30 s / logical cores × 100, i.e. 100% = whole machine, while `percentCPUUsage` in the Linux figures has 100% = one core; no per-process breakdown):

```
SUMMARY tray: processes=4 avgMB=320 maxMB=346 avgCPU=0.05% maxCPU=0.6% samples=117
```

CPU is low here too, even with real polling: almost all samples at or below 0.2% of the machine, one 0.6% spike over the hour. On the one-core scale of the Linux figures (12 logical cores) that is ~0.6% average, slightly above the ~0.5% rule, with one ~7% spike: a small absolute load, but not idle like the Linux throw-away profile (0.01%). Real polling and the 120 ms cursor poll are the candidates; #138 also stops the latter. Memory slowly went down from ~346 to ~303 MB per sample, likely Windows trimming the working set of idle processes. Like on Linux, the summed working set counts shared pages once per process, so it overstates the footprint; it is not comparable with the Linux PSS figures above, only roughly with the Linux summed working set (712 MB, before #99, throw-away profile, different tool and OS). Reading against the rule: CPU just above and memory above ~250 MB by a modest margin → **tuning** (#138), not point 5; which process dominates was not measured on Windows, so this follows the Linux breakdown by analogy. The remaining tuning step is the same as on Linux: destroy the window on close (~30-55 MB on Linux PSS; the Windows saving is not estimated). After that the rule applies as written above: point 5 only if tuning is not enough.

## Update (2026-10-22): 2a done (#23)

`core/usage.ts` holds fetch orchestration and snapshot computation: `createUsageCore(deps).buildUsageSnapshot(mode)` plus `computeAccountSnapshot`/`computeWindowSnapshot`/`resolvePeriodBounds` and the history draft. Everything Electron-bound is injected (`UsageCoreDeps`): the store (`UsageStore`: accounts, ui, history, last good result), the provider fetch, connection check, local insights, error wording, the format-drift hook, the clock and the logger. The per-account backoff state now lives inside the core instance. `main.ts` keeps windows, tray, IPC, timers, notifications, the local-insights utility process and the settings-store adapter. The code was moved without changes; `core/usage.test.ts` pins its behaviour on an in-memory store (first refresh, rise detection, stale fallback, backoff, manual cache, local insights, pruning, rolling-hours vs monthly windows). Next: 2b, a headless CLI over the same core. Not moved: the local insights cache policy (`computeLocalInsightsIfNeeded`, it starts an Electron utility process) and the notifications.

## Update (2026-10-25): Claude quotas removed (#140), new direction (#139)

Anthropic's [Claude Code legal and compliance](https://code.claude.com/docs/en/legal-and-compliance) page says that third-party developers may not offer Claude.ai sign-in in their apps, and *"may not collect, store, or intermediate Claude.ai credentials or session tokens — sign-in to a Claude account must complete through Anthropic's own flow"*, with enforcement possible without notice. Capturing `sessionKey` from an embedded login window, the only way this app read Claude quotas, is exactly that case. #140 removes it: no Claude sign-in, no claude.ai fetch, and the sessions stored by earlier versions are deleted at the first start. Claude accounts stay for the local insights, which read only local Claude Code files.

Reusing Claude Code's own OAuth tokens is forbidden by the same rule. Two allowed sources exist, both through Anthropic's unmodified binary and sign-in: the status line JSON (`rate_limits.five_hour` / `seven_day`), which Claude Code does **not** run in the VS Code extension (tested 2026-10-25, reported in anthropics/claude-code#21943), and the `/usage` panel of `claude` run in a pseudo-terminal (spike #141). They come back with the standalone core in Go (#139, milestone v0.6.0), which is also the answer to point 5: a small always-on core with the Electron UI as an optional client.

## Update (2026-10-31): Go core resource budget (#145)

Measured on Linux, `uname -m`: `x86_64`, with `go version go1.27.3 linux/amd64`. The skeleton exposes only `version` and `help`, with no providers or network calls.

| Target | Stripped size (MB) |
|---|---:|
| linux/amd64 | 2.7 |
| linux/arm64 | 2.7 |
| windows/amd64 | 2.9 |
| darwin/arm64 | 2.6 |
| darwin/amd64 | 2.8 |

For one `hypermiler version` run on linux/amd64, medians over 10 runs: **1.46 ms wall time** and **6.4 MB max RSS** (6,380 KiB; range 6,188-6,472). Method: each target built from `gocore/` with `CGO_ENABLED=0 GOOS=<os> GOARCH=<arch> go build -trimpath -ldflags "-s -w" ./cmd/hypermiler`, version left at `dev`, size = file bytes / 1,000,000. Wall time from `time.perf_counter()` around `subprocess.run([binary, "version"])` in Python, one fresh Python process per run. Max RSS from `busybox time -v` (a parent that forks, so the child inherits only busybox's own ~1.7 MB before `exec`; a Python parent would report its own ~14 MB instead).

This is the baseline to compare every later step against: adapters #148/#149 will add a spawned process and network time.

**With the Codex adapter (#148)**, same machine, real signed-in `codex-cli 0.163.0` (native binary found from the npm shim on `PATH`): one `hypermiler usage codex --json` takes **~0.53 s wall** and **~0.3 s CPU** (user + system of the whole process tree), over 3 runs with `busybox time -v`. Its max RSS is **~490 MB**, and that is the spawned `codex app-server`, not the core (which stays at the ~6 MB above): `ru_maxrss` of the children is the largest single descendant. It lasts half a second per refresh and nothing stays resident (no `codex` process left after the run), but it is the number to watch if the refresh pace gets faster.

**With the Copilot adapter (#149)** the binary carries `net/http`, TLS and the keyring (D-Bus on Linux): stripped sizes grow to **8.3 MB** linux/amd64, 7.7 linux/arm64, 8.2 windows/amd64, 7.5 darwin/arm64, 8.1 darwin/amd64 (from 2.6-2.9). One `usage copilot` that stops at the keyring lookup (no token stored) takes **~12.6 MB** max RSS (`busybox time -v`, 5 runs), with a negligible wall time; a real read adds the GitHub calls (each bounded at 10 s).
