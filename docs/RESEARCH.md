🌐 **English** | [Italiano](RESEARCH.it.md)

# RESEARCH.md — API research session (Day 1)

> v3 — primary use case corrected: the app's user is **the individual developer**, not an IT admin. They want to monitor **their own** periodic (monthly/weekly) Claude and/or Copilot credit — whether a personal plan or a seat paid by their company — to understand whether they are using it efficiently before it runs out. "Admin monitors the team" scenarios (Claude Admin API, Copilot org/enterprise Metrics API) are excluded: they are outside the project's interest.

---

## Short answer

| Service | Can the (non-admin) developer monitor their own consumption alone? | How |
|---|---|---|
| **Claude** | **Yes**, with both a personal plan and a Team/Enterprise company seat | Internal endpoint `claude.ai/api/organizations/{orgId}/usage` with their own session cookie, or local reading when using Claude Code |
| **GitHub Copilot** | **Yes with a personal plan** · **Only with an unofficial workaround if the seat is assigned by the company** | Personal plan: official `premium_request/usage` endpoint. Company seat: no public self-service endpoint; the only real source is the undocumented internal endpoint `copilot_internal/user` (the same used by the VS Code quota indicator) |
| **OpenAI / Codex** | **Yes**, for the ChatGPT account (Plus verified) | Internal endpoint `chatgpt.com/backend-api/wham/usage` with the access token of the user's own chatgpt.com web session (embedded login, Google included). No Codex CLI needed |

Details and sources below.

---

## 1. Claude — developer self-tracking (CONFIRMED)

