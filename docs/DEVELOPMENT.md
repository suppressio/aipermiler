🌐 **English** | [Italiano](DEVELOPMENT.it.md)

# Development

> How to build AIpermiler from source, test it and package it. For what the app does and how to read it, see the [README](../README.md).

## Requirements

- Node.js 22 LTS or later
- npm
- Go (version in `gocore/go.mod`) to build the core CLI that `npm start` and the packages bundle; not needed for `npm test`

## Run from source

```bash
git clone https://github.com/suppressio/aipermiler.git
cd aipermiler
npm install
npm start
```

`npm start` compiles TypeScript (main process + renderer), builds the Go core into `core-bin/` (`scripts/build-core.js`) and starts Electron. To use a core built elsewhere, set `AIPERMILER_CORE_BIN` to its absolute path: `npm start` then skips the Go build and the app runs that binary. Settings → Diagnostics shows the core version, or why it cannot run. On first start there are no accounts: open Settings and add one.

Running from source uses the same userData folder as the installed app, so it reads and writes the installed app's settings. To keep an installed copy untouched, start with another config home, e.g. `XDG_CONFIG_HOME=$(mktemp -d) npm start` on Linux.

## Build, lint and test

```bash
npm run build      # TypeScript (main + renderer) + static assets into dist/
npm run lint       # ESLint with type-aware rules (must stay at zero findings)
npm run typecheck  # tsc --noEmit on every TypeScript project
npm test           # build + unit tests (Node's built-in node:test runner)
npm run coverage   # tests with Node's built-in coverage
```

- **Unit tests** always run and never touch the network (`fetch` and the SDK are mocked). Test data is synthetic only.
- **No integration tests** since #179: the provider reads moved to the Go core, whose adapters are tested against fakes (`httptest`, a fake `codex app-server`). Real accounts are checked by hand with the core binary (`aipermiler usage <provider>`, see [CORE-CLI](CORE-CLI.md)), never with credentials pasted in chat or commits.

## Go core checks

