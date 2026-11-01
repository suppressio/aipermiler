🌐 [English](README.md) | **Italiano**

# AIpermiler

Un piccolo widget sempre in primo piano, per Windows, macOS e Linux, che ti dice a che ritmo stai usando le quote di **Claude**, **GitHub Copilot** e **Codex** (abbonamento ChatGPT) e quanto puoi ancora usare oggi perché la quota arrivi al rinnovo.

> **Le quote di Claude non sono disponibili in questa versione.** Anthropic non consente alle app di terzi di accedere a claude.ai né di salvarne la sessione ([Claude Code legal and compliance](https://code.claude.com/docs/en/legal-and-compliance)), ed era l'unico modo con cui questa app poteva leggerle. Un account Claude ora mostra solo gli insight locali di Claude Code. Le quote torneranno con una fonte consentita ([#139](https://github.com/suppressio/aipermiler/issues/139)).

<p align="center"><img src="docs/screenshots/widget-it.png" alt="Il widget di AIpermiler con dati di esempio" width="406"></p>

<sub>Dati di esempio, non un account reale.</sub>

- **Più account**, Copilot e Codex in qualsiasi combinazione (per esempio un account Codex personale e una licenza Copilot aziendale); un account Claude porta gli insight locali.
- **I tuoi giorni lavorativi**: ogni account può seguire un suo orario (giornata intera, mezza o libera per ogni giorno della settimana), così i weekend non contano come tempo in cui avresti potuto usare la quota.
- **Un budget per oggi**, ricalcolato ogni mattina da quanto resta.
- **Notifiche** quando una quota supera la tua soglia di allarme, o quando il consumo di oggi va molto oltre il budget di oggi.
- Interfaccia in italiano e inglese.

> Progetto personale in sviluppo attivo (`v0.x-beta`). Cosa è previsto e cosa è in corso: [roadmap](https://github.com/users/suppressio/projects/5).

---

## Installazione

Apri l'**[ultima release](https://github.com/suppressio/aipermiler/releases/latest)** e scarica, sotto *Assets*, il file per il tuo sistema:

| Sistema | File |
|---|---|
| Windows | `AIpermiler-Setup-<versione>.exe` |
| macOS (Apple Silicon) | `AIpermiler-<versione>-arm64.dmg` |
| Linux, qualsiasi distribuzione | `AIpermiler-<versione>.AppImage` |
| Debian, Ubuntu | `aipermiler_<versione>_amd64.deb` |

Le altre versioni, comprese le pre-release di test, sono nella pagina [Releases](https://github.com/suppressio/aipermiler/releases).

I pacchetti non sono firmati: Windows SmartScreen e macOS Gatekeeper mostrano un avviso la prima volta. L'app controlla le nuove versioni all'avvio e ogni 24 ore e apre nel browser il download giusto.

**Arrivi da IA Hypermiler (v0.4.x)?** Dalla v0.5.0 l'app si chiama AIpermiler. Le versioni fino alla 0.4.18 annunciano l'aggiornamento ma non riescono ad aprirne il download: scaricalo a mano dall'[ultima release](https://github.com/suppressio/aipermiler/releases/latest). Installalo come al solito: su Windows e su Debian/Ubuntu sostituisce IA Hypermiler, mentre su macOS e con l'AppImage puoi cancellare la vecchia app dopo. Al primo avvio impostazioni, account e storico vengono spostati, e gli account Copilot collegati restano collegati.

## Primo avvio

1. Apri **Impostazioni** (⚙ nel widget, o dall'icona nel tray) → **Aggiungi account**.
2. **Claude**: nessun login. Aggiungi l'account solo per gli insight locali (vedi sotto); le sue quote non sono disponibili in questa versione.
   **Codex (ChatGPT)**: serve Codex con accesso su questo computer, la [Codex CLI](https://developers.openai.com/codex/cli) (`codex login`) o l'estensione Codex di VS Code. Aggiungi un account con questo provider: legge subito l'utilizzo, tramite il tuo accesso a Codex, senza finestre di login e senza sessioni ChatGPT salvate dall'app. Se Codex è installato in un posto insolito, indica il percorso nell'account (*Programma Codex*). Senza accesso, l'account lo dice e ti indica dove accedere. Un solo account Codex può essere attivo, perché c'è un solo accesso a Codex per computer (#188 segue un accesso per account).
   **GitHub Copilot**: incolla un Personal Access Token (fine-grained con *Plan* in sola lettura, o classic senza scope), oppure accedi con una GitHub OAuth App. Per una licenza aziendale su un tenant `<nome>.ghe.com`, imposta prima il dominio GitHub. Il token si conserva nel portachiavi di sistema (Gestione credenziali di Windows, Portachiavi di macOS, GNOME Keyring o KWallet su Linux), non nei file dell'app; un token salvato da una versione precedente viene spostato lì al primo avvio.
3. Facoltativo, per ogni account: il **calendario di lavoro** (ultima sezione dell'account) e, solo se il provider non lo comunica, il **giorno di rinnovo**.

Il badge nel tray mostra un account e una finestra di limite come numero colorato (verde, arancione, rosso come nel widget, grigio se non aggiornato; su macOS, testo accanto all'icona). Scegli **Disattivato**, **Auto** o un **account · finestra** dal menu del tray o da Impostazioni → Aspetto. Auto segue la finestra critica dell'ultimo account il cui utilizzo è aumentato, resta fermo durante l'inattività e parte dall'utilizzo più alto. Il pannello resta sulla vista che stai leggendo. Un clic su un account o una finestra del pannello cambia solo il pannello, in ogni modalità; in Manuale, scegliere una vista dal tray o dalle Impostazioni porta il pannello su quella vista. Se l'account manuale è disabilitato o la finestra scompare, la voce selezionata è disabilitata e compare temporaneamente il valore Auto; la preferenza ritorna quando disponibile. Con **Avvisi critici** attivo (predefinito), ogni altra vista alla soglia di notifica o sopra compare a turno, dalla più alta, tra un badge normale e l'altro ogni 2 secondi in rosso (`!95`, oppure `100`; macOS `!95%`), indipendentemente dalle animazioni della finestra. Disattivare il badge disattiva anche gli avvisi. Il tooltip elenca tutti gli account. Chiudendo il widget l'app resta nel tray; *Esci* è nel suo menu.

---

## Come leggere il widget

Tutti i valori qui sotto riguardano la finestra di quota mostrata, di solito quella che richiede più attenzione. Con più account c'è una scheda per account; con più finestre di quota (per esempio il limite di 5 ore e quello settimanale di Claude) una lista sopra il valore principale le mostra tutte con un breve verdetto. Clicca una riga per vedere quella finestra.

Codex mostra il **Limite 5 ore** e il **Limite settimanale**, entrambi rolling. Come per le altre finestre rolling, il limite di 5 ore si legge in ore: tempo al reset, previsionale al reset e autonomia al ritmo attuale, con gli indicatori giornalieri nascosti. Il limite settimanale si legge per giorno lavorativo.

### Vista compatta

Il triangolo nella barra del titolo (tra riduci a icona e chiudi) riduce il widget alla sola percentuale e alla barra del consumo istantaneo della finestra con cui si apre; la finestra ha poi dimensione fissa. Un nuovo clic riporta alla vista completa, con la dimensione e la posizione precedenti. La scelta è ricordata tra i riavvii.

### Il verdetto di ogni finestra

Si basa sulla quota che resta **adesso**, ripartita sui giorni lavorativi fino al reset, confrontata con una ripartizione uniforme di tutto il periodo:

| Verdetto | Significato |
|---|---|
| **in linea** | entro ±5% della ripartizione uniforme |
| **margine** | hai usato meno del previsto: hai più quota al giorno a disposizione |
| **quota ridotta** | hai usato più del previsto: resta meno quota al giorno |
| **a rischio** | resta meno di metà della ripartizione uniforme al giorno, oppure al ritmo attuale finiresti prima del reset |

### Valore principale

La percentuale di quota usata e, sotto, il valore assoluto quando il provider lo fornisce (per esempio i crediti AI di Copilot, il credito extra di Claude in USD) e la data di rinnovo.

### Consumo istantaneo

A che velocità stai usando la quota in questo momento, in % della quota all'ora.

- **Come si calcola**: quanto è salito il consumo nell'ultima ora circa di letture. Mentre il consumo sale l'app interroga il provider ogni 5 minuti, altrimenti all'intervallo scelto nelle Impostazioni (30 minuti di default).
- **Target** (la tacca bianca): il ritmo che ti porterebbe esattamente al 100% al reset, ripartito sulle ore lavorative rimaste.
- **Come leggerlo**: la tacca è sempre a metà. Le tacche leggere segnano metà del target (a sinistra) e il doppio (a destra), e l'estremità destra vale quattro volte il target o più. La barra diventa rossa sopra il target. Un picco breve va bene; una barra rossa per ore è il segnale da tenere d'occhio.
- **Frecce sullo sfondo**: `>>>` indica che puoi consumare di più, `<<<` che conviene rallentare. Confrontano la media dell’ultima ora con il target e spariscono entro ±15% o in attesa di campioni. Più lo scarto cresce, più scorrono veloci, fino a un limite. Le frecce a destra compaiono solo nei giorni lavorativi full/half dell’account (lo schedule non ha orari di inizio/fine); con schedule disabilitato non ci sono vincoli di giorno. Le frecce a sinistra compaiono anche nei giorni liberi.

### Consumo giornaliero vs budget

Uno spazio per giorno: gli ultimi 7 o 30 giorni (Impostazioni → Intervallo grafico), poi i prossimi 2 o 5. Oggi è lo spazio con lo sfondo chiaro.

- **Barra larga**: quanto hai usato quel giorno. Rossa se ha superato il budget di quel giorno.
- **Barretta sottile accanto**: la ripartizione uniforme di una giornata lavorativa intera. La parte colorata è la quota lavorativa di quel giorno (tutta, o metà in una mezza giornata); la parte grigia è la quota che quel giorno non ha (tutta in un giorno libero).
- **Linea tratteggiata**, la parte che si muove:
  - su un giorno passato, il budget che avevi **quella mattina**;
  - oggi, il **budget di oggi**;
  - sui giorni a venire, quanto resta **ridistribuito** sui giorni lavorativi fino al reset.

  Una giornata pesante abbassa la linea dei giorni successivi; una leggera la alza.
- **Quota ideale** (in alto a destra): la ripartizione uniforme di una giornata lavorativa intera su tutto il periodo.

### Oggi: usato / budget

Quanto hai usato oggi rispetto al **budget di oggi**: quello che restava stamattina, diviso per i giorni lavorativi fino al reset (oggi compreso), per la quota di oggi (metà in una mezza giornata). Resta fisso per tutta la giornata.

Sotto: la quota che resta per giorno lavorativo da ora in poi, accanto a quella ideale, e quanto ti resta oggi o di quanto sei oltre.

### Indice di efficienza e rating

- **Indice di efficienza**: il ritmo ideale diviso per il tuo ritmo reale dall'inizio del periodo, sui giorni lavorativi. Sopra 1 stai usando meno del ritmo ideale, sotto 1 di più.
- **Stelle** (sull'intervallo del grafico): quanto ogni giorno è rimasto vicino alla sua quota ideale. 5 stelle: in media hai usato due terzi della quota o meno; 3 stelle: circa la quota; 1 stella: molto oltre.

### Previsionale

Dove arriveresti al reset continuando così: metà ritmo medio del periodo, metà ritmo degli ultimi tre giorni lavorativi completi. Può superare il 100%: serve proprio a vederlo.

### Giorni a scadenza

I giorni di calendario fino al reset e, tra parentesi, i giorni lavorativi secondo l'orario dell'account.

### Autonomia stimata

Per quanti giorni lavorativi basta la quota al ritmo attuale. **Compare solo se finiresti prima del reset**: altrimenti ripeterebbe soltanto il previsionale.

### Picco / media giornaliera

Il tuo giorno più pesante e la media tra i giorni completi dell'intervallo del grafico. Compare da due giorni completi in poi.

### Streak sotto budget

Giorni completi consecutivi, fino a ieri, rimasti entro il loro budget: gli stessi giorni che nel grafico non sono rossi. I giorni liberi senza consumo vengono saltati. Nascosto finché è 0.

### Consigli

Compaiono solo quando aggiungono qualcosa che i numeri sopra non dicono: finiresti prima del reset (e di quanto rallentare), un buon rating che lascia spazio a una sessione più lunga, una quota quasi esaurita vicino al reset, oppure (con gli insight locali) un legame tra i tuoi giorni più pesanti e i contesti molto lunghi.

### Colori

- **Valore principale e previsionale** diventano **arancioni** 5 punti sotto la tua soglia di allarme e **rossi** dalla soglia in su (Impostazioni → Notifiche, 80% di default).
- **Oggi: usato / budget** diventa **arancione** quando hai usato la soglia di allarme del budget di oggi (l'80% di default) e **rosso** solo quando lo superi.
- **Le barre del grafico** sono rosse quando quel giorno ha superato il suo budget (la linea tratteggiata).

### Insight locali (Claude Code, facoltativi)

Se li attivi su un account Claude, l'app legge le sessioni di Claude Code su questo computer (solo conteggi di token e nomi degli strumenti, mai il contenuto dei messaggi). Mostra quanta parte del tuo uso viene da contesti molto lunghi (oltre 150k) o da sessioni molto lunghe, e gli strumenti più usati. I token per ogni 1% di quota richiedono le quote di Claude, quindi in questa versione non sono mostrati.

---

## Impostazioni

| Sezione | Cosa puoi impostare |
|---|---|
| **Account** | aggiungere, connettere, disconnettere (cancella la sessione salvata), attivare/disattivare; per account: nome, calendario di lavoro, giorno di rinnovo se il provider non lo comunica, insight locali di Claude, dominio e autenticazione Copilot |
| **Aspetto** | lingua, stile della finestra (chiaro, scuro, trasparente), intervallo del grafico (7 o 30 giorni), intervallo di aggiornamento, colore d'accento, animazioni della finestra (attive di default) |
| **Notifiche** | soglia di allarme (%), usata anche per i colori |
| **Aggiornamenti** | versione installata, controllo manuale e automatico; **Guida** apre questa pagina |
| **Diagnostica** | segnalazione automatica di un cambio di formato del provider; file di report di diagnosi |

Le animazioni della finestra fanno risplendere e lampeggiare ogni 10 secondi i valori rossi quando l'account visualizzato ha una quota a rischio o esaurita (in vista compatta, il nome dell'account sotto il valore), cifre scorrevoli quando cambia la percentuale principale e un segmento nel colore d'accento attorno alla finestra durante ogni aggiornamento, avviato dal widget, dal timer, dal tray o dalle Impostazioni. Le sezioni del consiglio e degli insight locali si aprono e chiudono con una breve sfumatura e uno scorrimento; lo stesso effetto accompagna il passaggio fra vista compatta e completa, con la freccia rivolta in alto nella compatta e in basso nella completa. Disattivandole i cambi di vista sono immediati, i valori rossi restano illuminati senza lampeggio e le frecce di consumo restano ferme, e ruota solo l'icona di aggiornamento. Con la preferenza di sistema per ridurre il movimento non si muove nulla: l'aggiornamento usa un'icona attenuata con tooltip «Aggiornamento…». Gli effetti si fermano a finestra nascosta o ridotta a icona; il tray alterna i valori solo in presenza di un avviso critico.

## Notifiche

- **Soglia**: quando una quota supera la tua soglia di allarme, una volta al giorno per account.
- **Ritmo**: quando il consumo di oggi supera 1,5 volte il budget di oggi, una volta al giorno per account. Ti avvisa il giorno in cui le cose si mettono male, quando c'è ancora tempo per correggere.

## Privacy e sicurezza

- Il token di Copilot è salvato solo sul tuo computer, nel portachiavi di sistema, e non viene mai mostrato all'interfaccia né scritto nei log. Le sessioni di claude.ai salvate dalle versioni precedenti vengono cancellate al primo avvio, e così i cookie chatgpt.com del vecchio login Codex: Codex ora si legge tramite il tuo accesso a Codex, di cui l'app non salva nulla.
- L'app parla solo con l'API GitHub del tuo dominio, con Codex su questo computer (che parla da sé con ChatGPT) e con GitHub Releases per il controllo aggiornamenti. Nient'altro viene inviato altrove.
- **Segnalare un problema**: Impostazioni → Diagnostica → *Crea report di diagnosi* salva un file di testo in Download e apre una bozza di issue su GitHub. Il file **contiene** i tuoi valori di consumo (percentuali, importi, date di reset) ma nessun nome, id o credenziale. Le issue di questo repository sono **pubbliche**: leggi il file prima di allegarlo, oppure descrivi il problema senza.
- Codex si legge tramite `codex app-server`, un comando che Codex segna come sperimentale, quindi può cambiare. Se un provider cambia il formato delle risposte, si apre una bozza di issue precompilata con la sola struttura (nomi e tipi dei campi, mai i valori). Puoi disattivarla nelle Impostazioni.

---

## Sviluppo

Compilazione dai sorgenti, test, packaging e struttura del progetto sono in [docs/DEVELOPMENT.it.md](docs/DEVELOPMENT.it.md).

## Licenza

[MIT](LICENSE) © 2026 Daniele 'suppressio'.
