🌐 [English](EVOLUTION.md) | **Italiano**

# EVOLUTION.md — Direzioni future e analisi costi/benefici

> Documento di tracciamento, non di design. A differenza di `ARCHITECTURE.md` (cosa si sta costruendo) e `RESEARCH.md` (vincoli reali delle API), questo file raccoglie una retrospettiva concettuale aperta dall'utente dopo la chiusura dell'MVP: direzioni di evoluzione del progetto **non ancora decise né schedulate**, con una prima valutazione di costi/benefici per orientare le priorità. Va aggiornato quando emergono nuove considerazioni o quando una direzione passa da "valutata" a "decisa" (a quel punto la parte decisa migra in `ARCHITECTURE.md`/`CLAUDE.md` come per ogni altra funzionalità).

---

## Cornice di base (assunzioni confermate dall'utente, 2026-08-31)

Queste assunzioni correggono/restringono lo scope rispetto a una prima lettura più "aperta" delle direzioni sotto — vanno tenute presenti in ogni valutazione successiva:

- **L'utente è e rimane il dev**, proprietario di un suo budget di crediti (acquistato da sé, ottenuto col piano free, o assegnato dalla propria azienda). Non c'è un pubblico multi-utente da servire.
- **Un agente o un CLI esterno che consulta i dati dell'app agisce per conto del dev**, non è un consumer estraneo — è un'estensione dello stesso utente, non un pubblico terzo a cui garantire un contratto stabile nel tempo. Questo abbassa il costo/rischio percepito di un'eventuale API locale (punto 2 sotto), ma non lo azzera: un processo locale non autenticato resta comunque raggiungibile da qualunque altro processo sulla stessa macchina, non solo da strumenti del dev.
- **Va bene che sia una trasformazione grande**, non solo un refactor interno — l'utente la definisce esplicitamente un'**evoluzione**, non un problema da minimizzare.
- **Il debito tecnico ereditato va risolto man mano**, non tutto subito (es. il bug aperto della tab Copilot dopo login, `workSchedule.hoursPerDay` non ancora usato da `budget.ts`) — nessuna urgenza dichiarata su questi, restano nel changelog di `CLAUDE.md`.
- **L'interfaccia è considerata relativamente matura** (skin, drag, tab, icone — lavoro recente giudicato sufficiente per ora). Il gap più sentito non è nella UI in sé ma nel fatto che i contenuti mostrati restano troppo vicini a quello che Anthropic già offre nella propria dashboard.

---

## Le 5 direzioni

1. **Staccarsi dalla vista del provider.** L'obiettivo non è mostrare gli stessi numeri/grafici che Claude o GitHub già espongono nelle loro dashboard, ma decidere autonomamente quali dati aiutano davvero l'efficienza. Le metriche derivate già esistenti (`efficiencyIndex`, gauge istantaneo/sostenibile, rating a stelle, `generateDailyTip`) vanno già in questa direzione; restano invece un calco diretto della UI del provider il grafico "Andamento settimanale" (stesso bar-chart-per-giorno di claude.ai) e le tab per finestra di quota (semplice affiancamento delle due metriche che Anthropic espone).

2. **Separare dati/backend da UI, con un'API interna.** `main.ts` oggi mischia orchestrazione fetch, calcolo snapshot (`computeAccountSnapshot`/`computeWindowSnapshot`) e IPC/broadcast nello stesso file, con il calcolo snapshot scritto contro lo store di Electron (non puro come `budget.ts`). L'obiettivo è che i dati vengano prima dell'interfaccia — la UI è un "favore", non il prodotto. L'app deve poter vivere di solo tray di sistema (il main process già gira headless a finestra chiusa — questa parte esiste già), esponendo gli stessi dati anche fuori dal proprio renderer: un agente che si costruisce il proprio MCP sopra, o un client indipendente scritto da altri (es. un CLI in Go), entrambi però sempre per conto del dev proprietario dei dati.

3. **Genericità multi-provider (OpenAI, z.ai, altri).** Il contratto già imposto da CLAUDE.md (`fetchUsage(credentials): Promise<RawAccountUsage>` con `QuotaWindow` normalizzato) è già una base adatta ad aggiungere provider nuovi a livello di singolo service. Il vero collo di bottiglia è più in alto: `AppSettings.accounts` ha due soli slot nominati (`claude`/`copilot`), non un registro dinamico — così come i pannelli Impostazioni e le tab account nel widget.

