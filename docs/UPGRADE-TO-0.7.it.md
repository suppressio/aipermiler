🌐 [English](UPGRADE-TO-0.7.md) | **Italiano**

# Aggiornare alla v0.7.0: spostare lo storico nel core

> Guida utente, scritta in anticipo sulla v0.7.0 (non ancora rilasciata). Dalla v0.7.0 il core in Go tiene impostazioni e storico nel proprio store e l'app non li copia da sola: lo fa uno script, una volta (#310, #311).

## Quando serve

Fino alla v0.6.x l'app tiene account, impostazioni e storico nel proprio file cifrato. Dalla v0.7.0 li tiene il core, in `store.json`, nella sua cartella. Il vecchio file resta dov'è, intatto, quindi non si perde niente: lanci lo script una volta e il core ritrova tutto.

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
4. Scrive il nuovo `store.json`, leggibile solo dal tuo utente, ed elenca quello che ha lasciato fuori (per id di account, mai con i valori).

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

Aggiungi `--yes` per sostituire uno `store.json` esistente senza la domanda. Poi riavvia AIpermiler: account e storico sono tornati.

Lo script è un solo file e si può leggere prima di lanciarlo: [`scripts/migrate-core-store/`](../scripts/migrate-core-store/).
