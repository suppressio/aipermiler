🌐 [English](UPGRADE-TO-0.7.md) | **Italiano**

# Aggiornare alla v0.7.0: spostare lo storico nel core

> Guida utente, scritta in anticipo sulla v0.7.0 (non ancora rilasciata). Dalla v0.7.0 il core in Go tiene impostazioni e storico nel proprio store e l'app non li copia da sola: lo fa uno script, una volta (#310, #311).

## Quando serve

Fino alla v0.6.x l'app tiene account, impostazioni e storico nel proprio file cifrato. Dalla v0.7.0 li tiene il core, in `store.json`, nella sua cartella. Il vecchio file resta dov'è, intatto, quindi non si perde niente: lanci lo script una volta e il core ritrova tutto. Fino ad allora le Impostazioni mostrano un avviso con il link a questa guida; chiudilo con *Chiudi* quando hai finito.

| Sistema | File dell'app (solo letto) | Store del core (scritto) |
|---|---|---|
| Windows | `%APPDATA%\AIpermiler\aipermiler-config.json` | `%APPDATA%\aipermiler\store.json` (stessa cartella) |
| Linux | `~/.config/AIpermiler/aipermiler-config.json` | `~/.config/aipermiler/store.json` |
| macOS | `~/Library/Application Support/AIpermiler/aipermiler-config.json` | `~/Library/Application Support/aipermiler/store.json` (stessa cartella) |

Su Linux `~/.config` è `$XDG_CONFIG_HOME` quando è impostata.

Cosa viene copiato: gli account e le loro impostazioni (giorno di rinnovo, orario di lavoro, host e ambito di Copilot, percorsi dei binari), l'intervallo di aggiornamento, la soglia di notifica, la conservazione dello storico, lo storico giornaliero, i campioni recenti dell'indicatore istantaneo e l'ultima lettura riuscita di ogni account. Non viene copiato: il token di Copilot, che sta già nel portachiavi di sistema, e le impostazioni di finestra e aspetto, che restano all'app.

## Cosa fa lo script

1. Si ferma se il file dell'app manca o se il servizio del core è in esecuzione (si ferma con `aipermiler service stop`); ricontrolla subito prima di scrivere.
2. Legge e decifra il file dell'app, senza modificarlo.
3. Se il core ha già uno `store.json`, chiede prima di sostituirlo e lo conserva accanto come `store.json.bak-<data>`.
4. Scrive il nuovo `store.json`, leggibile solo dal tuo utente, ed elenca quello che ha lasciato fuori o cambiato (per id di account e campo, mai con i valori).

I valori che il core non accetta vengono corretti durante la copia, così in seguito le impostazioni del core si possono salvare senza errori: l'intervallo di aggiornamento resta tra 5 e 1440 minuti, la conservazione dello storico tra 1 e 3650 giorni e la soglia di notifica tra 1 e 100%. Un giorno di rinnovo fuori da 1-31 diventa nessuno, le ore al giorno fuori da 0-24 diventano 8, una quota manuale di Copilot negativa o un ambito di Copilot sconosciuto vengono tolti, e così un percorso del binario non assoluto (il core allora trova il binario da solo), un metodo di autenticazione di Copilot diverso da PAT o OAuth e le informazioni locali su un secondo account Claude (il core le conta per un solo account). Un account con un id che il core non può usare, o un account Copilot con un host che non è né `github.com` né un tenant `.ghe.com`, viene lasciato fuori: il suo token non deve mai andare a un altro host. Anche ogni valore cambiato o tolto viene elencato, per id di account e campo (per esempio `account "c": binaryPath is not absolute, dropped` o `settings.refreshIntervalMinutes out of range, clamped to 5`), mai con il valore stesso. Dopo, controlla nelle Impostazioni gli account e i campi elencati.

## Come lanciarlo

Prima chiudi AIpermiler (menu della tray, *Esci*).

Scarica [migrate-core-store.js](https://raw.githubusercontent.com/suppressio/aipermiler/main/scripts/migrate-core-store/migrate-core-store.js). Non serve Node.js: lo script lo esegue l'app stessa. In un terminale, nella cartella del download:

**Linux (.deb)**

```sh
ELECTRON_RUN_AS_NODE=1 /opt/AIpermiler/aipermiler migrate-core-store.js
```

**Linux (AppImage)**: uguale, con il percorso della tua AppImage al posto di `/opt/AIpermiler/aipermiler`.

**Windows** (PowerShell): imposta `$exe` su `AIpermiler.exe` nella cartella in cui è installata l'app (tasto destro sulla voce del menu Start, *Apri percorso file*; di solito `%LOCALAPPDATA%\Programs\aipermiler`, oppure la vecchia cartella di IA Hypermiler se hai aggiornato da lì). Qui l'app non risponde alle domande, quindi passa `--yes`:

```powershell
$exe = "$env:LOCALAPPDATA\Programs\aipermiler\AIpermiler.exe"
$env:ELECTRON_RUN_AS_NODE = 1
Start-Process -Wait -NoNewWindow $exe -ArgumentList '.\migrate-core-store.js', '--yes'
Remove-Item Env:ELECTRON_RUN_AS_NODE
```

**macOS**:

```sh
ELECTRON_RUN_AS_NODE=1 /Applications/AIpermiler.app/Contents/MacOS/AIpermiler migrate-core-store.js
```

Con Node.js 22 o successivo installato, `node migrate-core-store.js` funziona su ogni sistema.

Aggiungi `--yes` per sostituire uno `store.json` esistente senza la domanda (quello vecchio viene comunque conservato come backup). Se lo script dice che il servizio è in esecuzione ma nessun servizio gira, cancella il file `api.json` rimasto nella cartella del core. Poi riavvia AIpermiler: account e storico sono tornati.

Lo script è un solo file e si può leggere prima di lanciarlo: [`scripts/migrate-core-store/`](../scripts/migrate-core-store/).