4. **Ridefinire "efficienza" da pacing a valore-per-token.** Le metriche `efficiencyIndex`/`efficiencyRating` misurano oggi quanto il ritmo di consumo reale si discosta da un ritmo ideale per non esaurire la quota — non se quel consumo è speso bene. Il segnale per un'analisi di valore-per-token esiste già ma è isolato: `services/claudeLocalSessions.ts` legge dimensione contesto, durata sessione, uso tool/MCP, ma oggi è un pannello informativo scollegato dai numeri di consumo quota (e copre solo Claude, non Copilot). Il salto di valore sarebbe incrociare le due fonti per un consiglio azionabile ("consumi in fretta *perché* X") invece di un solo termometro.

5. **Costo in risorse di Electron, anche a finestra chiusa.** Anche in modalità solo-tray il processo main e buona parte del runtime Chromium/GPU restano residenti per tenere viva l'icona e il polling — un carico di lavoro concettualmente semplice paga l'overhead di un browser completo. Si aggancia al punto 2: se il core viene estratto come processo Node autonomo, si apre la possibilità che il pezzo sempre-attivo non sia più Electron. Tradeoff da non sottovalutare: tray icon e avvio automatico al login sono oggi forniti gratis e cross-platform da Electron; toglierlo dal pezzo sempre-attivo richiede reimplementarli per piattaforma.

---

## Punti di attenzione emersi dall'analisi "dall'esterno"

