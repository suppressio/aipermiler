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
aipermiler serve
aipermiler service stop [--json] [--timeout 15s]
aipermiler help
```

I comandi della CLI recuperano i dati su richiesta e terminano. L'unica eccezione è `serve` (vedi [Servizio](#servizio)): il processo sempre attivo di #31, che oggi risponde solo a `health`; refresh loop, store e snapshot del pacing devono ancora arrivare. #31 servirà gli stessi documenti via HTTP; #177 aggiunge il client TypeScript. Claude (#147), Codex (#148) e Copilot (#149) sono registrati; `token`, `login` e `raw` sono di Copilot (comandi per l'account, sotto). Un provider sconosciuto come `aipermiler usage nope` fallisce con exit 2, `Unknown provider: nope`. I test registrano un provider sintetico chiamato `fake`.

Mettere il provider prima dei suoi flag. Argomenti posizionali inattesi e flag sconosciuti sono errori di utilizzo.

| Flag comune | Comportamento |
|---|---|
| `--json` | Scrive su stdout un documento JSON compatto seguito da un solo newline; stderr contiene solo log. È un flag booleano standard (`--json=false` vuol dire testo, vale l'ultima occorrenza); per un'invocazione che fallisce prima o durante il parsing dei flag è riconosciuto ovunque negli argomenti, così anche l'errore di uso è un documento JSON. |
| `--timeout <duration>` | Deadline di Fetch, default `2m` per `usage` e `25s` per `token` e `raw`; deve essere maggiore di zero e al massimo `2m`. Usa le durate Go, ad esempio `50ms`, `10s`. I provider devono rispettare la deadline del context. |
| `--secret-stdin` | Legge stdin solo se il provider accetta secret. Massimo 64 KiB incluso il newline finale; rimuove un solo CRLF o LF finale, conservando gli altri spazi. Input vuoto, errori di lettura e input troppo grande sono errori di utilizzo. |

I secret sono accettati solo su stdin, mai come flag: argv è visibile nell'elenco dei processi. I parametri dell'account vanno nei flag del provider, le credenziali no. Senza `--secret-stdin`, stdin non viene letto e Fetch riceve un secret vuoto. La CLI oscura il secret fornito nei messaggi di errore del provider e nelle motivazioni stale; i provider devono escludere le credenziali da tutti i campi di utilizzo restituiti, dalle etichette degli endpoint diagnostici e dai messaggi.

## Provider

### Claude

```text
aipermiler usage claude [--json] [--claude-bin /percorso/assoluto/di/claude]
```

Le quote arrivano solo attraverso il binario `claude` dell'utente, non modificato, e il suo login (#139, #140): il core non legge mai le credenziali di Claude e non fa mai login. L'approccio è stato deciso in #183 e #218 ([spike](CLAUDE-USAGE-SOURCE.it.md)). Linux, macOS e Windows (#223). Claude non accetta `--secret-stdin`.

Una lettura:

1. `claude auth status --json`, limitato solo dalla scadenza. Si leggono solo `loggedIn`, `authMethod`, `subscriptionType` e `projectsDirectory`, mai email od organizzazione. `loggedIn: false` è `signed_out` e `authMethod` `api_key` o `third_party` (una API key, Bedrock, Vertex: nessuna quota di abbonamento) è `unavailable`, entrambi senza avviare una sessione (#228); `claude.ai`, `oauth_token` (`claude setup-token`) e valori sconosciuti proseguono; un output inutilizzabile lascia il controllo allo schermo. `subscriptionType` `enterprise` non ferma la sonda: spiega soltanto un `rate_limits` mancante (sotto).
2. `claude` parte in uno pseudo-terminale (50×140, `TERM=xterm-256color`; su Windows una ConPTY su `golang.org/x/sys/windows`, il `claude.exe` nativo avviato direttamente, sospeso, messo in un job object con `KILL_ON_JOB_CLOSE`, poi ripreso, mai con `CREATE_NO_WINDOW`, che rompe la ConPTY) dentro la cartella di probe del core, `<user cache>/aipermiler/claude-probe`, che deve essere vuota. I flag sono `--settings` con il nostro comando di status line, `--strict-mcp-config` e `--setting-sources local`. Nelle impostazioni dell'utente non viene scritto niente, e server MCP, hook e plugin dell'utente restano fuori. Le variabili d'ambiente lasciate da una sessione Claude Code genitore (`CLAUDECODE`, `CLAUDE_CODE_ENTRYPOINT`, `CLAUDE_CODE_SESSION_ID` e simili, un elenco fisso) vengono rimosse. Tutte le altre restano, `CLAUDE_CODE_*` comprese (provider, accesso, gateway, mTLS, percorso di Git Bash), così il core legge lo stesso accesso del Claude Code dell'utente (#228).
3. Al prompt di fiducia della cartella si risponde una volta sola, e solo per quella cartella vuota. Il core digita solo `/usage`, Esc e `/exit`, mai un prompt, quindi nessuna richiesta al modello sul piano dell'utente. Alla domanda di Claude Code su una API key nel suo ambiente (*Detected a custom API key*) non si risponde mai: la risposta predefinita è No, e qualunque Invio digitato lì salverebbe quella risposta nel Claude Code dell'utente, quindi il core la lascia com'è e chiude la sessione (#228). Per lo stesso motivo l'Invio dopo `/usage` parte solo quando il prompt mostra il `/usage` digitato: una schermata che non lo mostra è una finestra sconosciuta, che non riceve nessun Invio: il probe finisce alla scadenza senza confermare niente.

   Il probe aspetta eventi, mai tempi stimati, e non ha un limite di tempo suo: lo ferma solo la scadenza della CLI (`--timeout`, 2 min per `usage`), come protezione contro un `claude` bloccato (#266). Claude esegue il nostro comando di status line quando disegna il prompt, anche all'avvio prima di qualsiasi messaggio, e di nuovo dopo `/usage`, quella volta con `rate_limits` (verificato su Linux con 2.1.322 e su Windows 11 con 2.1.319). La prima esecuzione nel file sink, seguita da 0,5 s senza output, è il prompt; il silenzio da solo non lo è mai, perché una pausa durante un avvio lento ha lo stesso aspetto, e senza un'esecuzione la lettura non potrebbe comunque riuscire. Un'esecuzione vista prima della risposta al prompt di fiducia non conta. Solo allora si digita `/usage`, e il suo eco, il pannello, la sua chiusura e l'esecuzione della status line che segue si aspettano ciascuno per tutto il tempo che serve: su una VM Windows 11 con entrambi i core saturi, `claude` da solo ha impiegato 18-23 s a disegnare il prompt, e Windows PowerShell, che lì esegue la status line, diversi secondi in più. `/usage` non viene mai digitato due volte: un eco tardivo di entrambi manderebbe `/usage/usage` a Claude. Una finestra che non mostra mai `/usage` non riceve nessun Invio e finisce alla scadenza. La domanda sulla API key viene riconosciuta anche mentre si aspetta l'eco, e la scheda Usage disegnata senza finestre di quota una volta finito l'aggiornamento (vista con il Portachiavi irraggiungibile su macOS via SSH), quindi entrambe sono segnalate subito. Solo un'esecuzione dopo `/usage` senza `rate_limits` è un `format_drift`.
4. Claude esegue il comando di status line. È il sottocomando nascosto `aipermiler claude-statusline <file>`, che aggiunge come una riga il JSON ricevuto su stdin a un file `<user cache>/aipermiler/claude-status-*.jsonl` creato per questa lettura. Non crea mai un file e non stampa niente. Su Linux e macOS Claude esegue il comando con `sh`, quindi i due percorsi sono tra apici singoli. Su Windows lo esegue con Windows PowerShell, oppure con Git Bash quando è installato Git for Windows (entrambi verificati su Windows 11 con 2.1.302 e 2.1.316), e i due quotano in modo diverso: i percorsi vanno senza virgolette, le cartelle come nomi brevi 8.3 con slash in avanti (`C:/Users/MARIOR~1/…/aipermiler.exe`), che PowerShell, bash e cmd leggono tutti come la stessa parola. Un percorso che contiene ancora un carattere diverso da lettere e cifre ASCII e `:/._-~` (un volume senza nomi brevi) è `unavailable`. Chiuso il pannello con Esc, il core aspetta un'esecuzione successiva a `/usage`, o una che porti `rate_limits`.
5. `/exit` ha 5 s, poi viene ucciso l'intero albero di processi (il process group su POSIX, il job su Windows), e la sua fine si aspetta al massimo 2 s. Entrambi i limiti servono solo a non far trattenere da un'uscita bloccata una risposta già letta, e non la fanno mai fallire; un probe arrivato alla scadenza viene ucciso subito. Il core risponde quindi al massimo 7 s dopo la sua scadenza, e il timeout del client sta 15 s sopra (#266).
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
| Binario non trovato (anche `--claude-bin` relativo o sbagliato) | `unavailable`, reason `not_installed` | `Claude Code not found; install it or set --claude-bin` |
| Connesso con una API key o un cloud provider | `unavailable`, reason `no_subscription` | `Claude Code is signed in with an API key or a cloud provider, which has no subscription quota` |
| Claude Code chiede se usare una API key presente nel suo ambiente | `unavailable`, reason `api_key_question` | ``Claude Code asks whether to use the API key in its environment; answer it in claude, then retry`` |
| Non connesso (`auth status`, o la schermata di login/tema) | `signed_out` | ``Claude Code is not signed in; run `claude` and sign in`` |
| La scheda Usage senza finestre di quota | `not_ready` | `the usage panel shows no quota windows` |
| Prima è arrivata la scadenza: nessun prompt (nessuna esecuzione della status line), `/usage` mai mostrato al prompt (una finestra), nessun pannello, il pannello mai chiuso, o nessuna esecuzione della status line dopo `/usage` | `timeout` | `claude probe timed out <where>` / `claude probe timed out before the status line ran after /usage` |
| `subscriptionType` `enterprise` e status line eseguito senza `rate_limits` (un piano a consumo con crediti: il pannello `/usage` mostra solo "Usage credits", visto su 2.1.319, #249; la [doc della status line](https://code.claude.com/docs/en/statusline) prevede `rate_limits` solo per Pro, Max e Ultra, o dietro un Claude apps gateway con limite di spesa) e il pannello non ha il blocco "Usage credits", o lo ha in una valuta diversa da `$` | `unavailable`, reason `usage_credits` | `Claude Code is signed in on a plan billed through usage credits, which has no quota windows` |
| Stesso piano, blocco "Usage credits" presente ma non riconosciuto | `format_drift` | `Claude: the Usage credits block of /usage was not recognized`, endpoint `claude /usage panel (Usage credits)`, senza shape |
| Status line mai eseguito, nessun `rate_limits` (qualsiasi altro piano) o nessuna finestra riconosciuta | `format_drift` | `Claude: …`, endpoint `claude status line rate_limits`, solo la forma |
| `claude` non parte, esce prima, cartella di probe non vuota | `unavailable` | il motivo, mai lo schermo |
| Windows: un percorso del core o della cache che lo status line non può nominare senza virgolette | `unavailable` | `the status line command cannot name this path without quoting (no 8.3 short name?): <path>` |

Il mapping legge l'ultima esecuzione del status line che portava `rate_limits`. `five_hour` diventa `claude-5h` / `5-hour limit` / `rolling-hours` 5 e `seven_day` diventa `claude-7d` / `7-day limit` / `rolling-days` 7. Un `used_percentage` mancante o negativo salta la finestra; il valore non viene mai limitato; unità `percentage`, totale 100. Un `resets_at` positivo in secondi Unix diventa una data UTC, altrimenti null. `planTier` e `subscriptionRenewsAt` sono sempre null: lo status line non porta nessuno dei due. Le fixture sintetiche in `internal/claude/testdata/` portano il nome della versione di Claude Code di cui seguono la forma; il comportamento è stato visto su 2.1.314-2.1.316.

Su un piano a consumo con crediti (`subscriptionType` `enterprise`, niente `rate_limits`) il core legge solo il blocco "Usage credits" del pannello `/usage` che ha già aperto (#255; nessun'altra parte dello schermo viene analizzata). Sul testo normalizzato del pannello, l'ultimo ridisegno di `usagecredits` … `N%used` `$<speso>/$<limite>spent` … `resets<mese><giorno>(` dà la finestra della v0.4.x `spend` / `Spend limit`: `count`, used = speso, total = limite (USD, separatore delle migliaia `,` ammesso), `billing-cycle` 1. Il pannello dà solo mese e giorno: `resetsAt` è la prossima occorrenza di quella data a mezzanotte locale della macchina (la zona mostrata è quella della macchina, dedotto). Blocco visto su Windows 11 con 2.1.319. Se un giorno lo status line porterà questi dati (anthropics/claude-code#108569), sostituirà questa lettura.

**Polling.** Ogni lettura avvia una sessione Claude Code (circa 5 s misurati su Linux, 6-9 s su una VM Windows 11, 2.1.302-2.1.316) e aggiunge una riga a `history.jsonl` di Claude. Il client legge ogni 30 min di default e mai sotto i 5 min col timer (il ritmo adattivo di 5 min è il minimo); un aggiornamento manuale riusa una lettura di meno di 1 min (`main/refreshPolicy.ts`). La scadenza di default della CLI per `usage`, 2 min, è solo una protezione contro una lettura bloccata: una lettura aspetta ogni evento per tutto il tempo che serve (#266). Il client tiene il proprio timeout sopra (#180). Su Windows il core avviato da `execFile` di Node con `windowsHide` (come fa `main/coreClient.ts`) nella sessione desktop ha letto entrambe le finestre e non ha mostrato nessuna finestra in 14 s di screenshot presi ogni ~40 ms (una console visibile, in una prova di controllo, è stata vista in 148 frame su 189).

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

Handshake di sola lettura: `initialize` → `initialized` → `account/rateLimits/read` (`params: null`); chiama `account/read` (`refreshToken: false`) solo dopo `-32600` per distinguere le API key. Nessun metodo/flag di login, logout, consumo di reset credit, prompt o attivazione analytics. Ogni chiamata ha un timeout di 10 s entro la deadline CLI (default 2 min). Dopo la risposta chiude stdin e concede al figlio 3 s per terminare, poi lo uccide; in caso di errore lo uccide subito. Fetch attende sempre la pulizia. Stderr viene svuotato e mai loggato.

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
| `error` | oggetto, solo errori | `code` stabile e `message` leggibile; format drift ha anche `endpoint` e `shape`. `reason` facoltativo (additivo, #149): un caso noto dentro il codice, così un client può spiegarlo nella lingua dell'utente senza analizzare `message`. Reason note: `not_connected`, `token_rejected` (con `signed_out`); `enterprise_managed_seat`, `keyring_unavailable` e per Claude (#180) `not_installed`, `no_subscription`, `usage_credits`, `api_key_question` (con `unavailable`). Un client ignora una reason che non conosce. |
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
| `1` | Errore del provider. Anche gli errori di scrittura dell'output restituiscono 1 e registrano il problema; così fa `serve` quando non riesce ad avviarsi o si ferma per un errore del listener. |
| `2` | Errore di utilizzo: provider/comando sconosciuto, flag errati, argomenti extra, timeout o input secret non validi. |
| `3` | Solo `serve`: un altro servizio tiene già il lock. |
| `4` | Solo `service stop`: nessun servizio in esecuzione (niente da fermare; non è un errore per un installer). |
| `5` | Solo `service stop`: il servizio girava ancora allo scadere del tempo. |

Con `--json`, gli errori di utilizzo non hanno la chiave provider. L'help è solo testuale; richiederlo con `--json` produce un documento di errore di utilizzo. Senza `--json`, help/version conservano il comportamento testuale.

## Servizio

`aipermiler serve` tiene in piedi il processo come servizio sempre attivo (#31, prima fetta #247; approccio in #246). Non ha flag. Oggi risponde solo a `GET /api/v1/health`: il refresh loop, lo store e gli altri endpoint arrivano con le prossime sub-issue di #31. I log vanno su stderr (`AIPERMILER_LOG_LEVEL=info` mostra avvio e arresto).

- **Una sola istanza per cartella di configurazione** (quindi per utente, salvo `AIPERMILER_CONFIG_DIR` altrove; un filesystem di rete può non rispettare il lock). Non cancellare la cartella di configurazione mentre un servizio gira: il suo file di lock se ne va con lei, e un secondo servizio potrebbe partire accanto al primo. `serve` prende un lock esclusivo su `service.lock` nella cartella di configurazione (`flock(LOCK_EX|LOCK_NB)` su Unix, `LockFileEx` con `LOCKFILE_FAIL_IMMEDIATELY` su Windows). Un secondo `serve` stampa `The service is already running` ed esce con **3**. Il sistema operativo rilascia il lock quando il processo muore, quindi un `kill -9` non lascia nulla di stantio (testato su Unix con un processo ucciso; su Windows da verificare a mano sulla VM Windows 11).
- **Solo loopback.** Ascolta su `127.0.0.1` (non `localhost`, che può risolvere a `::1`) su una porta casuale. Ogni richiesta richiede `Authorization: Bearer <token>`, un token casuale di 32 byte generato a ogni avvio e confrontato in tempo costante (altrimenti 401). Un `Host` diverso da `127.0.0.1:<porta>` (DNS rebinding) e qualsiasi richiesta con header `Origin` ricevono 403 prima che il token venga guardato. La vera protezione è il token: i browser non mandano `Origin` sui semplici GET cross-origin, quindi il controllo su `Origin` restringe soltanto ciò che una pagina web può tentare.
- **`api.json`** nella cartella di configurazione, dice a un client come connettersi (su Unix il modo è `0600`; su Windows il file si affida all'ACL solo-utente che la cartella del profilo eredita, da verificare sulla VM Windows 11): `{ "schemaVersion": 1, "port", "token", "version", "contractVersion", "pid", "startedAt" }`. Viene scritto dopo che il listener è attivo, in modo atomico (file temporaneo + rename), dal processo che tiene il lock; un nuovo servizio rimuove l'`api.json` e i file temporanei lasciati da un'istanza morta, e un arresto pulito cancella il file. Un client non si fida mai del solo file: chiama `/health` col token, e una connessione rifiutata, un 401 o un `contractVersion` sconosciuto significano «nessun servizio utilizzabile». `pid` serve solo alla diagnostica. Dopo un crash il file conserva porta e token di un servizio morto; mandare quel token a chi ora occupa la porta non rivela nulla, perché ogni avvio ne genera uno nuovo. Qualsiasi processo dello stesso utente del sistema operativo può leggere il file e usare l'API: lo stesso confine di fiducia dei file dell'utente e del keyring.
- **`GET /api/v1/health`** restituisce `{ "schemaVersion": 1, "version", "contractVersion" }`.
- **`POST /api/v1/shutdown`** (autenticato come ogni richiesta) risponde `202 {"schemaVersion":1,"status":"stopping"}` e poi avvia lo stesso arresto pulito di SIGTERM. La risposta viene scritta e inviata prima che l'arresto cominci. Solo `POST`; ogni altro metodo riceve 405.

- **Arresto.** SIGINT o SIGTERM: annulla le richieste in corso, cancella `api.json` (così nessun client viene indirizzato a una porta che sta sparendo), smette di accettare, aspetta le richieste aperte fino a 10 s e chiude ciò che è ancora aperto, rilascia il lock. `serve` termina quando ogni connessione è chiusa, quindi gli endpoint devono rispettare il contesto della richiesta, che l'arresto annulla: un handler che lo ignora può girare ancora per poco dopo il rilascio del lock. Un arresto già richiesto quando il servizio scriverebbe `api.json` lo salta. Durante l'attesa il lock è ancora preso e `api.json` è già sparito: un client che avvia `serve` in quel momento riceve exit 3 senza `api.json`. Lo stesso accade mentre un altro servizio si sta avviando (lock preso, listener non ancora attivo): exit 3 senza `api.json` vuol dire un servizio che si avvia o si ferma, e il client riprova per poco più dei 10 s del limite dell'arresto. Se persiste, un servizio tiene il lock ma non è raggiungibile (il suo `api.json` è stato cancellato a mano, o è bloccato): il client lo segnala e ripiega sulla CLI; fermare quel processo spetta all'utente. Un arresto richiesto esce con 0: le connessioni ancora aperte al limite vengono chiuse con la forza e l'impossibilità di cancellare `api.json` viene solo registrata nel log. Ad arresto avviato, un ulteriore SIGINT o SIGTERM termina subito il processo con l'azione predefinita del segnale, senza pulizia (il prossimo avvio rimuove i residui); uno che arriva nell'istante fra il primo segnale e quel passaggio viene assorbito. La correttezza non dipende mai da un arresto pulito: il lock lo rilascia il sistema operativo e `api.json` è solo un suggerimento. Se un processo nascosto su Windows riceva SIGTERM al logoff non è ancora verificato (controllo sulla VM Windows 11 di #247).

### Fermare il servizio: `service stop` (#286, per l'installer e l'updater di #152)

`aipermiler service stop [--json] [--timeout <durata>]` è l'unico modo di fare un arresto pianificato, uguale su Linux, macOS e Windows, senza alcun segnale (Windows non ha un equivalente semplice di SIGTERM da uno script). L'installer e l'updater lo chiamano prima di sostituire il binario e trattano come successo sia l'exit **0** sia il **4**.

1. Prova il lock di `service.lock`. Libero: nessun servizio, exit **4** (`not_running`); un `api.json` rimasto non viene nemmeno letto.
2. Occupato: legge `api.json` e invia `POST /api/v1/shutdown` con il token, solo a `127.0.0.1:<porta>` (l'host non viene mai preso dal file). Finché il lock è occupato ma `api.json` manca o viene rifiutato (un servizio che sta partendo o si sta già fermando), riprova.
3. Controlla il lock finché è libero: exit **0** (`stopped`). Lock libero significa che il processo del servizio l'ha rilasciato, cioè è sparito o sta per uscire; un `serve` successivo può partire. Il processo può esistere ancora per un attimo: chi sostituisce l'eseguibile subito dopo (su Windows un eseguibile in uso non si sovrascrive) riprova per qualche secondo. `service stop` va eseguito come l'utente che fa girare il servizio: guarda la cartella di configurazione di chi lo lancia, e se non esiste risponde **4** senza crearla.
4. Se il lock è ancora occupato quando scade `--timeout` (default 15 s, poco sopra il limite di 10 s del servizio; al massimo 5 min): exit **5** (`timeout`). Il comando non uccide nulla: cosa fare dopo (aspettare, o fermare il processo in altro modo) lo decide il chiamante.

`--json` stampa un solo documento, `{ "schemaVersion": 1, "status": "stopped" | "not_running" | "timeout" }` (exit 0, 4, 5). Gli altri fallimenti (per esempio la cartella di configurazione non apribile) escono con 1 e un documento di errore; un errore d'uso esce con 2. Per un istante durante il controllo `service stop` tiene il lock lui stesso, quindi un `serve` avviato proprio allora può trovarlo occupato (exit 3) e deve riprovare, come nella finestra di avvio/arresto sopra.

## Store

`internal/store` (#295, parte di #146) è lo stato persistente del core e il suo unico scrittore. Lo userà il servizio (#31) quando gestirà il refresh loop (#283); oggi non lo chiama nessuno. L'import da electron-store è #296.

- **Un solo file**, `store.json` nella cartella di configurazione, modo `0600`, JSON in chiaro con `schemaVersion` (1). Non cifrato: l'unico segreto, il token Copilot, sta nel keyring dell'OS (#179). Il nome è diverso da `aipermiler-config.json` di Electron, perché su Windows e macOS le due app condividono la cartella e i file system non distinguono maiuscole e minuscole.
- **Contenuto**: `settings` (`refreshIntervalMinutes`, `retentionDays`, `notificationThresholdPercent`) e `accounts`. Un account ha id, provider, etichetta, `enabled`, il `renewalDay` manuale (0 = nessuno), l'orario di lavoro `schedule` (`days` da domenica, pesi 0 / 0.5 / 1), i campi del provider che servono al core (`host`, `scope`, `manualQuota`, `binaryPath`), lo storico `history` per id di finestra di quota (`dailyUsage` con le baseline e `recentSamples`, lo `history.State` di #162) e `lastGood`, l'ultima lettura riuscita. I campi sono additivi: se un client o un file più vecchio ne manca uno resta il default. I campi sconosciuti sono ignorati in lettura e persi alla scrittura successiva, quindi dopo un downgrade un core più vecchio cancella ciò che non conosce: un campo la cui perdita conta incrementa `schemaVersion`, e il core più vecchio allora rifiuta il file (`ErrNewerSchema`).
- **Scritture a prova di crash.** Ogni modifica scrive un file temporaneo nella stessa cartella, ne fa `fsync`, lo rinomina su `store.json` e sincronizza la cartella (non su Windows, dove una cartella non si apre per la sincronizzazione). Un crash lascia il file vecchio o quello nuovo, mai mezza giornata. Può lasciare un `store.json.*.tmp`: `RemoveLeftovers` li cancella e il servizio la chiamerà all'avvio, sotto il suo lock (#283).
- **Uno scrittore per processo.** `Store.Update(func(*State) error)` esegue la funzione su una copia dello stato sotto un mutex e la conferma; se la funzione fallisce o va in panic, o la scrittura fallisce, né il file né la memoria cambiano. Quando il file nuovo ha sostituito il vecchio la modifica è confermata, anche in memoria; un sync della cartella che fallisce dopo restituisce `ErrNotSynced` (la modifica potrebbe non sopravvivere a un'interruzione di corrente). Lo store tiene una copia propria di ciò che la funzione ha messo nello stato. Lo storico cambia solo tramite `Account.Record`, che chiama `history.Record`. I refresh sovrapposti (timer, manuale, risveglio dalla sospensione) sono applicati uno dopo l'altro, quindi danno lo storico di una esecuzione sequenziale in quell'ordine. Il file non è bloccato contro altri processi: ce n'è uno solo grazie al lock del servizio (`service.lock`).
- **Errori, mai un reset silenzioso.** Un file mancante è uno store vuoto con i default dell'app (non si scrive nulla finché non c'è il primo `Update`). Un file illeggibile, non JSON valido o senza `schemaVersion` valido è `ErrUnreadable`; uno `schemaVersion` più nuovo è `ErrNewerSchema`. `Open` non modifica mai il file.

## Client Electron

`main/coreClient.ts` (#177) è il lato app di questo contratto. Per ogni lettura esegue il binario con `execFile`: niente shell, `windowsHide` (nessuna finestra di console su Windows), `--json`, `--timeout 120s` e un timeout del client di 135 s (solo una protezione contro un core bloccato, #266), stdin sempre chiuso (con il segreto scritto prima, con `--secret-stdin`, quando c'è), al massimo 1 MiB di output. Il binario è `AIPERMILER_CORE_BIN` se impostato (assoluto), altrimenti `<resources>/core/aipermiler[.exe]` nel pacchetto o `core-bin/` del repository in sviluppo. Errori: binario assente o non avviabile → `CoreBinaryError`; ucciso al timeout del client → `CoreTimeoutError`; output che non è un documento v1, che contraddice il proprio exit code, o un errore di uso (exit 2, un bug negli argomenti) → `CoreOutputError`, senza mai citare l'output; `format_drift` → lo stesso `FormatDriftError` dei servizi TypeScript, così la segnalazione di drift funziona senza modifiche; gli altri codici → `CoreProviderError` con il codice. `version --json` alimenta Impostazioni → Diagnostica e il report di diagnosi. Codex legge da qui da #178 (`main/core.ts`, `main/providers.ts`), Copilot da #179 e Claude da #180; `paths --json` dà agli insight locali la cartella dei probe di Claude da saltare.

## Versionamento e fixture condivise

I campi aggiuntivi mantengono `schemaVersion: 1`; i client devono ignorare i campi sconosciuti. Rimuovere o rinominare un campo, o cambiarne il significato, incrementa la versione dello schema. Un client che vede una versione dello schema non supportata deve rifiutare esplicitamente il documento. Un codice di errore sconosciuto viene trattato come `unavailable`, conservando gli ultimi valori come stale.

I file indipendenti dalla lingua in `gocore/contract/v1/` sono esempi normativi. I test black-box Go della CLI riproducono ogni fixture indipendente dal provider byte per byte attraverso `RunWith`, un provider fake e un orologio fisso. I test del client TypeScript (`main/coreClient.test.ts`, #177) leggono gli stessi file. `version.json` usa `aipermiler`, `dev`, `linux`, `amd64`; il suo test Go sostituisce `buildinfo.Name`, `buildinfo.Version`, `runtime.GOOS` e `runtime.GOARCH` prima del confronto byte per byte per supportare versioni impostate in build ed esecuzioni multipiattaforma. Ogni fixture viene anche verificata come JSON compatto valido con `schemaVersion: 1` ed esattamente un newline finale. `usage-claude` è riprodotto dal test dell'adapter Claude (`internal/claude`), dalla sua fixture del status line 2.1.316 attraverso il mapping. I documenti per l'account (`account-connected`, `account-deleted`, `login-authorize`, `raw`) ed `error-unavailable-reason` sono arrivati con #149; il client TypeScript li legge da #179. I documenti sono scritti senza escape HTML (un URL mantiene una `&` letterale).
