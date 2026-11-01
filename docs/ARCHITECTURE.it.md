🌐 [English](ARCHITECTURE.md) | **Italiano**

# ARCHITECTURE.md — Struttura dell'applicazione (Giorno 1, Sessione 2 — Plan Mode)

> Documento di design prodotto in **plan mode**, come da flusso di lavoro in `CLAUDE.md`: descrive cosa viene costruito ed è stato aggiornato man mano che le funzionalità sono state implementate (sezioni marcate *implementato* / *aggiornato*).
> Si basa sui vincoli reali emersi in `RESEARCH.md` (v3): Claude espone soprattutto **percentuali di utilizzo** su finestre multiple e concorrenti (5 ore + settimanale), non un pool di token con un unico totale; Copilot espone invece contatori discreti (premium requests / crediti) su un ciclo di fatturazione mensile.

---

## 0. Una decisione di design che precede tutto il resto

Il concept originale in `CLAUDE.md` (`{ used, total, resetDate }`) assume **un solo contatore per servizio**. La ricerca mostra che non basta:

- **Claude**: 2-3 finestre di quota concorrenti e indipendenti — `five_hour` (rolling 5h), `seven_day` (rolling 7gg, tutti i modelli), `seven_day_opus` (rolling 7gg, solo Opus). Ognuna ha una propria `utilization` (%) e un proprio `resets_at`. Non c'è un "totale token mensile" da leggere dall'account: il rinnovo mensile esiste solo come **data di fatturazione dell'abbonamento**, scollegata dalle finestre di utilizzo.
- **Codex (ChatGPT)**: percentuali su finestre rolling di 5 ore e 7 giorni, lette dal core Go da `codex app-server` (#148) e riconosciute da `windowDurationMins`, mai dalla posizione primary/secondary.
- **Copilot**: un contatore discreto (`premium_requests` + `ai_credit`) legato al ciclo di fatturazione mensile — qui il modello "used/total/resetDate" originale funziona bene così com'è.

**Proposta:** generalizzare l'interfaccia dei service da un singolo contatore a una **lista di "finestre di quota"** (`quotaWindows`) per account, ciascuna con il proprio tipo di periodo (`rolling-hours`, `rolling-days`, `billing-cycle`) e la propria unità di misura (`percentage` per Claude e Codex, `count` per Copilot). L'indicatore "Token/gg" richiesto va quindi ridefinito come **"% di quota consumata per giorno lavorativo"** per Claude, e come "richieste premium consumate/giorno lavorativo" per Copilot — normalizzando comunque tutto a una percentuale per il confronto visivo nel widget quando serve mostrare un unico numero aggregato.

Questo va confermato esplicitamente perché cambia l'interfaccia `fetchUsage()` già scritta in `CLAUDE.md`:

```js
// Proposta di interfaccia estesa (sostituisce { used, total, resetDate, dailyHistory })
{
  planTier: string,
  subscriptionRenewsAt: Date | null,        // data di fatturazione, se nota
  quotaWindows: Array<{
    id: string,                              // 'five_hour' | 'seven_day' | 'seven_day_opus' | 'ai_credits' | ...
    label: string,
    periodType: 'rolling-hours' | 'rolling-days' | 'billing-cycle',
    periodLength: number | null,             // ore, giorni o mesi secondo periodType (5, 7, 1); null = ignota, niente pacing
    unit: 'percentage' | 'count',
    used: number,                            // 0-100 se percentage, valore assoluto se count
    total: number | null,                    // null se il servizio non espone un totale (caso Claude)
    resetsAt: Date,
  }>,
  dailyHistory: Array<{ date: string, perWindow: Record<string, number> }>,
}
```

Se preferisci restare più semplici e trattare solo la finestra più rilevante per servizio (es. solo `seven_day` per Claude, ignorando 5h e Opus), possiamo farlo come opzione di scope ridotto — ma perderesti la vista "sto per sforare la finestra delle 5 ore proprio oggi", che è probabilmente l'informazione più operativa per non restare bloccati a metà giornata.

---

## 1. Impostazioni (settings) — schema dati

Estende `store/index.ts` (default in `store/defaults.ts`, normalizzazione del file su disco in `store/normalize.ts`). Lo store cifrato (`encryptionKey` di `electron-store`) non contiene più segreti dei provider: il token Copilot sta nel keyring dell'OS, gestito dal core Go (#179), e Codex non salva nulla di segreto (#178). I segreti non arrivano mai al renderer.

**Rename in AIpermiler (v0.5.0, #200):** Electron ricava la cartella userData dal nome dell'app, quindi fino alla v0.4.18 lo store stava in `<appData>/ia-hypermiler`. Prima che lo store venga creato (`main/userDataBootstrap.ts`, il primo import di `main.ts`), `main/userDataMigration.ts` copia il vecchio `ia-hypermiler-config.json` nella nuova cartella come `aipermiler-config.json` (file temporaneo + rename, mai sopra uno store esistente), scrive un marcatore e poi cancella la vecchia cartella, best-effort, riprovando all'avvio successivo. Una copia fallita ferma l'app con un dialog di errore prima che lo store esista (altrimenti uno store vuoto nasconderebbe per sempre quello vecchio); senza marcatore la vecchia cartella non viene mai cancellata. Disconnessione e rimozione aspettano il refresh in corso, così lo spostamento del token qui sotto non può riscrivere un token che si sta cancellando. Si sposta solo lo store: il resto sono dati di Chromium che all'app non servono, comprese le partition claude.ai di #140. Non succede nulla quando userData non è la cartella predefinita `<appData>/AIpermiler` (test, misure). Il core sposta il token Copilot allo stesso modo: un segreto trovato solo sotto il vecchio servizio del keyring `hypermiler` viene copiato in `aipermiler` alla prima lettura e tolto dal vecchio servizio (`gocore/internal/secrets`, `Migrating`). Gli installer sostituiscono la vecchia app: il GUID NSIS è fissato su quello del vecchio appId, il `.deb` dichiara `Replaces`/`Breaks: ia-hypermiler`, il metainfo AppStream fa `<replaces>` del vecchio id.

**Copilot:** letto tramite la CLI del core Go da #179 (`aipermiler usage copilot --account <id> --host … --scope …`). Anche il collegamento passa dal core: un PAT va a `token set` su stdin, l'accesso con OAuth App è `login` (loopback PKCE nel core; l'app apre solo l'URL di autorizzazione annunciato dal core, verificato rispetto all'host dell'account). Il core tiene il token nel keyring dell'OS; disconnessione e rimozione chiamano `token delete`. `main.ts moveCopilotTokensToKeyring` consegna una volta il token salvato da una versione precedente, poi lo cancella dallo store.

**Codex:** letto tramite la CLI del core Go da #178: `main/providers.ts` interroga `main/core.ts` (`aipermiler usage codex [--codex-bin <percorso>]`, contratto in [CORE-CLI](CORE-CLI.it.md)), e il core esegue `codex app-server` con l'accesso a Codex dell'utente. Niente finestra di login, cookie di partition o token nell'app. Senza accesso arriva il codice `signed_out`, mostrato come indicazione per accedere (`error.codexSignedOut`). La `partition` dell'account resta solo perché i cookie chatgpt.com della vecchia route di login (#82) vengano cancellati una volta all'avvio.

```js
{
  // Registro di N account indipendenti dal provider (issue #4, EVOLUTION.md punto 3):
  // parte comune + parte specifica, unione discriminata su `provider`. Fino a v0.1.2 erano
  // due slot fissi { claude, copilot }: convertiti una volta all'avvio da store/migrate.ts,
  // mantenendo gli id 'claude'/'copilot' (così history.* resta valido senza riscritture).
  // La parte specifica per provider lato main vive in main/providers.ts.
  accounts: Array<{
    // --- comune ---
    id: string,                // 'claude'/'copilot' se migrato, altrimenti '<provider>-<uuid8>'
    provider: 'claude' | 'copilot' | 'codex',
    label: string,             // nome mostrato in tabella e nelle tab del widget ("Claude", "Claude 2"…)
    enabled: boolean,
    subscription: {
      renewalRule: { type: 'dayOfMonth', day: number } | { type: 'rrule', rrule: string },
    },
    workSchedule: {  // per account dalla 0.4.6 (prima era un'impostazione globale, ereditata da ogni account all'aggiornamento)
      enabled: boolean, // se false, ogni giorno vale come giornata piena (pacing non legato a giorni/ore specifici — es. account personale)
      days: {
        mon: 'full' | 'half' | 'off',
        tue: 'full' | 'half' | 'off',
        wed: 'full' | 'half' | 'off',
        thu: 'full' | 'half' | 'off',
        fri: 'full' | 'half' | 'off',
        sat: 'full' | 'half' | 'off',
        sun: 'full' | 'half' | 'off',
      },
      hoursPerDay: number, // un solo numero, non un intervallo inizio/fine (feedback utente, Giorno 2):
                           // l'arco lavorato si legge dai campioni del giorno, dal primo all'ultimo
                           // aumento (budget.todayActivitySpan, anticipato dalla prima sessione
                           // Claude Code locale di oggi); parte di oggi trascorsa = arco /
                           // hoursPerDay (budget.todayElapsedUnits, almeno 2h).
    },
  } & (
    { // --- provider: 'claude' ---
      authMethod: 'password' | 'google' | 'sso',
      session: {
        sessionKey: string,        // cifrato; sempre null da #140
        organizationId: string | null, // risolto automaticamente al login (GET /api/organizations)
        capturedAt: string,        // ISO date
        expiresAt: string | null,  // stimata ~30gg, da ri-validare
      },
      partition: string,         // 'persist:account-<id>': cookie claude.ai isolati per account,
                                 // cancellati da Disconnetti/Rimuovi e prima di ogni login (issue #4)
      localInsights: boolean,    // sessioni Claude Code locali attribuite a questo account (max 1)
    } | { // --- provider: 'codex' ---
      partition: string,         // persist:account-<id>, conteneva i vecchi cookie chatgpt.com (cancellati una volta, #178)
      binaryPath: string | null, // percorso assoluto di codex, o null: lo trova il core
    } | { // --- provider: 'copilot' ---
      authMethod: 'pat' | 'oauth',  // sceglie quale pannello di connessione mostrare; aggiornato dall'ultima connessione riuscita
      accountScope: 'personal' | 'organization', // sceglie gli endpoint letti (piano personale o seat aziendale)
      credentials: { token: string | null, username: string | null },  // token: null da #179 (sta nel keyring dell'OS del core,
                                 // voce copilot/<id>); qui attende solo un token di una versione precedente finché non viene spostato
      oauthApp: { clientId: string | null },  // non è un segreto; il client secret non viene mai persistito
      manualQuota: number, // l'API di billing non espone il totale del piano: valore inserito dall'utente
      experimentalWarningAcknowledged: boolean,
    }
  )>,


  ui: {
    language: 'auto' | 'en' | 'it',       // 'auto' segue la lingua del sistema (vedi §4c)
    windowStyle: 'filled' | 'filled-dark' | 'transparent-digital',
    alwaysOnTop: boolean,
    animations: boolean,                 // default true; reduced motion ha la precedenza
    accentColor: string,
    bounds: { x, y, width, height },       // posizione/dimensione persistita
    chartRange: 'week' | 'month',
    notificationThresholdPercent: number,  // default 80, configurabile
  },

  history: {
    // append-only, un record per giorno per finestra di quota; retention configurabile (default 90gg) per non far crescere il file all'infinito
    dailyUsage: Array<{ date: string, accountId: string, windowId: string, used: number }>,
    lastGood: Record<accountId, RawAccountUsage>, // ultimo dato riuscito per account (fallback)
  },

  advisorCache: { generatedAt: string, adviceText: string },

  meta: {
    notifiedToday: Record<string, boolean>, // flag anti-doppia-notifica per account/giorno
    claudeCookiesMigrated?: boolean,         // copia una tantum dei cookie da defaultSession alla partition del Claude migrato
  },
}
```

Sezioni del pannello impostazioni (finestra separata `renderer/settings.html`, aperta dal tray o da un'icona ingranaggio nel widget):

1. **Account e sessioni** — *(aggiornato, issue #4)* una **tabella** di account (Nome | Provider | Stato | Attivo | azioni Configura/Connetti/Disconnetti/Rimuovi) con "Aggiungi account"; "Configura" apre sotto la riga il pannello del provider (campi comuni + specifici). Descrizione originale: per Claude e Copilot: stato connessione, metodo (password/SSO/PAT/OAuth device), pulsante "Connetti/Riconnetti" che apre una `BrowserWindow` di login per Claude o il device-flow per Copilot, data di scadenza sessione stimata, toggle "seat aziendale" con avviso automatico se attivo su Copilot ("funzionalità sperimentale, può interrompersi senza preavviso").
2. **Rinnovo** — giorno di rinnovo abbonamento *(tipo piano e ambito dell'account Claude rimossi a 2026-10: inseriti a mano e non usati da nessun calcolo; gli account si distinguono dal nome)* (selettore semplice "giorno del mese"; dietro le quinte salvato come RRULE minimale `FREQ=MONTHLY;BYMONTHDAY=n` così in futuro si possono aggiungere ricorrenze diverse senza cambiare schema). *(Da 2026-10:)* il giorno manuale è solo un ripiego — la fine periodo di una finestra è il suo `resetsAt`, poi il `subscriptionRenewsAt` del provider (Copilot `quota_reset_date`; il report di fatturazione riparte per definizione il 1° del mese UTC successivo), poi il giorno manuale. Le Impostazioni bloccano il campo e mostrano la data del provider quando ogni finestra con pacing ne ha una (`renderer/renewal.ts`); oggi serve solo per il limite di spesa Claude aziendale.
3. **Calendario di lavoro** — *(per account dalla 0.4.6: ultima sezione del dettaglio di ogni account, compressa con un riassunto di una riga)* 7 selettori giorno con 3 stati (pieno/mezza/riposo), un interruttore "attivo" (spento = ogni giorno conta come giornata piena, es. account personale) e ore/giorno (riservato). Usato per calcolare budget e proiezioni su "giorni lavorativi rimanenti", non su giorni di calendario.
4. **Aspetto** — lingua (Automatica / English / Italiano), stile finestra (le tre skin descritte sotto), always-on-top, colore accento, intervallo grafico (settimana/mese) di default.
5. **Notifiche** — soglia percentuale di allarme (default 80%, come da `CLAUDE.md`, ma ora configurabile), eventualmente per-finestra (es. avviso separato per il limite 5h di Claude).
6. **Avanzate** — placeholder per le evoluzioni future (vedi §5): abilitazione futura server locale, export dati.

---

## 2. Finestra principale — le tre "skin"

Tutte leggono dagli stessi dati (IPC dal main, nessuna duplicazione di logica) e cambiano solo `renderer/style.css` + flag di creazione della `BrowserWindow`.

**Stile "pieno" (classico - light):**
sfondo opaco, layout a blocchi con bordi. *(Aggiornato:)* sempre `frame: false` con la titlebar custom dell'app, che compare in hover — con `frame: true` la titlebar nativa del sistema operativo compariva sopra quella custom (feedback utente).

**Stile "pieno" (dark):** *(aggiunta, feedback utente)*
Stessa struttura opaca/non trasparente della skin classica, con colori invertiti (sfondo scuro, testo chiaro) — vedi i token `--*-filled-dark` in `renderer/style.css`. Nessuna differenza di comportamento rispetto alla skin classica oltre ai colori (stessa titlebar custom, stesso comportamento hover, stesso layout).

**Stile "trasparente/digitale":**
`frame: false`, `transparent: true`, `titleBarStyle` nascosto. Numeri/barre in stile HUD (font monospazio, glow statico leggero). Pulsanti di sistema (chiudi/riduci) ricreati come controlli custom in overlay, `opacity: 0` di default e `opacity: 1` solo on-hover via CSS, con `-webkit-app-region: drag` sull'area libera per permettere lo spostamento finestra senza barra del titolo nativa.

**Animazioni di stato opzionali (#119, #66, #121):** le animazioni sono ammesse ma devono essere disattivabili, sobrie, comunicare uno stato e fermarsi con `prefers-reduced-motion`. Impostazioni → Aspetto → **Animazioni della finestra** (`ui.animations`, default `true`, normalizzato anche per gli store esistenti) controlla gli effetti nel solo renderer, senza dipendenze:

- Se una finestra di quota dell'account visualizzato è a rischio o esaurita, i valori mostrati in rosso (`.severity-warning` e il verdetto a rischio/esaurito delle righe delle finestre) hanno un glow rosso statico con `text-shadow`, disegnato una volta (#125; ha sostituito un bordo attorno a tutta la finestra, che non diceva quale valore fosse a rischio). Dove le righe delle finestre sono nascoste, l'allarme passa all'etichetta sotto il valore (rossa, con un pallino), così una finestra a rischio sotto la soglia di colore non resta muta: in vista compatta il nome dell'account, nella vista completa di un account con una sola finestra l'intera etichetta. Un timer fa lampeggiare quei valori ogni 10 s per 1,5 s (netto: tonalità quasi bianca con glow rosso, poi più scura, tre volte, con `color-mix`), senza animazioni fra un lampeggio e l'altro. Animare `color` ridisegna questi pochi valori di testo brevi: è l'unica eccezione accettata alla regola opacity/transform, perché una pulsazione di opacità del testo rosso si vedeva appena. Le barre sono ritagliate dal loro binario e non hanno glow. Il timer viene cancellato quando termina l'allarme, si disattiva l'impostazione, si richiede reduced motion o il documento viene nascosto/ridotto a icona.
- La percentuale principale fa scorrere verso l'alto le cifre cambiate con una transizione `translateY` ease-out di 700 ms. Primo rendering, cambi di account/finestra/lingua e segnaposto sono immediati. I separatori decimali e delle migliaia restano fermi; a transizione conclusa torna testo semplice e il valore completo resta sempre disponibile alle tecnologie assistive.
- Durante ogni aggiornamento (widget, timer, tray o Impostazioni), un segmento sottile nel colore d'accento, con `box-shadow` morbido statico, percorre tutti e quattro i bordi tramite `transform`. Le coordinate del viewport seguono ridimensionamento e vista compatta. `refreshOnce()` invia l'evento tipizzato `usage:refreshing` con `true` prima di ogni esecuzione; il preload lo espone come `onUsageRefreshing`. L'evento esistente `usage:update` termina gli aggiornamenti riusciti; un'esecuzione fallita invia `usage:refreshing` con `false` perché non seguirà uno snapshot. Entrambi i segnali saltano le finestre del widget assenti o distrutte. Il widget avvia inoltre l'indicatore in modo ottimistico quando richiede un aggiornamento.
- Le frecce del consumo istantaneo (#66) usano la stessa media dell’ultima ora (`instantRate`) e lo stesso target sostenibile del gauge. `renderer/instantFlow.ts` gestisce la zona morta inclusiva ±15%, direzione e velocità: 18 px/s × scarto relativo, massimo 36 px/s (un periodo di 48 px dura almeno 1,3 s). Valori mancanti/non validi non mostrano frecce; target zero con consumo positivo produce scorrimento a sinistra alla velocità massima. Le frecce a destra richiedono un giorno full/half nello schedule locale dell’account; con schedule disabilitato non ci sono vincoli. Lo schedule non definisce orari di inizio/fine: il vincolo vale per l’intero giorno. Le frecce a sinistra compaiono anche nei giorni liberi. Le frecce occupano solo lo spazio fra la fine della barra e il marker del target (`flowSpan`, #125) e puntano dove la barra dovrebbe arrivare, invece di sembrare un flusso lungo la barra. Uno strip separato, ritagliato e a bassa opacità scorre di un periodo esatto di 48 px usando solo `translateX`; nessuna animazione di background-position, paint/layout o loop JS per frame. Lo stato motion esistente mantiene le frecce statiche con animazioni disattivate, reduced motion o finestra nascosta.
- Le sezioni native `<details>` del consiglio e degli insight locali si aprono e chiudono in 200 ms: il corpo sfuma e scorre mentre FLIP accompagna le sezioni sottostanti. In chiusura il corpo viene tolto dal flusso una sola volta, mantenendolo visibile in posizione assoluta; `open` viene rimosso solo a fine effetto. Si intercetta il click del `<summary>`, mantenendo attivazione da tastiera e semantica accessibile native.
- Passando alla vista completa la finestra viene prima ridimensionata, poi le sezioni aggiuntive entrano con una sfumatura e uno scorrimento di 200 ms. Tornando alla compatta, escono prima del ridimensionamento e restano invisibili fino alla risposta IPC. La freccia punta in alto nella compatta, in basso nella completa, con rotazione di 180°. `layoutMotion.ts` gestisce FLIP, Web Animations API e sequenza delle chiamate IPC: i click ripetuti completano gli effetti in corso e prevale l’ultima scelta, senza resize concorrenti. Il main process mantiene il ridimensionamento immediato esistente.

A parte il lampeggio di colore dell'allarme, si animano solo `opacity`/`transform`: niente ombre, filtri, gradienti animati, `offset-path`, loop JS per frame o animazioni del tray. Con l'impostazione disattivata, valori immediati, glow rosso senza lampeggio e frecce statici; ruota solo l'SVG del pulsante di aggiornamento mentre è in corso. Con `prefers-reduced-motion: reduce` si ferma ogni movimento (incluse le transizioni hover) e l'aggiornamento appare come icona attenuata con tooltip localizzato «Aggiornamento…». I cambi di visibilità cancellano i timer, completano le transizioni delle cifre e del layout (anche disattivando il motion) e rimuovono le classi di animazione del refresh; riaprendo la finestra riparte l'attesa intera di 10 s. Le verifiche visive delle tre skin nelle due lingue e le misure CPU a finestra aperta (idle/allarme/refresh, metodo `app.getAppMetrics()` in [EVOLUTION.it.md](EVOLUTION.it.md)) vanno eseguite sul desktop del proprietario: Electron è bloccato nella sandbox di implementazione.

**Always-on-top:** toggle in impostazioni e nel menu del tray, applicato con `win.setAlwaysOnTop(bool, 'floating')`; persistito e riapplicato all'avvio.

---

## 3. Contenuto informativo del widget

Corpo centrale — numero grande "current usage": la finestra di quota più critica al momento (quella con `utilization` più vicina al reset o più alta in %), con etichetta di quale finestra è (es. "Limite settimanale: 62%").

Sotto, un grafico a barre/linea dei **picchi giornalieri**, selezionabile settimana/mese, con overlay della linea di budget ideale (pacing lineare) per vedere a colpo d'occhio se si è sopra o sotto.

*(Implementato così dopo EVOLUTION.md punto 1 — prima il grafico mostrava la % cumulata per giorno, un calco della dashboard del provider.)* Ogni barra è il **consumo di quel giorno** (`budget.dailyDeltas`: differenza con la base del giorno `dayStartUsed`, o col giorno precedente per i punti più vecchi, reset esclusi), accanto a una barretta con la quota uniforme di un giorno lavorativo pieno (colorata per la parte lavorativa di quel giorno, grigia per il resto: metà in una mezza giornata, tutta in un giorno libero) e a una linea tratteggiata con il **budget mobile** (`budget.chartDays`): su un giorno passato quanto era disponibile quella mattina (quanto restava a inizio giornata sulle unità lavorative da quel giorno al reset, per l'unità del giorno), oggi il budget di oggi, e sui 2 (vista 7 giorni) o 5 (vista 30 giorni) giorni a venire la quota restante ridistribuita — una giornata pesante abbassa la linea dei giorni successivi. Barre oltre il budget di quel giorno in `--warning`. I giorni di un periodo precedente mostrano solo il consumo. Lo spazio di oggi ha una fascia di sfondo leggera. Non mostrato per finestre `rolling-hours`. Se l'account ha più finestre di quota, sopra al valore corrente c'è una **lista con verdetto** calcolato dall'app (`budget.windowVerdict`), la critica per prima (a parità di utilizzo quella con pacing), al posto delle tab che affiancavano solo le metriche del provider. Il verdetto si basa sulla **quota restante ripartita** (`budget.redistributedQuota`: quanto resta ora diviso sulle unità lavorative da oggi in poi, accanto alla quota uniforme del periodo): a rischio sotto metà della quota uniforme, *quota ridotta* sotto il 95%, in linea entro ±5%, *margine* sopra — con la quota per giorno lavorativo nel testo. Non richiede storico, quindi c'è dal primo aggiornamento del mattino, e dopo una giornata pesante dice quanto resta da spendere invece di estendere quella giornata a tutto il periodo. Le finestre `rolling-hours` mantengono il verdetto su proiezione/autonomia; esaurita e nessun pacing invariati.

**Valore per token** *(EVOLUTION.md punto 4, solo account Claude con insight locali)*: "Resa" = token di output delle sessioni Claude Code locali per 1% di quota consumata (`budget.tokenYield`, con trend), e un consiglio causale su contesto ampio (`budget.consumptionCause`) mostrato **solo** se il segnale è netto. Limite dichiarato: l'SDK non espone l'orario dei singoli messaggi, ogni sessione è attribuita al giorno di ultima modifica.

Riquadro metriche:

- **Token (o % quota)/giorno lavorativo corrente** — richiesto. *(Implementato come "Oggi: usato / budget", `budget.todayBudget`:)* budget di oggi = quanto restava a inizio giornata diviso sulle unità lavorative da oggi in poi, fisso per la giornata; il consumo di oggi è misurato dalla base del giorno (`DailyUsagePoint.dayStartUsed`, esatta anche nel giorno di un reset). Oltre `PACE_ALERT_RATIO` (1,5×) parte una notifica di sistema, al massimo una al giorno per account — la sola soglia dell'80% arrivava troppo tardi (un consumo forte il primo giorno del mese passava inosservato).
- **Andamento settimanale** — richiesto (il grafico sopra)
- **Indice di efficienza** — richiesto. Proposta di formula: rapporto tra ritmo di consumo ideale e ritmo reale, calcolato sulle **unità lavorative** trascorse (non giorni di calendario):
  `efficiencyIndex = (idealPace) / (actualPace)` dove `idealPace = 100% / unitàLavorativeTotaliNelPeriodo` e `actualPace = utilizationAttuale / unitàLavorativeTrascorse`. Valore intorno a 1 = in linea; >1 = si sta usando meno del previsto (margine per usare di più); <1 = si sta consumando più veloce del sostenibile.
- **Previsionale** — richiesto: proiezione dell'utilizzo a fine periodo, estrapolando il ritmo sulle unità lavorative rimanenti. Il ritmo mescola al 50% la media del periodo con gli ultimi 3 giorni lavorativi completati (`budget.recentPacePerUnit`), così un cambio di abitudini si vede subito; non limitato a 100% (es. "227%" dice di quanto si sforerebbe). Il tempo trascorso conta la parte di oggi già lavorata (`budget.todayElapsedUnits`). Con meno di 2 unità lavorative trascorse previsionale e autonomia sono indicati come *stima preliminare* e i consigli basati su di essi sono saltati. Rating, picco/media e streak contano solo i giorni conclusi (lo 0% della giornata in corso valeva come giornata perfetta).
- **Giorni alla scadenza** — richiesto: sia giorni di calendario sia giorni **lavorativi** rimanenti (spesso più utile).
- **Giorni di autonomia stimati** *(aggiunta)* — a quanti giorni lavorativi si esaurirà la quota mantenendo il ritmo attuale, utile quando è < giorni alla scadenza (segnale di rischio più diretto del solo indice di efficienza). *(Aggiornato:)* mostrata solo in quel caso — altrimenti ripeteva il previsionale (`renderer/visibility.ts`).
- **Picco massimo vs media giornaliera** *(aggiunta; calcolato sui delta giornalieri, `budget.deltaStats`)* — per capire se i problemi sono concentrati in giornate anomale o distribuiti. *(Aggiornato:)* mostrato da due giorni completi in poi.
- **Streak sotto budget** *(aggiunta)* — giorni lavorativi consecutivi entro il budget ideale, per rinforzo positivo leggero (in linea con "sobria", quindi solo un numero, non badge/gamification vistosa). *(Aggiornato:)* nascosto finché è 0; un giorno lo interrompe se ha superato il suo budget mobile — gli stessi giorni rossi nel grafico.
- **Vista combinata multi-servizio** *(aggiunta, se entrambi Claude e Copilot attivi)* — un indicatore di "salute generale" che aggrega le percentuali delle finestre più critiche dei due servizi, utile per uno sguardo d'insieme prima di aprire il dettaglio.
- **Tips/consigli del giorno** — richiesto. *(Implementato:)* ricavati dai dati reali da `budget.generateDailyTip` (condizioni esplicite sulle metriche calcolate, mai una frase generica); *(aggiornato:)* solo consigli che aggiungono un'azione o una causa — tolti quelli che ripetevano gauge, previsionale o quota ridistribuita, e senza consiglio la sezione è nascosta; `agents/advisor.ts` (Claude Sonnet, cache 24h come da `CLAUDE.md`) è ancora uno stub.

---

## 4. Tray (system tray) cross-platform

`Tray` nativo Electron con icona per piattaforma (asset `.ico`/`.png`/`.icns` gestiti da `electron-builder`). Comportamento uniforme proposto:
- Click sinistro → toggle mostra/nascondi finestra principale (su macOS il click sinistro apre di norma il menu: gestiamo quindi mostra/nascondi anche da un voce di menu esplicita, per coerenza su tutte le piattaforme).
- Click destro (o click su macOS) → menu contestuale: Mostra/Nascondi, Impostazioni, Aggiorna ora, Always-on-top (toggle rapido), Esci; più "Aggiornamento disponibile (X)…" quando c'è una nuova versione.
- Tooltip icona: percentuale della finestra critica di ogni account (es. "Claude 62% · Copilot 40%"), dati non aggiornati segnalati (`main/traySummary.ts`).
- Badge di utilizzo (`main/trayBadge.ts`): selezione, tracking Auto, rilevamento avvisi, voci del menu e bitmap sono funzioni pure; il wiring Electron resta in `main/tray.ts`. `ui.trayBadge = { view, critical }`, dove `view` è `{ mode: 'off' }`, `{ mode: 'auto' }` (predefinito), oppure `{ mode: 'manual', accountId, windowId }`; `critical` è attivo per default. Il main persiste la scelta e pubblica `TrayViewState` (selezione, vista effettivamente mostrata, voci radio) tramite `view:get` / `view:update` nel contratto IPC condiviso; la selezione cambia solo dal menu del tray o dalle Impostazioni (`settings:set`), mai dal pannello. Tray e Impostazioni elencano Disattivato, Auto, poi ogni account · finestra. Ogni cambio di vista/snapshot ricostruisce il menu nativo.
- Auto segue l'ultimo account con un contatore aumentato rispetto allo snapshot precedente, ignorando risultati stale, nuove finestre e totali cambiati; aumenti simultanei usano il `lastUpdatedAt` più recente, con ordine stabile degli account a parità. Parte dall'utilizzo più alto e resta fermo durante l'inattività o dopo un reset. Il numero viene dalla finestra critica dell'account. In ogni modalità (Disattivato, Auto, Manuale) un clic nel pannello sposta solo la vista locale del pannello; in Auto e Disattivato il pannello la mantiene, in Manuale resta finché la selezione non cambia. Altrimenti in Manuale il pannello mostra la vista selezionata, anche quando il badge non può disegnarla (una finestra senza percentuale), e la vista effettiva del badge solo finché quella selezionata manca; un cambio di selezione dal tray o dalle Impostazioni arriva al pannello. Un account senza finestre di quota si apre in locale in ogni modalità, finché la selezione non cambia. Un account disabilitato o una finestra assente conserva la preferenza Manuale e la voce disabilitata selezionata, mostrando temporaneamente Auto; al ritorno si ripristina. Le vecchie stringhe migrano in `store/migrate.ts`: off → Disattivato, critical → Auto con avvisi attivi, id account → Manuale sulla finestra critica in cache (oppure finestra null risolta una sola volta al primo snapshot utile).
- Avviso critico: ogni altra finestra pari o sopra la soglia (`pickAlerts`, dalla più alta, i valori stale mantengono il segno) compare a turno ogni 2 s come `!NN`, poi torna la vista normale (normale → `!97` → `!84` → normale); il tooltip nomina ogni account. Windows/Linux disegnano una bitmap arrotondata da 32 px con glifi bianchi 3×5: verde / arancione / rosso dalla soglia di notifica, grigio se stale. macOS mantiene l'icona e usa il titolo (`62%`, `*` se stale). Critical controlla ogni altra coppia account/finestra rispetto a `ui.notificationThresholdPercent`: finché esiste un avviso un timer di 2 s esegue il ciclo descritto sopra, avvisi in rosso con `!95` (`100` perde `!` per entrare); macOS usa `!95%`. Questa informazione è indipendente da `ui.animations`. I valori del badge sono memorizzati fra aggiornamenti; il timer non legge lo store. Nessun timer in assenza di avvisi, con badge/avvisi disattivati o dopo l'uscita. Nessun overlay della barra applicazioni Windows; Linux richiede StatusNotifier (es. estensione AppIndicator su GNOME).

---

## 4b. Aggiornamenti dell'app (issue #5)

- **Cosa fa:** all'avvio (dopo ~10s, solo app pacchettizzata) e ogni 24h, `services/updates.ts` legge l'elenco delle Release GitHub del progetto e confronta la versione più alta (semver con pre-release, bozze escluse; le pre-release GitHub solo se la versione installata è una pre-release) con `app.getVersion()`. Se è più recente: notifica di sistema una sola volta per versione (`updates.notifiedVersion`), voce nel menu tray e card "Aggiornamenti" in Impostazioni con "Scarica X" (apre nel browser il pacchetto per l'OS in uso) e "Controlla ora". Disattivabile (`updates.autoCheck`).
- **Endpoint:** `GET https://api.github.com/repos/suppressio/aipermiler/releases` (API REST ufficiale, anonima, 60 richieste/h per IP: ampio margine). **Non** `/releases/latest`: la CI pubblica ogni release come pre-release (`releaseType: "prerelease"`) e il proprietario promuove a mano quelle provate, quindi serve l'elenco completo. Le pre-release possono contenere build ancora da provare: a chi usa una release normale vengono proposte solo release normali, a chi usa una pre-release anche le pre-release più recenti. Nessun dato dell'utente nella richiesta.
- **Scelta del pacchetto:** `.exe` su Windows, `.dmg` della stessa architettura su macOS (la CI produce solo arm64: su un Mac Intel si apre la pagina della release), `.AppImage` se l'app gira come AppImage (`process.env.APPIMAGE`) altrimenti `.deb` su Linux.
- **Perché niente installazione automatica (electron-updater):** scelta dell'utente — nessuna dipendenza nuova, e con pacchetti non firmati l'aggiornamento in-app non funzionerebbe comunque su macOS. L'URL aperto viene sempre dallo store (scritto dal main) e deve iniziare per `https://github.com/suppressio/aipermiler/`, mai da un valore passato dal renderer.

## 4c. Lingua dell'interfaccia (multilingua)

- **Lingue:** inglese (primaria) e italiano. `ui.language`: `'auto'` (italiano se la lingua del sistema è `it*`, altrimenti inglese) oppure una scelta esplicita; applicata subito a widget, Impostazioni e tray, senza riavvio.
- **Nessuna libreria:** dizionari piatti tipizzati + `Intl` per numeri e date. Il renderer non ha bundler, quindi non può importare da `node_modules`; per due lingue un dizionario per processo basta. `en.ts` è il riferimento, `it.ts` è tipizzato sulle stesse chiavi: una chiave mancante o in più non compila.
- **Ogni processo possiede i propri testi** (due progetti TypeScript separati): `renderer/i18n/` per l'interfaccia (compresi consigli del giorno e verdetti), `main/i18n/` per tray, notifiche, finestre di dialogo, finestra di login e pagina di callback OAuth.
- **Dati, non frasi:** `budget.generateDailyTip` restituisce `{ key, params }` e la frase la compone il renderer; le etichette delle finestre di quota si traducono per `id`. I messaggi d'errore tecnici dei service restano in inglese (sono codice); il main li incornicia nella lingua dell'utente nei casi noti (sessione scaduta).
- **Testo nell'HTML:** inglese di default + attributi `data-i18n`, `data-i18n-title`, `data-i18n-aria-label`, `data-i18n-placeholder`.
- **Codice e documenti:** il codice è tutto in inglese; i documenti per umani esistono in inglese (`X.md`) e italiano (`X.it.md`).

## 5. Evoluzioni future (non implementate ora, solo predisposte)

**Integrazione con strumenti grafici esterni (Rainmeter, KDE Plasma, ecc.):**
Per non doppiare la logica, `budget.js` e il calcolo delle metriche restano moduli puri nel main process, richiamabili sia dal canale IPC verso il renderer sia — in futuro — da un piccolo **server HTTP locale in loopback** (`127.0.0.1`, porta configurabile, token di accesso locale generato all'avvio) che espone un endpoint read-only tipo `GET /api/status` con lo stesso JSON usato internamente. Rainmeter può leggerlo con un plugin WebParser/JSON; un Plasmoid KDE con una piccola QML che fa fetch periodico. Nessuna implementazione ora: solo il vincolo architetturale "tieni la logica di calcolo separata dalla UI" già rispettato dalla struttura in `CLAUDE.md`.

**Analisi di utilizzo avanzata (modello usato, numero di agenti, attività parallele):**
Per Claude Code questi dati sono già presenti nei JSONL locali (modello, conteggio token per tipo, id di sessione) — quindi è la fonte più ricca e a costo quasi zero da cui partire per questa funzionalità. Per l'uso via webapp claude.ai e per Copilot i dati disponibili sono più poveri (solo utilizzo aggregato). Predisponiamo lo schema `history.dailyUsage` con un campo opzionale `meta` (ignorato dall'aggregazione v1) per non dover fare migrazioni quando arriverà questa funzione:
```js
{ date, accountId, windowId, used, meta: { model?, sessionId?, parallelAgents?, activityType? } }
```

**Retrospettiva e direzioni future non decise:**
Dopo la chiusura dell'MVP (Giorno 3), l'utente ha aperto una retrospettiva concettuale sulla direzione del progetto (staccarsi dalla vista dei provider, separare dati/backend da UI con un'API interna, genericità multi-provider, ridefinire "efficienza" verso il valore-per-token, costo in risorse di Electron in modalità solo-tray). Spostata in un documento dedicato per non mischiare "cosa si sta costruendo" (questo file) con "direzioni non ancora decise, in valutazione costi/benefici" — vedi **`EVOLUTION.it.md`**.

---

## 6. Impatto sulla struttura file (rispetto allo scheletro in `CLAUDE.md`)

Aggiunte proposte in Sessione 2 (confermate e implementate):
- `renderer/settings.html` + `renderer/settings.ts` + `renderer/settings.css` — finestra impostazioni separata.
- `main/windows.ts` — creazione/gestione delle due `BrowserWindow` (skin pieno/trasparente) e della finestra impostazioni, per non appesantire `main.ts`.
- `main/tray.ts` — logica tray isolata.
- `budget.ts` — esteso per multi-finestra, efficienza, previsionale, autonomia stimata (resta comunque un modulo puro, testabile con `budget.test.ts` come richiesto in `PLAN.md`).

Aggiunte ulteriori in Giorno 2, Sessione 1 (services layer reale):
- `main/claude-auth.ts` — cancella le sessioni claude.ai salvate dalle versioni precedenti; l'app non accede più a claude.ai (#140).
- `services/_http.ts` — helper HTTP condiviso tra i due service (timeout esplicito, errori leggibili, mai `null` silenzioso — regola CLAUDE.md).
- Migrazione a TypeScript (feedback utente, dopo Giorno 2 Sessione 1): tutti i file convertiti a `.ts`, tipi condivisi in `types/index.ts`, test con `node:test` (`budget.test.ts`, `services/*.test.ts`, `tests/integration/*`). Dettagli in CLAUDE.md §"Build e test".
- `store.history.lastGood.<accountId>` — cache dell'ultimo snapshot riuscito per servizio, usata come fallback quando una fetch fallisce (mostrato in UI con timestamp e indicazione "dato non aggiornato").

Le aggiunte successive sono elencate nella struttura dei file di `CLAUDE.md` (registro account e provider, normalizzazione dello store, i18n, controllo aggiornamenti).

---

## Domande aperte confermate prima di scrivere il codice (storico)

1. Generalizzare `fetchUsage()` al modello multi-finestra (§0), o partire da una versione semplificata (una sola finestra "principale" per servizio)? → multi-finestra, adottato.
2. La formula di efficienza del §3 è utile così? → adottata.
3. Confermare le aggiunte proposte (giorni di autonomia stimati, picco vs media, streak, vista combinata)? → implementate le prime tre.
4. Tray: "click sinistro = toggle, click destro = menu" su tutte le piattaforme? → adottato, con mostra/nascondi anche come voce di menu.
