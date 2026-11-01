🌐 [English](DEVELOPMENT.md) | **Italiano**

# Sviluppo

> Come compilare AIpermiler dai sorgenti, testarlo e creare i pacchetti. Per cosa fa l'app e come leggerla, vedi il [README](../README.it.md).

## Requisiti

- Node.js 22 LTS o superiore
- npm
- Go (versione in `gocore/go.mod`) per compilare la CLI del core inclusa da `npm start` e dai pacchetti; non serve per `npm test`

## Avvio dai sorgenti

```bash
git clone https://github.com/suppressio/aipermiler.git
cd aipermiler
npm install
npm start
```

`npm start` compila TypeScript (main process + renderer), compila il core Go in `core-bin/` (`scripts/build-core.js`) e avvia Electron. Per usare un core compilato altrove imposta `AIPERMILER_CORE_BIN` al suo percorso assoluto: `npm start` salta la build Go e l'app esegue quel binario. Impostazioni → Diagnostica mostra la versione del core, o perché non può essere eseguito. Al primo avvio non ci sono account: apri le Impostazioni e aggiungine uno.

L'esecuzione dai sorgenti usa la stessa cartella userData dell'app installata, quindi il primo avvio su una macchina con IA Hypermiler (≤ v0.4.18) installato ne sposta le impostazioni e ne cancella la vecchia cartella ([ARCHITETTURA §1](ARCHITECTURE.it.md#1-impostazioni-settings--schema-dati)). Per lasciare intatta una copia installata, avvia con un'altra cartella di configurazione, per esempio `XDG_CONFIG_HOME=$(mktemp -d) npm start` su Linux.

## Build, lint e test

```bash
npm run build      # TypeScript (main + renderer) + asset statici in dist/
npm run lint       # ESLint con regole basate sui tipi (deve restare a zero segnalazioni)
npm run typecheck  # tsc --noEmit su ogni progetto TypeScript
npm test           # build + test unitari (runner integrato node:test)
npm run coverage   # test con la coverage integrata di Node
```

- I **test unitari** girano sempre e non toccano mai la rete (`fetch` e l'SDK sono simulati). I dati di test sono solo sintetici.
- **Nessun test di integrazione** da #179: le letture dei provider sono passate al core Go, i cui adapter sono testati contro dei finti (`httptest`, un finto `codex app-server`). Gli account reali si verificano a mano con il binario del core (`aipermiler usage <provider>`, vedi [CORE-CLI](CORE-CLI.it.md)), mai con credenziali incollate in chat o nei commit.

## Controlli del core Go

Il core Go sta in `gocore/` (modulo a sé, `go.mod` fissa la versione di Go; per ora niente nella build di Electron ne dipende). Servono Go 1.24+ e [golangci-lint](https://golangci-lint.run/) v2. Da `gocore/`:

```bash
gofmt -l .           # non deve stampare nulla
go vet ./...
go test -race ./...
golangci-lint run    # config rigorosa in .golangci.yml, include le regole di import di depguard
```

Il job `go-checks` in `.github/workflows/ci.yml` esegue gli stessi controlli su ogni PR verso `next` e la fa fallire come `lint-test`. Dopo i test, lo step `cross-compile` compila `./cmd/aipermiler` per Linux amd64/arm64, Windows amd64 e macOS arm64/amd64 con `CGO_ENABLED=0`, `-trimpath` e `-ldflags "-s -w"`, stampando ogni dimensione da una directory temporanea. Le regole di import (quale pacchetto può importare quale) sono la sezione `depguard` di `gocore/.golangci.yml`; una violazione è un errore di lint.

`cmd/aipermiler/main.go` chiama soltanto `internal/cli.Run`: un binario con `usage`, `version` e `help` (funzionano anche `-h`/`--help`). `usage <provider>` supporta testo leggibile e il contratto versionato `--json`; `version --json` riporta build e piattaforma del core. Codex è registrato (#148); Copilot segue in #149; `serve` resta lavoro futuro (#31). Gli exit code sono 0 per successo (anche con dati stale), 1 per errori dei provider e 2 per argomenti non validi. `internal/usage` contiene modello indipendente dai provider, documenti JSON, codici di errore, diagnostica con sola struttura e rendering testuale; `internal/provider` contiene l'interfaccia dei provider e le factory. Le fixture JSON normative condivise sono in `gocore/contract/v1/`, verificate byte per byte dai test Go e lette dai test del client TypeScript (`main/coreClient.test.ts`). Vedi [CORE-CLI](CORE-CLI.it.md) per il contratto completo e la gestione dei secret.

`internal/codex` contiene discovery del binario nativo, sonda app-server one-shot e mapping delle quote, con fixture sintetiche da schema e test del ciclo di vita del processo (#148). `internal/copilot` è l'adapter Copilot, con i comandi `token`/`login`/`raw` e il loopback OAuth PKCE (#149), testato contro un finto GitHub (`httptest`); `internal/github` contiene gli host ammessi e il client JSON (10 s per chiamata); `internal/secrets` il keyring dell'OS (`github.com/zalando/go-keyring`, l'unica dipendenza del core) e uno store in memoria per i test. Su Linux il keyring è il Secret Service della sessione desktop (GNOME Keyring, KWallet): senza, Copilot segnala `keyring_unavailable`.

`internal/buildinfo` contiene l'unica costante `Name` (ora `aipermiler`, in attesa di #9) e la `Version` impostata in compilazione (default `dev`). `internal/config` individua/crea la directory di configurazione, `internal/logging` costruisce il logger e `internal/pacing` contiene il modello puro di pacing. Compilazione e avvio da `gocore/`:

```bash
go build -trimpath -ldflags "-s -w -X github.com/suppressio/aipermiler/gocore/internal/buildinfo.Version=dev" ./cmd/aipermiler
./aipermiler version
```

`config.Dir()` unisce `os.UserConfigDir()` e `buildinfo.Name`; `config.Ensure()` la crea con permessi `0o700` (solo proprietario su Unix), senza scrivere file. `AIPERMILER_CONFIG_DIR` sostituisce la directory per test/sviluppo e deve essere un percorso assoluto quando non vuota. `AIPERMILER_LOG_LEVEL` accetta `debug`, `info`, `warn` o `error`, con default `warn`; un valore non valido ripiega su `warn` ed emette un solo warning. I log vanno su stderr; stdout è riservato all'output dei comandi e al contratto JSON (#26). Help/version non creano la directory di configurazione. Le misure delle risorse sono in [EVOLUTION](EVOLUTION.it.md#aggiornamento-2026-10-31-budget-di-risorse-del-core-go-145).

## Packaging

```bash
npm run package       # installer per la piattaforma corrente in release/
npm run package:test  # build di prova locale: lint + test, poi pacchetti .deb e Windows in release/test/
```

- I pacchetti contengono la CLI del core Go: `npm run build:core` (`scripts/build-core.js`, senza cgo, versione da `package.json` o `AIPERMILER_CORE_VERSION`; `--target win32-x64` e simili per la cross-compilazione) scrive `core-bin/aipermiler[.exe]`, e `extraResources` di electron-builder lo copia in `<resources>/core/`. L'app lo esegue con `execFile` (`main/coreClient.ts`, vedi [CORE-CLI](CORE-CLI.it.md)). `core-bin/` contiene un solo binario alla volta, così un core Linux non finisce mai in un pacchetto Windows.
- `npm run package`: build + core + `electron-builder` → `.dmg` su macOS, `.exe`/NSIS su Windows, `.AppImage` e `.deb` su Linux, in `release/`.
- `npm run package:test`: versione `<versione>.dev.<timestamp>` (la stessa per il core), mai pubblicata; il core viene cross-compilato per ogni pacchetto subito prima. L'`.exe` NSIS per Windows richiede Wine (o Windows); senza, uno `.zip` pronto da avviare. È il modo consigliato per provare una modifica su una macchina reale.
- Linux: i centri software leggono i metadati AppStream, non il file `control` del `.deb`. `build/linux/com.suppressio.aipermiler.metainfo.xml` (licenza, autore, descrizione EN + IT, release) viene installato in `/usr/share/metainfo/` dalla configurazione `deb.fpm` in `package.json`; la voce `.desktop` ha `Name=AIpermiler`. Aggiornare le `<release>` del file a ogni versione e validarlo con `appstreamcli validate`.

### Build multipiattaforma (GitHub Actions)

`.github/workflows/build.yml` builda in parallelo su macOS, Windows e Linux dallo stesso commit:

- ogni job installa Go e compila il core sul proprio runner, per il proprio sistema e architettura, prima del packaging;
- un tag `v*` pushato (es. `v0.4.0-beta`) → lint, test, pacchetti allegati alla Release GitHub del tag; i tag `-rc` (es. `v0.4.14-beta-rc.1`) sono esclusi;
- un avvio manuale (`gh workflow run build.yml`, o la tab Actions) → build di prova: pacchetti solo come artifact del run (14 giorni), nessuna release, nessuna notifica di aggiornamento.

`.github/workflows/ci.yml` esegue lint e test su Linux per ogni PR verso `next` e ogni push su `next`.

I pacchetti non sono firmati: macOS Gatekeeper e Windows SmartScreen mostrano un avviso all'installazione.

---

## Struttura del progetto

```
aipermiler/
├── main.ts, preload.ts        ← processo principale Electron + bridge sicuro verso il renderer
├── core/                      ← orchestrazione dei fetch e calcolo degli snapshot, store iniettato, senza Electron (+ test)
├── main/                      ← finestre, tray, OAuth Copilot, logica per provider, client della CLI del core, ritmo dei refresh, i18n del main
├── renderer/                  ← widget e finestra Impostazioni (HTML/CSS/TS vanilla) + i18n dell'interfaccia
├── services/                  ← fetch dell'utilizzo Copilot, sessioni locali di Claude Code, controllo aggiornamenti (+ test)
├── diagnostics/               ← segnalazione dei cambi di formato e file di report diagnostico
├── store/                     ← persistenza locale (electron-store cifrato), default, normalizzazione, migrazione
├── budget.ts                  ← calcolo budget/efficienza/previsionale/grafico (+ budget.test.ts)
├── agents/                    ← agente Claude per i consigli d'uso (stub)
├── types/                     ← tipi TypeScript condivisi
├── tests/                     ← helper dei test e controllo del contratto IPC
├── docs/                      ← documentazione, inglese + italiano (anche questo file) + screenshot del README (dati sintetici)
└── README.md, CLAUDE.md       ← guida utente, memoria di progetto per Claude Code
```

Approfondimenti:

- [`ARCHITECTURE.it.md`](ARCHITECTURE.it.md) — modello dati, finestre, indicatori, estensibilità
- [`PACING.it.md`](PACING.it.md) — il modello di pacing: definizioni, casi limite, invarianti, stato, tabelle di esempi
- [`RESEARCH.it.md`](RESEARCH.it.md) — cosa si può leggere davvero dalle API di Claude, Copilot e Codex, e con quali limiti
- [`EVOLUTION.it.md`](EVOLUTION.it.md) — retrospettiva e direzioni future con analisi costi/benefici
- [`PLAN.it.md`](PLAN.it.md) — il piano originale giorno per giorno
- [`CLAUDE.md`](../CLAUDE.md) — memoria di progetto per Claude Code (in inglese): stack, regole, milestone

## Regole di sicurezza

- `nodeIntegration: false` e `contextIsolation: true` sempre attivi: il renderer non ha mai accesso diretto a Node.js; ogni valore che arriva da lui via IPC viene validato.
- Nessun segreto dei provider nell'app: il token Copilot sta nel keyring dell'OS, gestito dal core Go (#179); Codex non tiene nulla di segreto (letto tramite il core e l'accesso a Codex dell'utente). Il client secret OAuth di Copilot si digita nelle Impostazioni e passa al core su stdin, mai salvato. Nulla arriva al renderer o ai log.
- Gli URL aperti nel browser sono costruiti nel main process da prefissi fissi, mai presi dal renderer.

## Stato del progetto

- ✅ Dati reali da Claude (endpoint interno + sessione) e Copilot (piani personali e licenze aziendali su un tenant `.ghe.com`; una licenza su github.com gestita da un'enterprise non espone dati, vedi `RESEARCH.it.md`)
- ✅ Utilizzo Codex tramite il core Go e `codex app-server` con l'accesso a Codex dell'utente (#148, #178), finestre rolling di 5 ore e settimanale
- ✅ Più account, interfaccia italiano/inglese, controllo aggiornamenti, diagnostica dei cambi di formato, report diagnostico
- ✅ Visualizzazioni proprie: consumo giornaliero vs budget mobile, verdetti per finestra, gauge istantaneo, rating, valore per token
- ✅ TypeScript severo (`noUncheckedIndexedAccess`, `exactOptionalPropertyTypes`, …), ESLint con controllo dei tipi a zero segnalazioni, test unitari su tutti i moduli puri
- ✅ CI multipiattaforma che pubblica le pre-release; build di prova locali
- 🟨 Agente consigli (`agents/advisor.ts`) ancora uno stub
- ✅ Contratto IPC condiviso tra main, preload e renderer (`types/ipc.ts`)
- ✅ Nucleo testabile estratto da `main.ts` (`core/usage.ts`, store e fetch dei provider iniettati)
- ⬜ Prossimi passi: CLI headless sopra il nucleo (docs/EVOLUTION.md 2b)