The Go core lives in `gocore/` (its own module, `go.mod` pins the Go version; the packages ship its binary, see *Packaging*). Needs Go 1.26+ and [golangci-lint](https://golangci-lint.run/) v2. From `gocore/`:

```bash
gofmt -l .           # must print nothing
go vet ./...
go test -race ./...
golangci-lint run    # strict config in .golangci.yml, includes the depguard import rules
```

The `go-checks` job in `.github/workflows/ci.yml` runs the same checks on every PR to `next` and fails the PR like `lint-test`. After the tests, its `cross-compile` step builds `./cmd/aipermiler` for Linux amd64/arm64, Windows amd64 and macOS arm64/amd64 with `CGO_ENABLED=0`, `-trimpath` and `-ldflags "-s -w"`, printing each size from a temporary directory. Import rules (which package may import which) are the `depguard` section of `gocore/.golangci.yml`; a violation is a lint failure.

`cmd/aipermiler/main.go` only calls `internal/cli.Run`: one binary with `usage`, `version` and `help` (`-h`/`--help` also work). `usage <provider>` supports readable text and the versioned `--json` contract; `version --json` reports the core build and platform. Codex is registered (#148); Copilot follows in #149; `serve` remains future work (#31). Exit codes are 0 for success (including stale usage), 1 for provider errors and 2 for invalid arguments. `internal/usage` owns the provider-neutral model, JSON documents, error codes, structure-only diagnostics and text rendering; `internal/provider` owns the provider interface and factories. Shared normative JSON fixtures live in `gocore/contract/v1/`, verified byte for byte by Go tests and read by the TypeScript client tests (`main/coreClient.test.ts`). See [CORE-CLI](CORE-CLI.md) for the full contract and secret handling.

`internal/codex` owns native binary discovery, the one-shot app-server probe and quota mapping, with synthetic schema fixtures and process lifecycle tests (#148). `internal/copilot` is the Copilot adapter, its `token`/`login`/`raw` commands and the OAuth PKCE loopback (#149), tested against a fake GitHub (`httptest`); `internal/github` holds the allowed hosts and the JSON client (10 s per call); `internal/secrets` the OS keyring (`github.com/zalando/go-keyring`, the core's only dependency) and an in-memory store for tests. On Linux the keyring is the Secret Service of the desktop session (GNOME Keyring, KWallet): without one, Copilot reports `keyring_unavailable`.

`internal/buildinfo` owns the single `Name` constant (currently `aipermiler`, pending #9) and the build-time `Version` (default `dev`). `internal/config` locates/creates the config directory, `internal/logging` builds the logger, and `internal/pacing` holds the pure pacing model. Build and run from `gocore/`:

```bash
go build -trimpath -ldflags "-s -w -X github.com/suppressio/aipermiler/gocore/internal/buildinfo.Version=dev" ./cmd/aipermiler
./aipermiler version
```

`config.Dir()` joins `os.UserConfigDir()` with `buildinfo.Name`; `config.Ensure()` creates it with mode `0o700` (owner-only on Unix), writing no files. `AIPERMILER_CONFIG_DIR` overrides the directory for tests/development and must be an absolute path when nonempty. `AIPERMILER_LOG_LEVEL` accepts `debug`, `info`, `warn` or `error`, defaulting to `warn`; an invalid value falls back to `warn` and emits one warning. Logs go to stderr; stdout is reserved for command output and the JSON contract (#26). Help/version do not create a config directory. Resource measurements are in [EVOLUTION](EVOLUTION.md#update-2026-10-31-go-core-resource-budget-145).

## Packaging

```bash
npm run package       # installer for the current platform in release/
npm run package:test  # local test build: lint + tests, then .deb and Windows packages in release/test/
```

- The packages carry the Go core CLI: `npm run build:core` (`scripts/build-core.js`, cgo-free, version from `package.json` or `AIPERMILER_CORE_VERSION`; `--target win32-x64` and the like cross-compile) writes `core-bin/aipermiler[.exe]`, and electron-builder `extraResources` copies it to `<resources>/core/`. The app runs it with `execFile` (`main/coreClient.ts`, see [CORE-CLI](CORE-CLI.md)). `core-bin/` holds one binary at a time, so a Linux core never ships in a Windows package.
- `npm run package`: build + core + `electron-builder` → `.dmg` on macOS, `.exe`/NSIS on Windows, `.AppImage` and `.deb` on Linux, in `release/`.
- Main-process bundle (#36): electron-builder's `beforePack` hook (`scripts/bundle-main.js`) replaces `dist/main.js`, `dist/preload.js` and `dist/services/claudeLocalSessions.worker.js` with esbuild bundles (CommonJS, `node24` for Electron 45), and `build.files` packs only those three files and `dist/renderer/`. `date-fns`, `electron-store` and their dependencies live in the bundles, so they are dev dependencies; only `@anthropic-ai/claude-agent-sdk` stays a runtime dependency, external to the bundle (ESM, loaded by a native `import()`). Paths to the renderer, preload and worker come from `main/appDir.ts` (the entry's folder), never from a module's own `__dirname`, which differs between the bundle and the plain tsc output used by `npm start` and the tests. The asar went from 18 MB to 5.6 MB.
- `npm run package:test`: version `<version>.dev.<timestamp>` (the core gets the same), never published; the core is cross-compiled for each package right before it. The Windows NSIS `.exe` needs Wine (or Windows); without it, a ready-to-run `.zip`. This is the preferred way to try a change on a real machine.
- Linux: software centers read AppStream metadata, not the `.deb` control file. `build/linux/com.suppressio.aipermiler.metainfo.xml` (license, author, EN + IT description, releases) is installed to `/usr/share/metainfo/` by the `deb.fpm` config in `package.json`; the `.desktop` entry has `Name=AIpermiler`. Update the file's `<release>` on every version and validate it with `appstreamcli validate`.

### Cross-platform build (GitHub Actions)

`.github/workflows/build.yml` builds on macOS, Windows and Linux in parallel from the same commit:

- every job sets up Go and builds the core on its own runner, for its own OS and architecture, before packaging;
- a pushed `v*` tag (e.g. `v0.4.0-beta`) → lint, tests, packages attached to the GitHub Release of the tag; `-rc` tags (e.g. `v0.4.14-beta-rc.1`) are skipped;
- a manual run (`gh workflow run build.yml`, or the Actions tab) → test build: packages only as run artifacts (14 days), no release, no update notification.

`.github/workflows/ci.yml` runs lint and tests on Linux for every PR to `next` and every push to it.

Packages are not signed: macOS Gatekeeper and Windows SmartScreen show a warning at install time.

---

## Project structure

```
aipermiler/
├── main.ts, preload.ts        ← Electron main process + secure bridge to the renderer
├── core/                      ← fetch orchestration and snapshot computation, store injected, no Electron (+ tests)
├── main/                      ← windows, tray, Copilot OAuth, per-provider logic, core CLI client, refresh pace, main-process i18n
├── renderer/                  ← widget and Settings window (vanilla HTML/CSS/TS) + UI i18n
├── services/                  ← Copilot usage fetch, local Claude Code sessions, update check (+ tests)
├── diagnostics/               ← format-drift report and diagnostic report file
├── store/                     ← local persistence (encrypted electron-store), defaults, normalization, migration
├── budget.ts                  ← budget/efficiency/projection/chart logic (+ budget.test.ts)
├── agents/                    ← Claude agent for usage advice (stub)
├── types/                     ← shared TypeScript types
├── tests/                     ← test helpers and the IPC contract guard
├── docs/                      ← documentation, English + Italian (this file too) + README screenshots (synthetic data)
└── README.md, CLAUDE.md       ← user guide, project memory for Claude Code
```

Further reading:

- [`ARCHITECTURE.md`](ARCHITECTURE.md) — data model, windows, indicators, extensibility
- [`PACING.md`](PACING.md) — the pacing model: definitions, edge cases, invariants, state, example tables
- [`RESEARCH.md`](RESEARCH.md) — what can really be read from the Claude, Copilot and Codex APIs, and with which limits
- [`EVOLUTION.md`](EVOLUTION.md) — retrospective and future directions with a cost/benefit analysis
- [`PLAN.md`](PLAN.md) — the original day-by-day plan
- [`CLAUDE.md`](../CLAUDE.md) — project memory for Claude Code: stack, rules, milestones

## Security rules

- `nodeIntegration: false` and `contextIsolation: true` always: the renderer never has direct access to Node.js; every value coming from it through IPC is validated.
- No provider secret in the app: the Copilot token lives in the OS keyring, owned by the Go core (#179); Codex keeps nothing secret (read through the core and the user's own Codex sign-in). The Copilot OAuth client secret is typed in Settings and handed to the core on stdin, never saved. Nothing reaches the renderer or logs. They never reach the renderer or logs.
- URLs opened in the browser are built in the main process from fixed prefixes, never taken from the renderer.

## Project status

- ✅ Real data from Claude (internal endpoint + session) and Copilot (personal plans, and company seats on a `.ghe.com` tenant; a github.com seat managed by an enterprise exposes no data, see `RESEARCH.md`)
- ✅ Codex usage through the Go core and `codex app-server` with the user's own Codex sign-in (#148, #178), rolling 5-hour and weekly windows
- ✅ Several accounts, English/Italian UI, update check, format-drift diagnostics, diagnostic report
- ✅ Own visualizations: daily consumption vs moving budget, window verdicts, instant gauge, rating, value per token
- ✅ Strict TypeScript (`noUncheckedIndexedAccess`, `exactOptionalPropertyTypes`, …), type-aware ESLint at zero findings, unit tests on every pure module
- ✅ Cross-platform CI publishing pre-releases; local test builds
- 🟨 Advice agent (`agents/advisor.ts`) still a stub
- ✅ Shared IPC contract between main, preload and renderer (`types/ipc.ts`)
- ✅ Testable core extracted from `main.ts` (`core/usage.ts`, store and provider fetch injected)
- ⬜ Next: headless CLI on top of the core (docs/EVOLUTION.md 2b)
