🌐 [English](SPIKE-CLAUDE-USAGE-PTY.md) | **Italiano**

# Spike #141 / #183 — Quote Claude da `claude /usage` in un PTY

> Parte di #139. Stato: **fatto su Linux (#141), Windows 11 e macOS Intel (#183), via libera come fonte Claude per il core Go (#147).** Non verificati: Apple Silicon e il cask Homebrew (#193), un vero prompt di aggiornamento. Ogni affermazione dice se è stata eseguita (*verificato*) o dedotta (*dedotto*).

## Setup

**#141, 2026-10-26, Linux** (Debian testing, Claude Code 2.1.311, login claude.ai Max): `scripts/spikes/claude-usage-pty.py`, Python `pty.fork()`, finestra 50×140, `TERM=xterm-256color`, `--settings '{"disableAllHooks":true}'`, cwd in una cartella di lavoro, poi si digita `/usage` + Invio e si termina il processo.

**#183, 2026-10-31, Windows e macOS**, con una sonda Go usa e getta che fa quello che farà il core (`scripts/spikes/claude-usage-pty-go/`): trova il binario, lo avvia in un PTY (`creack/pty` su Linux/macOS, ConPTY tramite `golang.org/x/sys/windows` su Windows), aspetta il prompt, digita `/usage`, ricostruisce lo schermo con un piccolo emulatore VT, lo analizza, termina l'intero albero di processi, riporta tempo reale e CPU. Stesso login claude.ai su ogni OS, fatto dal maintainer con `claude` → `/login` e chiuso alla fine. La sonda non apre mai i file di credenziali di Claude.