A member — **even without an admin role** — of a seat-based Team/Enterprise plan sees their own usage bar on claude.ai under **Settings → Usage**: the 5-hour limit, the "all models" weekly limit and the Opus weekly limit, each with its reset date. Official sources: [Team plan FAQ](https://support.claude.com/en/articles/9266767-what-is-the-team-plan), [How usage and length limits work](https://support.claude.com/en/articles/11647753-how-do-usage-and-length-limits-work).

Behind that bar there is an undocumented internal endpoint:

```
GET https://claude.ai/api/organizations/{organization_id}/usage
```

Reachable with **only the developer's own session cookie (`sessionKey`)** — no admin permission needed, the endpoint returns the caller's personal data. It answers with `five_hour`, `seven_day`, `seven_day_sonnet`, `seven_day_opus`, each with `utilization` and a reset date. Confirmed by the code of two third-party tools implementing it: [linuxlewis/claude-usage (SPEC.md)](https://github.com/linuxlewis/claude-usage/blob/main/SPEC.md) and [steipete/CodexBar](https://github.com/steipete/CodexBar/blob/main/docs/claude.md).

**Claude Code (CLI) with a company seat:** the local logs in `~/.claude/projects/**/*.jsonl` are written normally regardless of the licence type. Since version 2.1.92 Claude Code also exposes rate-limit data directly in the **statusline**, and a **`/usage`** command shows the current consumption — no scraping needed. Sources: [Claude Code — statusline docs](https://code.claude.com/docs/en/statusline), [Claude Code cost tracking](https://avinashsangle.com/blog/claude-code-cost-tracking), [cc-friend/ccost](https://github.com/cc-friend/ccost).

**SSO:** it does not appear to change the mechanism. After an SSO login (SAML/OIDC via WorkOS, [official guide](https://support.claude.com/en/articles/13132885-set-up-single-sign-on-sso)) the user still lands in a normal browser session on claude.ai, so the `sessionKey` cookie is issued the same way (TTL ~30 days). A reasonable but not 100% documented assumption — some enterprise MDM policies might enforce shorter sessions; to be verified in the field with a real account.

**In practice for the app:** the user does a "real" login in an embedded browser window (Electron `BrowserWindow`, with password/Google or SSO), the `sessionKey` cookie is captured, and the internal endpoint is polled periodically. Alternatively/additionally, if the developer uses Claude Code, `/usage` or the local JSONL files can be read without network.

**Addendum (Day 3 — verified with a real account): claude.ai is behind Cloudflare.** The `sessionKey` cookie alone is not enough to get past Cloudflare's bot management from a "bare" HTTP request (no browser engine): without the `cf_clearance` cookie too (obtained from the same real browser session used for the login) and a plausible User-Agent, the endpoint answers `403` with the "Just a moment..." interstitial page instead of the expected JSON. Implemented fix: `main/claude-auth.ts` rebuilds the full Cookie header by reading at runtime every cookie of the Electron session for the claude.ai domain (not only `sessionKey`), and `services/claude.ts` also sends a desktop browser User-Agent. Known limit not solved yet: if `cf_clearance` expires and Cloudflare requires a new interactive JS check, the login must be redone (it reopens a real `BrowserWindow` and passes the check again) — there is no automatic background refresh yet.

**Addendum 2 (Day 3 — verified with a real account): the field names of the usage response are not stable.** After getting past Cloudflare, the real response received from a connected account NO longer had `five_hour`/`seven_day`/`seven_day_opus` filled in (all present but `null`, together with other historical names such as `seven_day_sonnet`/`seven_day_oauth_apps`/`seven_day_cowork`/`seven_day_omelette`, also `null`), but keys with arbitrary undocumented names — observed in practice: `cinder_cove`, `omelette_promotional`, `tangelo`, `iguana_necktie`, `nimbus_quill`. Real example (real values of the response, NOT the ones shown in an automatic report — see below):

```json
{
  "five_hour": null, "seven_day": null, "seven_day_opus": null,
  "omelette_promotional": { "utilization": 0, "resets_at": null, "limit_dollars": null, "used_dollars": null, "remaining_dollars": null },
  "cinder_cove": { "utilization": 39.52, "resets_at": "2026-09-13T14:38:47Z", "limit_dollars": 1000, "used_dollars": 395.17, "remaining_dollars": 604.83 }
}
```

Most likely hypothesis: intentional obfuscation of the field names on Anthropic's side (non-semantic code names, e.g. fruit/place names), perhaps to discourage scraping by third-party tools like those above — not an occasional rename, since none of the documented historical names survived. The names may well rotate again in the future.

**Fix implemented in `services/claude.ts` (`buildQuotaWindows`):** fields are no longer read by fixed name but by **value shape** — any key whose value has a numeric `utilization` is treated as a valid quota window, whatever its name. If the window also exposes numeric `limit_dollars`/`used_dollars` (like `cinder_cove` above — likely a pay-as-you-go extra credit in dollars), it is modelled as `unit: 'count'` with the real amounts instead of a plain percentage, using the existing multi-window model (see ARCHITECTURE.md §0). 0% windows without a reset date and without amounts (like `omelette_promotional` above) are dropped because they cannot be told apart from a field not applicable to the account's plan. This approach survives a further rotation of the names, as long as the JSON shape (a numeric `utilization` per window) stays the same — if that changes too, the automatic format-drift report kicks in anyway (see CLAUDE.md).

**Addendum 3 (2026-10-01 — company account, issue #6): a new `spend` object.** On a company (Team/Enterprise seat) account every window was `null`, including `extra_usage.utilization`, so no window was recognized and the app reported a format drift. The data moved to two new top-level fields: `spend` (`enabled`, `percent`, `severity`, `used`/`limit` as `{ amount_minor, currency, exponent }`, `cap.credits`, `disclaimer`, …) and `limits` (an array, empty on that account — its element shape is unknown). **Fix:** `buildQuotaWindows` reads `spend` as a "Spend limit" window: real amounts (`amount_minor / 10^exponent`) when `used`/`limit` are well-formed, otherwise `percent`; `enabled: false` is a recognized state with no window. It is added only when no dollar window already exists, because on personal accounts it reports the same extra-usage money. `limits` is not parsed until a populated example is seen; personal accounts were unaffected at the time.

**Addendum 4 (2026-10-02 — same company account): `extra_usage` is the same budget as `spend`.** From the afternoon of 2026-10-01 `extra_usage` is populated: `utilization` (unrounded), `monthly_limit` and `used_credits` in minor units with `decimal_places` (2 = cents), `currency`, `is_enabled`, `spend_limit_reached`, `user_disabled`, `disabled_reason`, `credits_ever_enabled`, plus `daily` and `weekly` (null so far — possibly future daily/weekly caps, shape unknown). A real report (values not reproduced here) showed the same used amount and the same limit as `spend.used`/`spend.limit`, with `spend.percent` rounded to an integer. **Fix:** `buildQuotaWindows` reads `monthly_limit`/`used_credits`/`decimal_places` as a monthly dollar window (`periodLength: 1`, paced, label *Extra usage (monthly)*); when it carries the same amounts as `spend` only `spend` is kept (its history started first). Should they ever differ, both are shown. The older dollar shape (`limit_dollars`/`used_dollars`, personal accounts, no period) keeps the previous rule: `spend` is not added. The manual "report usage responses" (Settings → Diagnostics) is how this was observed.

**Addendum 5 (2026-10-02 — personal account, diagnostic report): `limits` and `seven_day_breakdown`.** The `limits` array, empty before, is populated: elements `{ kind, percent, resets_at, is_active, severity, group, scope }`, with `kind: "session"` and `"weekly_all"` carrying the same values as `five_hour` and `seven_day` (`is_active` marks the limit currently binding). **Fix:** `buildQuotaWindows` reads `limits` only as a fallback when no named window is recognized, mapping the known kinds to the same window ids (history continues); unknown kinds get their own id and no pacing. A new `seven_day_breakdown` object (`as_of`, `rows: [{ display_name, key, percent }]`) splits the weekly limit by model — not read yet, a candidate for a future view. Copilot `copilot_internal/user` on a company seat: `chat` and `completions` snapshots have `unlimited: true` (no quota to pace), `premium_interactions` has a real `entitlement`.

**Addendum 6 (2026-10-09 — Claude Free account, issue #44): Free tier returns all window fields as null.** When the subscription expires and the account falls back to the Free tier, the `/usage` endpoint returns all obfuscated window keys as `null`, `limits: []` (empty), and `member_dashboard_available: false`. Observed field list at that time: `amber_cistern`, `amber_gauge`, `amber_ladder`, `brass_thimble`, `cedar_ember`, `cinder_cove`, `copper_kite`, `extra_usage`, `five_hour`, `harbor_lantern`, `iguana_necktie`, `juniper_tide`, `nimbus_quill`, `omelette_promotional`, `seven_day`, `seven_day_breakdown`, `seven_day_cowork`, `seven_day_oauth_apps`, `seven_day_omelette`, `seven_day_opus`, `seven_day_sonnet`, `spend`, `tangelo`, `wattle_ember` — all `null`; plus `limits: []`, `member_dashboard_available: false`, `weekly_scoped_shares: null`. **Fix:** `buildQuotaWindows` checks `member_dashboard_available === false` before throwing a `FormatDriftError`; when it is `false` no quota window is available (Free tier has no usage bar in the product) and an empty array is returned — a known state, not a format drift.

---

## 2. GitHub Copilot — developer self-tracking

### 2.1 Personal plan (Free/Individual/Pro+) — CONFIRMED, official route

Official, documented endpoints, callable by the developer with their own fine-grained PAT ("Plan" permission, read) or classic PAT:

- `GET /users/{username}/settings/billing/premium_request/usage`
- `GET /users/{username}/settings/billing/ai_credit/usage`

Source: [REST API — Billing usage](https://docs.github.com/en/rest/billing/usage). Fully self-service and automatable.

**Addendum (2026-07-24 — switch to `ai_credit/usage`).** Implemented in `services/copilot.ts` the switch from `premium_request/usage` (used by the first implementation) to `ai_credit/usage`, the second option already listed above but not chosen at the time. Reason: GitHub retired the "premium requests" model on June 1st, 2026 (see the addendum in §2.2) — `premium_request/usage` is not formally deprecated (same response shape, per the REST docs), but it most likely no longer reflects real consumption for accounts already migrated to the new system, so it risks always returning 0. Real response schema (different from what the first, never-verified implementation assumed — see CLAUDE.md): a single `timePeriod` for the **whole report** (not a date per `usageItem`: the monthly filter happens server side through the `year`/`month` query parameters), and every `usageItem` exposes `netAmount` (net amount in USD after discounts), not a plain `quantity`. Conversion adopted in `sumCreditsUsed()`: sum of `netAmount` over all report items, then USD → AI credits (1 credit = $0.01, see [GitHub Changelog, 2026-06-19](https://github.blog/changelog/2026-06-19-ai-credits-consumed-per-user-now-in-the-copilot-usage-metrics-api/)). **Not verified with a real account yet** — to confirm with `npm test` + a real "Plan" (read-only) PAT locally.

**Addendum (2026-07-30 — first real test: `ai_credit/usage` answers 404).** With a real account (the same one used for the §2.2 tests, where Copilot consumption is handled by a company seat, not a personally purchased plan), `GET /users/{username}/settings/billing/ai_credit/usage` answered `404 Not Found` (`{"message":"Not Found","documentation_url":".../billing-ai-credit-usage-report-for-a-user","status":"404"}`). Consistent with the REST docs, which state that the user endpoints "apply only if the user has purchased their own Copilot plan" — when Copilot access comes from a seat assigned by the organization, the personal endpoint may legitimately have nothing to return for that username, whichever of the two paths (`ai_credit`/`premium_request`) is called. It is therefore unclear whether the 404 is due to this (likely) or to an incomplete rollout of the endpoint. **Mitigation implemented** in `services/copilot.ts` (`fetchBillingUsageReport`): a 404 on `ai_credit/usage` automatically falls back to `premium_request/usage` (same response shape according to the docs, no parsing difference) before giving up — other errors (network, 401/403) do not trigger the fallback, so a credentials problem is not hidden. Still to verify with an account that really has a **personal** Copilot plan (Free/Pro/Pro+) whether `ai_credit/usage` answers 404 there too (endpoint not available yet) or 200 (confirming that the observed 404 was due only to the company seat).

**Addendum (later — personal Free account: both billing endpoints answer 404).** As recorded in `services/copilot.ts`: with a real personal Free account both `ai_credit/usage` and `premium_request/usage` answered 404, although the github.com/settings/billing page of the same account shows real "Included credits" consumption — these official REST endpoints evidently do not cover that kind of plan. The service now makes a last attempt on the internal `copilot_internal/user` endpoint (§2.2), which powers the VS Code quota indicator for any Copilot account, treated as best-effort.

### 2.2 Seat assigned by the company (Business/Enterprise) — NO public self-service endpoint (CONFIRMED)

This is the critical point, verified with multiple sources:

- The same user endpoints (`premium_request/usage`, `ai_credit/usage`) **return only the consumption billed to the personal plan** — when the licence is managed/billed by the organization, the developer's usage **does not appear**. Sources: [REST API — Billing usage](https://docs.github.com/en/rest/billing/usage), [Copilot user management](https://docs.github.com/en/rest/copilot/copilot-user-management).
- The org endpoint (`/orgs/{org}/copilot/...`) with the `?user=` filter answers **403 "cannot filter usage by user"** for enterprise-owned orgs.
- The enterprise endpoint requires a classic PAT with the **`admin:enterprise`** scope — reserved to admins.
- More confirmations from the community: [navikt/copilot issue #111](https://github.com/navikt/copilot/issues/111), [GitHub Community Discussion #184208](https://github.com/orgs/community/discussions/184208).

**The only real source of the data** in this scenario: the quota indicator shown by the Copilot Chat extension in VS Code. That indicator reads from an **undocumented internal endpoint**:

```
GET https://api.github.com/copilot_internal/user
```

which returns `quota_snapshots` with `premium_interactions`, `chat`, `completions`, `quota_reset_date`, `copilot_plan` — exactly the data the app needs (consumption, quota, renewal date), and it also works for seats assigned by an organization because it is what powers the IDE itself. Source: [GitHub Community Discussion #178117](https://github.com/orgs/community/discussions/178117). It is technically reproducible with the developer's Copilot token, but it is **undocumented, unversioned, and its use outside official clients is in a grey area with respect to the Copilot Terms of Service** — to be treated as a best-effort workaround, not a stable integration.

**Hypothesis disproved (2026-07-23) — the token type makes no difference.** Analysing an old prototype of the user's (`copilot_hypermiler`, never run successfully — no data saved in the checkout analysed), it turned out to call the same `copilot_internal/user` but with a **GitHub OAuth App token** (Authorization Code + PKCE, `github.com/login/oauth/authorize` + `/access_token`, `read:user` scope) instead of a Personal Access Token, with the hypothesis that the endpoint answered with a full `quota_snapshots` only to that kind of token. Implemented in `main/copilot-oauth.ts` and tested with a real company seat: **exactly the same response** as with the PAT (no `quota_snapshots`, only feature/plan/org flags). Confirmed not only by the error message but by the structure signature (`shapeSignature()` in `services/_shape.ts`, computed on field names + types): the automatic report correctly **did not reopen** a second issue draft because the signature matched exactly the one already reported for the PAT — proof that both tokens receive a structurally identical response. The hypothesis is therefore closed: the credential type (classic/fine-grained PAT, OAuth App) does not affect the `copilot_internal/user` response; the endpoint no longer exposes quota data for company seats, period, however one authenticates.

### 2.3 Verified community projects — they confirm the same limitation

- **[Fail-Safe/CopilotPremiumUsageMonitor](https://github.com/Fail-Safe/CopilotPremiumUsageMonitor)**: the "Personal spend" mode uses the personal Enhanced Billing endpoint with a "Plan" (read-only) PAT → on an org-managed seat it returns empty/404, the same limitation as §2.2. The "Org" mode requires `read:org` and gives only aggregate metrics (active users, suggestions), not premium requests per user.
- The other known trackers (copilot-usage-tracker, m-marqx Copilot Premium Request Tracker) use the same billing endpoints and share the same limitation.

No community project solves the company-seat case "cleanly"; those that manage (IDE indicators) do it through the internal endpoint of §2.2.

**Addendum (2026-07-24 — two more leads explored at the user's request, both closed, neither unlocks the company seat):**
- *Viva Insights Copilot Dashboard export* ([Microsoft Learn](https://learn.microsoft.com/en-us/viva/insights/org-team-insights/export-copilot-metrics)): requires "global" access to the Copilot Dashboard (auto-detected senior leader, admin delegation, Viva Insights Analyst or M365 Global Admin) — the docs say explicitly that "users with non-global access... can't access this feature": no self-service path for a non-admin developer. Even with that access, the exported data are *adoption* metrics (prompts/actions/active days, per user but with anonymized IDs), not AI Credits consumption/remaining quota — a different data domain from what we need.
- *Copilot Usage Metrics API — extension to Copilot app activity* ([GitHub Changelog, 2026-07-28](https://github.blog/changelog/2026-07-28-github-copilot-app-usage-metrics-now-expand-across-report-rollups/)): the same API already known from §2.2/§3 (`ai_credits_used`), still reserved to enterprise/organization owners or the "View Copilot Metrics" custom role. The novelty only adds Copilot app activity (sessions/requests/prompts/tokens) to the existing rollups — it does not change the permission model nor add a remaining-quota figure for a single non-admin user.

**Addendum (research 2026-07-23): why `quota_snapshots` disappeared — GitHub changed its billing model, not just a format.** On June 1st, 2026 GitHub moved from "premium requests" (the model `quota_snapshots` was built on) to a new token-consumption-based **"AI Credits"** system ([GitHub Blog](https://github.blog/news-insights/company-news/github-copilot-is-moving-to-usage-based-billing/)). So it is not an ordinary drift: the underlying quota model was retired at product level. The "Credits" panel shown by VS Code (as in the user's screenshot) is the new "Copilot spend meter" introduced in VS Code 1.125 (June 17-22, 2026). **Decisive point, verified**: Microsoft does not expose this data via API either — there is an open, unresolved issue on `microsoft/vscode` asking exactly to "Expose credit usage... programmatically" ([microsoft/vscode#319571](https://github.com/microsoft/vscode/issues/319571)). The only official API with AI Credits consumption per user (`ai_credits_used`, added on 2026-06-19 to the Copilot Usage Metrics API) requires **organization/enterprise administrator** permissions ([GitHub Changelog](https://github.blog/changelog/2026-06-19-ai-credits-consumed-per-user-now-in-the-copilot-usage-metrics-api/)) — out of this project's scope (see the v3 note at the top of the file: "admin monitors the team" scenarios... excluded). Even up-to-date third-party trackers (e.g. `steipete/CodexBar`) list this as explicitly open/unsolved work. **Conclusion**: for a company seat there is, today, no known self-service route — neither ours nor anyone else's — for a non-admin developer. The *personal* route (§2.1, `premium_request/usage`/`ai_credit/usage`) remains valid: the limitation concerns only organization/enterprise-managed seats.

**Addendum (2026-07-23 — verified with a real account, company seat): `copilot_internal/user` no longer returns `quota_snapshots`.** First real connection of a Copilot account with a company seat (`accountScope: organization`): the response contained no quota field at all (no `quota_snapshots`, no `premium_interactions`/`chat`/`completions`, no reset date), only feature flags (`chat_enabled`, `cli_enabled`, `copilot_app_enabled`, etc.), `copilot_plan`, `login`, `organization_list`/`organization_login_list` and internal `endpoints`. Full structure (no real value) reported by the automatic report — see CLAUDE.md, "Diagnostics: automatic format-drift report". This confirms in the field the risk already flagged in §2.2 ("may break without notice"): the endpoint is no longer a usable quota source, at least for this account/moment. No fix attempted in `services/copilot.ts`: there is nothing to extract from the new response (zero fields related to percentage/consumption), so no parser can be written without inventing an undocumented format (forbidden by CLAUDE.md). The app behaves as designed: `FormatDriftError` → no crash, `emptyAccountSnapshot` with `lastError` shown in the widget instead of invented data. If GitHub ever exposes an official endpoint for per-seat consumption, it should be integrated there; as of now monitoring a company seat has no reliable self-service solution (see also §3).

**Addendum (2026-10-01 — token-based billing snapshots are back, but not for enterprise-managed seats; issue #7).** Prompted by VS Code 1.132 showing "AI credits used" without a budget ([microsoft/vscode#319589](https://github.com/microsoft/vscode/issues/319589)). VS Code's open source code (MIT) reads the credits from **this same endpoint** (`product.json` → `entitlementUrl: https://api.github.com/copilot_internal/user`, plain `Authorization: Bearer`), with snapshot fields `credits_used`, `entitlement`, `quota_remaining`, `unlimited`, `has_quota`, `quota_reset_at` (epoch seconds) and root fields `token_based_billing`, `quota_reset_date_utc` (`src/vs/base/common/defaultAccount.ts`, `chatEntitlementService.ts` → `getQuotaUsage`). Probed with `scripts/probe-copilot-shape.js` (structure only):
- **personal account, OAuth token (GitHub CLI):** HTTP 200 with full `quota_snapshots` (`chat`, `completions`, `premium_interactions`) including `credits_used`;
- **company account, OAuth token:** HTTP 200 with `access_type_sku: "enterprise_managed"`, `assigned_date: null`, empty `organization_list`/`enterprise_list` and **no** `quota_snapshots`/`token_based_billing`/`quota_reset_date` — the same as with a PAT in July. VS Code itself has nothing to show for such an account. The only source remains the admin-only Usage Metrics API.
- **company account, PAT, personal billing endpoint:** `ai_credit/usage` now answers **400** `"Unable to get billing usage data."` instead of 404.

**Implemented in `services/copilot.ts`:** the 400 with that message is treated like the 404 (move to the next source, other 400s stay errors); snapshots are read with VS Code's rules (unlimited → credits used without total; `entitlement > 0` → used = entitlement − quota_remaining out of entitlement; `entitlement: 0` or unlimited without credits → nothing to show, not a drift; otherwise the percentage); an `enterprise_managed` response without snapshots throws `CopilotUsageUnavailableError`, shown as a translated explanation instead of a format-drift issue draft. Still to verify: whether a PAT (rather than an OAuth token) also gets the snapshots on a personal account. **Also checked the same day:** the github.com "Copilot usage" settings page announced on 2026-07-20 shows no consumption for this enterprise-managed account either, and the admin-only Usage Metrics API is not an option for the user. Conclusion: for an enterprise-managed seat there is **no source at all** today, for the app or for the user. If GitHub starts sending `quota_snapshots` for these seats, the current parser picks them up with no code change.

**Addendum (2026-10-01, later the same day — the company seat lives on a `.ghe.com` tenant).** The dead end above was the wrong identity: the user's company uses **GitHub Enterprise Cloud with data residency**, on its own domain `<tenant>.ghe.com`. The real Copilot seat belongs to the account on that domain (API on `api.<tenant>.ghe.com`); the github.com account linked to the enterprise has no seat, hence `enterprise_managed` without data. Observed by the user in VS Code's DevTools: `GET https://api.<tenant>.ghe.com/copilot_internal/user` with VS Code's OAuth token answers with `access_type_sku: "copilot_for_business_seat_quota"`, `copilot_plan: "business"`, `token_based_billing: true` and full `quota_snapshots` — `chat`/`completions` unlimited with `credits_used`, `premium_interactions` with a credit `entitlement`, `quota_remaining`, `credits_used`; `quota_reset_at` is 0, the reset comes from `quota_reset_date`. VS Code derives the endpoints the same way (`githubEndpoints.ts`: `*.ghe.com` → `api.<host>`). **Implemented:** a "GitHub domain" per Copilot account (`services/githubHost.ts`: only `github.com` or `<tenant>.ghe.com`, because the token is sent there), used for `/user`, billing, `copilot_internal/user` and OAuth (`<host>/login/oauth`); the company scope no longer locks the account disabled; unlimited categories with zero credits are not shown. **Verified the same day with a PAT created on the tenant:** same response (HTTP 200, full `quota_snapshots`, `token_based_billing: true`) — a PAT is enough, no OAuth App needed. **Personal Free account, in the app (same day):** connecting with OAuth shows the quotas (`premium_interactions`, `chat`, `completions`) — the OAuth sign-in is no longer labelled experimental. Real Free-plan values: `chat` 200 and `completions` 2000 with their own `entitlement`/`quota_remaining`, `premium_interactions` with `entitlement: 0`, `has_quota: false`, `percent_remaining: 0` = **no premium allotment**, not "100% used" (versions up to 0.4.2 showed 100%; since 0.4.3 it is skipped, as in VS Code). A fine-grained PAT with "Plan" gets the billing report instead (spend in dollars, net of the included allotment). **Since v0.4.5 the personal scope tries `copilot_internal/user` first** (real quotas) and uses the billing report only as a fallback.

---

## 3. Implications for the app

For an MVP aimed at the individual developer, the most solid architecture is:

1. **Claude:** embedded browser login (works for personal plans and company seats, with or without SSO) → capture `sessionKey` → poll `claude.ai/api/organizations/{orgId}/usage`. If the developer also/only uses Claude Code, add reading `/usage` or the local JSONL files as an extra source (or a zero-risk alternative).
2. **Copilot:**
   - Developer with a **personal** plan: official fine-grained PAT on `premium_request/usage` — a solid, headless, low-risk route.
   - Developer with a **company seat**: no official route. Options to present as such to the user: (a) read the quota indicator in VS Code by hand (no integration, just an on-screen instruction), or (b) call the internal `copilot_internal/user` endpoint with the developer's Copilot token, clearly labelled in the UI as an **experimental/best-effort** feature, with a warning that it may break without notice.

For the configuration model, it is still necessary to ask the user, for every connected account: the login method (classic or SSO — it only determines the flow of the session-capture browser window, not the availability of the data), whether the plan is personal or assigned by the company (it determines which Copilot endpoint to use, and whether the user must be warned that Copilot data will be "best-effort"), the plan type (to compute/validate the total quota), and — where the API gives no reliable reset date — the renewal date entered by hand.

---

## 4. Architectural recommendation

1. **Claude** (personal or company, with or without SSO): primary route through the internal endpoint + sessionKey, secondary/complementary route through local Claude Code. Good reliability, low friction for the user (one-off login in an embedded window).
2. **Personal Copilot:** official route, high priority, to implement as the first "solid" service.
3. **Copilot with a company seat:** experimental feature through the undocumented internal endpoint — implement it but flag the risk clearly in the UI (may change/break without notice, not supported by GitHub).

Out of scope, by explicit choice: any team/organization-level monitoring feature (Claude Admin API, Copilot org/enterprise Metrics/Billing API) — the app stays a tool for the individual developer about their own consumption, not an IT/admin panel.

---

## 5. Local data sources for behavioural insights (Addendum 2026-08-18)

Prompt: an "Account & Usage" panel in VS Code (Claude Code extension) shows, besides the quota percentage, behavioural insights — "87% of your usage was at >150k context", "22% from sessions active 8+ hours", a breakdown by MCP server — with the explicit caption *"Approximate, based on local sessions on this machine — does not include other devices or claude.ai"*. Confirmed: **it is not account data retrievable through the endpoint we already use** (`services/claude.ts`, `/organizations/{id}/usage` — only `utilization`/`resets_at`/amounts per window, no per-session field: verified by the absence of these fields both in the parsing and in this file). It is local telemetry computed by the extension from its own session transcripts saved on disk.

### 5.1 Claude Code (CLI + VS Code extension) — CONFIRMED, same source

- **Path**: `~/.claude/projects/<cwd-with-non-alphanumeric-characters-replaced-by-dashes>/<session-id>.jsonl`. Verified empirically on this machine — one JSONL file per session, one JSON line per event (user message, assistant answer, tool call).
- **The VS Code extension uses the same engine/format as the CLI**, not a separate path: confirmed both empirically (this working session, conducted inside VS Code, writes exactly to that path) and by external sources. For the developer, "Claude CLI" and "Claude Code in VS Code" are therefore **a single source**, not two entries in a list of configurable sources.
- **Useful content, inspected ONLY for structure/keys (never real message content)**: every `assistant` entry has `message.usage` with `input_tokens`, `cache_creation_input_tokens`, `cache_read_input_tokens`, `output_tokens`, `output_tokens_details.thinking_tokens`, `server_tool_use` (`web_search_requests`/`web_fetch_requests` counts), besides `message.model`, `timestamp`, `sessionId`, `cwd`, `gitBranch`. The sum `cache_read_input_tokens + cache_creation_input_tokens + input_tokens` is the direct base for a "context size per turn" indicator equivalent to VS Code's. `content` blocks of type `tool_use` have a `name` field (name of the invoked tool/MCP server) readable without looking at the call parameters.
- **Undocumented format, unstable across versions**: multiple sources (official Claude Code docs, third-party guides) explicitly warn that the internal layout changes release after release and recommend not parsing it directly from external scripts, preferring `/export` or the official "script interfaces". The storage path itself already changed once (February 2026). Same caution already applied to the internal endpoint of `services/claude.ts`: defensive parsing, never a crash, never an invented value when a field is missing.
- **Retention**: automatic deletion after 30 days by default (`cleanupPeriodDays`) — it limits how far back any indicator built on this source can look, regardless of the `history.retentionDays` (90 days) already used for the account-level history.
- **Recommended route — the official SDK instead of direct parsing**: there is an official TypeScript package, `@anthropic-ai/claude-agent-sdk`, with `listSessions({ dir, limit })` and `getSessionMessages(sessionId, { dir, limit })` — it abstracts file discovery/the exact on-disk layout, more resilient than parsing the JSONL directly. *(Later verified with a real call: the returned messages do include `usage`; the dependency was approved by the user and is used by `services/claudeLocalSessions.ts`.)*
- **JetBrains extension — a DIFFERENT source, out of scope for now**: it reads from `~/.claude/sessions/<project-hash>/` (`session.json` + `conversation.jsonl`), not from `~/.claude/projects/`. Noted for a possible future extension, not addressed now: the user explicitly chose to start from this IDE only (VS Code).

### 5.2 Claude Desktop — NOT CONFIRMED for "normal" use with a claude.ai account

- The official documentation found (`claude.com/docs/third-party/claude-desktop/data-storage`) explicitly describes the **"3P" (third-party/enterprise-managed)** variant: *"Claude Desktop on 3P has no Anthropic account. There is no sign-in step, no cloud-stored conversation history"* — a different deployment from the personal claude.ai account (pro/max/team/enterprise) the app already handles for Claude via `sessionKey`.
- In that 3P variant, Cowork/Chat/Code sessions are stored locally under `local-agent-mode-sessions/` (macOS: `~/Library/Application Support/Claude-3p/`, Windows: `%LOCALAPPDATA%\Claude-3p\`, moved from `%APPDATA%` in a recent update), with one `audit.jsonl` per session (tool invocations, permission decisions — chained via HMAC) but **no explicit mention of token/usage statistics** in that log.
- For "normal" Claude Desktop (standard claude.ai account, the relevant case for most of the app's users): Chat conversations live in the cloud account, synced across devices — the very reason for having an account. The documentation found shows no equivalent structured local log for this case.
- **Conclusion**: source not confirmed, unlike Claude Code (verified empirically on this machine). Not included in v1 of the feature; re-check only if really needed in the future, with a direct inspection on a machine that has Claude Desktop installed with a normal claude.ai account.

---

## 6. OpenAI / Codex — ChatGPT subscription quota without the Codex CLI (Addendum 2026-10-18, #82)

**Verdict: GO.** The usage of a ChatGPT/Codex subscription is readable with the same model as Claude: embedded login in a per-account Electron partition, then HTTP calls from the main process. Neither the Codex CLI nor an IDE extension is needed. Verified on 2026-10-18 with a real Plus account (Google login); only statuses and response structure were recorded.

### 6.1 Where the data comes from
The Codex CLI app-server method `account/rateLimits/read` (the first attempt in #17) is only a proxy: the CLI binary calls `GET https://chatgpt.com/backend-api/wham/usage` with the ChatGPT OAuth token of its own login. The same endpoint answers to the access token of an ordinary chatgpt.com **web session**.

Spike #142 (2026-10-27) ran `account/rateLimits/read` for real and sets it as the Codex source for the Go core, without the cookie: see [SPIKE-CODEX-APP-SERVER.md](SPIKE-CODEX-APP-SERVER.md).

### 6.2 Flow (verified)
1. Login window on `https://chatgpt.com/auth/login` in a dedicated partition, **stock Electron User-Agent**. Google login works. Rewriting the User-Agent (removing `Electron/x`) makes Google refuse it ("This browser or app may not be secure"): a User-Agent that contradicts the client hints is exactly what Google flags. Never touch it.
   After login chatgpt.com lands on the chat: the window must close on the first navigation back to `chatgpt.com` where the session check succeeds (as `main/claude-auth.ts` does), not on a timer.
2. Login done when `GET https://chatgpt.com/api/auth/session` returns JSON with `accessToken` (a JWT). The session itself lives in the `__Secure-next-auth.session-token.*` cookies (about 90 days); the access token lasts about 10 days, so it is read again from `/api/auth/session` on each refresh and never stored.
3. `GET https://chatgpt.com/backend-api/wham/usage` with `Authorization: Bearer <accessToken>` → 200 JSON.

Results of the probe (one variable at a time):

| Request | Result |
|---|---|
| From the page, Bearer + `ChatGPT-Account-Id` | 200 |
| From the page, Bearer only | 200 |
| From the page, cookies only, no Bearer | 401 `{detail}` |
| From the main process (`session.fetch` on the partition, no window open), `/api/auth/session` then `wham/usage` | 200 and 200 |
| From the main process with Node `fetch` (second probe, #86): `/api/auth/session` with the partition's Cookie header + its User-Agent | 200, `accessToken` present, no `cf-mitigated` |
| Node `fetch`, `wham/usage` with Bearer (partition User-Agent, with or without cookies; also Node's default User-Agent) | 200 |

So cookies alone are not enough, the Bearer is. `ChatGPT-Account-Id` is not needed on a personal account; it is probably needed to pick the workspace on Team/Enterprise (the JWT carries `chatgpt_account_id` under `https://api.openai.com/auth`). Cloudflare cookies (`cf_clearance`, `__cf_bm`) are set by the login and the partition's `session.fetch` sends them: no challenge seen.

**Implementation choice (#86):** the same path as Claude works: Node `fetch` through `services/_http.ts`, with the Cookie header rebuilt from the partition and the partition's User-Agent for `/api/auth/session`, then the Bearer alone for `wham/usage`. `session.fetch` stays the fallback should Cloudflare start refusing Node requests.

The token belongs to the chatgpt.com web client (its own `client_id`): no other client is impersonated. Token-sharing tokens from *Sign in with ChatGPT* are refused by this endpoint (401 `rejected_by_access_enforcement`, openai/codex#54219), which is irrelevant here.

### 6.3 Response structure (Plus, 2026-10-18)
Top level: `user_id`, `account_id`, `email`, `plan_type`, `rate_limit`, `code_review_rate_limit`, `additional_rate_limits`, `model_usage`, `credits`, `spend_control`, `rate_limit_reached_type`, `promo`, `rate_limit_reset_credits`.

- `rate_limit`: `allowed`, `limit_reached`, `primary_window`, `secondary_window`; each window has `used_percent` (consumed, 0-100), `limit_window_seconds`, `reset_after_seconds`, `reset_at` (Unix seconds). Observed: primary 18000 s (5 h), secondary 604800 s (7 days). Third parties report a weekly-only `primary_window` with `secondary_window: null`: the window is identified by `limit_window_seconds`, never by its field name.
- `additional_rate_limits` and `code_review_rate_limit`: `null` here; other plans/models may fill them (shape not observed yet → drift-safe handling).
- `credits`: `has_credits`, `unlimited`, `overage_limit_reached`, `balance` (**string**), `approx_local_messages`, `approx_cloud_messages` (arrays of two numbers).
- `model_usage`: keyed by model name, `available`, `available_at`, `credits_would_enable`.
- `/api/auth/session` also returns `user`, `account` (`planType`, `structure`, …), `sessionToken` and a `WARNING_BANNER` field: none of it may ever be logged or sent to the renderer.

### 6.4 Not verified yet
Free, Pro, Team/Enterprise (workspace selection, `ChatGPT-Account-Id`), company SSO, `additional_rate_limits` filled, behaviour when the session cookie expires (expected: `/api/auth/session` without `accessToken` → "sign in again").

### 6.5 Risks
Undocumented internal endpoint, like Claude's: it can change without notice → `FormatDriftError` and the drift report, as for the other providers. Reading one's own usage with one's own session is the same pattern as Claude; no automation of the chat itself.

## Sources

**Claude / Anthropic**
- [Team plan FAQ](https://support.claude.com/en/articles/9266767-what-is-the-team-plan)
- [How usage and length limits work](https://support.claude.com/en/articles/11647753-how-do-usage-and-length-limits-work)
- [linuxlewis/claude-usage — SPEC.md](https://github.com/linuxlewis/claude-usage/blob/main/SPEC.md)
- [steipete/CodexBar — claude.md](https://github.com/steipete/CodexBar/blob/main/docs/claude.md)

**Local data sources (Addendum 2026-08-18, §5)**
- [Claude Desktop — User identity and local data](https://claude.com/docs/third-party/claude-desktop/data-storage)
- [Claude Code — Manage sessions](https://code.claude.com/docs/en/sessions)
- [Claude Code — Work with sessions (Agent SDK)](https://code.claude.com/docs/en/agent-sdk/sessions)
- [Agent SDK reference — TypeScript](https://platform.claude.com/docs/en/agent-sdk/typescript)
- [Claude Agent SDK — Building a session browser (cookbook)](https://platform.claude.com/cookbook/claude-agent-sdk-05-building-a-session-browser)
- [anthropics/claude-agent-sdk-typescript — Issue #165, listing past sessions](https://github.com/anthropics/claude-agent-sdk-typescript/issues/165)
- [Claude Code — statusline docs](https://code.claude.com/docs/en/statusline)
- [Claude Code cost tracking](https://avinashsangle.com/blog/claude-code-cost-tracking)
- [cc-friend/ccost](https://github.com/cc-friend/ccost)
- [Set up SSO](https://support.claude.com/en/articles/13132885-set-up-single-sign-on-sso)
- Additional community repos (personal plan, since v2): [lugia19/Claude-Usage-Extension](https://github.com/lugia19/Claude-Usage-Extension), [ryoppippi/ccusage](https://github.com/ryoppippi/ccusage), [phuryn/claude-usage](https://github.com/phuryn/claude-usage)

**GitHub Copilot**
- [REST API — Billing usage](https://docs.github.com/en/rest/billing/usage)
- [Copilot user management](https://docs.github.com/en/rest/copilot/copilot-user-management)
- [GitHub Community Discussion #184208](https://github.com/orgs/community/discussions/184208)
- [navikt/copilot issue #111](https://github.com/navikt/copilot/issues/111)
- [GitHub Community Discussion #178117 — copilot_internal/user](https://github.com/orgs/community/discussions/178117)
- [Fail-Safe/CopilotPremiumUsageMonitor](https://github.com/Fail-Safe/CopilotPremiumUsageMonitor)

**OpenAI / Codex (Addendum 2026-10-18, §6)**
- [openai/codex#54219 — token sharing cannot read plan usage](https://github.com/openai/codex/issues/54219)
- [steipete/CodexBar — codex.md](https://github.com/steipete/CodexBar/blob/main/docs/codex.md)
- [ajhochy/Rhythm#1568 — wham/usage response](https://github.com/ajhochy/Rhythm/issues/1568)
- [openai/codex#10869 — wham/usage polling](https://github.com/openai/codex/issues/10869)
