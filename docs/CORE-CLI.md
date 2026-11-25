🌐 **English** | [Italiano](CORE-CLI.it.md)

# Go core CLI

> Implemented in #26: transport-neutral JSON contract v1 and terminal output for layer 1 of #139. Codex adapter implemented in #148; Copilot adapter in #149; Claude adapter in #147 (#219-#221, Windows #223).

## Commands and scope

```text
aipermiler usage <provider> [--json] [--timeout <duration>] [--secret-stdin] [provider flags]
aipermiler token set <provider> [--json] [provider flags]      # token on stdin
aipermiler token delete <provider> [--json] [provider flags]
aipermiler login <provider> [--json] [--timeout 5m] [provider flags] [login flags]   # client secret on stdin
aipermiler raw <provider> [provider flags]
aipermiler version [--json]
aipermiler paths [--json]
aipermiler insights claude [--json] [--days N] [--timeout <duration>]
aipermiler serve
aipermiler service stop [--json] [--timeout 15s]
aipermiler help
```

The CLI commands fetch on demand and exit. The one exception is `serve` (see [Service](#service)): the always-on process of #31, which reads the accounts on its own refresh loop (#283) and serves the same usage documents over HTTP, with `refresh` and `raw` (#248), reads and writes the settings (#284), runs the Copilot account actions (#285), serves the pacing snapshot (#326) and the history for the diagnostic report (#339). #177 adds the TypeScript client. Claude (#147), Codex (#148) and Copilot (#149) are registered; `token`, `login` and `raw` are Copilot's (account commands below). An unknown provider such as `aipermiler usage nope` fails with exit 2, `Unknown provider: nope`. Tests register a synthetic provider named `fake`.

Put the provider before its flags. Unexpected positional arguments and unknown flags are usage errors.

| Common flag | Behavior |
|---|---|
| `--json` | Write one compact JSON document plus a single newline to stdout; stderr carries logs only. A standard boolean flag (`--json=false` means text, the last occurrence wins); for an invocation that fails before or during flag parsing it is recognized anywhere in the arguments, so the usage error is a JSON document too. |
| `--timeout <duration>` | Fetch deadline, default `2m` for `usage` and `25s` for `token` and `raw`; must be greater than zero and at most `2m`. Uses Go durations, e.g. `50ms`, `10s`. Providers must honor the context deadline. |
| `--secret-stdin` | Read stdin only if the provider accepts secrets. At most 64 KiB including a trailing newline; remove one final CRLF or LF, preserving other whitespace. Empty input, read failures and oversized input are usage errors. |

Secrets are accepted only on stdin, never as flags: argv is visible in the process list. Account parameters belong in provider flags, credentials do not. Without `--secret-stdin`, stdin is not read and Fetch receives an empty secret. The CLI redacts the supplied secret from provider error messages and stale reasons; providers must keep credentials out of all returned usage fields, diagnostic endpoint labels and messages.

## Providers

### Claude

```text
aipermiler usage claude [--json] [--claude-bin /absolute/path/to/claude]
```

Quotas come only through the user's own, unmodified `claude` binary and sign-in (#139, #140): the core never reads Claude's credentials and never signs in. The approach was decided on #183 and #218 ([spike](CLAUDE-USAGE-SOURCE.md)). Linux, macOS and Windows (#223). Claude accepts no `--secret-stdin`.

One fetch:

1. `claude auth status --json`, bounded only by the deadline. Only `loggedIn`, `authMethod`, `subscriptionType` and `projectsDirectory` are read, never email or organisation. `loggedIn: false` is `signed_out` and `authMethod` `api_key` or `third_party` (an API key, Bedrock, Vertex: no subscription quota) is `unavailable`, both without starting a session (#228); `claude.ai`, `oauth_token` (`claude setup-token`) and unknown values go on; unusable output leaves the check to the screen. `subscriptionType` `enterprise` does not stop the probe: it only explains a missing `rate_limits` (below).
2. `claude` starts in a pseudo-terminal (50×140, `TERM=xterm-256color`; on Windows a ConPTY on `golang.org/x/sys/windows`, the native `claude.exe` started directly, suspended, put in a job object with `KILL_ON_JOB_CLOSE`, then resumed, never with `CREATE_NO_WINDOW`, which breaks ConPTY) inside the core's probe folder `<user cache>/aipermiler/claude-probe`, which must be empty. The flags are `--settings` with our own status line command, `--strict-mcp-config` and `--setting-sources local`. Nothing is written to the user's settings, and MCP servers, user hooks and plugins stay out. The environment markers of a parent Claude Code session (`CLAUDECODE`, `CLAUDE_CODE_ENTRYPOINT`, `CLAUDE_CODE_SESSION_ID` and the like, a fixed list) are removed. Every other variable stays, `CLAUDE_CODE_*` included (provider, sign-in, gateway, mTLS, Git Bash path), so the core reads the same sign-in as the user's Claude Code (#228).
3. The folder trust prompt is answered once, only for that empty folder. The core types only `/usage`, Esc and `/exit`, never a prompt, so no model request is made on the user's plan. Claude Code's question about an API key in its environment (*Detected a custom API key*) is never answered: its default is No, and any Enter typed into it would store that answer in the user's Claude Code, so the core leaves it untouched and kills the session (#228). For the same reason Enter follows `/usage` only once the prompt shows the typed `/usage`: a screen that does not echo it is an unknown dialog, which gets no Enter: the probe ends at the deadline without confirming anything.

   The probe waits for events, never for guessed times, and has no time limit of its own: only the CLI deadline (`--timeout`, 2 min for `usage`) ends it, as a guard against a `claude` that hangs (#266). Claude runs our status line command as it draws its prompt, also at startup before any message, and again after `/usage`, that time with `rate_limits` (verified on Linux with 2.1.322 and Windows 11 with 2.1.319). The first run in the sink file, then 0.5 s without output, is the prompt; silence alone never is, since a pause during a slow start looks the same, and without a run the read could not succeed anyway. A run seen before the folder trust answer does not count. `/usage` is typed only then, and its echo, the panel, its closing and the status line run after it are each awaited as long as they take: on a Windows 11 VM with both cores saturated, `claude` alone took 18-23 s to draw its prompt, and Windows PowerShell, which runs the status line there, several more seconds. `/usage` is never typed twice: a late echo of both would send `/usage/usage` to Claude. A dialog that never echoes `/usage` gets no Enter and ends at the deadline. The question about an API key is recognized also while the echo is awaited, and the usage tab drawn without quota windows once it is done refreshing (seen with the Keychain out of reach on macOS over SSH), so both are reported at once. Only a run after `/usage` without `rate_limits` is a `format_drift`.
4. Claude runs the status line command. It is the hidden subcommand `aipermiler claude-statusline <file>`, which appends the JSON it receives on stdin as one line to a file `<user cache>/aipermiler/claude-status-*.jsonl` created for this fetch. It never creates a file and prints nothing. On Linux and macOS Claude runs the command with `sh`, so both paths are single-quoted. On Windows it runs it with Windows PowerShell, or with Git Bash when Git for Windows is installed (both verified on Windows 11 with 2.1.302 and 2.1.316), and the two quote differently: the paths go unquoted, their folders as 8.3 short names with forward slashes (`C:/Users/MARIOR~1/…/aipermiler.exe`), which PowerShell, bash and cmd all read as the same word. A path that still holds a character other than ASCII letters, digits and `:/._-~` (a volume without short names) is `unavailable`. Once the panel is closed with Esc, the core waits for a run after `/usage`, or one carrying `rate_limits`.
5. `/exit` gets 5 s, then the whole process tree is killed (the process group on POSIX, the job on Windows), and its end is awaited at most 2 s. Both bounds only keep a stuck exit from holding an answer already read, never fail it; a probe that reached the deadline is killed at once. The core therefore answers at most 7 s after its deadline, and the client's timeout is 15 s above it (#266).
6. Cleanup, also after a failed probe: the sink file is deleted, and so is the probe's own transcript. That is the `transcript_path` of a status line run whose `cwd` is the probe folder, only a `.jsonl` directly inside a folder of `projectsDirectory` (paths compared as files, not as spellings). The folder is removed too when nothing else is left in it. Without `projectsDirectory` nothing is deleted. A fetch also deletes the sink files of earlier fetches cut short (a killed core), when untouched for 10 min (#229). Nothing else of Claude's is touched: the `/usage` line in `history.jsonl` stays.

Discovery order (#219):

1. Explicit `--claude-bin`: absolute and native, otherwise `unavailable`, without fallback.
2. `claude` (`claude.exe` on Windows) in the PATH entries, in order. On Windows each entry is also looked into as an npm prefix: `<entry>\node_modules\@anthropic-ai\claude-code\bin\claude.exe`, the binary its `claude.cmd` shim runs (with Node from winget the prefix is Node's own folder, not `%APPDATA%\npm`; verified).
3. `~/.local/bin/claude`.
4. Fixed prefixes: on macOS `/opt/homebrew/bin/claude`, `/usr/local/bin/claude`, then the npm package under `/usr/local` and `/opt/homebrew`; on Linux the npm package under `/usr/local/lib` and `/usr/lib`; on Windows the npm package under `%APPDATA%\npm` and the winget package under `%LOCALAPPDATA%`.
5. The native binary of the Claude Code extension of VS Code, VS Code Insiders, Cursor, Windsurf and VS Code Server, newest version first.

Only native binaries qualify: at least 1 MB, never `.js`/`.cjs` (Node is never run), `.exe` on Windows, executable elsewhere. Symlinks are resolved and duplicates skipped. npm 11 no longer runs install scripts by default: without `--allow-scripts=@anthropic-ai/claude-code` the package keeps its small stub instead of the native binary, and that install is skipped (verified on Windows with npm 11.19). On Windows 11 the four layouts were found side by side and each one read both windows: winget, npm, native installer, VS Code extension.

| Condition | Code | Meaning/message |
|---|---|---|
| Binary not found (also a relative or wrong `--claude-bin`) | `unavailable`, reason `not_installed` | `Claude Code not found; install it or set --claude-bin` |
| Signed in with an API key or a cloud provider | `unavailable`, reason `no_subscription` | `Claude Code is signed in with an API key or a cloud provider, which has no subscription quota` |
| Claude Code asks whether to use an API key in its environment | `unavailable`, reason `api_key_question` | ``Claude Code asks whether to use the API key in its environment; answer it in claude, then retry`` |
| Signed out (`auth status`, or the login/theme screen) | `signed_out` | ``Claude Code is not signed in; run `claude` and sign in`` |
| The usage tab without quota windows | `not_ready` | `the usage panel shows no quota windows` |
| The deadline came first: no prompt (no status line run), `/usage` never shown at the prompt (a dialog), no panel, the panel never closed, or no status line run after `/usage` | `timeout` | `claude probe timed out <where>` / `claude probe timed out before the status line ran after /usage` |
| `subscriptionType` `enterprise` and the status line ran without `rate_limits` (a plan billed through usage credits: the `/usage` panel shows only "Usage credits", seen on 2.1.319, #249; the [status line docs](https://code.claude.com/docs/en/statusline) list `rate_limits` only for Pro, Max and Ultra, or behind a Claude apps gateway with a spend limit) and the panel has no "Usage credits" block, or one in a currency other than `$` | `unavailable`, reason `usage_credits` | `Claude Code is signed in on a plan billed through usage credits, which has no quota windows` |
| Same plan, "Usage credits" block present but not recognized | `format_drift` | `Claude: the Usage credits block of /usage was not recognized`, endpoint `claude /usage panel (Usage credits)`, no shape |
| Status line never ran, no `rate_limits` (any other plan), or no recognized window | `format_drift` | `Claude: …`, endpoint `claude status line rate_limits`, shape only |
| `claude` cannot start, exits early, probe folder not empty | `unavailable` | the reason, never the screen |
| Windows: a core or cache path the status line cannot name without quoting | `unavailable` | `the status line command cannot name this path without quoting (no 8.3 short name?): <path>` |

Mapping reads the last status line run that carried `rate_limits`. `five_hour` maps to `claude-5h` / `5-hour limit` / `rolling-hours` 5 and `seven_day` to `claude-7d` / `7-day limit` / `rolling-days` 7. A missing or negative `used_percentage` skips the window; the value is never clamped; unit `percentage`, total 100. A positive Unix-second `resets_at` becomes a UTC date, anything else is null. `planTier` and `subscriptionRenewsAt` are always null: the status line carries neither. The synthetic fixtures in `internal/claude/testdata/` are named after the Claude Code version whose shape they follow; the behaviour was seen on 2.1.314-2.1.316.

On a plan billed through usage credits (`subscriptionType` `enterprise`, no `rate_limits`) the core reads only the "Usage credits" block of the `/usage` panel it already opened (#255; no other part of the screen is parsed). On the normalized panel text, the last redraw of `usagecredits` … `N%used` `$<spent>/$<limit>spent` … `resets<mon><day>(` gives the v0.4.x window `spend` / `Spend limit`: `count`, used = spent, total = limit (USD, `,` thousands allowed), `billing-cycle` 1. The panel gives month and day only: `resetsAt` is the next occurrence of that date at local midnight of the machine (the zone it shows is this machine's, inferred). Block seen on Windows 11 with 2.1.319. Should the status line ever carry these figures (anthropics/claude-code#108569), it replaces this read.

**Polling.** Every fetch starts a Claude Code session (about 5 s measured on Linux, 6-9 s on a Windows 11 VM, 2.1.302-2.1.316) and adds a line to Claude's `history.jsonl`. The client polls every 30 min by default and never below 5 min on a timer (the adaptive 5 min pace is the floor); a manual refresh reuses a fetch less than 1 min old (`main/refreshPolicy.ts`). The CLI's default deadline for `usage`, 2 min, is only a guard against a hung read: a read waits for each event as long as it takes (#266). The client keeps its own timeout above it (#180). On Windows the core started by Node's `execFile` with `windowsHide` (as `main/coreClient.ts` does) in the desktop session read both windows and showed no window over 14 s of screenshots taken every ~40 ms (a visible console in a control run was caught in 148 of 189 frames).

### Codex

```text
aipermiler usage codex [--json] [--codex-bin /absolute/path/to/codex]
```

`--codex-bin` selects an explicit native binary; a relative path or a missing/non-executable binary returns `unavailable`, without fallback. Codex accepts no `--secret-stdin`: it uses the user's own CLI or extension login. The core runs only `codex app-server`, once per fetch, without a shell. It inherits the environment unchanged (including `CODEX_HOME`); Aipermiler reads nothing under `CODEX_HOME`, including `auth.json`. On Windows it runs in the user's logon session with no console window, so keyring credentials remain accessible.

Discovery, in order:

1. Explicit `--codex-bin`.
2. Native `codex` (`codex.exe` on Windows) in absolute PATH directories, in order; Windows `.cmd`, `.ps1` and extensionless shims are skipped.
3. macOS fixed prefixes: `/opt/homebrew/bin/codex`, then `/usr/local/bin/codex`.
4. npm global locations: `NPM_CONFIG_PREFIX`, then `npm_config_prefix` (`lib/node_modules` on Unix, `node_modules` on Windows); Windows `APPDATA/npm/node_modules`; Linux `/usr/lib/node_modules`, `/usr/local/lib/node_modules`, `~/.npm-global/lib/node_modules`; macOS `/usr/local/lib/node_modules`, `/opt/homebrew/lib/node_modules`.
5. Newest numeric `openai.chatgpt-<version>[-<platform>]` extension with a current-OS binary across `~/.vscode`, `.vscode-server`, `.vscode-insiders`, `.cursor`, `.windsurf`, `.vscode-oss` (`extensions/` under each). Uses `bin/linux-<arch>`, `bin/windows-<arch>`, or macOS `bin/macos-<arch>` then `bin/darwin-<arch>`, with `x86_64`/`aarch64`.

On Unix, a candidate resolving to `@openai/codex/bin/codex.js` is replaced with the native binary in that package's `node_modules/@openai/<native-package>/vendor/<triple>/bin/`; a missing native binary skips that candidate. Executable headers are checked (ELF, Mach-O or PE for the current OS); shell scripts and other shims are skipped. Neither `node` nor the JS shim is run. Homebrew and Apple Silicon discovery still need real-machine verification (#174).

The read-only handshake is `initialize` → `initialized` → `account/rateLimits/read` (`params: null`); `account/read` (`refreshToken: false`) runs only after `-32600` to distinguish API keys. No login, logout, reset-credit consumption, prompt or analytics opt-in methods/flags are used. Each call has its own 10 s timeout within the CLI deadline (default 2 min). After a response, stdin closes and the child has 3 s to exit before being killed; errors kill it immediately. Fetch always waits for cleanup. Stderr is drained and never logged.

| Condition | Code | Meaning/message |
|---|---|---|
| Binary not found | `unavailable` | `Codex CLI or VS Code extension not found; install it or set --codex-bin` |
| Signed out (`-32600`, no API key) | `signed_out` | `Sign in with Codex (CLI or extension)` |
| API-key login | `unavailable` | `Codex is signed in with an API key, which has no subscription quota` |
| Offline/internal RPC error (`-32603`) | `unavailable` | `codex app-server: <rpc message>`; no fresh values, no partial result |
| Call or caller deadline | `timeout` | Request timed out; process reaped before returning |
| Missing quota structure/no recognized window | `format_drift` | `Codex: no quota window recognized`, endpoint `codex app-server account/rateLimits/read`, shape only |
| Other RPC errors / early process exit | `unavailable` | RPC code and message / process exit code; never stderr |

Mapping selects `rateLimitsByLimitId["codex"]` when present, otherwise `rateLimits` (the schema requires `rateLimits` in either case). Other buckets are ignored: their meaning per plan is unverified. `individualLimit`, `credits`, `rateLimitResetCredits` and `rateLimitUpsell` are ignored too. Synthetic fixtures in `internal/codex/testdata/` follow the generated codex 0.163.0 schemas; the unknown-shape fixture intentionally violates the schema to test drift.

Windows are recognized by duration in either `primary` or `secondary`, with the first duplicate ID winning. Positive whole hours below a day map to `codex-<n>h`, `<n>-hour limit`, `rolling-hours`; whole days map to `codex-<n>d`, `<n>-day limit`, `rolling-days`. Thus the usual windows remain `codex-5h` / `5-hour limit` and `codex-7d` / `7-day limit`, preserving stored history. Other durations are skipped. `usedPercent` must be a nonnegative number and is never clamped; unit is `percentage`, total is 100. Positive Unix-second `resetsAt` becomes a UTC date; absent/null/nonpositive reset is null. `planTier` is the selected bucket's `planType` or null; `subscriptionRenewsAt` is always null.

### Copilot

`aipermiler usage copilot --account <id> [--host github.com|<tenant>.ghe.com] [--scope personal|organization] [--manual-quota <credits>]`, port of `services/copilot.ts` (#149).

- **The token lives in the OS keyring** (decided on #179; library `github.com/zalando/go-keyring`): Windows Credential Manager, macOS Keychain (through `/usr/bin/security` with the secret on its stdin), Secret Service on Linux. Service `aipermiler`, entry `copilot/<account id>`. It never crosses argv, stdin of `usage`, or any JSON document. There is no file fallback: an unreachable keyring (e.g. a Linux session without Secret Service, or a Windows SSH logon without the user's logon session, as for Codex in #158) is `unavailable` with reason `keyring_unavailable`.
- `--account` is required (letters, digits, `.`, `_`, `-`, at most 128): it names the keyring entry. `--host` must normalize to `github.com` or `<tenant>.ghe.com` (the token is sent only there: API on `api.<host>`, OAuth on `<host>/login/oauth`). Invalid flags are usage errors (exit 2), before any keyring or network access.
- **Sources**, as in the TypeScript service: `copilot_internal/user` (the undocumented endpoint behind the VS Code quota indicator) for both scopes; for the personal scope, when it has no quotas, the billing report (`ai_credit/usage`, then `premium_request/usage` on a 404 or a 400 "Unable to get billing usage data") with `--manual-quota` as the total. Each request has a 10 s timeout.
- **Windows**: one per `quota_snapshots` category, in the response's key order, id = the category key, label `Copilot — <key>`, `billing-cycle` of 1 month, VS Code's rules (unlimited → credits used without total, nothing when there are none; entitlement > 0 → entitlement − remaining of the entitlement; entitlement 0 → nothing; otherwise the percentage). Billing report → `ai_credits` / `AI credits`, count, reset and renewal on the 1st of the next UTC month. Same ids as before, so the stored history keeps matching.
- **Errors**: no token stored → `signed_out`, reason `not_connected`; GitHub answers 401 → `signed_out`, reason `token_rejected`; enterprise-managed github.com seat (no data at all, #7) → `unavailable`, reason `enterprise_managed_seat`; no category recognized or no `usageItems` → `format_drift` with the shape.

## Paths

`paths` prints the folders a client must know about (#180): today `claudeProbe`, the folder every Claude probe runs in (`<user cache>/aipermiler/claude-probe`, not created by this command). A client that reads Claude Code sessions (the app's local insights) skips the sessions whose `cwd` is that folder: they are the core's `/usage` reads, not the user's work.

```json
{"schemaVersion":1,"claudeProbe":"/home/synthetic/.cache/aipermiler/claude-probe"}
```

Text output: `claude probe: <path>`. Unexpected arguments are usage errors (exit 2); a user cache folder that cannot be located is `unavailable` (exit 1).

## Local insights

```text
aipermiler insights claude [--json] [--days N] [--timeout <duration>]
```

Behavioural insights from the local Claude Code sessions (#298, part of #150; approach in #297): output tokens per local day, the share spent with a large context or in long sessions, the most used tools, and the first session start of each day. The output is the app's `ClaudeLocalInsights`, counted correctly (the double count of #301). Electron reads them from this command since #299 (`main/coreClient.ts`, `insights()`).

`--days` is the window, default 30, from 1 to 366; outside it is a usage error (exit 2). `--timeout` is as for `usage` (default and at most `2m`).

**Source.** Claude Code's own session files, read directly: `CLAUDE_CONFIG_DIR`, else `~/.claude` (`%USERPROFILE%\.claude` on Windows), then `projects/*/<session>.jsonl` and `projects/*/<session>/subagents/*.jsonl`, modified within the window; no cap on the number of sessions. The format is Claude Code's, not a documented interface, hence the drift check below. Only `type`, `timestamp`, `requestId`, `cwd`, `message.usage` and the `name` and `id` of `tool_use` blocks are read: never message text, tool parameters or tool results, and nothing of a file is logged (the debug log carries counts only). Sessions whose `cwd` is the core's Claude probe folder (`paths`) are skipped, their subagents too.

**Counting.**
- One count per API response: its lines repeat the same `usage`, so it is taken once per `requestId` across every file (a subagent file can repeat a response of the main file); of several copies the largest output wins. A line without `requestId` counts on its own. Every line counts, including the part before a compaction.
- Each `tool_use` block counts once (by its `id` when present, across files).
- A response goes to the local day of its first timestamp, only when that falls in the window. `firstSessionStartByDay` is, per local day, the earliest start (first timestamp) of a main session (not a subagent) on the day it started, as `createdAt` in the TypeScript reader: a session still writing after midnight does not start the next day.
- High context: `input + cache_read + cache_creation` above 150,000 tokens. Long session: 8 hours or more from the first to the last timestamp of the main session (its subagents' responses included). Top 5 tools, shares rounded to one decimal.
- Unknown fields, corrupt lines and unreadable files are skipped; a file that changes while being read does not fail the run.

**Result.** With output tokens in the window, `insights` is the object below; without them `insights` is `null` (nothing to show, not an error).

```json
{"schemaVersion":1,"provider":"claude","insights":{"computedAt":"2026-10-31T21:10:00Z","windowDays":7,"sessionsAnalyzed":1,"totalOutputTokens":4000,"highContextOutputTokens":1000,"longSessionOutputTokens":0,"highContextSharePercent":25,"longSessionSharePercent":0,"topTools":[{"name":"Bash","sharePercent":50},{"name":"Read","sharePercent":50}],"daily":[{"date":"2026-10-31","outputTokens":4000,"highContextOutputTokens":1000}],"firstSessionStartByDay":{"2026-10-31":"2026-10-31T08:00:00.000Z"}}}
```

**Format drift.** When the window has `assistant` lines but none carries a numeric `message.usage.output_tokens`, the answer is the `format_drift` error with endpoint `projects/*/*.jsonl` and the shape (keys and types, never values) of one `assistant` line, exit 1. Other failures (the folder cannot be read, deadline) are `unavailable` and `timeout`.

Text output: a title line with the window, sessions and output tokens, the two shares, and the top tools. A missing sessions folder is not an error: `insights` is `null`.

## Account commands

They manage a provider account's credentials in the core (Copilot only, today). Their documents follow the same rules as the others (one compact document per line, `schemaVersion` 1, error documents and exit codes as for `usage`).

- `token set <provider>`: reads a token (a GitHub PAT) on stdin, checks it with `GET /user`, stores it in the keyring under `--account` and prints `{"schemaVersion":1,"provider":"copilot","username":"<login>"}`. A rejected token is never stored. Also the way Electron hands over the token it kept before #179.
- `token delete <provider>`: removes the account's token (disconnect, account removal); deleting a missing token is not an error. Prints `{"schemaVersion":1,"provider":"copilot","deleted":true}`.
- `login <provider> --client-id <id> [--locale en|it]`: OAuth App sign-in, Authorization Code + PKCE over the loopback `http://127.0.0.1:8123/callback` (the callback registered in the user's own GitHub OAuth App; port of `main/copilot-oauth.ts`). The **client secret** is read on stdin. The core starts no browser: with `--json` it first prints the event `{"schemaVersion":1,"provider":"copilot","event":"authorize","url":"<authorize URL>"}` for the caller to open (Electron: `shell.openExternal`), then waits (default `--timeout 5m`, at most `15m`); after the callback it exchanges the code (10 s), checks the token with `GET /user`, stores it and prints the `username` document. So stdout carries **two** documents, one per line. Without `--json` it prints the URL to open, then `Copilot: connected as <login>`. The browser page after the callback is in the `--locale` language. Denied, forged state or a failed exchange → `unavailable`; no authorization in time → `timeout`; port 8123 busy → `unavailable`.
- `raw <provider>`: the provider's raw usage response, **with its values**, for the diagnostic report (the client redacts it before writing the file): `{"schemaVersion":1,"provider":"copilot","endpoint":"{api}/copilot_internal/user","response":{…}}`. Always JSON.

## Text output

Default output is for people and terminal agents, **not a stable contract**. One line per window, for example:

```text
Codex · 5h 21% · resets 22:10
Copilot · Monthly credits 250/1000 (25%) · resets 2026-11-30 22:10
```

Rolling windows use `<n>h` or `<n>d`; billing windows use their label. Percentage windows already contain percentages; count windows show `used/total (percentage%)`, or only `used` if total is null. Percentages round to the nearest integer. A zero total shows `used/0` without an undefined percentage. Resets within the next 24 hours use local `HH:mm`; at exactly 24 hours, later, or in the past they use `YYYY-MM-DD HH:mm`. Missing resets omit that part. Stale values append ` · stale` to each line. Zero windows produce `<Display> · no quota windows` (also suffixed when stale). Text provider errors go to stderr as one line `<provider>: <message>`; usage errors also print help.

## JSON contract v1

All dates are RFC 3339 UTC strings with `Z`, at second precision. All keys of `RawAccountUsage` and `QuotaWindow` are always present: unknown nullable fields are `null`, never omitted. `quotaWindows` is always an array, including `[]`. JSON field names, enum values and error codes are language-neutral; clients must not parse human-readable labels or messages.

### Envelope fields

| Field | Type / presence | Meaning |
|---|---|---|
| `schemaVersion` | integer, always `1` | Contract version, independent of the binary version. |
| `provider` | string, success/provider error only | Registered provider ID; omitted for usage errors. |
| `fetchedAt` | ISO string, success only | CLI clock immediately after Fetch returns, including stale results; not the original freshness timestamp of cached values. |
| `stale` | boolean, success only | Provider returned last known values rather than fresh values. |
| `staleReason` | `{code, message}`, only when stale | Reason for fallback, using provider error codes. No endpoint or shape here. |
| `usage` | `RawAccountUsage`, success only | Provider data, without pacing or history. |
| `error` | object, errors only | Stable `code` and readable `message`; format drift also has `endpoint` and `shape`. Optional `reason` (additive, #149): a known case inside the code, so a client can explain it in the user's language without parsing `message`. Known reasons: `not_connected`, `token_rejected` (with `signed_out`); `enterprise_managed_seat`, `keyring_unavailable`, and for Claude (#180) `not_installed`, `no_subscription`, `usage_credits`, `api_key_question` (with `unavailable`). A client ignores a reason it does not know. |
| `name`, `version`, `os`, `arch` | strings, version document only | Binary identity, build-time version (default `dev`), Go OS and architecture. |

### Usage and quota fields

| Object / field | Type | Meaning |
|---|---|---|
| usage.`planTier` | string or null | Provider plan name. |
| usage.`subscriptionRenewsAt` | ISO string or null | Provider subscription renewal. |
| usage.`quotaWindows` | array of quota windows | Reported limits, possibly empty. |
| window.`id` | string | Window identifier. |
| window.`label` | string | Display label. |
| window.`periodType` | `rolling-hours`, `rolling-days`, `billing-cycle` | Reset model. |
| window.`periodLength` | number or null | Hours, days, or months for billing cycles; null if unknown. |
| window.`unit` | `percentage` or `count` | How to read used/total. |
| window.`used` | number | Already a percentage for percentage windows; absolute consumption for count windows. |
| window.`total` | number or null | Total quota when known. |
| window.`resetsAt` | ISO string or null | Window reset time. |

### Full document examples

These are the normative single-line fixtures from `gocore/contract/v1/`; no README or Go package is needed in that directory. Provider `fake` and all values are synthetic.

#### Success with percentage windows

```json
{"schemaVersion":1,"provider":"fake","fetchedAt":"2026-10-31T21:10:00Z","stale":false,"usage":{"planTier":"plus","subscriptionRenewsAt":null,"quotaWindows":[{"id":"five-hour","label":"5 hours","periodType":"rolling-hours","periodLength":5,"unit":"percentage","used":21,"total":100,"resetsAt":"2026-10-31T22:10:00Z"},{"id":"weekly","label":"7 days","periodType":"rolling-days","periodLength":7,"unit":"percentage","used":42,"total":100,"resetsAt":"2026-11-07T21:10:00Z"}]}}
```

#### Success with a count window

```json
{"schemaVersion":1,"provider":"fake","fetchedAt":"2026-10-31T21:10:00Z","stale":false,"usage":{"planTier":null,"subscriptionRenewsAt":"2026-11-30T21:10:00Z","quotaWindows":[{"id":"credits","label":"Monthly credits","periodType":"billing-cycle","periodLength":1,"unit":"count","used":250,"total":1000,"resetsAt":"2026-11-30T21:10:00Z"}]}}
```

#### Success with last known values

```json
{"schemaVersion":1,"provider":"fake","fetchedAt":"2026-10-31T21:10:00Z","stale":true,"staleReason":{"code":"unavailable","message":"Provider is temporarily unavailable"},"usage":{"planTier":"plus","subscriptionRenewsAt":null,"quotaWindows":[{"id":"five-hour","label":"5 hours","periodType":"rolling-hours","periodLength":5,"unit":"percentage","used":21,"total":100,"resetsAt":"2026-10-31T22:10:00Z"},{"id":"weekly","label":"7 days","periodType":"rolling-days","periodLength":7,"unit":"percentage","used":42,"total":100,"resetsAt":"2026-11-07T21:10:00Z"}]}}
```

#### Signed out

```json
{"schemaVersion":1,"provider":"fake","error":{"code":"signed_out","message":"Sign in required"}}
```

#### Not ready

```json
{"schemaVersion":1,"provider":"fake","error":{"code":"not_ready","message":"Usage is not ready yet"}}
```

#### Timeout

```json
{"schemaVersion":1,"provider":"fake","error":{"code":"timeout","message":"Request timed out"}}
```

#### Format drift

```json
{"schemaVersion":1,"provider":"fake","error":{"code":"format_drift","message":"Unrecognized usage response","endpoint":"/usage","shape":{"active":"boolean","items":[{"remaining":"number"}],"label":"string","reset":"null"}}}
```

#### Unavailable

```json
{"schemaVersion":1,"provider":"fake","error":{"code":"unavailable","message":"Provider is temporarily unavailable"}}
```

#### Usage error

```json
{"schemaVersion":1,"error":{"code":"usage","message":"Unknown provider: nope"}}
```

#### Version

```json
{"schemaVersion":1,"name":"aipermiler","version":"dev","os":"linux","arch":"amd64"}
```

### Error codes and client behavior

| Code | Meaning | Client action |
|---|---|---|
| `signed_out` | No usable authenticated session. | Show sign-in state, not a generic error. |
| `not_ready` | The provider has not produced usage yet. | Retry later. |
| `timeout` | The fetch deadline expired, including wrapped context deadlines. | Keep last values as stale and retry later. |
| `format_drift` | The response structure is no longer recognized. | Offer a drift report using endpoint and shape only, never response values. |
| `unavailable` | Other provider/network failure. | Keep last values as stale and retry later. |
| `usage` | Invalid CLI invocation; only exit 2 documents. | Correct the arguments before retrying. |

`shape` preserves sorted field names and type names (`string`, `number`, `boolean`, `null`), replaces arrays with one representative element (or `[]`), and uses `truncated` beyond depth 4. It never contains response values. Providers pass decoded JSON through `usage.ExtractShape` and must supply safe messages and endpoint labels without account IDs or credentials.

### Exit codes

| Exit | Meaning |
|---|---|
| `0` | Success, including stale values, help and version. |
| `1` | Provider error. Output write failures also return 1 and log the failure; so does `serve` when it cannot start or stops on a listener error. |
| `2` | Usage error: unknown provider/command, bad flags, extra arguments, invalid timeout or secret input. |
| `3` | `serve` only: another service already holds the lock. |
| `4` | `service stop` only: no service was running (nothing to stop; not an error for an installer). |
| `5` | `service stop` only: the service was still running when the time ran out. |

With `--json`, usage errors have no provider key. Help is text-only; requesting help with `--json` produces a usage-error document. Without `--json`, help/version retain their plain text behavior.

## Service

`aipermiler serve` keeps the process up as the always-on service (#31, first slice #247; approach in #246). It takes no flags. It reads the accounts on its refresh loop (below) and serves what the loop holds (endpoints below, #248), the settings (#284), the pacing snapshot (#326; local insights in it with #324) and the history (#339), and runs the Copilot account actions (#285). Logs go to stderr (`AIPERMILER_LOG_LEVEL=info` shows start and stop).

- **One instance per config folder** (so per user, unless `AIPERMILER_CONFIG_DIR` points elsewhere; a network filesystem may not honour the lock). Do not delete the config folder while a service runs: its lock file goes with it, so a second service could start next to the first. `serve` takes an exclusive lock on `service.lock` in the config folder (`flock(LOCK_EX|LOCK_NB)` on Unix, `LockFileEx` with `LOCKFILE_FAIL_IMMEDIATELY` on Windows). A second `serve` prints `The service is already running` and exits with **3**. The operating system drops the lock when the process dies, so a `kill -9` leaves nothing stale (tested on Unix with a killed process; on a Windows 11 VM with `taskkill /F`: the next `serve` takes the lock, replaces the dead instance's `api.json` and answers `/health`; a second `serve` meanwhile exits 3, #292).
- **Loopback only.** It listens on `127.0.0.1` (not `localhost`, which may resolve to `::1`) on a random port. Every request needs `Authorization: Bearer <token>`, a 32-byte random token generated at each start and compared in constant time (401 otherwise). A `Host` other than `127.0.0.1:<port>` (DNS rebinding) and any request with an `Origin` header get 403 before the token is looked at. The token is the real protection: browsers send no `Origin` on simple cross-origin GETs, so the `Origin` check only narrows what a web page can try.
- **`api.json`** in the config folder, tells a client how to connect (on Unix the mode is `0600`; on Windows the folder and the file inherit the profile's ACL: the user, SYSTEM and Administrators, no other user (`icacls` on a Windows 11 VM, #292)): `{ "schemaVersion": 1, "port", "token", "version", "contractVersion", "pid", "startedAt" }`. It is written after the listener is up, atomically (temp file + rename), by the process holding the lock; a new service removes the `api.json` and temp files a dead instance left, and a clean stop deletes the file. A client never trusts the file alone: it calls `/health` with the token, and a refused connection, a 401 or an unknown `contractVersion` means "no usable service". `pid` is for diagnostics only. After a crash the file keeps a dead service's port and token; sending that token to whatever now owns the port leaks nothing, because every start generates a new one. Any process of the same OS user can read the file and use the API: the same trust boundary as the user's own files and the keyring.
- **Refresh loop** (#283). Right after the lock, the service deletes the store's leftover temp files and opens `store.json`; an unreadable store or one written by a newer core stops the start (exit 1), never an empty store in its place. Then it reads every enabled account at once and keeps reading with the same policy as Electron (`internal/refresh/policy`, ported from `main/refreshPolicy.ts`): the configured interval, every 5 min while consumption rises, a per-account backoff after failures (never beyond the interval), and a 1 min cache for manual refreshes. It looks at the wall clock every minute, so it also reads at once after a resume from sleep (a check more than 3 min late), and a clock set back restarts the wait instead of holding it back. One read at a time; requests that arrive during a run join one more run with the strongest mode (`scheduled` < `manual` < `forced`). An account is read by running the same binary, `usage <provider> --json --timeout 120s` with the account's flags, and the service waits 15 s more than that deadline and keeps at most 1 MiB of its output, as Electron does; on Windows the child gets no console window. Each fresh value becomes the account's `lastGood` and one sample per window through `history.Record`, committed at once; a failed read keeps `lastGood`, which is then stale until a read succeeds. A store change that is saved but not synced to disk is logged and never retried (a second `Record` would count the sample twice). The stop cancels a read in progress: the child gets SIGTERM in its own process group (a job object on Windows), `usage` cancels its read on SIGINT/SIGTERM and ends its own processes (the Claude PTY), and whatever is left is killed; nothing of that read is recorded.
- **`GET /api/v1/health`** returns `{ "schemaVersion": 1, "version", "contractVersion" }` (fixture `contract/v1/service/health.json`). It stays under `/api/v1` and keeps these fields whatever the contract version, so any client can always ask. `contractVersion` is the `schemaVersion` of the documents this service writes: a client that finds one lower than its own, or one it does not know, treats the service as older (unusable), says so and falls back to the CLI. The client side, with its test against a fake older service, is `main/serviceClient.ts` (#182, [Electron client](#electron-client)).
- **Account documents** (#248), served from what the loop holds, never by reading a provider on the request. Every body is a contract v1 document, `Content-Type: application/json`, one line.
  - `GET /api/v1/accounts/{id}/usage`: the document `usage <provider> --json` prints, byte for byte: the account's `lastGood` with `fetchedAt` = when it was read; `stale: true` with the last read's error as `staleReason` while the latest read failed; the error document of that read when there was never a good one. Until the service has read the account since it started, its `lastGood` may be old, from before a restart: it is served as stale, `staleReason` `unavailable` with reason `not_read_yet` (fixture `contract/v1/service/usage-not-read.json`), or, with no `lastGood`, as the error document `unavailable` with reason `not_read_yet` (503, `contract/v1/service/error-not-read.json`). Not `not_ready`: clients read that as Claude Code not reaching its prompt. `staleReason` may carry a `reason` like an error (additive in v1). The same holds after a settings change that enables an account or changes its reads, until the loop has read it again (Settings, below, #284). No client changes `store.json` behind the service's back (the v0.7 migration script refuses when it finds the service through `api.json` and `/health`). A child that outlived its deadline is a `timeout`, as the CLI's own deadline.
  - `GET /api/v1/usage`: `{ "schemaVersion": 1, "accounts": [ { "id", "document" } ] }`, every enabled account in the store's order, each `document` as above (fixture `contract/v1/service/usage-list.json`). Always 200: the errors are inside the documents.
  - `GET /api/v1/accounts/{id}/snapshot[?chartDays=7|30]` (#326): the pacing snapshot. The body is the account's usage document above, the same fields with the same values and freshness (`stale`, `staleReason`, `not_read_yet`), plus one field, `pacing`: `{ "critical", "windows": [...] }`, built by `internal/pacing/snapshot` from the store (the account's history, work schedule, manual renewal day, `retentionDays`), its `lastGood` and the moment of the request, in the service's local calendar. `critical` is the index of the window the widget opens on (-1 without windows); each window, in the order of `usage.quotaWindows`, has its `id`, `used`, `remaining`, `period` (`start`, `end`), `units` (working units: `total`, `elapsed`, `remaining`, `fromToday`, `todayElapsed`, `remainingHours`), `daysToReset`, `efficiency`, `baseline`, `usedToday`, `todayBudget`, `paceAlert`, `redistribution` (`perUnit`, `evenShare`, `units`), `summary` (`pace`, `projection`, `autonomy`, `workingUnitsToReset`, `preliminary`), `hourly` (rolling-hours windows: `hoursLeft`, `projectedAtReset`, `autonomyHours`, `resetPassed`), `instantPace`, `trend` (the instant trend of D-3.28, the gauge arrows: `accelerating`, `slowing-down` or `steady`; `null` when it cannot be measured, and on every window while the last read failed or the service has not read the account yet, since the values are then the last known ones), `ceiling`, `target`, `gauge`, `judgement` (`verdict`, `reason`, `autonomyHours`), `chart` (slots with the local `day` as `YYYY-MM-DD`, `delta`, `fullShare`, `dayUnit`, `budget`, `upcoming`, `overBudget`), `stats` (`days`, `peak`, `average`, `streak`), `rating` (`stars`, `average`) and `tip` (`key`, `params`, as data: the client writes the sentence). The definitions are those of `docs/PACING.md` §3. Values are exact, never rounded (I-4): the client rounds when it shows them. An undefined value is `null`, never 0 (I-5); a window without a utilization (a count window without a total) has `used: null` and nothing else defined. `chartDays` (default 7) is the span of the chart and of the rating; any other value, or the parameter given more than once, is a `usage` error (400), checked after the account (an unknown or disabled account is 404 whatever the query). The tip is drawn at random among the ones that hold, at each request, as Electron does today. The snapshot is a view: it adds no sample (the loop recorded the read when it made it) and never writes the store. Without a good read the answer is the usage endpoint's error document, with its status. Fixture `contract/v1/service/snapshot.json` (a rolling-hours, a rolling-days, a billing-cycle count window and a count window without a total, at a fixed moment, UTC; the rolling-hours window is accelerating, the others have no trend). The local insights of PACING.md §3.15 are not in it yet (#324).
  - `GET /api/v1/accounts/{id}/history[?days=n]` (#339): the account's history as `store.json` keeps it, read-only, for the diagnostic report (nothing is removed from it when its history moves to the service, #181): `{ "schemaVersion": 1, "provider", "history": { "days", "windows": [ { "id", "daily": [ { "day", "used", "dayStartUsed" } ], "samplesToday": [ { "at", "used" } ] } ] } }`. One entry per window the store holds a history for, by `id`; `daily` are the daily points from `days` local days before today through today (default 7, as the report: up to 8 points), `day` the local day as `YYYY-MM-DD`; `samplesToday` the samples of today's local day (the store also keeps the last 4 hours, which after midnight reach into yesterday: those are left out), `at` in UTC. Values are exact, as stored. Empty lists are `[]`. The history does not depend on the last read: an account never read answers 200 with what it has (often `"windows": []`). `days` is an integer 0-3650 (the longest retention), given at most once; anything else is a `usage` error (400), checked after the account (an unknown or disabled account is 404 whatever the query), as on the snapshot. Days and today follow the service's local calendar, as the refresh loop records them. The endpoint never writes the store. Fixture `contract/v1/service/history.json` (the snapshot fixture's account at its moment, UTC).
  - `POST /api/v1/refresh` with an optional body `{ "mode": "scheduled" | "manual" | "forced" }` (no body or no mode: `manual`, a client asking is a user asking; unknown fields ignored; at most 4 KiB). It asks the loop for a run (coalesced with the runs already asked for, with the strongest mode, so calling it twice costs at most one more run; the backoff and the 1 min manual cache apply as in the loop) and answers like `GET /api/v1/usage` once a run that started after the request has ended. It waits as long as the run lasts (accounts are read one at a time: a Claude read takes 5-9 s, each read is bounded at 135 s): the client sets its own timeout, and a client that goes away or a stop ends the wait (503).
  - `GET /api/v1/accounts/{id}/raw`: runs `raw <provider> --json` with the account's flags (25 s, as the CLI; one raw read at a time, a second request meanwhile gets 503) and passes its document through unchanged: the raw response for the diagnostic report (Copilot today), or the CLI's error document (Claude and Codex: a usage error, they have no raw response).
  - HTTP status, mapped from the error code as the CLI's exit code is: 200 for a usage document (stale included) or a raw one; 400 for a `usage` error (exit 2: a bad request body, a raw read the provider does not support, or a read the CLI refused for the account's flags); 502 for any other provider error (exit 1); 503 to retry later: `not_ready`, `not_read_yet`, a request cut by a stop, a raw read while another one runs, a service without a refresh loop (`unavailable` error documents without a provider); 404 with a `usage` error document for an account that does not exist or is disabled (the service neither reads nor serves disabled accounts). A client reads the document whatever the status.
- **Settings** (#284). The service is the only writer of `store.json`, so clients read and change the settings through it (the Electron Settings window will, #181). Every body is one line of JSON, `Content-Type: application/json`.
  - `GET /api/v1/settings`: `{ "schemaVersion": 1, "settings": { "refreshIntervalMinutes", "retentionDays", "notificationThresholdPercent" }, "accounts": [ { "id", "provider", "label", "enabled", "renewalDay", "schedule": { "enabled", "days", "hoursPerDay" }, "host"?, "scope"?, "manualQuota"?, "binaryPath"? } ] }` in the store's order (fixture `contract/v1/service/settings.json`). The fields mean what they mean in the store (below); `host`, `scope` and `manualQuota` appear on Copilot accounts when set, `binaryPath` on Claude and Codex accounts when set. Never the history, `lastGood` or a secret: the Copilot token stays in the keyring (#179) and the document has no field for it.
  - `PUT /api/v1/settings` with the same document (at most 256 KiB, unknown fields ignored) replaces the settings and the account list, all or nothing, and answers like `GET`. An account keeps its history and `lastGood` by `id`; an account missing from the list is removed with them; a new `id` starts empty. Validation: `schemaVersion` 1, `settings` and `accounts` present; `refreshIntervalMinutes` 5-1440 (a shorter interval would read no sooner than the 5 min fast pace), `retentionDays` 1-3650, `notificationThresholdPercent` 1-100; `id` 1-128 of `A-Z a-z 0-9 . _ -` (it names the keyring entry), unique; `provider` `claude`, `codex` or `copilot`, and the same as before for an existing `id` (its history would be read as another provider's: remove it and add a new one); `renewalDay` 0 (none) or 1-31; `schedule.days` 7 weights, Sunday first, each 0, 0.5 or 1; `schedule.hoursPerDay` over 0 and at most 24; `host` only on Copilot, `github.com` or `<tenant>.ghe.com`, stored normalized (`https://`, `api.`, a path and upper case are tolerated, as in `--host`); `scope` only on Copilot, `personal` or `organization`; `manualQuota` only on Copilot, 0 or more; `binaryPath` only on Claude and Codex, absolute (an empty one means none). Invalid input answers **400** (413 over the size limit) with a `usage` error document whose `reason` is `invalid_settings` and whose message starts with the field's JSON path and a colon (`accounts[1].renewalDay: must be 0 (none) or 1-31`, fixture `contract/v1/service/error-invalid-settings.json`); nothing is written. A store that cannot be written answers 503 `unavailable`. Two clients writing at the same time: the last write wins, whole.
  - After the write, the accounts added, removed, enabled, disabled or read differently (another `host`, `scope`, `manualQuota` or `binaryPath`) are handed to the loop, which forgets what it knew of them (last error, last attempt, backoff) and reads the enabled ones at once, alone, as a `forced` read would: the other accounts keep their schedule, and the `PUT` does not wait for the read. Until that read ends the account is served as after a restart: `staleReason` `unavailable` with reason `not_read_yet` on its old `lastGood`, or the `not_read_yet` error document, never the old values as fresh or the old error. A read already in progress when the settings change does not count as the account's read: its error and backoff are dropped, and its values are kept only if the account is still enabled and read the same way (otherwise dropped too); the account stays `not_read_yet` until the new read ends. A read that starts between the write and the hand-over to the loop is treated the same way and repeated: one extra read. A change of label, schedule, renewal day or global settings does not read again (the new interval applies from the next tick).
- **Copilot account actions** (#285). Each wraps the CLI command of the same name ([Account commands](#account-commands)), run as a child of the same binary with the account's `--account` and `--host`: the child alone talks to GitHub and the keyring. The account must exist in the store (enabled or not: an account can be connected before it is enabled; 404 otherwise) and be a Copilot one (400 `usage` otherwise). Secrets travel only in the request body (loopback, bearer token) and on the child's stdin: never in its arguments, an answer, a log, `store.json` or an error message; the service keeps none of them, and an answer of the child that carried one would be replaced by an `unavailable` error document. Bodies are one JSON object of at most 128 KiB; a secret is at most 64 KiB, as on the CLI's stdin. A bad body is a 400 `usage` error that never quotes it.
  - `PUT /api/v1/accounts/{id}/token` with `{ "token": "<GitHub PAT>" }`: `token set` (25 s), the token on stdin. Answers the CLI's document: `{"schemaVersion":1,"provider":"copilot","username":"<login>"}` (200), or its error document (a rejected token is `signed_out`, reason `token_rejected`, and is not stored).
  - `DELETE /api/v1/accounts/{id}/token`: `token delete`; answers `{"schemaVersion":1,"provider":"copilot","deleted":true}` (deleting a missing token is not an error).
  - After a successful `token` call or login the account is handed to the loop as after a settings change: it forgets what it knew and reads the account at once, as a `forced` read would (connected: its quotas; disconnected: `signed_out`). Until then it is `not_read_yet`.
  - `POST /api/v1/accounts/{id}/login` with `{ "clientId", "clientSecret", "locale"? }` (`en` default, or `it`: the language of the browser page after the callback): starts `login` (OAuth App, Authorization Code + PKCE, `--timeout 5m`) in the background, the client secret on stdin, kept nowhere (the user types it each time). It answers **202** with the CLI's authorize event, `{"schemaVersion":1,"provider":"copilot","event":"authorize","url":"…"}`, as soon as the child prints it: the client opens the URL in the browser. If the child ends first (port 8123 busy) it answers that error document. If no URL comes within 10 s the login is stopped and the answer is a `timeout` error document. One login at a time, because the OAuth callback is the fixed port 8123: a second `POST` meanwhile, for any account, gets **409** with an `unavailable` error document, reason `login_in_progress`.
  - `GET /api/v1/accounts/{id}/login`: the login's status. **202** with the authorize event while it waits for the browser; then its result, kept until the next login starts: the `username` document (200) or the CLI's error document (`timeout` after 5 min without authorization, `unavailable` for a denied authorization or a failed exchange, with the usual status mapping). 404 (`usage`) when the last login was not this account's or there was none.
  - `DELETE /api/v1/accounts/{id}/login`: cancels a running login (the child is ended, the port freed) and answers its status as `GET` does: `unavailable`, reason `login_cancelled`, or the result if the login had already ended. A stop of the service cancels a running login too.
  - Removing a Copilot account with `PUT /api/v1/settings` deletes its token (`token delete`) once the new settings are committed. A token that cannot be deleted (for example an unreachable keyring) does not undo the removal: it is logged at warn with the account id and the error code, and the response is the usual one.
- **`POST /api/v1/shutdown`** (authenticated like every request) answers `202 {"schemaVersion":1,"status":"stopping"}` and then starts the same clean stop as SIGTERM. The reply is written and flushed before the stop begins. Only `POST`; any other method gets 405.

- **Stop.** SIGINT or SIGTERM: cancel in-flight requests, delete `api.json` (so no client is pointed at a port that is going away), stop accepting, wait for open requests up to 10 s and close whatever is still open, release the lock. `serve` exits once every connection is closed, so endpoints must honour the request context, which the stop cancels: a handler that ignores it may still run briefly after the lock is released. A stop already requested when the service would write `api.json` skips it. While it drains the lock is still held and `api.json` is already gone: a client that starts `serve` in that window gets exit 3 with no `api.json`. The same happens while another service is starting (lock taken, listener not up yet): exit 3 with no `api.json` means a service is starting or stopping, and the client retries for a little longer than the 10 s stop bound. If it persists, a service holds the lock but cannot be reached (its `api.json` was deleted by hand, or it is stuck): the client reports it and falls back to the CLI; stopping that process is the user's call. A requested stop exits 0: connections still open at the bound are closed by force and a failure to delete `api.json` is only logged. Once the stop is under way, a further SIGINT or SIGTERM ends the process at once with the signal's default action, without cleanup (the next start removes the leftovers); one that arrives in the instant between the first signal and that switch is absorbed. Correctness never depends on a clean stop: the lock is released by the operating system and `api.json` is only a hint. On Windows a hidden `serve` in the user's session gets the logoff as SIGTERM and stops cleanly (`api.json` deleted, lock released; Windows 11 VM, #292).
- **`api.json` held open on Windows** (Windows 11 VM, #292). A reader that opens it with delete sharing, as Node does (`FILE_SHARE_DELETE`), blocks nothing. A reader that opens it without (the .NET and PowerShell default, `FileShare.Read`) blocks its replacement and deletion while the file is open: the service tries again for up to 1 second each time it replaces or deletes it (the stop deletes it twice). Past that, the stop leaves the file (logged, `Cannot delete api.json`) and a starting `serve` exits 1 (`write api.json: rename …: Access denied`). Clients read it with delete sharing or close it at once.

### Stopping the service: `service stop` (#286, for the installer and the updater of #152)

`aipermiler service stop [--json] [--timeout <duration>]` is the one way a planned stop is done, the same on Linux, macOS and Windows, with no signal involved (Windows has no simple equivalent of SIGTERM from a script). The installer and the updater call it before they replace the binary, and treat exit **0** and **4** as success.

1. It tries the lock of `service.lock`. Free: no service, exit **4** (`not_running`); a stale `api.json` is not even read.
2. Held: it reads `api.json` and sends `POST /api/v1/shutdown` with the token, to `127.0.0.1:<port>` only (the host is never taken from the file). While the lock is held but `api.json` is missing or refused (a service that is starting or already stopping), it keeps retrying.
3. It polls the lock until it is free: exit **0** (`stopped`). The lock being free means the service process has released it, that is, it is gone or about to exit; a following `serve` can start. The process may still exist for a moment: whoever replaces the binary right after (on Windows a running executable cannot be overwritten) retries for a few seconds. Run `service stop` as the user who runs the service: it looks in the caller's config folder, and answers **4** without creating it when it does not exist.
4. If the lock is still held when `--timeout` ends (default 15 s, a little above the service's own 10 s bound; at most 5 min): exit **5** (`timeout`). The command does not kill anything: what to do next (wait, or stop the process by other means) is the caller's call.

`--json` prints one document, `{ "schemaVersion": 1, "status": "stopped" | "not_running" | "timeout" }` (exit 0, 4, 5). Other failures (for example the config folder cannot be opened) exit 1 with an error document; a usage error exits 2. For a moment while it checks, `service stop` holds the lock itself, so a `serve` started at that very instant may find it taken (exit 3) and must retry, as in the start/stop window above.

## Store

`internal/store` (#295, part of #146) is the core's persisted state and its only writer. The service (#31) opens it at start and is its only writer: the refresh loop (#283) and the settings endpoint (#284). The import from electron-store is a standalone script (#310).

- **One file**, `store.json` in the config folder, mode `0600`, plain JSON with `schemaVersion` (1). Not encrypted: the only secret, the Copilot token, is in the OS keyring (#179). The name differs from Electron's `aipermiler-config.json`, because on Windows and macOS both apps share the config folder and the file systems are case-insensitive.
- **Content**: `settings` (`refreshIntervalMinutes`, `retentionDays`, `notificationThresholdPercent`) and `accounts`. An account has its id, provider, label, `enabled`, the manual `renewalDay` (0 = none), the work `schedule` (`days` Sunday first, weights 0 / 0.5 / 1), the provider fields the core needs (`host`, `scope`, `manualQuota`, `binaryPath`), its `history` per quota window id (`dailyUsage` with baselines and `recentSamples`, the `history.State` of #162) and `lastGood`, the last successful read. Fields are additive: a client or an older file missing one keeps its default. Unknown fields are ignored on read and dropped at the next write, so after a downgrade an older core erases what it does not know: a field whose loss matters bumps `schemaVersion`, and the older core then refuses the file (`ErrNewerSchema`).
- **Crash-safe writes.** Every change writes a temp file in the same folder, `fsync`s it, renames it over `store.json` and syncs the folder (not on Windows, where a folder cannot be opened for sync). A crash leaves the old file or the new one, never half a day. A crash can leave a `store.json.*.tmp`: `RemoveLeftovers` deletes them; the service calls it at start, under its lock (#283).
- **One writer per process.** `Store.Update(func(*State) error)` runs the function on a copy of the state under a mutex and commits it; if the function fails or panics, or the write fails, neither the file nor the memory changes. Once the new file has replaced the old one the change is committed, in memory too; a folder sync that fails after that returns `ErrNotSynced` (the change may not survive a power loss). The store keeps its own copy of what the function put into the state. History changes only through `Account.Record`, which calls `history.Record`. Overlapping refreshes (timer, manual, wake from sleep) are applied one after the other, so they give the history of a sequential run in that order. The file is not locked against other processes: the service lock (`service.lock`) guarantees there is one.
- **Errors, never a silent reset.** A missing file is an empty store with the app's defaults (nothing is written until the first `Update`). A file that is unreadable, not valid JSON or without a valid `schemaVersion` is `ErrUnreadable`; a newer `schemaVersion` is `ErrNewerSchema`. `Open` never modifies the file.

## Electron client

`main/coreClient.ts` (#177) is the app's side of this contract. For each read it runs the binary with `execFile`: no shell, `windowsHide` (no console window on Windows), `--json`, `--timeout 120s` and a client timeout of 135 s (only guards against a hung core, #266), stdin always closed (the secret written first with `--secret-stdin` when there is one), at most 1 MiB of output. The binary is `AIPERMILER_CORE_BIN` when set (absolute), else `<resources>/core/aipermiler[.exe]` in the package or `core-bin/` of the repository in development. Errors: binary missing or not startable → `CoreBinaryError`; killed at the client timeout → `CoreTimeoutError`; output that is not a v1 document, contradicts its exit code, or a usage error (exit 2, a bug in the arguments) → `CoreOutputError`, never quoting the output; `format_drift` → the same `FormatDriftError` as the TypeScript services, so the drift report works unchanged; other codes → `CoreProviderError` with the code. `version --json` feeds Settings → Diagnostics and the diagnostic report. Codex reads through it since #178 (`main/core.ts`, `main/providers.ts`), Copilot since #179 and Claude since #180; `insights claude --json` gives the local insights since #299 (the core skips its probe folder itself).

**Reading from the service** (#182, `main/serviceClient.ts`). When the service runs, an account's usage comes from it instead of a CLI run. On every read the app reads `api.json` in the core's config folder (the same folder as the Go `config.Dir()`: `AIPERMILER_CONFIG_DIR`, else `<user config>/aipermiler`; Node reads the file with delete sharing on Windows), calls `/api/v1/health` with the token on `127.0.0.1:<port>` (2 s), checks that `contractVersion` is the client's own (1), then reads `GET /api/v1/accounts/{id}/usage` (10 s) with the same parser as the CLI's output. It falls back to the CLI for that read, with a reason, when there is no `api.json` (`not_running`), nobody answers at its port (`unreachable`), the token is refused (`unauthorized`), `contractVersion` is lower, higher or missing (`incompatible`), the account is unknown to the service (404, e.g. added after the v0.7 migration: `unknown_account`), the service refused the read (400, `rejected`), it answers a 5xx without an error document of the account's provider (its own 503s while stopping or without a refresh loop: `failed`) or something that is not a v1 document (`malformed`). The requests never follow a redirect. An error document of the account's provider, with 502 or 503 (a failed read, `not_ready`, `not_read_yet`, or a stale document), is the account's answer, not a fallback: while the service serves an account the app never probes it itself, so a CLI probe can run next to the service's own only on a fallback. `not_read_yet` is shown as a neutral "refreshing" state with the last data, never as a failure. A service document carries the time the service read it (`fetchedAt`): the app records a history sample only when that time is newer than its last good read, so the same document read twice is not a second sample, and stamps the sample with that time (a read at 23:58 picked up at 00:03 counts for the earlier day). Until #181 the app keeps its own timers and history next to the service's. The source of each account's last read (service, or CLI and why) is shown in Settings → Diagnostics and in the diagnostic report (`interpretation.source`). The diagnostic report reads Claude and Codex through the same path (no probe of its own while the service runs) and Copilot's raw response through the CLI.

## Versioning and shared fixtures

Additive fields keep `schemaVersion: 1`; clients must ignore unknown fields. Removing or renaming a field, or changing its meaning, bumps the schema version. A client seeing an unsupported schema version must reject the document explicitly. An unknown error code is treated as `unavailable`, preserving last values as stale.

The language-neutral files in `gocore/contract/v1/` are normative examples. Go CLI black-box tests reproduce every provider-neutral fixture byte for byte through `RunWith`, a fake provider and a fixed clock. The TypeScript client tests (`main/coreClient.test.ts`, #177) read the same files. `version.json` uses `aipermiler`, `dev`, `linux`, `amd64`; its Go test substitutes `buildinfo.Name`, `buildinfo.Version`, `runtime.GOOS` and `runtime.GOARCH` before the byte comparison so build-time versions and cross-platform runs are supported. Every fixture is also checked as valid compact JSON with `schemaVersion: 1` and exactly one trailing newline. The service's own documents live in `contract/v1/service/` (`health`, `usage-list`, `usage-not-read`, `error-not-read`, #248; `snapshot`, #326; `history`, #339), reproduced by the `internal/service` tests and checked as compact versioned JSON with the others; the TypeScript client reads them since #182 (`history` from #341; `main/serviceClient.test.ts`, listed by the every-fixture test of `main/coreClient.test.ts`). `usage-claude` is reproduced by the Claude adapter's own test (`internal/claude`), from its 2.1.316 status line fixture through the mapping. The account documents (`account-connected`, `account-deleted`, `login-authorize`, `raw`) and `error-unavailable-reason` came with #149; the TypeScript client reads them from #179. The documents are written without HTML escaping (a URL keeps a literal `&`). The insights documents (`insights-claude`, `insights-empty`, `error-insights-drift`, #298) are reproduced by the CLI tests from synthetic session files; the TypeScript client reads them from #299.
