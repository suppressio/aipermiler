// entry.ts — Electron entry point (package.json "main"). Runs the userData migration
// before anything else (#200) and loads the app (main.ts) only when it succeeded: a
// failed copy must stop the app before the store exists, or electron-store would
// create an empty one and the old data would never move. The error is shown after
// `ready`, since before it Linux only prints dialog.showErrorBox to stderr.

import { app, dialog } from 'electron';
import { userDataMigration } from './main/userDataBootstrap';
import { resolveLocale, setLocale, t } from './main/i18n/index';

// Shown after `ready` (the message is built then, in the system language), then the
// app quits: never a silent exit.
function failStartup(message: () => string): void {
  void app.whenReady().then(() => {
    setLocale(resolveLocale('auto', app.getLocale()));
    dialog.showErrorBox(t('startup.failedTitle'), message());
    app.exit(1);
  });
}

const fatal = userDataMigration.fatal;
if (fatal === null) {
  import('./main').catch((err: unknown) => {
    console.error('[main] startup failed:', err);
    failStartup(() => (err instanceof Error ? err.message : String(err)));
  });
} else {
  console.error('[main] userData migration failed:', fatal);
  failStartup(() => t('startup.migrationFailed', { detail: fatal }));
}
