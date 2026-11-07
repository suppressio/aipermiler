🌐 [English](CORE-CLI.md) | **Italiano**

# CLI del core Go

> Implementato in #26: contratto JSON v1 indipendente dal trasporto e output per terminale del livello 1 di #139. Adapter Codex implementato in #148, Copilot in #149, Claude in #147 (#219-#221, Windows #223).

## Comandi e ambito

```text
aipermiler usage <provider> [--json] [--timeout <duration>] [--secret-stdin] [provider flags]
aipermiler token set <provider> [--json] [provider flags]      # token su stdin
aipermiler token delete <provider> [--json] [provider flags]
aipermiler login <provider> [--json] [--timeout 5m] [provider flags] [login flags]   # client secret su stdin
aipermiler raw <provider> [provider flags]
aipermiler version [--json]
aipermiler paths [--json]
aipermiler help
```

La CLI recupera i dati su richiesta e termina. Non ci sono server, porte, daemon, store condiviso o snapshot del pacing. #31 servirà gli stessi documenti via HTTP; #177 aggiunge il client TypeScript. Claude (#147), Codex (#148) e Copilot (#149) sono registrati; `token`, `login` e `raw` sono di Copilot (comandi per l'account, sotto). Un provider sconosciuto come `aipermiler usage nope` fallisce con exit 2, `Unknown provider: nope`. I test registrano un provider sintetico chiamato `fake`.

Mettere il provider prima dei suoi flag. Argomenti posizionali inattesi e flag sconosciuti sono errori di utilizzo.

