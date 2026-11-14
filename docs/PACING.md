🌐 **English** | [Italiano](PACING.it.md)

# PACING.md — The pacing model

> The model behind every number the widget, chart, notifications and tips show: definitions, edge cases, invariants, state, and tables of examples. It is **independent of the code**: when the code and this document disagree, the document is the intent and the code is the bug (the known disagreements are listed in §9 and are the input of the refactoring, #75). The example tables become the table-driven tests of the next issue.
>
> Status: **draft for review** (#74). Nothing in the code changes with this document.

---

## 0. Conventions

- **Quota percentage points** (`pp`): every quantity of quota is a percentage of the window's total, 0-100 (a window over its limit can exceed 100). A `count` window with a total is first normalized (`used / total × 100`); without a total it has no utilization and no pacing at all.
- **Units**: `pp` (quota), `pp/unit` (per working unit), `pp/h` (per hour), `h` (hours), `unit` (working units, §2).
- **Rounding**: a value is rounded **only to be shown or stored**, never to be used by another computation (invariant I-4). Tables show the rounded result; the formulas use the exact one.
- **Time**: all days are **local** calendar days (`YYYY-MM-DD` in local time), never UTC.
- **`now`** is always an argument: the model has no hidden clock.
- **Scenario S**, used by every table unless a row says otherwise: schedule Monday-Friday full days (Saturday and Sunday off), `hoursPerDay = 8`, billing cycle **[2026-10-01, 2026-11-01)**, `now` = **Tuesday 2026-10-20 10:00**, 3 hours of work observed today, no recent pace unless stated. In S: the period has **N = 22** working units (October 2026 has 22 weekdays), the even share is **s = 100 / 22 = 4.545 pp/unit** (0.568 pp/h over 8 h days), 13 units are over before today, today is a full day (1 unit) of which 0.375 is elapsed (3 h / 8 h), so elapsed **E = 13.375**, remaining **R = 8.625**, and **U = 9** counting today whole.

---

## 1. Window kinds

A provider exposes one or more **quota windows** of an account. The kind decides which quantities exist.

| Kind (`periodType`) | Example | Period | Reset | Day-based quantities (§3.2-3.8) | Hour-based quantities (§3.9-3.10) |
|---|---|---|---|---|---|
| **Rolling hours** (`rolling-hours`) | Claude/Codex 5-hour window | `periodLength` hours ending at `resetsAt` | `resetsAt` | **none** (a "day" spans several resets) | all |
| **Rolling days** (`rolling-days`) | Claude/Codex weekly window | `periodLength` days ending at `resetsAt` | `resetsAt` | all | target, instant pace |
| **Billing cycle** (`billing-cycle`, known length) | Copilot monthly quotas, Claude `spend`/`extra_usage` | `periodLength` months ending at the reset | window `resetsAt`, else provider `subscriptionRenewsAt`, else the manual day of month | all | target, instant pace |
| **Billing cycle of unknown length / one-off credits** (`periodLength = null`) | credits recognized only by value shape | **unknown start** | own `resetsAt` if any | none: only days to reset | instant pace only (no target, I-6) |

A window without a reset date and without a way to derive it has no forward-looking quantity at all (§5, E6).

**Period.** `[start, end)` where `end` is the reset and `start = end − periodLength` (hours, days or calendar months). **Rolling hours and rolling days keep their exact timestamps**; a rolling day is 24 hours. **Billing-cycle bounds are truncated to local days** for working-unit counting (§2), including the part of today already worked. Rolling windows require their own `resetsAt`: provider renewal and the manual day are billing-cycle fallbacks only.

---

## 2. Time: calendar and working

**D-2.1 Day unit** `u(d)` ∈ {1, 0.5, 0} — full, half, off day, from the account's schedule; **every day is 1 when the schedule is disabled**. Domain: a local calendar day.

**D-2.2 Working units between** `W(a, b) = Σ u(d)` for local days `d` in `[a, b)`; `0` when `b ≤ a`. For billing cycles this is the period total `N = W(start, end)`. For rolling days, use `Wᵣ(a, b) = Σ u(d) × duration([a, b) ∩ d) / 24 h` over overlapping local days, with `0` when `b ≤ a`; then `N = Wᵣ(start, end)`. With the schedule disabled `u(d) = 1`; with it enabled full, half and off days retain their weights, including partially overlapping first and last days.

**D-2.3 Today's elapsed units** `e` **for billing cycles** (the part of today already worked), domain `[0, u(today)]`:
- schedule enabled: `0` while no activity was seen today; otherwise `min(u(today), max(2 h, worked) / hoursPerDay)` where *worked* = last increase − first activity of the day (§3.1); `0` on a day off;
- schedule disabled: fraction of the calendar day elapsed (`10:00` → 0.4167).

For billing cycles, D-2.4 to D-2.6 hold for `today` inside the period, `start ≤ today < end` (local days). For rolling days they hold for `start ≤ now < end` (exact instants). Outside it: **before the start** `E = 0`, `R = U = N`; **from the end on** `E = N`, `R = U = 0`.

**D-2.4 Elapsed units** `E = W(start, today) + e`.

**D-2.5 Remaining units** `R = (u(today) − e) + W(today + 1, end)`. Invariant: `E + R = N` (I-8). For rolling days, `E = Wᵣ(start, now)` and `R = Wᵣ(now, end)`: observed activity and the 2-hour floor do not apply. Their `e = Wᵣ(max(start, today), now)` is the weighted overlap elapsed today.

**D-2.6 Units from today** `U = u(today) + W(today + 1, end)` — today counted whole (`R` with `e = 0`). Used wherever a fixed-for-the-day number is needed (today's budget, redistributed quota). For rolling days, `U = Wᵣ(max(start, today), end)`: only the part of today within the window counts, and `U` stays fixed during that day.

**D-2.7 Remaining working hours** `H = R × hoursPerDay`, also with the schedule disabled (every day is then a working day of `hoursPerDay` hours). Only where a window is read in hours (§3.9) `H = calendar hours to the reset`.

| # | Input | Expected |
|---|---|---|
| T-1 | Mon-Fri, [Mon 10-05, Mon 10-12) | W = 5 |
| T-2 | same, Friday `half` | W = 4.5 |
| T-3 | same, schedule disabled | W = 7 |
| T-4 | b ≤ a | W = 0 |
| T-5 | S | N = 22, s = 4.545, E = 13.375, R = 8.625, U = 9 |
| T-6 | S, 0 h worked yet (no activity) | e = 0, E = 13, R = 9 |
| T-7 | S, activity but only 0.5 h worked | e = 2/8 = 0.25 (floor of 2 h) |
| T-8 | S, 12 h of activity span | e = 1 (capped at the day unit) |
| T-9 | S on Saturday 10-24 10:00 | e = 0, E = 17, R = 5, U = 5 |
| T-10 | S with the schedule disabled (N = 31) | e = 10/24 = 0.4167, E = 19.4167, R = 11.5833, H = 92.67 h |
| T-11 | `now` before the period start | E = 0, R = N |
| T-12 | `now` after the period end | E = N, R = 0 |
| T-13 | S, Friday 10-30 17:00, 7.9 h worked | e = 0.9875, R = 0.0125, H = 0.1 h |
| T-14 | S with `now` = Monday 11-02 09:00, no activity (cycle over, no fresh reset yet) | E = 22, R = 0, U = 0 |
| T-15 | HYP#78, rolling 7 days [Wed 2026-10-14 23:00, Wed 10-21 23:00), now Sat 10-17 18:38, schedule disabled | N = 7, E = 4058/1440 ≈ 2.82, R = 6022/1440 ≈ 4.18, U = 119/24, e = 1118/1440 |
| T-16 | same window, Mon-Fri full, now Fri 10-16 12:00 | N = 5, E = 37/24, R = 83/24, U = 95/24, e = 0.5 |
| T-17 | same window, Mon-Fri full, now Sat 10-17 18:38 (day off inside the window) | N = 5, E = 49/24, R = U = 71/24, e = 0 |
| T-18 | as T-16, Friday half | N = 4.5, E = 31/24, R = 77/24, U = 83/24, e = 0.25 |
| T-19 | rolling 7 days [Wed 10-14 00:00, Wed 10-21 00:00), now Sat 10-17 18:38, schedule disabled | N = 7, E = 5438/1440, R = 4642/1440, U = 4 |
| T-20 | rolling 7 days [Wed 10-14 01:00, Wed 10-21 01:00), now Sat 10-17 18:38, schedule disabled | N = 7, E = 5378/1440, R = 4702/1440, U = 97/24 |

---

## 3. Quantities

### 3.1 Used, remaining, today's activity

- **D-3.1 Used** `x` (pp): the normalized utilization of the window now. Not clamped: 103 stays 103.
- **D-3.2 Remaining** `rem = max(0, 100 − x)` (pp).
- **D-3.3 Today's activity span**: the samples of the local day are compared with the previous one (the first with the day's baseline `x₀`, §7); a rise **above 0.05 pp** is activity, a drop is a reset, not activity. `start` = the sample before the first rise (or the first rise itself if it is the day's first sample); `end` = the last rise; `start` moves earlier to the day's first local Claude Code session when that is earlier. No rise → no span. Worked hours = `end − start`.

| # | Input | Expected |
|---|---|---|
| A-1 | baseline 57.00, samples 09:00 57.00, 10:00 57.03 | no span (rise 0.03 ≤ 0.05) |
| A-2 | baseline 57.00, samples 09:00 57.00, 10:00 58.00, 12:30 59.00 | start 09:00, end 12:30, worked 3.5 h |
| A-3 | baseline 57.00, first sample of the day 10:00 58.00 | start = end = 10:00, worked 0 h (floor of 2 h applies through D-2.3) |
| A-4 | baseline 57.00, samples 09:00 57.00, 10:00 3.00 (reset) | no span |
| A-5 | as A-2 and a Claude Code session started 08:15 | start 08:15, worked 4.25 h |
| A-6 | normalization: count 150/300; count without total; percentage 37.5 | 50; none; 37.5 |

### 3.2 Ideal pace

**D-3.4 Even share** `s = 100 / N` (pp/unit); the share of a day is `s × u(day)`. Domain: `N > 0`, otherwise undefined (no pacing).
**D-3.5 Ideal hourly pace** `s_h = s / hoursPerDay` for day-based windows; `100 / periodLength` for rolling hours (5 h → 20 pp/h).

### 3.3 Efficiency index

**D-3.6** `idx = s / (x / E)` — ideal pace over the actual period-average pace; > 1 consuming less than planned, < 1 more. Cumulative since the period start, no blending. Null when `x < 0.1` (noise), `N ≤ 0` or `E ≤ 0`. Not for rolling hours.

### 3.4 Recent pace and blended pace

**D-3.7 Recent pace** `p_r` (pp/unit): over the last 3 **completed** working days of the history (today excluded, days off and reset days skipped): `Σ delta / Σ u`. Null with fewer than 2 usable days.
**D-3.8 Blended pace** `p = (x / E + p_r) / 2` (just `x / E` when `p_r` is null; just `p_r` when `E = 0`; null when neither exists). It is the one pace used by projection, autonomy and the verdict.

### 3.5 Projection at reset and autonomy

**D-3.9 Projection** `proj = x + p × R` (pp), **not capped at 100** (227% says how far over the pace leads). No pace → `x`.
**D-3.10 Autonomy** `a = (100 − x) / p` working units, `0` when `x ≥ 100`, `∞` when `p ≤ 0` or unknown. Compared with the time left `R`.

### 3.6 Instant pace, ceiling, target (the gauge)

**D-3.11 Instant pace** `v` (pp/h): from the last sample at or before `now − 1 h` (but not older than `now − 2 h`; otherwise the oldest sample inside the last hour) to the newest, `(x_new − x_old) / hours`. Null with fewer than 2 samples or less than 5 minutes between them; a drop (reset) counts as 0.
**D-3.12 Ceiling** `C = rem / H` (pp/h) — the fastest pace that does not exhaust the quota before the reset, over the hours that can actually be worked (D-2.7; calendar hours for rolling hours, and when no working time is left). `0` when `rem = 0`.
**D-3.13 Target** `T = min(C, s_h)` — what the gauge compares `v` with. It is the ceiling when the user is behind (`C < s_h`: slow down) and the even hourly share otherwise: **the target never exceeds the ceiling and never rises because the reset is near** (I-1, I-2). Undefined (the gauge shows `v` without a target) when the period start is unknown (`s_h` unknown, I-6) or the reset is missing or already past.
**D-3.14 Gauge position** `g(v, T)`: `0` if `v ≤ 0`; `1` if `T ≤ 0`; else `0.5 + log₂(v / T) / 4` clamped to `[0.02, 1]` (target in the middle, ½× at a quarter, 2× at three quarters, ≥ 4× at the end).

| # | Input | Expected |
|---|---|---|
| G-1 | samples 10:00 40.0, 10:30 41.0, 11:00 42.5; now 11:00 | v = 2.5 pp/h (anchor 10:00) |
| G-2 | samples 10:50 40.0, 11:00 40.2; now 11:00 | v = 1.2 pp/h (no anchor, oldest in the hour) |
| G-3 | samples 10:56 40.0, 11:00 40.2 | v = null (< 5 min) |
| G-4 | samples 08:30 40.0 (2.5 h old), 10:50 41.0, 11:00 41.2 | anchor too old: v from 10:50 → 1.2 pp/h |
| G-5 | samples 10:00 40.0, 10:50 3.0 (reset), 11:00 3.5 | v = 0 (negative delta across the reset clamped to 0) |
| G-6 | S, x = 60 | rem = 40, H = 69 h, C = 0.580, s_h = 0.568, **T = 0.57** |
| G-7 | S, x = 40 (ahead) | C = 0.870, T = s_h = 0.57 (never above the even share) |
| G-8 | S, x = 80 (behind) | C = 0.290, T = 0.29 |
| G-9 | 5-hour window, 0 used, 5 h left | C = 20, s_h = 20, T = 20 |
| G-10 | 5-hour window, x = 67, 2 h left | C = 16.5, s_h = 20, T = 16.5 |
| G-11 | S, x = 100 | rem = 0, C = 0, T = 0; gauge: 0 if v = 0, 1 if v > 0 |
| G-12 | S, x = 66.9, Friday 10-30 17:00 (H = 0.1 h) | C = 331, **T = 0.57** (see §8, #72) |
| G-13 | g(5, 10) / g(10, 10) / g(20, 10) / g(40, 10) / g(0, 10) / g(0.1, 10) / g(5, 0) | 0.25 / 0.5 / 0.75 / 1 / 0 / 0.02 / 1 |

### 3.7 Today's budget

**D-3.15 Today's budget** `B = (100 − x₀) / U × u(today)` (pp) — what was left at the **start** of the day spread over the working units from today on, times today's unit. **Fixed for the whole day.** `x₀` is the day's baseline (§7). Undefined (null) without a baseline, on a day off, outside the period, or when `U = 0`.
**D-3.16 Used today** `used_today = x − x₀`, never negative; if the value fell by more than **1 pp** below the baseline a reset happened in the day and `used_today = x` (the new count). Both sides at the **stored precision** (0.01 pp): comparing a live value with a stored one requires the same rounding. A drop of up to 1 pp is noise (a provider falling back to an integer percent), not a reset.
**D-3.17 Pace alert**: `used_today > 1.5 × B`, with `B > 0`.

| # | Input | Expected |
|---|---|---|
| B-1 | S, x₀ = 57, x = 60 | B = 4.78, used_today = 3 |
| B-2 | S, x₀ = 57.04, x = 57.0449 | used_today = 0 (both at 0.01: 57.04 − 57.04) |
| B-3 | S, x₀ = 40.0, x = 39.6 | used_today = 0 (drop 0.4 ≤ 1: not a reset) |
| B-4 | S, x₀ = 40.0, x = 3.5 | used_today = 3.5 (reset) |
| B-5 | S, x₀ = 90, x = 103 | B = 1.11, used_today = 13, alert (13 > 1.67) |
| B-6 | S on Saturday | B = null |
| B-7 | S, no baseline | B = null |
| B-8 | B = 4.78 | alert above 7.17: used 7.2 → yes; 7.17 → no |
| B-9 | S, Friday 10-30 17:00, x₀ = 58, x = 60 | B = 42 (U = 1), used_today = 2 |

### 3.8 Redistributed quota

**D-3.18 Redistributed quota** `q = rem / U` (pp/unit): what is left **now** over the working units from today on (today whole). Next to it the even share `s` and `U`. It follows today's consumption while the day runs (B does not). Null when `U = 0` or `N = 0`. It is the primary pacing signal: it needs no history.
**D-3.19 Ratio** `ρ = q / s`.

| # | Input | Expected |
|---|---|---|
| Q-1 | S, x = 60 | q = 4.444, s = 4.545, U = 9, ρ = 0.978 |
| Q-2 | S, x = 40 | q = 6.667, ρ = 1.467 |
| Q-3 | S, x = 80 | q = 2.222, ρ = 0.489 |
| Q-4 | S on Saturday 10-24, x = 70 | U = 5, q = 6.0, ρ = 1.32 |
| Q-5 | S, x = 100 | q = 0, ρ = 0 |
| Q-6 | start of window, S on 10-01 09:00, x = 0 | U = 22, q = s = 4.545, ρ = 1 |

### 3.9 Hourly outlook (rolling-hours windows)

**D-3.20** `hoursLeft = max(0, resetsAt − now)`; `projectedAtReset = x + v × hoursLeft` (no measured pace counts as 0); `autonomyHours = 0` when `x ≥ 100`, `(100 − x) / v` when `v > 0`, otherwise null. Null without a reset moment. **Reset passed** (`resetsAt ≤ now`, stale data, E5): `hoursLeft = 0`, `projectedAtReset = x`, `autonomyHours` null whatever `x` and `v`, and the window has no forward-looking verdict (§3.10 step 2 does not apply).

| # | Input | Expected |
|---|---|---|
| H-1 | x = 67, 2 h left, v = 10 | projected 87, autonomy 3.3 h |
| H-2 | x = 67, 2 h left, v = 20 | projected 107, autonomy 1.65 h |
| H-3 | x = 67, 2 h left, v = null | projected 67, autonomy null |
| H-4 | x = 100, 2 h left | projected ≥ 100, autonomy 0 |
| H-5 | reset passed: resetsAt 09:00, now 10:00, x = 80, v = 10 | hoursLeft 0, projected 80, autonomy null, verdict no pacing |

### 3.10 Verdict

Evaluated in this order; the first that applies wins. `p` is the blended pace, `preliminary` is `E < 2`.

1. `x ≥ 100` → **exhausted**.
2. Rolling hours with the reset still ahead (`resetsAt > now`): `projectedAtReset > 100` → **at risk** (with autonomy hours), else **on track**. With the reset passed it falls through to 5 (E5).
3. With a redistribution (`q`, `s > 0`):
   a. `p` known, not preliminary, and `p > 1.5 × q` → **at risk** (pace).
   b. `ρ < 0.5` → **at risk**; `ρ < 0.95` → **behind**; `ρ ≤ 1.05` → **on track**; otherwise **ahead**.
4. No redistribution but a pace: `a < R` → **at risk** (autonomy); `proj > 100` → **at risk** (projection); otherwise **on track**.
5. Nothing known → **no pacing**.

The widget opens on an exhausted or at-risk window first, then the most used, then the one with pacing.

| # | Input | Expected |
|---|---|---|
| V-1 | S, x = 100 | exhausted |
| V-2 | S, x = 60 (ρ 0.978), p = 4.743 | on track |
| V-3 | S, x = 65 (ρ 0.856), p = 4.86 | behind |
| V-4 | S, x = 80 (ρ 0.489) | at risk |
| V-5 | S, x = 40 (ρ 1.467) | ahead |
| V-6 | S, x = 60 (ρ 0.978), p = 7.0 (> 1.5 × 4.444 = 6.667) | at risk (pace) |
| V-7 | as V-6 but preliminary | on track |
| V-8 | ρ exactly 0.95 / 1.05 / 0.5 | on track / on track / behind |
| V-9 | rolling hours, projected 87 / 107 | on track / at risk (1.65 h) |
| V-10 | no redistribution, a = 8.4 < R = 8.625 | at risk (autonomy) |
| V-11 | unknown period, nothing computable | no pacing |
| V-12 | pick: 5-hour at 67 % on track, weekly at 55 % at risk | weekly first |

### 3.11 Rating (consistency)

**D-3.21 Daily delta** of a completed day: `x_end − x₀` of that day (pp), null when negative (reset in between, not attributable); the day's baseline when recorded, otherwise the previous day's last value. **Ideal share of the day** `s × u(day)`.
**D-3.22 Rating**: over the last 7 or 30 completed days with an ideal share > 0 and a delta: per day `r = min(3, share / delta)` (3 when `delta = 0`), `avg = mean(r)`, stars `5` if `avg ≥ 1.5`, `4` if `≥ 1.1`, `3` if `≥ 0.9`, `2` if `≥ 0.6`, else `1`. Days off are excluded. **No rating** (null) when there is no usable day, **no consumption at all** on the rated days, or the window is rolling hours or without pacing. Today is never rated.

| # | Input | Expected |
|---|---|---|
| R-1 | share 4.55; deltas 2.0, 4.55, 9.1 | ratios 2.275, 1, 0.5; avg 1.26 → **4 stars** |
| R-2 | deltas 0, 0 | no rating (nothing consumed) |
| R-3 | deltas 0, 1.0 (share 4.55) | ratios 3, 3 (capped) → avg 3, 5 stars |
| R-4 | a reset day (delta null) among two normal days | the reset day is ignored |
| R-5 | avg 1.5 / 1.1 / 0.9 / 0.6 / 0.59 | 5 / 4 / 3 / 2 / 1 stars |
| R-6 | a Saturday with use 3.0 | excluded (share 0) |

(#67 replaces the efficiency index and the stars with a new reading of consistency; this table describes what is shown today and is the place to put the new definition's cases.)

### 3.12 Chart, peak/average, streak

**D-3.23 Chart slots**: the last 7 or 30 days ending today and 2 or 5 days to come. Per slot: `delta` (past and today), `fullShare = s` (inside the period only), `dayUnit`, and the **moving budget**:
- past day with a baseline `x₀(d)`: `(100 − x₀(d)) / W(d, end) × u(d)`;
- today: `B`;
- future day: `q × u(d)`;
- outside the period or without pacing: none.
`overBudget = delta > limit` where `limit` is the moving budget, or `s × u(d)` when there is none; strict inequality.
**D-3.24 Peak / average**: over completed days (before today) with a delta, days off included.
**D-3.25 Streak**: counting back from the most recent completed day; a day off without use is skipped; a day `overBudget`, or without any limit, ends it. Null without a limit to compare with or without any consumption.

| # | Input | Expected |
|---|---|---|
| C-1 | S, Thu 10-15, x₀ = 52 | budget = 48 / 12 × 1 = 4.0 |
| C-2 | delta 4.2 / 4.0 against 4.0 | over / not over |
| C-3 | S, x = 60, Wed 10-21 slot | q × 1 = 4.44 |
| C-4 | S, Sat 10-24 slot | 0 |
| C-5 | slot before 10-01 | no budget, no share, never over |
| C-6 | completed days (recent first): Mon 2.0 under, Sun 0, Sat 0, Fri 3.5 under, Thu 5.0 over | peak 5.0, avg 2.1, streak 2 |
| C-7 | all deltas 0 | peak 0, avg 0, streak null |
| C-8 | one completed day only | peak/average hidden (needs 2 days) |

### 3.13 Projection summary and "preliminary"

Projection and autonomy rest on `E` working units; below **2** they are flagged *preliminary* and the warning colour and the pace override of the verdict are off.

| # | Input | Expected |
|---|---|---|
| P-1 | S, x = 60, p_r = 5.0 | x/E = 4.486, p = 4.743, proj = **100.9**, a = **8.4**, working days to reset 8.6 |
| P-2 | S, x = 60, no p_r | p = 4.486, proj = 98.7, a = 8.9 |
| P-3 | S on 10-01 09:00, x = 0 | E = 0, p null, proj = 0, a = ∞ (hidden), preliminary |
| P-4 | S, x = 100 | a = 0 |
| P-5 | S, x = 0.05 | efficiency index null (< 0.1) |
| P-6 | S, x = 60 | efficiency index = 4.545 / 4.486 = **1.01** |
| P-7 | S, x = 103 | p = 7.70, proj = 169.4 (uncapped); verdict exhausted wins |

### 3.14 Tips

A tip is **data** (`{ key, params }`); the candidates are those whose condition holds, one is picked at random among them, none → no tip. **An exhausted quota (`x ≥ 100`) has no candidates at all** (I-3, D3): the conditions below apply only when `x < 100`. A tip is shown only when it adds an action or a cause that no other number states.

| Key | Condition | Params |
|---|---|---|
| `autonomy` | not preliminary, `x < 100`, `0 < a < R` (finite), and the displayed reduction is **between 1 % and 99 %** | `a`, `R`, `reduction = (1 − a / R) × 100` |
| `rating` | stars ≥ 4 | stars, avg |
| `nearReset` / `nearResetToday` | days to reset ≤ 2 (0 → *today*) and `x ≥ 70` | days, `x` |
| `cause` | consumption cause is clear (§3.15) | percentages, days |

| # | Input | Expected |
|---|---|---|
| TP-1 | a = 8.4, R = 8.6 | `autonomy`, reduction 2 % |
| TP-2 | a = 0 (x = 100) | no `autonomy` tip (§8, #73) |
| TP-3 | a = 0.04, R = 8.6 (reduction 99.5 → 100) | no `autonomy` tip |
| TP-4 | a = 8.58, R = 8.6 (reduction 0.23 → 0) | no `autonomy` tip |
| TP-5 | stars 3 | no `rating` tip |
| TP-6 | x = 72, 2 days / 0 days to reset | `nearReset` / `nearResetToday` |
| TP-7 | x = 69, 1 day to reset | no tip |
| TP-8 | preliminary | no `autonomy` tip |
| TP-9 | x = 100, 1 day to reset, stars 5, clear cause | no tip (exhausted) |

### 3.15 Local insights (Claude, opt-in)

**D-3.26 Yield**: over days present both in the quota history and in the local sessions, with a positive delta and output tokens: `Σ output tokens / Σ delta` (tokens per pp). Needs ≥ 3 days and ≥ 1 pp in total. Trend: yield of the second half over the first half, in %.
**D-3.27 Consumption cause**: ≥ 5 compared days; days above the median delta vs the others; stated only when the share of tokens produced above 150k context is ≥ 1.5× and ≥ 20 points higher on the heavy days.

| # | Input | Expected |
|---|---|---|
| Y-1 | tokens/delta: 30k/3, 45k/3, 60k/3 | 15 000 tokens/pp; trend +100 % |
| Y-2 | 2 days only | null |
| Y-3 | total delta 0.8 pp | null |
| Y-4 | deltas 1..5, high-context share 60 % (days 4-5) vs 20 % (days 1-3) | cause (60, 20, 5 days) |
| Y-5 | same, 30 % vs 20 % | null (gap 10 < 20) |
| Y-6 | 4 days | null |

### 3.16 Period end and renewal

The period end is, in order: the window's `resetsAt`, the provider's `subscriptionRenewsAt`, the manual day of month (`resolveRenewalDate`: the next occurrence strictly after `now`). Only `dayOfMonth` is supported. A day beyond the length of a month (29-31) means the last day of that month, never the following month.

| # | Input | Expected |
|---|---|---|
| N-1 | day 15, now 10-20 | 2026-11-15 |
| N-2 | day 20, now 10-20 10:00 | 2026-11-20 (the day is already started, strictly after) |
| N-3 | window reset 10-27 and provider renewal 11-01 | end 10-27 |
| N-4 | no window reset, provider renewal 11-01 | end 11-01 |
| N-5 | none of them, manual day 15 | end 2026-11-15 |
| N-6 | day 31, now 10-20 | 2026-10-31 |
| N-7 | day 31, now 10-31 | 2026-11-30 (shorter month: its last day) |
| N-8 | day 30, now 01-31 | 2026-02-28 (the January occurrence has passed; February is clamped) |
| N-9 | day 29, now 2028-02-10 | 2028-02-29 (leap year) |

### 3.17 Notifications

| Notification | Condition | Frequency |
|---|---|---|
| Threshold | any window of the account has `x ≥ threshold` (default 80; 79.99 no) | once per day per account |
| Pace | any paced window has `used_today > 1.5 × B`, `B > 0` | once per day per account |

### 3.18 Colours

Main value and projection: orange 5 pp below the threshold, red from the threshold (no colour while preliminary). Today's used/budget: orange from `threshold %` of the budget, red above 100 % of it. Chart bar: red when `overBudget`. All from the same quantities as above, never from separately rounded copies.

---

## 4. Refresh cadence (context for the state)

A refresh reads the provider, records, computes, stores the result in memory for the renderer. After a fresh fetch where a window rose, the next one comes after 5 minutes, otherwise after the configured interval; failures back off per account. Only **freshly fetched** values are samples. See CLAUDE.md, *Auto-refresh*.

---

## 5. Edge cases

Each row says what the quantities are. "—" = undefined, the indicator is hidden (never shown as 0). Scenario S unless stated.

| # | Edge case | Input | Expected |
|---|---|---|---|
| E1 | **Start of window** | S on Thu 10-01 09:00, x = 0, x₀ = 0 | E = 0, R = 22, idx —, proj 0, a hidden, B = 4.55 (used 0), q = s = 4.55, verdict on track, preliminary, T = 0.57, v — |
| E2 | **Last minutes before the reset** | S, Fri 10-30 17:00, 7.9 h worked, x = 60, x₀ = 58 | R = 0.0125, proj 60.0, a = 14.7 > R (no tip), q = 40 (ρ 8.8, ahead), B = 42, **T = 0.57** (ceiling 400 ignored) |
| E3 | **Quota exhausted** | S, x = 100, x₀ = 90 | verdict exhausted, a = 0, rem 0, q = 0, T = 0, B = 1.11 used 13 (pace alert), no `autonomy` tip, no `rating`-based push to consume |
| E3b | **Over the limit** | S, x = 103 | as E3; shown value 103, never clamped; remaining 0 |
| E4 | **No usage yet today** | S, x = x₀ = 60, no activity | e = 0, E = 13, R = 9, used_today 0, B = 4.44, v = 0 or —, gauge position 0, today not rated |
| E5 | **Reset passed** (stale last response, `resetsAt` < `now`) | 5-hour window, resetsAt 09:00, now 10:00, x = 80 stale | no forward-looking values: hoursLeft 0, projected = 80, autonomy —, T —, verdict from x only (exhausted if ≥ 100, else no pacing); shown as stale until a fresh fetch |
| E6 | **Missing reset date** | rolling window without `resetsAt` | no pacing, no target, only x |
| E6b | billing cycle without window reset | provider renewal 11-01 used; none → manual day | see N-3..N-5 |
| E7 | **Schedule disabled** | S, disabled | see T-10; idx = 1.04 for x = 60; T from H = 92.67 h: C = 0.43, s_h = 100/(31×8) = 0.40, T = 0.40 |
| E8 | **Non-working day** | S, Sat 10-24 10:00, x = x₀ = 70 | e = 0, B —, q = 6.0 (U = 5), slot limit 0: any use today is `overBudget`, no `rating` entry (share 0), streak skips the day only if unused |
| E9 | **Unknown period start** | billing cycle `periodLength = null`, resetsAt 10-27, x = 72 | days to reset 7; idx, proj, a, B, q, T, rating **—**; verdict no pacing; v shown without target; tip `nearReset` only within 2 days |
| E10 | **Half day** | S, Friday `half`, 10:00, 4 h worked | e = min(0.5, 4/8) = 0.5; R drops accordingly |
| E11 | **First day of history** | no history point | baseline: 0 if the period started today, else the current value (consumption before the first refresh is unknown) |
| E12 | **Reset during the day** | x₀ = 60, now 3.5 | used_today = 3.5; the day's delta is null (not attributable) |
| E13 | **Day without a refresh** | app closed all of Tue, last stored value Mon 60, Wed first refresh 64 | Wed's baseline = Mon's last value (60), so Wed's delta is 4 and includes Tue's use; Tue has no point and no bar |
| E14 | **Count window without total** | used 12, total null | utilization —, no pacing, never picked as critical over one with a value |

---

## 6. Invariants

- **I-1** The target never exceeds the ceiling: `T ≤ C`.
- **I-2** The target never grows because the reset is near: `T ≤ s_h`, and `T` is independent of how few hours are left (only `C` is).
- **I-3** A tip never asks for a reduction of 100 % or more, or of less than 1 %; nothing is advised on an exhausted quota.
- **I-4** A shown value is never derived from another rounded value. A value is rounded at its last step; a live value compared with a stored one is first brought to the stored precision (0.01 pp).
- **I-5** A value that does not exist is hidden, not shown as 0 (no history → no rating, not five stars; no usage → no pace).
- **I-6** Nothing forward-looking is fabricated for an unknown period start or a missing/past reset.
- **I-7** Today is never judged as a completed day (rating, peak, streak); today's budget is fixed during the day.
- **I-8** `E + R = N`; `0 ≤ e ≤ u(today)`; `U ≥ R`.
- **I-9** The streak, the colour of a chart bar and the budget it is compared with are the same flag.
- **I-10** Projection is never capped; remaining is never negative; utilization is never clamped for display.
- **I-11** Calendar days are local.
- **I-12** One writer per piece of state (§7).

---

## 7. State

What is persisted, who writes it, when, at what precision. Everything else in this document is **computed on every refresh from this state and the live response**, and never stored.

| State | Content | Single writer | When | Precision | Read by |
|---|---|---|---|---|---|
| `history.dailyUsage` | per account and window and local day: `used` (last value of the day), `dayStartUsed` (baseline) | the refresh (`recordDailyUsage`) | the day's point on every **fresh** refresh; `dayStartUsed` set once when the point is created | 0.01 pp | deltas, chart, rating, recent pace, today's budget |
| `dayStartUsed` rule | previous day's last value if inside the period and not dropped by more than 1 pp; 0 if the period started after it or a reset happened; with no history 0 when the period started today, else the current value | `updateDailyPoint` (pure) through the same writer | at creation of the day's point | 0.01 pp | B, used_today, activity span |
| `history.recentSamples` | timestamped `used` per account and window | the refresh (`recordRecentSample`) | only a **freshly fetched** value | 0.01 pp | instant pace, activity span |
| `history.lastGood` | last successful raw response per account | the fetch path | on a successful fetch | exact | shown as stale when a fetch fails |
| `meta.notifiedToday` | `<account>:threshold:<day>`, `<account>:pace:<day>` flags | the notification step | when a notification fires | — | the same step; only today's flags are kept |
| Account settings | schedule, manual renewal day, host, … | Settings (`settings:set`) | on user save | — | every computation |
| Local insights cache | tokens per day, first session of the day | the utility process result | at most every few minutes | — | yield, cause, activity span |

Retention: daily points `retentionDays`; samples the whole current day and at least 4 hours.

Rules: a computation **reads** state and never writes it; the snapshot sent to the renderer is not persisted; the renderer reads values and never recomputes them (it only formats).

---

## 8. Regression cases (the issues that led here)

| Issue | What went wrong | Case | Expected |
|---|---|---|---|
| #64 | today's budget/usage compared values at different precisions; a provider's integer fallback read as a reset | B-2, B-3, B-4 | used_today 0; 0; 3.5 |
| #64 | the rating tip stated a ratio different from the tooltip | R-1 + TP | the tip's `avgRatio` is the rating's `avgRatio` (1.26) |
| #72 | the gauge target reached 331 %/h near the reset (remaining quota over a handful of working hours) | **G-12**, E2 | T = 0.57 (the even share), not 331; C = 331 is the ceiling only |
| #73 | the advice asked for "a pace 100 % lower" on an exhausted quota | **TP-2**, TP-3, E3 | no tip |
| HYP#78 | a rolling 7-day window was shifted to midnight: the first hour counted as a whole day and the last 23 hours were dropped | **T-15**: Wed 14/10 23:00 → Wed 21/10 23:00 local; now Sat 17/10 18:38, schedule disabled | elapsed **2.82** days, remaining **4.18**, replacing 3.78 / 3.22; full precision 4058/1440 and 6022/1440 |
| #67 | efficiency index and stars replaced shortly after being introduced | R-1…R-6, P-6 | redefined by #67 against this table |

---

## 9. Known deviations of the current code (input of #75)

Found while writing the model; each becomes a failing case and an item of the refactoring. **No fix before #75** (see the issue's rule).

1. **Target = ceiling** (`sustainableHourlyRate` returns `C`): G-7 gives 0.87 instead of 0.57, G-12 gives 331 instead of 0.57. Only the calendar fallback prevents the zero-hours case.
2. **Target for an unknown period start** is the calendar ceiling (one-off credits); the model has none (I-6).
3. **`autonomy` tip** has no bounds on the reduction: TP-2 shows 100 %, TP-4 0 %. It is computed from the *rounded* autonomy and working days (TP-1: 8.4 / 8.6), not the exact ones (I-4).
4. **Verdict ratio** is computed from `perUnit` and `idealPerUnit` already rounded to 0.01 (I-4): `ρ` at the 0.95/1.05/0.5 edges can flip. The verdict compares `proj > 100` on the value rounded to 0.1.
5. **Daily point written on every refresh, also stale** (the lone `fresh` flag is applied to samples only): the model has one rule, fresh only (I-12).
6. **Reset passed**: `sustainableHourlyRate` answers `0` instead of "undefined"; the gauge then reads as "always over".
7. **Rolling window without `resetsAt`** falls back to the manual renewal rule as period end; meaningless for a rolling window (E6).
8. **Rolling-days bounds truncated to local days** (`startOfDay`): HYP#78 counts 3.78 elapsed / 3.22 remaining instead of 2.82 / 4.18. Confirmed model (§1-2): keep exact rolling timestamps and weight each local day's overlap by its scheduled unit, divided by 24 hours; billing cycles keep whole local days. Implemented in the Go `quotaperiod` package (HYP#161); TypeScript integration remains part of HYP#75.
9. ~~**`resolveRenewalDate` with day 29-31** overflowed into the following month~~ — fixed (#111, N-6..N-9).
10. **`budget.ts` mixes** day-based pacing, hour-based gauge, history bookkeeping, chart, insights and tips in one file; the model suggests the split used by this document (§2 time, §3.2-3.8 pacing, §3.6/3.9 hourly, §3.11-3.12 history, §3.14-3.15 advice).

---

## 10. Decisions to confirm in review

- **D1** `T = min(C, s_h)` (the even hourly share caps the target). Alternative: `T = C` with a floor on the hours left. The cap is the only rule that satisfies I-2 for any `H`.
- **D2** No target when the period start is unknown (one-off credits lose the target they have today).
- **D3** An exhausted quota shows no tip at all, and the projection stays uncapped.
- **D4** Daily points are written from fresh data only.
- **D5** Reset passed: forward-looking values hidden until a fresh fetch.
- **D6** Rating thresholds and the 3× cap, the ±5 % band, 0.5×, 1.5× pace ratio and the 2-unit preliminary limit stay as they are (tuning is #28, after real use).
