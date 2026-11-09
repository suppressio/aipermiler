🌐 **English** | [Italiano](UPGRADE-FROM-0.4.it.md)

# Upgrading from IA Hypermiler (v0.4.x)

> User guide, current. For whoever installed AIpermiler v0.6.0 or later straight over IA Hypermiler v0.4.x, without starting v0.5.0 once (#260). The app no longer moves those settings by itself (#204).

## When you need it

v0.5.0 renamed the app and moved its settings to the new folder on the first start. From v0.6.0 the app no longer does it. If you went from v0.4.x straight to v0.6.0 or later, AIpermiler starts without your accounts, settings and history, but the old ones are still on disk:

| System | Old settings | New settings |
|---|---|---|
| Windows | `%APPDATA%\ia-hypermiler\ia-hypermiler-config.json` | `%APPDATA%\AIpermiler\aipermiler-config.json` |
| Linux | `~/.config/ia-hypermiler/ia-hypermiler-config.json` | `~/.config/AIpermiler/aipermiler-config.json` |
| macOS | `~/Library/Application Support/ia-hypermiler/ia-hypermiler-config.json` | `~/Library/Application Support/AIpermiler/aipermiler-config.json` |

On Linux, `~/.config` is `$XDG_CONFIG_HOME` when that is set.

The script copies the old file to the new place. The file holds accounts, settings, history and the Copilot token of v0.4.x, so connected Copilot accounts stay connected.

## What the script does

1. Stops if the old settings are missing, or if AIpermiler or IA Hypermiler is running.
2. If AIpermiler already has settings (for example accounts you added again by hand), asks before going on and keeps them as `aipermiler-config.json.bak-<date>` next to the new file.
3. Copies the old file, checks that the copy is identical, then deletes the old `ia-hypermiler` folder.

## Run it

Quit AIpermiler first (tray menu, *Quit*).

**Windows**: download [migrate-legacy-settings.ps1](https://raw.githubusercontent.com/suppressio/aipermiler/main/scripts/migrate-legacy/migrate-legacy-settings.ps1), then in PowerShell, in the download folder:

```powershell
powershell -ExecutionPolicy Bypass -File .\migrate-legacy-settings.ps1
```

**Linux and macOS**: download [migrate-legacy-settings.sh](https://raw.githubusercontent.com/suppressio/aipermiler/main/scripts/migrate-legacy/migrate-legacy-settings.sh), then in a terminal, in the download folder:

```sh
sh migrate-legacy-settings.sh
```

Start AIpermiler again: your accounts are back.

The scripts are short and can be read before running them: [`scripts/migrate-legacy/`](../scripts/migrate-legacy/).