| Flag comune | Comportamento |
|---|---|
| `--json` | Scrive su stdout un documento JSON compatto seguito da un solo newline; stderr contiene solo log. È un flag booleano standard (`--json=false` vuol dire testo, vale l'ultima occorrenza); per un'invocazione che fallisce prima o durante il parsing dei flag è riconosciuto ovunque negli argomenti, così anche l'errore di uso è un documento JSON. |
| `--timeout <duration>` | Deadline di Fetch, default `25s`; deve essere maggiore di zero e al massimo `2m`. Usa le durate Go, ad esempio `50ms`, `10s`. I provider devono rispettare la deadline del context. |
| `--secret-stdin` | Legge stdin solo se il provider accetta secret. Massimo 64 KiB incluso il newline finale; rimuove un solo CRLF o LF finale, conservando gli altri spazi. Input vuoto, errori di lettura e input troppo grande sono errori di utilizzo. |

I secret sono accettati solo su stdin, mai come flag: argv è visibile nell'elenco dei processi. I parametri dell'account vanno nei flag del provider, le credenziali no. Senza `--secret-stdin`, stdin non viene letto e Fetch riceve un secret vuoto. La CLI oscura il secret fornito nei messaggi di errore del provider e nelle motivazioni stale; i provider devono escludere le credenziali da tutti i campi di utilizzo restituiti, dalle etichette degli endpoint diagnostici e dai messaggi.

## Provider

### Claude

```text
aipermiler usage claude [--json] [--claude-bin /percorso/assoluto/di/claude]
```

Le quote arrivano solo attraverso il binario `claude` dell'utente, non modificato, e il suo login (#139, #140): il core non legge mai le credenziali di Claude e non fa mai login. L'approccio è stato deciso in #183 e #218 ([spike](SPIKE-CLAUDE-USAGE-PTY.it.md)). Linux, macOS e Windows (#223). Claude non accetta `--secret-stdin`.

Una lettura:

1. `claude auth status --json` (5 s). Si leggono solo `loggedIn`, `authMethod` e `projectsDirectory`, mai email od organizzazione. `loggedIn: false` è `signed_out` e `authMethod` `api_key` o `third_party` (una API key, Bedrock, Vertex: nessuna quota di abbonamento) è `unavailable`, entrambi senza avviare una sessione (#228); `claude.ai`, `oauth_token` (`claude setup-token`) e valori sconosciuti proseguono; un output inutilizzabile lascia il controllo allo schermo.
2. `claude` parte in uno pseudo-terminale (50×140, `TERM=xterm-256color`; su Windows una ConPTY su `golang.org/x/sys/windows`, il `claude.exe` nativo avviato direttamente, sospeso, messo in un job object con `KILL_ON_JOB_CLOSE`, poi ripreso, mai con `CREATE_NO_WINDOW`, che rompe la ConPTY) dentro la cartella di probe del core, `<user cache>/aipermiler/claude-probe`, che deve essere vuota. I flag sono `--settings` con il nostro comando di status line, `--strict-mcp-config` e `--setting-sources local`. Nelle impostazioni dell'utente non viene scritto niente, e server MCP, hook e plugin dell'utente restano fuori. Le variabili d'ambiente lasciate da una sessione Claude Code genitore (`CLAUDECODE`, `CLAUDE_CODE_ENTRYPOINT`, `CLAUDE_CODE_SESSION_ID` e simili, un elenco fisso) vengono rimosse. Tutte le altre restano, `CLAUDE_CODE_*` comprese (provider, accesso, gateway, mTLS, percorso di Git Bash), così il core legge lo stesso accesso del Claude Code dell'utente (#228).
3. Al prompt di fiducia della cartella si risponde una volta sola, e solo per quella cartella vuota. Il core digita solo `/usage`, Esc e `/exit`, mai un prompt, quindi nessuna richiesta al modello sul piano dell'utente. Alla domanda di Claude Code su una API key nel suo ambiente (*Detected a custom API key*) non si risponde mai: la risposta predefinita è No, e qualunque Invio digitato lì salverebbe quella risposta nel Claude Code dell'utente, quindi il core la lascia com'è e chiude la sessione (#228). Per lo stesso motivo l'Invio dopo `/usage` parte solo quando il prompt mostra il `/usage` digitato: una schermata ferma che non lo mostra è una finestra sconosciuta, e il probe finisce `not_ready` senza confermare niente.
4. Claude esegue il comando di status line. È il sottocomando nascosto `aipermiler claude-statusline <file>`, che aggiunge come una riga il JSON ricevuto su stdin a un file `<user cache>/aipermiler/claude-status-*.jsonl` creato per questa lettura. Non crea mai un file e non stampa niente. Su Linux e macOS Claude esegue il comando con `sh`, quindi i due percorsi sono tra apici singoli. Su Windows lo esegue con Windows PowerShell, oppure con Git Bash quando è installato Git for Windows (entrambi verificati su Windows 11 con 2.1.302 e 2.1.316), e i due quotano in modo diverso: i percorsi vanno senza virgolette, le cartelle come nomi brevi 8.3 con slash in avanti (`C:/Users/MARIOR~1/…/aipermiler.exe`), che PowerShell, bash e cmd leggono tutti come la stessa parola. Un percorso che contiene ancora un carattere diverso da lettere e cifre ASCII e `:/._-~` (un volume senza nomi brevi) è `unavailable`. Chiuso il pannello con Esc, il core aspetta fino a 3 s un'esecuzione che porti `rate_limits`.
5. `/exit` ha 5 s, poi viene ucciso l'intero albero di processi (il process group su POSIX, il job su Windows). Il probe ha un limite di 25 s.
6. Pulizia, anche dopo un probe fallito: si cancella il file del sink e anche il transcript del probe stesso. È il `transcript_path` di un'esecuzione del status line il cui `cwd` è la cartella di probe, solo un `.jsonl` direttamente dentro una cartella di `projectsDirectory` (percorsi confrontati come file, non come stringhe). Si rimuove anche la cartella, se non resta altro dentro. Senza `projectsDirectory` non si cancella niente. Una lettura cancella anche i file del sink di letture precedenti interrotte (un core ucciso), se fermi da 10 min (#229). Nient'altro di Claude viene toccato: la riga di `/usage` in `history.jsonl` resta.

Ordine di ricerca (#219):

1. `--claude-bin` esplicito: assoluto e nativo, altrimenti `unavailable`, senza fallback.
2. `claude` (`claude.exe` su Windows) nelle voci del PATH, in ordine. Su Windows ogni voce viene guardata anche come prefisso npm: `<voce>\node_modules\@anthropic-ai\claude-code\bin\claude.exe`, il binario che esegue il suo shim `claude.cmd` (con Node da winget il prefisso è la cartella di Node stessa, non `%APPDATA%\npm`; verificato).
3. `~/.local/bin/claude`.
4. Prefissi fissi: su macOS `/opt/homebrew/bin/claude`, `/usr/local/bin/claude`, poi il pacchetto npm sotto `/usr/local` e `/opt/homebrew`; su Linux il pacchetto npm sotto `/usr/local/lib` e `/usr/lib`; su Windows il pacchetto npm sotto `%APPDATA%\npm` e il pacchetto winget sotto `%LOCALAPPDATA%`.
5. Il binario nativo dell'estensione Claude Code di VS Code, VS Code Insiders, Cursor, Windsurf e VS Code Server, dalla versione più recente.

Valgono solo binari nativi: almeno 1 MB, mai `.js`/`.cjs` (Node non viene mai eseguito), `.exe` su Windows, eseguibili altrove. I symlink vengono risolti e i duplicati saltati. npm 11 non esegue più gli script di installazione di default: senza `--allow-scripts=@anthropic-ai/claude-code` il pacchetto resta con il suo piccolo stub al posto del binario nativo, e quell'installazione viene saltata (verificato su Windows con npm 11.19). Su Windows 11 le quattro installazioni sono state trovate insieme e ognuna ha letto entrambe le finestre: winget, npm, native installer, estensione VS Code.

| Condizione | Codice | Significato/messaggio |
|---|---|---|
| Binario non trovato | `unavailable` | `Claude Code not found; install it or set --claude-bin` |
| Connesso con una API key o un cloud provider | `unavailable` | `Claude Code is signed in with an API key or a cloud provider, which has no subscription quota` |
| Claude Code chiede se usare una API key presente nel suo ambiente | `unavailable` | ``Claude Code asks whether to use the API key in its environment; answer it in claude, then retry`` |
| Non connesso (`auth status`, o la schermata di login/tema) | `signed_out` | ``Claude Code is not signed in; run `claude` and sign in`` |
| Prompt non raggiunto (schermata sconosciuta), `/usage` non mostrato 3 s dopo averlo digitato, o nessun pannello 12 s dopo `/usage` | `not_ready` | `claude did not reach its prompt (an unknown screen?)` / `claude did not show /usage at its prompt (an unknown screen?)` / `no usage panel after /usage` |
| Scadenza dopo il prompt | `timeout` | `claude probe timed out <where>` |
| Status line mai eseguito, nessun `rate_limits` o nessuna finestra riconosciuta | `format_drift` | `Claude: …`, endpoint `claude status line rate_limits`, solo la forma |
| `claude` non parte, esce prima, cartella di probe non vuota | `unavailable` | il motivo, mai lo schermo |
| Windows: un percorso del core o della cache che lo status line non può nominare senza virgolette | `unavailable` | `the status line command cannot name this path without quoting (no 8.3 short name?): <path>` |

Il mapping legge l'ultima esecuzione del status line che portava `rate_limits`. `five_hour` diventa `claude-5h` / `5-hour limit` / `rolling-hours` 5 e `seven_day` diventa `claude-7d` / `7-day limit` / `rolling-days` 7. Un `used_percentage` mancante o negativo salta la finestra; il valore non viene mai limitato; unità `percentage`, totale 100. Un `resets_at` positivo in secondi Unix diventa una data UTC, altrimenti null. `planTier` e `subscriptionRenewsAt` sono sempre null: lo status line non porta nessuno dei due. Le fixture sintetiche in `internal/claude/testdata/` portano il nome della versione di Claude Code di cui seguono la forma; il comportamento è stato visto su 2.1.314-2.1.316.

**Polling.** Ogni lettura avvia una sessione Claude Code (circa 5 s misurati su Linux, 6-9 s su una VM Windows 11, 2.1.302-2.1.316) e aggiunge una riga a `history.jsonl` di Claude. Il client legge ogni 30 min di default e mai sotto i 5 min col timer (il ritmo adattivo di 5 min è il minimo); un aggiornamento manuale riusa una lettura di meno di 1 min (`main/refreshPolicy.ts`). La scadenza di default della CLI, 25 s, copre l'intera lettura; il client tiene il proprio timeout sopra (#180). Su Windows il core avviato da `execFile` di Node con `windowsHide` (come fa `main/coreClient.ts`) nella sessione desktop ha letto entrambe le finestre e non ha mostrato nessuna finestra in 14 s di screenshot presi ogni ~40 ms (una console visibile, in una prova di controllo, è stata vista in 148 frame su 189).

### Codex

```text
aipermiler usage codex [--json] [--codex-bin /percorso/assoluto/a/codex]
```

`--codex-bin` seleziona un binario nativo esplicito; un percorso relativo o un binario assente/non eseguibile restituisce `unavailable`, senza fallback. Codex non accetta `--secret-stdin`: usa il login dell'utente nella CLI o nell'estensione. Il core esegue solo `codex app-server`, una volta per fetch, senza shell. Eredita l'ambiente invariato (incluso `CODEX_HOME`); Aipermiler non legge nulla sotto `CODEX_HOME`, nemmeno `auth.json`. Su Windows gira nella sessione di login dell'utente senza finestra console, per mantenere accessibili le credenziali nel keyring.

Ordine di discovery:

1. `--codex-bin` esplicito.
2. `codex` nativo (`codex.exe` su Windows) nelle directory assolute del PATH, in ordine; su Windows salta gli shim `.cmd`, `.ps1` e senza estensione.
3. Prefissi fissi macOS: `/opt/homebrew/bin/codex`, poi `/usr/local/bin/codex`.
4. Percorsi globali npm: `NPM_CONFIG_PREFIX`, poi `npm_config_prefix` (`lib/node_modules` su Unix, `node_modules` su Windows); Windows `APPDATA/npm/node_modules`; Linux `/usr/lib/node_modules`, `/usr/local/lib/node_modules`, `~/.npm-global/lib/node_modules`; macOS `/usr/local/lib/node_modules`, `/opt/homebrew/lib/node_modules`.
5. Estensione `openai.chatgpt-<version>[-<platform>]` con versione numericamente più recente e binario per l'OS corrente, tra `~/.vscode`, `.vscode-server`, `.vscode-insiders`, `.cursor`, `.windsurf`, `.vscode-oss` (`extensions/` sotto ciascuna). Usa `bin/linux-<arch>`, `bin/windows-<arch>`, oppure su macOS `bin/macos-<arch>` e poi `bin/darwin-<arch>`, con `x86_64`/`aarch64`.

Su Unix, un candidato risolto in `@openai/codex/bin/codex.js` viene sostituito con il binario nativo nel `node_modules/@openai/<native-package>/vendor/<triple>/bin/` dello stesso pacchetto; se manca il nativo, salta il candidato. Controlla gli header eseguibili (ELF, Mach-O o PE per l’OS corrente); salta script shell e altri shim. Non avvia né `node` né lo shim JS. La discovery con Homebrew e su Apple Silicon richiede ancora verifica su macchine reali (#174).

Handshake di sola lettura: `initialize` → `initialized` → `account/rateLimits/read` (`params: null`); chiama `account/read` (`refreshToken: false`) solo dopo `-32600` per distinguere le API key. Nessun metodo/flag di login, logout, consumo di reset credit, prompt o attivazione analytics. Ogni chiamata ha un timeout di 10 s entro la deadline CLI (default 25 s). Dopo la risposta chiude stdin e concede al figlio 3 s per terminare, poi lo uccide; in caso di errore lo uccide subito. Fetch attende sempre la pulizia. Stderr viene svuotato e mai loggato.

| Condizione | Codice | Significato/messaggio |
|---|---|---|
| Binario non trovato | `unavailable` | `Codex CLI or VS Code extension not found; install it or set --codex-bin` |
| Non connesso (`-32600`, senza API key) | `signed_out` | `Sign in with Codex (CLI or extension)` |
| Login con API key | `unavailable` | `Codex is signed in with an API key, which has no subscription quota` |
| Offline/errore RPC interno (`-32603`) | `unavailable` | `codex app-server: <rpc message>`; nessun dato fresco o risultato parziale |
| Deadline della chiamata o del chiamante | `timeout` | Richiesta scaduta; processo raccolto prima del ritorno |
| Struttura quota assente/nessuna finestra riconosciuta | `format_drift` | `Codex: no quota window recognized`, endpoint `codex app-server account/rateLimits/read`, sola shape |
| Altri errori RPC / uscita anticipata del processo | `unavailable` | Codice e messaggio RPC / exit code del processo; mai stderr |

Il mapping seleziona `rateLimitsByLimitId["codex"]` quando presente, altrimenti `rateLimits` (lo schema richiede comunque `rateLimits`). Gli altri bucket sono ignorati: il loro significato per piano non è verificato. Ignora anche `individualLimit`, `credits`, `rateLimitResetCredits` e `rateLimitUpsell`. Le fixture sintetiche in `internal/codex/testdata/` seguono gli schemi generati da codex 0.163.0; quella con shape sconosciuta viola intenzionalmente lo schema per provare il drift.

Le finestre sono riconosciute dalla durata in `primary` o `secondary`; in caso di ID duplicato vince la prima. Ore intere positive sotto un giorno diventano `codex-<n>h`, `<n>-hour limit`, `rolling-hours`; giorni interi diventano `codex-<n>d`, `<n>-day limit`, `rolling-days`. Le finestre abituali restano quindi `codex-5h` / `5-hour limit` e `codex-7d` / `7-day limit`, preservando lo storico. Le altre durate sono saltate. `usedPercent` deve essere un numero non negativo e non viene mai limitato a 100; unità `percentage`, totale 100. `resetsAt` positivo in secondi Unix diventa una data UTC; reset assente/null/non positivo resta null. `planTier` è il `planType` del bucket scelto o null; `subscriptionRenewsAt` è sempre null.

### Copilot

`aipermiler usage copilot --account <id> [--host github.com|<tenant>.ghe.com] [--scope personal|organization] [--manual-quota <crediti>]`, port di `services/copilot.ts` (#149).

- **Il token sta nel keyring dell'OS** (deciso su #179; libreria `github.com/zalando/go-keyring`): Gestione credenziali di Windows, Portachiavi di macOS (tramite `/usr/bin/security` con il segreto sul suo stdin), Secret Service su Linux. Servizio `aipermiler`, voce `copilot/<id account>`. Non passa mai da argv, dallo stdin di `usage` o da un documento JSON. Non c'è ripiego su file: un keyring irraggiungibile (per esempio una sessione Linux senza Secret Service, o un logon SSH su Windows senza la sessione di logon dell'utente, come per Codex in #158) è `unavailable` con reason `keyring_unavailable`.
- `--account` è obbligatorio (lettere, cifre, `.`, `_`, `-`, al massimo 128): dà il nome alla voce del keyring. `--host` deve normalizzarsi in `github.com` o `<tenant>.ghe.com` (il token va solo lì: API su `api.<host>`, OAuth su `<host>/login/oauth`). Flag non validi sono errori di uso (exit 2), prima di qualsiasi accesso a keyring o rete.
- **Fonti**, come nel servizio TypeScript: `copilot_internal/user` (l'endpoint non documentato dietro l'indicatore di quota di VS Code) per entrambi gli ambiti; per l'ambito personale, quando non ha quote, il report di billing (`ai_credit/usage`, poi `premium_request/usage` su un 404 o un 400 "Unable to get billing usage data") con `--manual-quota` come totale. Ogni richiesta ha un timeout di 10 s.
- **Finestre**: una per categoria di `quota_snapshots`, nell'ordine delle chiavi della risposta, id = la chiave della categoria, etichetta `Copilot — <chiave>`, `billing-cycle` di 1 mese, regole di VS Code (illimitata → crediti usati senza totale, niente se sono zero; entitlement > 0 → entitlement − rimanenti sull'entitlement; entitlement 0 → niente; altrimenti la percentuale). Report di billing → `ai_credits` / `AI credits`, conteggio, reset e rinnovo il 1° del mese UTC successivo. Stessi id di prima, così lo storico salvato continua a combaciare.
- **Errori**: nessun token salvato → `signed_out`, reason `not_connected`; GitHub risponde 401 → `signed_out`, reason `token_rejected`; licenza github.com gestita da un'enterprise (nessun dato, #7) → `unavailable`, reason `enterprise_managed_seat`; nessuna categoria riconosciuta o niente `usageItems` → `format_drift` con la shape.

## Percorsi

`paths` stampa le cartelle che un client deve conoscere (#180): oggi `claudeProbe`, la cartella in cui gira ogni probe di Claude (`<user cache>/aipermiler/claude-probe`, che questo comando non crea). Un client che legge le sessioni di Claude Code (gli insight locali dell'app) salta le sessioni con `cwd` in quella cartella: sono le letture `/usage` del core, non il lavoro dell'utente.

```json
{"schemaVersion":1,"claudeProbe":"/home/synthetic/.cache/aipermiler/claude-probe"}
```

Output testuale: `claude probe: <percorso>`. Argomenti in più sono errori d'uso (exit 2); una cartella di cache utente che non si trova è `unavailable` (exit 1).

## Comandi per l'account

Gestiscono nel core le credenziali dell'account di un provider (per ora solo Copilot). I loro documenti seguono le stesse regole degli altri (un documento compatto per riga, `schemaVersion` 1, documenti di errore ed exit code come per `usage`).

- `token set <provider>`: legge un token (un PAT GitHub) su stdin, lo verifica con `GET /user`, lo salva nel keyring sotto `--account` e stampa `{"schemaVersion":1,"provider":"copilot","username":"<login>"}`. Un token rifiutato non viene mai salvato. È anche il modo in cui Electron consegna il token che teneva prima di #179.
- `token delete <provider>`: rimuove il token dell'account (disconnessione, rimozione dell'account); cancellare un token assente non è un errore. Stampa `{"schemaVersion":1,"provider":"copilot","deleted":true}`.
- `login <provider> --client-id <id> [--locale en|it]`: accesso con OAuth App, Authorization Code + PKCE sul loopback `http://127.0.0.1:8123/callback` (la callback registrata nella GitHub OAuth App dell'utente; port di `main/copilot-oauth.ts`). Il **client secret** si legge su stdin. Il core non avvia browser: con `--json` stampa prima l'evento `{"schemaVersion":1,"provider":"copilot","event":"authorize","url":"<URL di autorizzazione>"}` che il chiamante apre (Electron: `shell.openExternal`), poi attende (default `--timeout 5m`, al massimo `15m`); dopo la callback scambia il codice (10 s), verifica il token con `GET /user`, lo salva e stampa il documento con `username`. Quindi stdout porta **due** documenti, uno per riga. Senza `--json` stampa l'URL da aprire, poi `Copilot: connected as <login>`. La pagina del browser dopo la callback è nella lingua di `--locale`. Accesso negato, state falsificato o scambio fallito → `unavailable`; nessuna autorizzazione in tempo → `timeout`; porta 8123 occupata → `unavailable`.
- `raw <provider>`: la risposta grezza di utilizzo del provider, **con i suoi valori**, per il report di diagnosi (il client la redige prima di scrivere il file): `{"schemaVersion":1,"provider":"copilot","endpoint":"{api}/copilot_internal/user","response":{…}}`. Sempre JSON.

## Output testuale

L'output predefinito è per persone e agenti nel terminale, **non è un contratto stabile**. Una riga per finestra, ad esempio:

```text
Codex · 5h 21% · resets 22:10
Copilot · Monthly credits 250/1000 (25%) · resets 2026-11-30 22:10
```

Le finestre rolling usano `<n>h` o `<n>d`; quelle billing usano la propria etichetta. Le finestre percentuali contengono già percentuali; quelle count mostrano `used/total (percentuale%)`, oppure solo `used` se total è null. Le percentuali si arrotondano all'intero più vicino. Un totale zero mostra `used/0` senza percentuale indefinita. I reset entro le prossime 24 ore usano `HH:mm` locale; a esattamente 24 ore, dopo, o nel passato usano `YYYY-MM-DD HH:mm`. Senza reset la parte corrispondente è omessa. I valori stale aggiungono ` · stale` a ogni riga. Zero finestre producono `<Display> · no quota windows` (con suffisso anche quando stale). Gli errori testuali dei provider vanno su stderr in una riga `<provider>: <message>`; gli errori di utilizzo stampano anche l'help.

## Contratto JSON v1

Tutte le date sono stringhe UTC RFC 3339 con `Z`, alla precisione del secondo. Tutte le chiavi di `RawAccountUsage` e `QuotaWindow` sono sempre presenti: i campi nullable sconosciuti sono `null`, mai omessi. `quotaWindows` è sempre un array, incluso `[]`. Nomi dei campi JSON, valori enum e codici di errore sono indipendenti dalla lingua; i client non devono interpretare etichette o messaggi leggibili.

### Campi dell'envelope

| Campo | Tipo / presenza | Significato |
|---|---|---|
| `schemaVersion` | intero, sempre `1` | Versione del contratto, indipendente dalla versione del binario. |
| `provider` | stringa, solo successo/errore provider | ID del provider registrato; omesso negli errori di utilizzo. |
| `fetchedAt` | stringa ISO, solo successo | Orologio della CLI subito dopo il ritorno di Fetch, inclusi i risultati stale; non è il timestamp originale di freschezza dei valori in cache. |
| `stale` | booleano, solo successo | Il provider ha restituito gli ultimi valori noti invece di dati freschi. |
| `staleReason` | `{code, message}`, solo quando stale | Motivo del fallback, con i codici di errore dei provider. Qui non ci sono endpoint o shape. |
| `usage` | `RawAccountUsage`, solo successo | Dati del provider, senza pacing o cronologia. |
| `error` | oggetto, solo errori | `code` stabile e `message` leggibile; format drift ha anche `endpoint` e `shape`. `reason` facoltativo (additivo, #149): un caso noto dentro il codice, così un client può spiegarlo nella lingua dell'utente senza analizzare `message`. Reason note: `not_connected`, `token_rejected` (con `signed_out`); `enterprise_managed_seat`, `keyring_unavailable` (con `unavailable`). Un client ignora una reason che non conosce. |
| `name`, `version`, `os`, `arch` | stringhe, solo documento version | Identità del binario, versione di build (default `dev`), OS e architettura Go. |

### Campi usage e quota

| Oggetto / campo | Tipo | Significato |
|---|---|---|
| usage.`planTier` | stringa o null | Nome del piano del provider. |
| usage.`subscriptionRenewsAt` | stringa ISO o null | Rinnovo dell'abbonamento del provider. |
| usage.`quotaWindows` | array di finestre quota | Limiti riportati, eventualmente vuoto. |
| window.`id` | stringa | Identificatore della finestra. |
| window.`label` | stringa | Etichetta da mostrare. |
| window.`periodType` | `rolling-hours`, `rolling-days`, `billing-cycle` | Modello di reset. |
| window.`periodLength` | numero o null | Ore, giorni o mesi per i cicli billing; null se sconosciuto. |
| window.`unit` | `percentage` o `count` | Come leggere used/total. |
| window.`used` | numero | Già percentuale per le finestre percentage; consumo assoluto per quelle count. |
| window.`total` | numero o null | Quota totale quando nota. |
| window.`resetsAt` | stringa ISO o null | Data di reset della finestra. |

### Esempi completi dei documenti

Sono le fixture normative su singola riga in `gocore/contract/v1/`; quella directory non richiede README né package Go. Il provider `fake` e tutti i valori sono sintetici.

#### Successo con finestre percentuali

```json
{"schemaVersion":1,"provider":"fake","fetchedAt":"2026-10-31T21:10:00Z","stale":false,"usage":{"planTier":"plus","subscriptionRenewsAt":null,"quotaWindows":[{"id":"five-hour","label":"5 hours","periodType":"rolling-hours","periodLength":5,"unit":"percentage","used":21,"total":100,"resetsAt":"2026-10-31T22:10:00Z"},{"id":"weekly","label":"7 days","periodType":"rolling-days","periodLength":7,"unit":"percentage","used":42,"total":100,"resetsAt":"2026-11-07T21:10:00Z"}]}}
```

#### Successo con una finestra count

```json
{"schemaVersion":1,"provider":"fake","fetchedAt":"2026-10-31T21:10:00Z","stale":false,"usage":{"planTier":null,"subscriptionRenewsAt":"2026-11-30T21:10:00Z","quotaWindows":[{"id":"credits","label":"Monthly credits","periodType":"billing-cycle","periodLength":1,"unit":"count","used":250,"total":1000,"resetsAt":"2026-11-30T21:10:00Z"}]}}
```

#### Successo con gli ultimi valori noti

```json
{"schemaVersion":1,"provider":"fake","fetchedAt":"2026-10-31T21:10:00Z","stale":true,"staleReason":{"code":"unavailable","message":"Provider is temporarily unavailable"},"usage":{"planTier":"plus","subscriptionRenewsAt":null,"quotaWindows":[{"id":"five-hour","label":"5 hours","periodType":"rolling-hours","periodLength":5,"unit":"percentage","used":21,"total":100,"resetsAt":"2026-10-31T22:10:00Z"},{"id":"weekly","label":"7 days","periodType":"rolling-days","periodLength":7,"unit":"percentage","used":42,"total":100,"resetsAt":"2026-11-07T21:10:00Z"}]}}
```

#### Sessione non autenticata

```json
{"schemaVersion":1,"provider":"fake","error":{"code":"signed_out","message":"Sign in required"}}
```

#### Dati non ancora pronti

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

#### Provider non disponibile

```json
{"schemaVersion":1,"provider":"fake","error":{"code":"unavailable","message":"Provider is temporarily unavailable"}}
```

#### Errore negli argomenti

```json
{"schemaVersion":1,"error":{"code":"usage","message":"Unknown provider: nope"}}
```

#### Versione

```json
{"schemaVersion":1,"name":"aipermiler","version":"dev","os":"linux","arch":"amd64"}
```

### Codici di errore e comportamento del client

| Codice | Significato | Azione del client |
|---|---|---|
| `signed_out` | Nessuna sessione autenticata utilizzabile. | Mostrare lo stato di accesso, non un errore generico. |
| `not_ready` | Il provider non ha ancora prodotto i dati di utilizzo. | Riprovare più tardi. |
| `timeout` | Deadline del recupero scaduta, incluse le deadline context incapsulate. | Conservare gli ultimi valori come stale e riprovare più tardi. |
| `format_drift` | La struttura della risposta non è più riconosciuta. | Proporre un report drift con solo endpoint e shape, mai i valori della risposta. |
| `unavailable` | Altro errore del provider o della rete. | Conservare gli ultimi valori come stale e riprovare più tardi. |
| `usage` | Invocazione CLI non valida; solo documenti con exit 2. | Correggere gli argomenti prima di riprovare. |

`shape` conserva i nomi ordinati dei campi e i nomi dei tipi (`string`, `number`, `boolean`, `null`), sostituisce gli array con un elemento rappresentativo (oppure `[]`) e usa `truncated` oltre la profondità 4. Non contiene mai i valori della risposta. I provider passano il JSON decodificato attraverso `usage.ExtractShape` e devono fornire messaggi ed etichette degli endpoint sicuri, senza ID degli account o credenziali.

### Exit code

| Exit | Significato |
|---|---|
| `0` | Successo, inclusi valori stale, help e version. |
| `1` | Errore del provider. Anche gli errori di scrittura dell'output restituiscono 1 e registrano il problema. |
| `2` | Errore di utilizzo: provider/comando sconosciuto, flag errati, argomenti extra, timeout o input secret non validi. |

Con `--json`, gli errori di utilizzo non hanno la chiave provider. L'help è solo testuale; richiederlo con `--json` produce un documento di errore di utilizzo. Senza `--json`, help/version conservano il comportamento testuale.

## Client Electron

`main/coreClient.ts` (#177) è il lato app di questo contratto. Per ogni lettura esegue il binario con `execFile`: niente shell, `windowsHide` (nessuna finestra di console su Windows), `--json`, `--timeout 25s` e un timeout del client di 30 s, stdin sempre chiuso (con il segreto scritto prima, con `--secret-stdin`, quando c'è), al massimo 1 MiB di output. Il binario è `AIPERMILER_CORE_BIN` se impostato (assoluto), altrimenti `<resources>/core/aipermiler[.exe]` nel pacchetto o `core-bin/` del repository in sviluppo. Errori: binario assente o non avviabile → `CoreBinaryError`; ucciso al timeout del client → `CoreTimeoutError`; output che non è un documento v1, che contraddice il proprio exit code, o un errore di uso (exit 2, un bug negli argomenti) → `CoreOutputError`, senza mai citare l'output; `format_drift` → lo stesso `FormatDriftError` dei servizi TypeScript, così la segnalazione di drift funziona senza modifiche; gli altri codici → `CoreProviderError` con il codice. `version --json` alimenta Impostazioni → Diagnostica e il report di diagnosi. Codex legge da qui da #178 (`main/core.ts`, `main/providers.ts`), Copilot da #179 e Claude da #180; `paths --json` dà agli insight locali la cartella dei probe di Claude da saltare.

## Versionamento e fixture condivise

I campi aggiuntivi mantengono `schemaVersion: 1`; i client devono ignorare i campi sconosciuti. Rimuovere o rinominare un campo, o cambiarne il significato, incrementa la versione dello schema. Un client che vede una versione dello schema non supportata deve rifiutare esplicitamente il documento. Un codice di errore sconosciuto viene trattato come `unavailable`, conservando gli ultimi valori come stale.

I file indipendenti dalla lingua in `gocore/contract/v1/` sono esempi normativi. I test black-box Go della CLI riproducono ogni fixture indipendente dal provider byte per byte attraverso `RunWith`, un provider fake e un orologio fisso. I test del client TypeScript (`main/coreClient.test.ts`, #177) leggono gli stessi file. `version.json` usa `aipermiler`, `dev`, `linux`, `amd64`; il suo test Go sostituisce `buildinfo.Name`, `buildinfo.Version`, `runtime.GOOS` e `runtime.GOARCH` prima del confronto byte per byte per supportare versioni impostate in build ed esecuzioni multipiattaforma. Ogni fixture viene anche verificata come JSON compatto valido con `schemaVersion: 1` ed esattamente un newline finale. `usage-claude` è riprodotto dal test dell'adapter Claude (`internal/claude`), dalla sua fixture del status line 2.1.316 attraverso il mapping. I documenti per l'account (`account-connected`, `account-deleted`, `login-authorize`, `raw`) ed `error-unavailable-reason` sono arrivati con #149; il client TypeScript li legge da #179. I documenti sono scritti senza escape HTML (un URL mantiene una `&` letterale).