- I 5 punti non sono iniziative indipendenti: sono la stessa direzione (da "widget" a "piattaforma dati personale sull'uso AI, con la UI come un client fra tanti") vista da 5 angolazioni.
- L'ironia strutturale del punto 2: l'app ha già tutta una disciplina di diagnostica per quando *l'API di un provider* cambia formato sotto di lei (`FormatDriftError`). Se l'app stessa diventa un'API consultata da altri strumenti del dev, dovrà affrontare lo stesso problema al contrario (stabilità del proprio contratto nel tempo) — non c'è ancora un pensiero su come. Risposta in #26: contratto versionato con fixture condivise, vedi [CORE-CLI](CORE-CLI.it.md).
- Tensione non banale tra punto 1/3 (parità tra provider) e punto 4 (l'insight più ricco esiste solo per Claude via file locali): più si investe nell'analisi profonda, più il prodotto diventa asimmetrico a favore di Claude.
- Il punto 5 è oggi un'ipotesi non misurata: nessuno ha ancora verificato quanta RAM/CPU consumi realmente l'app in modalità solo-tray su una macchina reale — prima di investire in una soluzione (costosa: demone indipendente + packaging/autostart per piattaforma), vale la pena quantificare il problema.
- Esiste un'alternativa a basso costo per buona parte del bisogno del punto 2: un comando CLI headless on-demand (es. `--json`/export) invece di un server sempre attivo con autenticazione di rete — vedi riga "2b" nella tabella sotto.

---

## Valutazione costi/benefici

| # | Direzione | Beneficio | Costo | Rischio | Dipendenze |
|---|---|---|---|---|---|
| **1** | Staccarsi dalla vista del provider (grafico/tab propri, non calco di Anthropic) | Alto — differenzia realmente il prodotto dalla dashboard Anthropic; le metriche derivate esistono già, manca la gerarchia visiva giusta | Basso — nessuna infrastruttura nuova, principalmente decidere cosa promuovere e cosa degradare in UI | Basso, reversibile | Nessuna — può partire subito |
| **2a** | Estrarre un core puro (fetch+snapshot) da `main.ts` | Medio diretto, alto come prerequisito — sblocca 2b/2c, testabilità, riuso | Medio — refactor vero, ma coperto da 91+ test esistenti, basso rischio di regressione | Basso | Nessuna |
| **2b** | CLI headless on-demand (`--json`/export) invocato dal dev o da un suo agente | Alto rispetto al costo — copre "l'agente fa il volere del dev" senza rete, porte, demoni | Basso, si appoggia su 2a | Basso — nessuna superficie di rete nuova | 2a |
| **2c** | Server HTTP locale sempre attivo (accesso continuo/da remoto) | Alto ma solo se serve davvero accesso continuo, non on-demand | Alto — autenticazione locale comunque necessaria, gestione lifecycle, nodo irrisolto se vive dentro o fuori Electron | Medio — nuova superficie residente | 2a, e solo se 2b si dimostra insufficiente |
| **3** | Multi-provider genericità (schema/UI) | Basso oggi — nessun secondo provider reale ancora collegato | Medio se fatto ora e a sé; molto più basso se fatto insieme al refactor di 2a | Rischio di over-engineering per un bisogno ipotetico | Conviene farlo durante 2a, non come intervento a parte |
| **4** | Ridefinire efficienza: pacing → valore-per-token | Alto — il più vicino allo scopo dichiarato dell'app | Medio-alto — incrociare due fonti oggi separate, rischio di correlazioni deboli se fatto in fretta | Medio — un cattivo insight mina la fiducia più di nessun insight | Rende di più se fatto dopo il punto 1 |
| **5** | Ridurre il costo Electron in modalità solo-tray | Sconosciuto — nessuno ha ancora misurato quanto pesa davvero oggi | Alto se risolto per davvero (demone indipendente + packaging/autostart per 3 OS) — il punto più caro della lista | Alto: rischio di investire tanto in un problema che potrebbe essere più teorico che reale | Nessuna per la misurazione; 2a se poi si decide di agire |

---

## Sequenza suggerita

1. **Punto 1** — costo basso, beneficio alto, nessuna dipendenza.
2. **2a**, insieme al **punto 3** — toccare lo schema account una volta sola, non due.
3. **2b** — quasi gratis una volta fatto 2a.
4. **Punto 4** — dopo il punto 1, per non ripetere l'errore del pannello scollegato.
5. **Punto 5** — prima misurare il consumo reale in tray, poi eventualmente agire (il più caro di tutti).
6. **2c** — solo se dopo aver usato 2b per un po' emerge un bisogno reale di accesso continuo/da remoto.

---

## Stato (aggiornato 2026-09-30)

Scelta dell'utente: evolvere l'app "man mano con l'uso", **senza stravolgerne l'architettura** — quindi i punti 1, 3 e 4, fattibili dentro la struttura attuale (Electron + main.ts + IPC + store). Il 2a (estrazione del core) non è stato fatto: il punto 3 è stato realizzato senza di esso, a costo contenuto. Sequenza eseguita: issue #3 → punto 3 → punto 1 → punto 4, un commit per fase.

| # | Stato | Cosa è stato fatto |
|---|---|---|
| **1** | ✅ fatto | Grafico "consumo giornaliero vs budget" (delta per giorno + quota ideale, `budget.dailyDeltas`) al posto della % cumulata; lista finestre con verdetto calcolato dall'app (`budget.windowVerdict`) al posto delle tab; picco/media e streak calcolati sui delta (sul cumulato erano di fatto sbagliati). |
| **3** | ✅ fatto (con issue #4) | Registro di N account indipendenti dal provider (`AccountConfig[]`, parte comune + parte specifica), `main/providers.ts` per ciò che dipende dal provider, tabella account in Impostazioni, migrazione automatica dallo schema a due slot (`store/migrate.ts`). Una partition Electron per ogni account Claude: risolve il bug del logout che non cancellava la sessione. Aggiungere un provider nuovo (es. OpenAI) = un service con `fetchUsage` + un ramo per ogni funzione di `main/providers.ts` + un template di dettaglio in `settings.html`. |
| **4** | ✅ fatto (prima versione) | "Resa" (token di output per 1% di quota, con trend) e consiglio causale su contesto ampio, mostrato solo con segnale netto. Resta la **tensione** annotata sopra: esiste solo per Claude (unica sorgente locale). Limite noto: una sessione è attribuita al giorno della sua ultima modifica (l'SDK non espone l'orario dei messaggi). |
| 2a/2b/2c, 5 | ⏸ non affrontati | Come da sequenza suggerita; il punto 5 resta da **misurare** prima di qualunque intervento. |

## Domande aperte / prossimo passo

- Verificare con l'uso reale se le soglie di `consumptionCause` (≥5 giorni, ≥1.5×, ≥20 punti) sono troppo severe (la frase non compare mai) o troppo lasche.
- Misurare il punto 5 (RAM/CPU in modalità solo-tray) prima di decidere se affrontare 2a/5.

## Aggiornamento (2026-10-01): 2a diventa il prossimo passo

La richiesta successiva dell'utente — rendere l'app più robusta e passare a uno sviluppo test-driven — ha cambiato la priorità del **2a**: senza estrarre da `main.ts` un nucleo puro (con lo store iniettato), la logica di orchestrazione non si può testare senza avviare Electron, quindi non si può lavorare test-first. Fatto nel frattempo: TypeScript più severo, ESLint con controllo dei tipi a zero segnalazioni e bloccante in CI, normalizzazione dello store, validazione dell'input IPC. Ordine concordato: prima un contratto IPC unico (canali + tipi condivisi tra main, preload e renderer), poi l'estrazione del nucleo (2a), con test scritti prima per fissare il comportamento attuale. 2b resta "quasi gratis" una volta fatto 2a.

## Aggiornamento (2026-10-16): misura del punto 5 — strumento pronto, numeri da raccogliere

`scripts/measure-resources.js` carica l'app compilata in un profilo usa e getta (nessun account, nessuna rete) e campiona `app.getAppMetrics()` prima con il widget aperto, poi dopo averlo chiuso (solo tray), stampando un CSV e un riepilogo di media/massimo di RAM (working set) e CPU totali su tutti i processi dell'app. È multipiattaforma: la stessa esecuzione vale su Linux, Windows e macOS:

```
npm run build
env -u ELECTRON_RUN_AS_NODE npx electron scripts/measure-resources.js 5 240 30   # minuti aperta, minuti solo-tray, secondi tra i campioni
```

**Risultato: non ancora misurato.** L'ambiente in cui è stato preparato non riesce ad avviare Electron (mancano le librerie GTK), quindi qui non si dichiara nessun valore. Da fare su macchine reali (Linux, Windows): lanciare il comando per qualche ora, aggiungere in questa sezione le righe di riepilogo e confrontare con un'app simile sempre residente nel tray misurata allo stesso modo (es. un client di chat o di sincronizzazione: "Memoria (working set privato attivo)" di Task Manager / RSS di `ps` sommato sull'albero dei processi).

Regola di decisione proposta, da confermare con i numeri: totale solo-tray sotto ~250 MB e CPU media sotto ~0,5% → **niente** (punto 5 chiuso); sensibilmente sopra ma dominato da un solo processo (GPU/renderer tenuto vivo) → prima **tuning** (es. distruggere la finestra invece di nasconderla, `app.disableHardwareAcceleration()`); solo se il tuning non basta → punto 5 (dopo 2a).

Osservazione dalla lettura del codice: il polling del cursore ogni 120 ms (`startWindowHoverPolling`) continua anche in modalità solo-tray quando `mainWindow` è nascosta e non distrutta; è il primo candidato se la CPU non risulta a riposo.

## Aggiornamento (2026-10-18): punto 5 misurato su Linux

Linux (Debian, KDE Plasma su X11, driver NVIDIA, 24 core, 62 GB di RAM), build da `next` dopo la v0.4.14-beta, `measure-resources.js 5 60 30` sul desktop vero (profilo usa e getta, nessun account). Lo script nasconde la finestra come il pulsante di chiusura del widget (`window:close` in `main.ts`), quindi il renderer e il polling del cursore ogni 120 ms restano vivi come nell'uso reale:

```
SUMMARY open: processes=4 avgMB=712 maxMB=712 avgCPU=0.02% maxCPU=0.0% samples=6
SUMMARY tray: processes=4 avgMB=711 maxMB=712 avgCPU=0.01% maxCPU=0.0% samples=117
```

Il working set sommato conta le pagine condivise tra i processi (librerie di Chromium, mappature del driver) una volta per processo, quindi sovrastima l'occupazione reale. La PSS (`/proc/<pid>/smaps_rollup`, pagine condivise divise tra chi le usa) dell'intero albero di processi, letta in modalità solo tray, era **~351 MB**: processo GPU 136 MB, browser 86 MB, renderer 55 MB, servizio di rete 26 MB, zygote e broker il resto. Una prima esecuzione che distruggeva la finestra invece di nasconderla dava ~320 MB: il renderer non c'era più, il processo GPU sì.

Lettura secondo la regola sopra: CPU a riposo (0,01% di media, il polling del cursore non si vede), memoria sopra ~250 MB e il processo GPU è la quota maggiore → prima **tuning** (#99): `app.disableHardwareAcceleration()` (il widget è HTML/SVG semplice, senza animazioni), poi distruggere la finestra alla chiusura (~30-55 MB). Ipotesi, da verificare dopo #99: il tuning potrebbe portarla a circa 180-220 MB, sotto la soglia di ~250 MB della regola. Se non ci arriva, o se un'app nativa da tray misurata allo stesso modo è molto più leggera, il punto 5 o una shell con la webview di sistema passano alla revisione architetturale (#12) con questi numeri. Non fatto: il confronto con un'altra app da tray, e Windows (#98).

**#99, 2026-10-22** (stessa macchina Linux, X11/KDE, profilo usa e getta, PSS in sola tray dell'albero di processi dell'app): `app.disableHardwareAcceleration()` l'ha portata da ~378 a ~295 MB (−83 MB, −22%), quasi tutto dal processo GPU (172 → 97 MB: Chromium tiene un processo GPU software). CPU sempre a riposo. Le tre skin si vedono correttamente, transparent-digital compresa (screenshot sul display reale). Tenuta: nettamente più leggera senza regressioni visibili, anche se ancora sopra la soglia di ~250 MB; il prossimo passo di tuning è distruggere la finestra alla chiusura. Windows non ancora verificato (#98).

**#98, 2026-10-25** (Windows, v0.4.17-beta installata, quindi accelerazione hardware già disattivata; **profilo reale** con account e rete, non quello usa e getta; sola tray per 60 min, campioni ogni 30 s, i primi 3 scartati; working set e CPU sommati su tutti i processi `ia-hypermiler`, misurati con `Get-Process` di PowerShell invece di `measure-resources.js`, così non serviva un checkout dei sorgenti; CPU % = delta dei secondi di CPU / 30 s / core logici × 100, cioè 100% = intera macchina, mentre `percentCPUUsage` nei valori Linux ha 100% = un core; nessun dettaglio per processo):

```
SUMMARY tray: processes=4 avgMB=320 maxMB=346 avgCPU=0.05% maxCPU=0.6% samples=117
```

CPU bassa anche qui, pur con il polling reale: quasi tutti i campioni pari o sotto lo 0,2% della macchina, un solo picco dello 0,6% nell'ora. Sulla scala a un core dei valori Linux (12 core logici) corrisponde a ~0,6% di media, poco sopra la soglia di ~0,5%, con un picco di ~7%: un carico piccolo in assoluto, ma non a riposo come il profilo usa e getta di Linux (0,01%). I candidati sono il polling reale e il polling del cursore a 120 ms; #138 ferma anche quest'ultimo. La memoria è scesa lentamente da ~346 a ~303 MB per campione, probabilmente perché Windows riduce il working set dei processi inattivi. Come su Linux, il working set sommato conta le pagine condivise una volta per processo, quindi sovrastima l'occupazione reale; non è confrontabile con i valori PSS di Linux qui sopra, solo in modo approssimativo con il working set sommato di Linux (712 MB, prima di #99, profilo usa e getta, strumento e sistema diversi). Lettura secondo la regola: CPU appena sopra e memoria sopra ~250 MB di poco → **tuning** (#138), non il punto 5; quale processo domini non è stato misurato su Windows, quindi si segue per analogia il dettaglio di Linux. Il passo di tuning rimasto è lo stesso di Linux: distruggere la finestra alla chiusura (~30-55 MB in PSS su Linux; il risparmio su Windows non è stimato). Dopo, la regola vale come scritta sopra: punto 5 solo se il tuning non basta.

## Aggiornamento (2026-10-22): 2a fatto (#23)

`core/usage.ts` contiene l'orchestrazione dei fetch e il calcolo degli snapshot: `createUsageCore(deps).buildUsageSnapshot(mode)` più `computeAccountSnapshot`/`computeWindowSnapshot`/`resolvePeriodBounds` e la bozza di storico. Tutto ciò che dipende da Electron è iniettato (`UsageCoreDeps`): lo store (`UsageStore`: account, ui, storico, ultimo risultato valido), il fetch dei provider, il controllo di connessione, le local insights, il testo degli errori, l'hook di format drift, l'orologio e il logger. Lo stato di backoff per account vive ora dentro l'istanza del core. `main.ts` tiene finestre, tray, IPC, timer, notifiche, il processo utility delle local insights e l'adattatore verso lo store. Il codice è stato spostato senza modifiche; `core/usage.test.ts` ne fissa il comportamento su uno store in memoria (primo refresh, rilevamento del rialzo, ripiego sui dati vecchi, backoff, cache del refresh manuale, local insights, potatura, finestre a ore vs mensili). Prossimo passo: 2b, una CLI headless sullo stesso core. Non spostati: la politica di cache delle local insights (`computeLocalInsightsIfNeeded`, avvia un processo utility di Electron) e le notifiche.

## Aggiornamento (2026-10-25): quote di Claude rimosse (#140), nuova direzione (#139)

La pagina [Claude Code legal and compliance](https://code.claude.com/docs/en/legal-and-compliance) di Anthropic dice che gli sviluppatori di terze parti non possono offrire il login a Claude.ai nelle loro app e *"may not collect, store, or intermediate Claude.ai credentials or session tokens — sign-in to a Claude account must complete through Anthropic's own flow"*, con possibili provvedimenti senza preavviso. Catturare il `sessionKey` da una finestra di login incorporata, l'unico modo con cui questa app leggeva le quote di Claude, è proprio quel caso. #140 lo rimuove: niente login a Claude, niente fetch da claude.ai, e le sessioni salvate dalle versioni precedenti vengono cancellate al primo avvio. Gli account Claude restano per gli insight locali, che leggono solo file locali di Claude Code.

Riusare i token OAuth di Claude Code è vietato dalla stessa regola. Esistono due fonti consentite, entrambe tramite il binario non modificato di Anthropic e il suo login: il JSON della status line (`rate_limits.five_hour` / `seven_day`), che Claude Code **non** esegue nell'estensione VS Code (provato il 2026-10-25, segnalato in anthropics/claude-code#21943), e il pannello `/usage` di `claude` avviato in uno pseudo-terminale (spike #141). Tornano con il core autonomo in Go (#139, milestone v0.6.0), che è anche la risposta al punto 5: un core piccolo sempre attivo con la UI Electron come client opzionale.

## Aggiornamento (2026-10-31): budget di risorse del core Go (#145)

Misurato su Linux, `uname -m`: `x86_64`, con `go version go1.27.3 linux/amd64`. Lo scheletro espone soltanto `version` e `help`, senza provider o chiamate di rete.

| Target | Dimensione stripped (MB) |
|---|---:|
| linux/amd64 | 2,7 |
| linux/arm64 | 2,7 |
| windows/amd64 | 2,9 |
| darwin/arm64 | 2,6 |
| darwin/amd64 | 2,8 |

Per una singola esecuzione di `hypermiler version` su linux/amd64, mediane su 10 esecuzioni: **1,46 ms di wall time** e **6,4 MB di max RSS** (6.380 KiB; intervallo 6.188-6.472). Metodo: ogni target compilato da `gocore/` con `CGO_ENABLED=0 GOOS=<os> GOARCH=<arch> go build -trimpath -ldflags "-s -w" ./cmd/hypermiler`, versione lasciata a `dev`, dimensione = byte del file / 1.000.000. Wall time da `time.perf_counter()` attorno a `subprocess.run([binary, "version"])` in Python, un processo Python nuovo per ogni esecuzione. Max RSS da `busybox time -v` (un padre che fa fork, quindi il figlio eredita prima dell'`exec` solo i ~1,7 MB di busybox; un padre Python riporterebbe invece i propri ~14 MB).

Questa è la baseline con cui confrontare ogni passo successivo: gli adapter #148/#149 aggiungeranno un processo avviato e tempo di rete.

**Con l'adapter Codex (#148)**, stessa macchina, `codex-cli 0.163.0` reale con login (binario nativo trovato dallo shim npm nel `PATH`): un `hypermiler usage codex --json` richiede **~0,53 s di wall time** e **~0,3 s di CPU** (user + system dell'intero albero di processi), su 3 esecuzioni con `busybox time -v`. La max RSS è **~490 MB**, ed è del `codex app-server` avviato, non del core (che resta ai ~6 MB sopra): `ru_maxrss` dei figli è quella del discendente più grande. Dura mezzo secondo per refresh e non resta nulla in memoria (nessun processo `codex` dopo l'esecuzione), ma è il numero da tenere d'occhio se il ritmo di refresh diventa più rapido.

**Con l'adapter Copilot (#149)** il binario include `net/http`, TLS e il keyring (D-Bus su Linux): le dimensioni stripped salgono a **8,3 MB** linux/amd64, 7,7 linux/arm64, 8,2 windows/amd64, 7,5 darwin/arm64, 8,1 darwin/amd64 (da 2,6-2,9). Un `usage copilot` che si ferma alla lettura del keyring (nessun token salvato) richiede **~12,6 MB** di max RSS (`busybox time -v`, 5 esecuzioni), con un wall time trascurabile; una lettura reale aggiunge le chiamate a GitHub (ciascuna limitata a 10 s).
