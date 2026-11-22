🌐 **English** | [Italiano](UPGRADE-TO-0.7.it.md)

# Upgrading to v0.7.0: moving your history to the core

> User guide, written ahead of v0.7.0 (not released yet). From v0.7.0 the Go core keeps settings and history in its own store, and the app does not copy them over by itself: a script does it once (#310, #311).

## When you need it

Up to v0.6.x the app keeps accounts, settings and history in its own encrypted file. From v0.7.0 the core keeps them in `store.json`, in its own folder. The old file stays where it is, untouched, so nothing is lost: run the script once and the core gets everything back.

| System | App's file (read only) | Core's store (written) |
|---|---|---|
| Windows | `%APPDATA%\AIpermiler\aipermiler-config.json` | `%APPDATA%\aipermiler\store.json` (same folder) |
| Linux | `~/.config/AIpermiler/aipermiler-config.json` | `~/.config/aipermiler/store.json` |
| macOS | `~/Library/Application Support/AIpermiler/aipermiler-config.json` | `~/Library/Application Support/aipermiler/store.json` (same folder) |

On Linux, `~/.config` is `$XDG_CONFIG_HOME` when that is set.

What is copied: accounts and their settings (renewal day, work schedule, Copilot host and scope, binary paths), the refresh interval, the notification threshold, the history retention, the daily history, the recent samples of the instant gauge and the last good read of each account. Not copied: the Copilot token, which already lives in the system keyring, and the window and display settings, which stay with the app.

## What the script does

1. Stops if the app's file is missing, or if the core service is running.
2. Reads and decrypts the app's file, without changing it.
3. If the core already has a `store.json`, asks before replacing it and keeps it as `store.json.bak-<date>` next to it.
4. Writes the new `store.json`, readable by your user only, and lists anything it left out (by account id, never with values).

## Run it

Quit AIpermiler first (tray menu, *Quit*).

Download [migrate-core-store.js](https://raw.githubusercontent.com/suppressio/aipermiler/main/scripts/migrate-core-store/migrate-core-store.js). You do not need Node.js: the app itself runs the script. In a terminal, in the download folder:

**Linux (.deb)**

```sh
ELECTRON_RUN_AS_NODE=1 /opt/AIpermiler/aipermiler migrate-core-store.js
```

**Linux (AppImage)**: the same, with the path of your AppImage in place of `/opt/AIpermiler/aipermiler`.

**Windows** (PowerShell):

```powershell
$env:ELECTRON_RUN_AS_NODE=1; & "$env:LOCALAPPDATA\Programs\AIpermiler\AIpermiler.exe" .\migrate-core-store.js
```

**macOS**:

```sh
ELECTRON_RUN_AS_NODE=1 /Applications/AIpermiler.app/Contents/MacOS/AIpermiler migrate-core-store.js
```

With Node.js 22 or later installed, `node migrate-core-store.js` works on every system.

Add `--yes` to replace an existing `store.json` without the question. Then start AIpermiler again: your accounts and history are back.

The script is one file and can be read before running it: [`scripts/migrate-core-store/`](../scripts/migrate-core-store/).