- **Windows 11** (build 26300, VM su un overlay temporaneo), quattro installazioni affiancate: installer nativo (2.1.316), winget `Anthropic.ClaudeCode` (2.1.302), npm globale con Node 26 (2.1.316), estensione VS Code `anthropic.claude-code` (2.1.314). Sonda compilata come app GUI (`-H=windowsgui`, senza console propria, come il core).
- **macOS 14.8.9 Sonoma** (VM Intel, quella di #171), tre installazioni: installer nativo, npm globale (`sudo npm i -g`, Node da nodejs.org, prefisso `/usr/local`), estensione VS Code. Sonda eseguita via SSH e come job launchd nella sessione grafica dell'utente (`launchctl bootstrap gui/<uid>`), il contesto da cui partono le app GUI (una vera `.app` aperta dal Finder non è stata provata).
- **Linux** (Debian testing, 2.1.316): la stessa sonda Go, per confronto con Python.

Flag usati in ogni prova: `--settings '{"disableAllHooks":true}' --strict-mcp-config --setting-sources local` (niente hook, server MCP, impostazioni utente o di progetto, plugin). `--bare` non si può usare: il suo help dice "OAuth and keychain are never read", quindi un login claude.ai per lui non esiste.

## Risultati

| Domanda | Risposta |
|---|---|
| Pilotarlo in un PTY funziona? | Sì sui tre OS (verificato). La TUI parte, `/usage` apre la scheda *Usage* del pannello impostazioni, entrambe le finestre vengono lette. Su Windows il ridisegno di ConPTY usa spostamenti del cursore e cancellazioni invece di righe semplici: serve un emulatore VT (posizionamento del cursore, cancellazione di riga/schermo, scroll), togliere gli escape non basta. |
| Campi mostrati | `Current session` (finestra di 5 h) e `Current week (all models)`, ognuna con `NN% used` e `Resets <ora> (<zona IANA>)`, es. `Resets 12:10am (Europe/Rome)`, `Resets Oct 28, 10pm (Europe/Rome)`. Sopra un blocco `Session` (costo, durata, token di questa esecuzione), sotto `Usage credits` e una ripartizione locale "what's contributing" (non serve). Le righe settimanali per modello non comparivano su questo account; il parser tiene qualsiasi `<etichetta>` + `NN% used`, quindi verrebbero raccolte. Stesso layout su Linux, Windows e macOS (2.1.302-2.1.316). |
| `/usage` consuma quota o chiama un modello? | No. Il pannello mostra `Total cost: $0.0000` e `0 input, 0 output` token, e le percentuali sono rimaste identiche in prove ripetute (verificato, #141 e #183). |
| Lascia un file di sessione? | Nessun transcript sotto `~/.claude/projects` (non si invia alcun messaggio, #141). Tocca però lo stato di Claude all'avvio (backup di `.claude.json`, cache del changelog, sync di skill/plugin), come qualsiasi avvio. |
| Costo per sonda | Sonda Go, con login, dall'avvio al kill (verificato): Linux ~2,9-3,5 s reali, ~1,7 s CPU; Windows ~3,1-3,7 s reali, ~1,5-1,9 s CPU (contabilità del job object, tutto l'albero); macOS ~2,8-3,1 s reali, ~1,1-1,3 s CPU (9 prove, 3 per binario). Il prompt è pronto dopo ~1,6-2,2 s; il resto è l'attesa di 1,2 s di quiete prima di digitare e il caricamento del pannello. Con un refresh ogni 30 min è ~0,1% di un core. Il prototipo Python di #141 impiegava ~8,6 s solo per le sue attese fisse di 3 s. |
| Albero di processi e residui | Claude avvia processi di supporto: su Windows il job ne ha contati 30-35 in una sonda, 1 ancora vivo al kill. Nessun processo `claude` rimasto su nessun OS (verificato): su POSIX si termina il gruppo di processi (`pty.Start` imposta `Setsid`, quindi `claude` guida un gruppo suo); su Windows `claude.exe` sta in un job object con `KILL_ON_JOB_CLOSE` e si termina il job. Su Windows anche il solo `TerminateProcess` di `claude.exe` non ha lasciato nulla (verificato con `-nojob`): il job è una rete di sicurezza, non la correzione di una perdita osservata. |
| Finestra di console (Windows) | Con la sonda compilata come GUI ed eseguita nella sessione desktop (`schtasks /it`), 167 screenshot a ~55 ms l'uno dall'altro non hanno mostrato **nessuna finestra** (verificato; una prova di controllo con `timeout.exe` ne mostrava una, quindi il metodo vede le finestre). **`CREATE_NO_WINDOW` rompe ConPTY**: lo schermo resta vuoto e la sonda va in timeout dopo 25 s (verificato). È l'opposto della regola Codex (`docs/SPIKE-CODEX-APP-SERVER.it.md`, dove `CREATE_NO_WINDOW` serve per un figlio su pipe): la pseudo console nasconde già il figlio. |
| Prompt di fiducia nella cartella | Una cartella nuova riceve `Quick safety check: Is this a project you created or one you trust?` prima del prompt (verificato su Windows in `%TEMP%`, su Linux sotto `$HOME`, su macOS in `~/Library/Caches`). Sotto `/tmp` su Linux non compare, ed è per questo che #141 non l'ha visto (perché `/tmp` ne sia esente non è stato indagato). Risposto una volta (Giù + Invio su "Yes, I trust this folder") in una cartella di lavoro **fissa**, viene ricordato: le sonde successive nella stessa cartella sono andate dritte al prompt (verificato su Windows e macOS). Una cartella temporanea nuova a ogni sonda lo incontrerebbe ogni volta. |
| Offline / aggiornamento fallito | Il pannello mostra gli ultimi numeri in cache più `Showing last-known usage as of … (could not refresh)` (verificato offline su Windows in 3,2 s e su macOS in 2,8 s, scollegando la rete della VM; #141 su Linux). Nessun lungo timeout di rete: tenere i valori, segnarli come non aggiornati. |
| Senza login | Dopo un logout la TUI parte normalmente con `Not logged in · Run /login` nel footer (Windows 0,45 s, macOS 0,41 s, verificato). Una cartella di configurazione nuova mostra la scelta del tema prima di qualsiasi login (Linux, macOS, ~0,5 s, verificato). `Select login method` (#141) è la schermata dopo il tema. Tutti e tre si riconoscono prima di digitare `/usage`. |
| `claude auth status` | Stampa JSON (`loggedIn`, `authMethod`, più email e organizzazione con login) in ~0,16-0,25 s, senza PTY (verificato su Windows e macOS). Un controllo preliminare economico prima di una sonda completa. Il core legge solo `loggedIn` e `authMethod`, non salva né registra il resto. |
| Portachiavi macOS e SSH | Claude tiene il login claude.ai nel Portachiavi di login. Via SSH non è raggiungibile: `claude auth status` dice `loggedIn: false` mentre lo stesso comando dalla sessione grafica dice `true` (verificato). Anche la TUI via SSH non mostra alcun "Not logged in": `/usage` si apre con il solo blocco `Session` e nessuna finestra, quindi la sonda ha aspettato i suoi 12 s e ha restituito `not_ready` (verificato). Stessa lezione di Codex su Windows: il core deve girare nella sessione grafica dell'utente, e l'adapter dovrebbe trattare "scheda Usage aperta, nessuna finestra" come login assente dopo un `auth status` negativo, non come timeout. |
| Prompt di aggiornamento o avviso | Non riprodotto: la build winget più vecchia (2.1.302, con 2.1.316 già uscita) è partita senza alcun prompt. La sonda tratta una schermata sconosciuta senza prompt come `not_ready` al timeout; l'espressione regolare per l'aggiornamento è un'ipotesi (non verificato). L'installer nativo si aggiorna da solo in background; se l'avvio di una sonda inneschi un download non è stato misurato (`DISABLE_AUTOUPDATER=1` nell'ambiente del figlio lo eviterebbe, non verificato). |
| Insidie | Togliere `CLAUDE_CODE_*` e `CLAUDECODE` dall'ambiente del figlio quando l'app stessa gira dentro Claude Code (i marcatori ereditati cambiano il comportamento). Impostare `TERM=xterm-256color`. `--no-session-persistence` è rifiutato fuori da `--print`. Gli spostamenti del cursore possono far sparire ogni spazio (`Currentsession`, `12%used`, `Resets12:10am`), quindi non contare mai su spazi o `\b` tra le parole. |
| Stabilità del parsing | È una TUI, non un'API. Tenere una fixture dello schermo grezzo per ogni versione di Claude Code e fallire in modo esplicito (format drift) quando non si trova nessuna delle due finestre. |

### Dove stanno i binari

Tutto verificato installando, tranne Homebrew (letto dai metadati del cask su formulae.brew.sh).

| OS | Installazione | Dove sta il binario nativo | Nel `PATH`? |
|---|---|---|---|
| Windows | installer nativo (`irm https://claude.ai/install.ps1 \| iex`) | `%USERPROFILE%\.local\bin\claude.exe` (una copia di ~335 MB; versioni sotto `.local\share\claude\versions\`) | **No**, l'installer non lo aggiunge |
| Windows | winget `Anthropic.ClaudeCode` | `%LOCALAPPDATA%\Microsoft\WinGet\Packages\Anthropic.ClaudeCode_Microsoft.Winget.Source_8wekyb3d8bbwe\claude.exe` | sì, symlink in `WinGet\Links` |
| Windows | npm globale | `%APPDATA%\npm\node_modules\@anthropic-ai\claude-code\bin\claude.exe` | solo shim: `claude.cmd` (lancia direttamente quell'`.exe`, senza Node), `claude.ps1`, uno script `sh` |
| Windows | estensione VS Code | `%USERPROFILE%\.vscode\extensions\anthropic.claude-code-<ver>-win32-x64\resources\native-binary\claude.exe` | no |
| macOS | installer nativo (`curl -fsSL https://claude.ai/install.sh \| bash`) | `~/.local/bin/claude` → `~/.local/share/claude/versions/<ver>` | non nel `PATH` di launchd |
| macOS | npm globale (Node di nodejs.org) | `/usr/local/bin/claude` → `/usr/local/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe`, un Mach-O nativo nonostante il nome | `/usr/local/bin` non è nel `PATH` di launchd |
| macOS | estensione VS Code | `~/.vscode/extensions/anthropic.claude-code-<ver>-darwin-x64/resources/native-binary/claude` | no |
| macOS | cask Homebrew `claude-code` / `claude-code@latest` (dedotto) | `$(brew --prefix)/bin/claude`: `/opt/homebrew/bin` su Apple Silicon, `/usr/local/bin` su Intel (lo stesso percorso di npm) | no |
| Linux | npm globale | `<prefix>/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe` | tramite `<prefix>/bin/claude` |

Il postinstall di npm crea un hard link del binario nativo del pacchetto di piattaforma in `bin/claude.exe` (letto in `install.cjs`; su macOS il file aveva 2 link). Se il postinstall non è girato, `bin/claude.exe` è uno stub sotto i 4 KB e si usa un `cli-wrapper.cjs` (letto nel pacchetto, non riprodotto): scartare ogni candidato sotto 1 MB o che finisce in `.js`/`.cjs`. A differenza di Codex, gli shim npm su Windows non richiedono mai Node; su macOS il symlink punta già al file nativo. Il binario incluso nell'estensione ha una versione sua (2.1.314 accanto alla 2.1.316 della CLI), quindi nessuna delle due fonti si può dare per più recente.

### Libreria PTY

| Opzione | Licenza | Dipendenze | Dimensione | Note |
|---|---|---|---|---|
| **`github.com/creack/pty` v1.1.24** (Linux, macOS) | MIT | nessuna | ~1.850 righe | Il pacchetto PTY Go di fatto standard; su Windows non ha un'implementazione utilizzabile |
| **ConPTY tramite `golang.org/x/sys/windows`** (Windows) | BSD-3 | già nel core (indiretta via `go-keyring`, v0.27.0: diventerebbe diretta) | ~130 righe nostre (`pty_windows.go`) | `CreatePseudoConsole`, la attribute list, i job object e `ComposeCommandLine` sono tutti in x/sys; manca solo la struct di contabilità del job (usata per la cifra di CPU) |
| `github.com/aymanbagabas/go-pty` v0.2.3 (entrambi) | MIT | porta `u-root`, `x/crypto`, un `x/sys` più recente; richiede Go 1.25 | — | Scartata: più pesante e costringe ad aggiornare Go nel core (1.24) |
| PTY POSIX nostro su `x/sys/unix` | — | nessuna nuova | ~100 righe per OS (gli ioctl di Linux e Darwin differiscono) | Possibile, ma più codice nostro da mantenere per poco guadagno |

L'impatto sulla dimensione del binario del core non è stato misurato; dalla dimensione del codice ci si aspetta che sia piccolo (dedotto).

## Decisione

- **Fonte Claude primaria per il core Go: `claude` + `/usage` una tantum in un PTY**, 50×140, `TERM=xterm-256color`, flag `--settings '{"disableAllHooks":true}' --strict-mcp-config --setting-sources local`; ricostruire lo schermo con un emulatore VT, analizzarlo, terminare l'intero albero. Timeout per sonda ~25 s; `not_ready` se nessun pannello compare 12 s dopo aver digitato.
- **PTY:** `creack/pty` su Linux/macOS, ConPTY scritto su `golang.org/x/sys/windows` (nessuna libreria PTY per Windows). Proposta con i costi sopra; si aggiunge al core solo con l'ok del proprietario in #147.
- **Cartella di lavoro:** una sola cartella fissa e vuota dell'app (es. nella cartella cache dell'OS), non una cartella temporanea nuova a ogni sonda, così il prompt di fiducia arriva al massimo una volta. Proposta per il proprietario: il core risponde da solo a quel prompt, solo per la propria cartella vuota (dentro non c'è nulla di cui fidarsi; le impostazioni vengono solo da `--setting-sources local`). Alternativa: riportare `not_ready` con un suggerimento e far lanciare `claude` lì una volta all'utente.
- **Stati prima di `/usage`:** scelta del tema, `Select login method` o `Not logged in` → login assente; prompt di fiducia → come sopra; qualsiasi altra cosa che blocca → `not_ready` al timeout. `could not refresh` → valori tenuti, segnati come non aggiornati. Facoltativo, `claude auth status` prima (~0,2 s): `loggedIn: false` → login assente senza PTY, e rende uno stato chiaro anche la "scheda Usage senza finestre" di macOS.
- **Windows:** avviare direttamente il `claude.exe` nativo (mai `claude.cmd`/`.ps1`), **senza** `CREATE_NO_WINDOW`, sospeso, dentro un job object con `KILL_ON_JOB_CLOSE`, poi riprenderlo; terminare il job alla fine.
- **macOS:** guardare nei prefissi fissi, mai contare sul `PATH` della shell; far girare il core nella sessione grafica dell'utente (via SSH o come demone di sistema il login nel Portachiavi non si vede).
- **Ordine di ricerca del binario:**
  1. percorso esplicito dalle impostazioni dell'account;
  2. voci del `PATH` (`claude`, `claude.exe`), symlink risolti;
  3. `~/.local/bin/claude[.exe]` dell'installer nativo (non nel `PATH` su Windows, né nel `PATH` di launchd);
  4. prefissi fissi per OS: Windows npm `%APPDATA%\npm\node_modules\@anthropic-ai\claude-code\bin\claude.exe`, poi la cartella del pacchetto winget; macOS `/opt/homebrew/bin/claude`, `/usr/local/bin/claude`, poi il `lib/node_modules/…/bin/claude.exe` di npm sotto `/usr/local` e `/opt/homebrew`; Linux il `lib/node_modules/…/bin/claude.exe` di npm sotto `/usr/local` e `/usr`;
  5. l'estensione più recente della famiglia VS Code (`.vscode`, `.vscode-insiders`, `.cursor`, `.windsurf`, `.vscode-server`): `anthropic.claude-code-<ver>-<os>-<arch>/resources/native-binary/claude[.exe]`, solo la cartella dell'OS corrente, la più recente per **semver** (la sonda ordina i nomi lessicalmente, e così mette 2.1.99 dopo 2.1.100).

  Scartare destinazioni `.js`/`.cjs` e file sotto 1 MB (stub npm); deduplicare per percorso risolto.
- **Regole fisse (invariate):** eseguire solo il binario `claude` non modificato con il login dell'utente; mai leggere i suoi file di credenziali; mai chiedere all'utente di fare login dentro la nostra app (#140); togliere `CLAUDE_CODE_*`/`CLAUDECODE` dall'ambiente del figlio.
- **Da fare prima del rilascio:** Apple Silicon e Homebrew (#193); una fixture dello schermo grezzo per versione di Claude Code, test dalle fixture (#147).

## Prototipo

- `scripts/spikes/claude-usage-pty.py` (#141) — Python, solo POSIX. `--raw` stampa lo schermo ripulito; i flag extra per `claude` vanno dopo `--`.
- `scripts/spikes/claude-usage-pty-go/` (#183) — Go, Linux/macOS/Windows, con un suo `go.mod` (le dipendenze restano fuori dal core). `go run . [-bin PATH] [-discover] [-raw] [-out FILE] [-timeout 25s] [-workdir DIR] [-accept-trust] [-launchd-path]`; solo Windows: `-nojob`, `-nowindow` (le verifiche negative sopra). Stampa un risultato JSON con lo stato, le finestre, i tempi e i candidati trovati.

Entrambi sono usa e getta: non fanno parte di build, lint o test.
