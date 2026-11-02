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
aipermiler help
```

The CLI fetches on demand and exits. There is no server, port, daemon, shared store or pacing snapshot. #31 will serve the same documents over HTTP; #177 adds the TypeScript client. Claude (#147), Codex (#148) and Copilot (#149) are registered; `token`, `login` and `raw` are Copilot's (account commands below). An unknown provider such as `aipermiler usage nope` fails with exit 2, `Unknown provider: nope`. Tests register a synthetic provider named `fake`.

Put the provider before its flags. Unexpected positional arguments and unknown flags are usage errors.

| Common flag | Behavior |
|---|---|
| `--json` | Write one compact JSON document plus a single newline to stdout; stderr carries logs only. A standard boolean flag (`--json=false` means text, the last occurrence wins); for an invocation that fails before or during flag parsing it is recognized anywhere in the arguments, so the usage error is a JSON document too. |
| `--timeout <duration>` | Fetch deadline, default `25s`; must be greater than zero and at most `2m`. Uses Go durations, e.g. `50ms`, `10s`. Providers must honor the context deadline. |
| `--secret-stdin` | Read stdin only if the provider accepts secrets. At most 64 KiB including a trailing newline; remove one final CRLF or LF, preserving other whitespace. Empty input, read failures and oversized input are usage errors. |

Secrets are accepted only on stdin, never as flags: argv is visible in the process list. Account parameters belong in provider flags, credentials do not. Without `--secret-stdin`, stdin is not read and Fetch receives an empty secret. The CLI redacts the supplied secret from provider error messages and stale reasons; providers must keep credentials out of all returned usage fields, diagnostic endpoint labels and messages.

## Providers

### Claude

```text
aipermiler usage claude [--json] [--claude-bin /absolute/path/to/claude]
```

Quotas come only through the user's own, unmodified `claude` binary and sign-in (#139, #140): the core never reads Claude's credentials and never signs in. The approach was decided on #183 and #218 ([spike](SPIKE-CLAUDE-USAGE-PTY.md)). Linux, macOS and Windows (#223). Claude accepts no `--secret-stdin`.

One fetch:

1. `claude auth status --json` (5 s). Only `loggedIn`, `authMethod` and `projectsDirectory` are read, never email or organisation. `loggedIn: false` is `signed_out` and `authMethod` `api_key` or `third_party` (an API key, Bedrock, Vertex: no subscription quota) is `unavailable`, both without starting a session (#228); `claude.ai`, `oauth_token` (`claude setup-token`) and unknown values go on; unusable output leaves the check to the screen.
2. `claude` starts in a pseudo-terminal (50×140, `TERM=xterm-256color`; on Windows a ConPTY on `golang.org/x/sys/windows`, the native `claude.exe` started directly, suspended, put in a job object with `KILL_ON_JOB_CLOSE`, then resumed, never with `CREATE_NO_WINDOW`, which breaks ConPTY) inside the core's probe folder `<user cache>/aipermiler/claude-probe`, which must be empty. The flags are `--settings` with our own status line command, `--strict-mcp-config` and `--setting-sources local`. Nothing is written to the user's settings, and MCP servers, user hooks and plugins stay out. The environment markers of a parent Claude Code session are removed.
3. The folder trust prompt is answered once, only for that empty folder. The core types only `/usage`, Esc and `/exit`, never a prompt, so no model request is made on the user's plan.
4. Claude runs the status line command. It is the hidden subcommand `aipermiler claude-statusline <file>`, which appends the JSON it receives on stdin as one line to a file `<user cache>/aipermiler/claude-status-*.jsonl` created for this fetch. It never creates a file and prints nothing. On Linux and macOS Claude runs the command with `sh`, so both paths are single-quoted. On Windows it runs it with Windows PowerShell, or with Git Bash when Git for Windows is installed (both verified on Windows 11 with 2.1.302 and 2.1.316), and the two quote differently: the paths go unquoted, their folders as 8.3 short names with forward slashes (`C:/Users/MARIOR~1/…/aipermiler.exe`), which PowerShell, bash and cmd all read as the same word. A path that still holds a character other than ASCII letters, digits and `:/._-~` (a volume without short names) is `unavailable`. Once the panel is closed with Esc, the core waits up to 3 s for a run carrying `rate_limits`.
5. `/exit` gets 5 s, then the whole process tree is killed (the process group on POSIX, the job on Windows). The probe is bounded by 25 s.
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
| Binary not found | `unavailable` | `Claude Code not found; install it or set --claude-bin` |
| Signed in with an API key or a cloud provider | `unavailable` | `Claude Code is signed in with an API key or a cloud provider, which has no subscription quota` |
| Signed out (`auth status`, or the login/theme screen) | `signed_out` | ``Claude Code is not signed in; run `claude` and sign in`` |
| No prompt reached (an unknown screen), or no panel 12 s after `/usage` | `not_ready` | `claude did not reach its prompt (an unknown screen?)` / `no usage panel after /usage` |
| Deadline after the prompt | `timeout` | `claude probe timed out <where>` |
| Status line never ran, no `rate_limits`, or no recognized window | `format_drift` | `Claude: …`, endpoint `claude status line rate_limits`, shape only |
| `claude` cannot start, exits early, probe folder not empty | `unavailable` | the reason, never the screen |
| Windows: a core or cache path the status line cannot name without quoting | `unavailable` | `the status line command cannot name this path without quoting (no 8.3 short name?): <path>` |

Mapping reads the last status line run that carried `rate_limits`. `five_hour` maps to `claude-5h` / `5-hour limit` / `rolling-hours` 5 and `seven_day` to `claude-7d` / `7-day limit` / `rolling-days` 7. A missing or negative `used_percentage` skips the window; the value is never clamped; unit `percentage`, total 100. A positive Unix-second `resets_at` becomes a UTC date, anything else is null. `planTier` and `subscriptionRenewsAt` are always null: the status line carries neither. The synthetic fixtures in `internal/claude/testdata/` are named after the Claude Code version whose shape they follow; the behaviour was seen on 2.1.314-2.1.316.

**Polling.** Every fetch starts a Claude Code session (about 5 s measured on Linux, 6-9 s on a Windows 11 VM, 2.1.302-2.1.316) and adds a line to Claude's `history.jsonl`. The client polls every 30 min by default and never below 5 min on a timer (the adaptive 5 min pace is the floor); a manual refresh reuses a fetch less than 1 min old (`main/refreshPolicy.ts`). The CLI's 25 s default deadline covers the whole fetch; the client keeps its own timeout above it (#180). On Windows the core started by Node's `execFile` with `windowsHide` (as `main/coreClient.ts` does) in the desktop session read both windows and showed no window over 14 s of screenshots taken every ~40 ms (a visible console in a control run was caught in 148 of 189 frames).

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

The read-only handshake is `initialize` → `initialized` → `account/rateLimits/read` (`params: null`); `account/read` (`refreshToken: false`) runs only after `-32600` to distinguish API keys. No login, logout, reset-credit consumption, prompt or analytics opt-in methods/flags are used. Each call has its own 10 s timeout within the CLI deadline (default 25 s). After a response, stdin closes and the child has 3 s to exit before being killed; errors kill it immediately. Fetch always waits for cleanup. Stderr is drained and never logged.

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
| `error` | object, errors only | Stable `code` and readable `message`; format drift also has `endpoint` and `shape`. Optional `reason` (additive, #149): a known case inside the code, so a client can explain it in the user's language without parsing `message`. Known reasons: `not_connected`, `token_rejected` (with `signed_out`); `enterprise_managed_seat`, `keyring_unavailable` (with `unavailable`). A client ignores a reason it does not know. |
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
| `1` | Provider error. Output write failures also return 1 and log the failure. |
| `2` | Usage error: unknown provider/command, bad flags, extra arguments, invalid timeout or secret input. |

With `--json`, usage errors have no provider key. Help is text-only; requesting help with `--json` produces a usage-error document. Without `--json`, help/version retain their plain text behavior.

## Electron client

`main/coreClient.ts` (#177) is the app's side of this contract. For each read it runs the binary with `execFile`: no shell, `windowsHide` (no console window on Windows), `--json`, `--timeout 25s` and a client timeout of 30 s, stdin always closed (the secret written first with `--secret-stdin` when there is one), at most 1 MiB of output. The binary is `AIPERMILER_CORE_BIN` when set (absolute), else `<resources>/core/aipermiler[.exe]` in the package or `core-bin/` of the repository in development. Errors: binary missing or not startable → `CoreBinaryError`; killed at the client timeout → `CoreTimeoutError`; output that is not a v1 document, contradicts its exit code, or a usage error (exit 2, a bug in the arguments) → `CoreOutputError`, never quoting the output; `format_drift` → the same `FormatDriftError` as the TypeScript services, so the drift report works unchanged; other codes → `CoreProviderError` with the code. `version --json` feeds Settings → Diagnostics and the diagnostic report. Codex reads through it since #178 (`main/core.ts`, `main/providers.ts`); Copilot follows in #179.

## Versioning and shared fixtures

Additive fields keep `schemaVersion: 1`; clients must ignore unknown fields. Removing or renaming a field, or changing its meaning, bumps the schema version. A client seeing an unsupported schema version must reject the document explicitly. An unknown error code is treated as `unavailable`, preserving last values as stale.

The language-neutral files in `gocore/contract/v1/` are normative examples. Go CLI black-box tests reproduce every provider-neutral fixture byte for byte through `RunWith`, a fake provider and a fixed clock. The TypeScript client tests (`main/coreClient.test.ts`, #177) read the same files. `version.json` uses `aipermiler`, `dev`, `linux`, `amd64`; its Go test substitutes `buildinfo.Name`, `buildinfo.Version`, `runtime.GOOS` and `runtime.GOARCH` before the byte comparison so build-time versions and cross-platform runs are supported. Every fixture is also checked as valid compact JSON with `schemaVersion: 1` and exactly one trailing newline. `usage-claude` is reproduced by the Claude adapter's own test (`internal/claude`), from its 2.1.316 status line fixture through the mapping. The account documents (`account-connected`, `account-deleted`, `login-authorize`, `raw`) and `error-unavailable-reason` came with #149; the TypeScript client reads them from #179. The documents are written without HTML escaping (a URL keeps a literal `&`).
