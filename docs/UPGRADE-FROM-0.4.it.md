🌐 [English](UPGRADE-FROM-0.4.md) | **Italiano**

# Aggiornare da IA Hypermiler (v0.4.x)

> Guida utente, attuale. Per chi ha installato AIpermiler v0.6.0 o successiva direttamente sopra IA Hypermiler v0.4.x, senza avviare una volta la v0.5.0 (#260). L'app non sposta più da sola queste impostazioni (#204).

## Quando serve

La v0.5.0 ha rinominato l'app e al primo avvio ha spostato le impostazioni nella nuova cartella. Dalla v0.6.0 l'app non lo fa più. Se sei passato dalla v0.4.x direttamente alla v0.6.0 o successiva, AIpermiler parte senza account, impostazioni e storico, ma quelli vecchi sono ancora sul disco:

| Sistema | Impostazioni vecchie | Impostazioni nuove |
|---|---|---|
| Windows | `%APPDATA%\ia-hypermiler\ia-hypermiler-config.json` | `%APPDATA%\AIpermiler\aipermiler-config.json` |
| Linux | `~/.config/ia-hypermiler/ia-hypermiler-config.json` | `~/.config/AIpermiler/aipermiler-config.json` |
| macOS | `~/Library/Application Support/ia-hypermiler/ia-hypermiler-config.json` | `~/Library/Application Support/AIpermiler/aipermiler-config.json` |

Su Linux, `~/.config` è `$XDG_CONFIG_HOME` quando è impostata.

Lo script copia il vecchio file nel posto nuovo. Il file contiene account, impostazioni, storico e il token Copilot della v0.4.x, quindi gli account Copilot collegati restano collegati.

## Cosa fa lo script

1. Si ferma se le vecchie impostazioni mancano, o se AIpermiler o IA Hypermiler è in esecuzione.
2. Se AIpermiler ha già delle impostazioni (per esempio account che hai riaggiunto a mano), chiede prima di continuare e le conserva come `aipermiler-config.json.bak-<data>` accanto al file nuovo.
3. Copia il vecchio file, controlla che la copia sia identica, poi cancella la vecchia cartella `ia-hypermiler`.

## Come lanciarlo

Prima chiudi AIpermiler (menu del tray, *Esci*).

**Windows**: scarica [migrate-legacy-settings.ps1](https://raw.githubusercontent.com/suppressio/aipermiler/main/scripts/migrate-legacy/migrate-legacy-settings.ps1), poi in PowerShell, nella cartella del download:

```powershell
powershell -ExecutionPolicy Bypass -File .\migrate-legacy-settings.ps1
```

**Linux e macOS**: scarica [migrate-legacy-settings.sh](https://raw.githubusercontent.com/suppressio/aipermiler/main/scripts/migrate-legacy/migrate-legacy-settings.sh), poi in un terminale, nella cartella del download:

```sh
sh migrate-legacy-settings.sh
```

Riavvia AIpermiler: gli account sono tornati.

Gli script sono brevi e si possono leggere prima di lanciarli: [`scripts/migrate-legacy/`](../scripts/migrate-legacy/).
