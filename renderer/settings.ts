// settings.ts — Settings window logic (no direct access to Node.js).
// Every visible string goes through t() or data-i18n attributes (renderer/i18n).

import type { AccountConfig, AccountId, AppSettings, ProviderId, UpdateSettings, TrayViewState } from '../types/index';
import type { AipermilerBridge } from '../types/ipc';
import { byId } from './dom.js';
import { applyTranslations, formatDate, formatDateTime, formatTime, resolveLocale, setLocale, t, type MessageKey } from './i18n/index.js';
import { summarizeWorkSchedule } from './schedule.js';
import { windowLabel } from './windowLabel.js';
import { renewalFromProvider } from './renewal.js';

declare global {
  interface Window {
    aipermiler: AipermilerBridge;
  }
}

const DAY_KEYS = ['mon', 'tue', 'wed', 'thu', 'fri', 'sat', 'sun'] as const;
const DAY_STATUSES = ['full', 'half', 'off'] as const;

let settings: AppSettings | null = null;
let trayView: TrayViewState | null = null;
// Last state actually persisted (confirmed with "Save" or just received from
// getSettings()): used by "Cancel" to restore the form, and to tell which fields
// really changed since the last save (e.g. not to recreate the window on every
// Save when the style was not touched).
let savedSettings: AppSettings | null = null;
let saveTimer: ReturnType<typeof setTimeout> | null = null;
// Account whose detail panel is open in the table (one at a time).
let expandedAccountId: AccountId | null = null;
// Account just created with "Add account" and not saved yet: same detail form,
// only the title changes ("New … account" instead of "Configure …").
let newAccountId: AccountId | null = null;

// Provider names are proper names: not translated.
const PROVIDER_LABELS: Record<ProviderId, string> = { claude: 'Claude', copilot: 'GitHub Copilot', codex: 'Codex (ChatGPT)' };

type PlainRecord = Record<string, unknown>;

function isPlainRecord(value: unknown): value is PlainRecord {
  return typeof value === 'object' && value !== null;
}

function getPath(obj: unknown, path: string): unknown {
  return path.split('.').reduce<unknown>((o, k) => (isPlainRecord(o) ? o[k] : undefined), obj);
}

function setPath(obj: PlainRecord, path: string, value: unknown): void {
  const keys = path.split('.');
  const last = keys.pop();
  if (last === undefined) return;
  let cursor: PlainRecord = obj;
  for (const key of keys) {
    const next = cursor[key];
    if (isPlainRecord(next)) {
      cursor = next;
    } else {
      const created: PlainRecord = {};
      cursor[key] = created;
      cursor = created;
    }
  }
  cursor[last] = value;
}

function topLevelKey(path: string): string {
  return path.split('.')[0] ?? path;
}

function readFieldValue(el: HTMLInputElement | HTMLSelectElement): unknown {
  if (el.id === 'tray-badge-select') return JSON.parse(el.value) as unknown;
  if (el instanceof HTMLInputElement) {
    if (el.type === 'checkbox') return el.checked;
    if (el.type === 'number') return el.value === '' ? null : Number(el.value);
  }
  if (el instanceof HTMLSelectElement && el.dataset.type === 'number') {
    return el.value === '' ? null : Number(el.value);
  }
  return el.value;
}

// --- Language ------------------------------------------------------------------

function applyLanguage(language: AppSettings['ui']['language']): void {
  const locale = resolveLocale(language, navigator.language);
  setLocale(locale, navigator.language);
  document.documentElement.lang = locale;
  applyTranslations(document);
}

// Week grid: labels and options carry data-i18n keys, so applyTranslations
// translates them like the static markup.
function buildWeekGrid(grid: HTMLElement): void {
  grid.innerHTML = '';
  for (const day of DAY_KEYS) {
    const labelEl = document.createElement('span');
    labelEl.dataset.i18n = `settings.schedule.${day}`;
    const select = document.createElement('select');
    select.dataset.accountField = `workSchedule.days.${day}`;
    for (const status of DAY_STATUSES) {
      const optionEl = document.createElement('option');
      optionEl.value = status;
      optionEl.dataset.i18n = `settings.schedule.${status}`;
      select.appendChild(optionEl);
    }
    grid.appendChild(labelEl);
    grid.appendChild(select);
  }
}

// The account's own work schedule (it used to be one global card): one template,
// cloned into the slot of every provider's detail.
function fillScheduleSlot(detail: HTMLElement): void {
  const slot = detail.querySelector<HTMLElement>('[data-role="schedule-slot"]');
  const root = byId('tpl-work-schedule', HTMLTemplateElement).content.firstElementChild;
  if (!slot || !(root instanceof HTMLElement)) return;
  const schedule = root.cloneNode(true) as HTMLElement;
  const grid = schedule.querySelector<HTMLElement>('[data-role="week-grid"]');
  if (grid) buildWeekGrid(grid);
  slot.replaceWith(schedule);
}

function fieldElements(): (HTMLInputElement | HTMLSelectElement)[] {
  return Array.from(document.querySelectorAll<HTMLInputElement | HTMLSelectElement>('[data-field]'));
}

// --- Accounts table ----------------------------------------------------------------

function accounts(): AccountConfig[] {
  return Array.isArray(settings?.accounts) ? settings.accounts : [];
}

// Claude (#180) and Codex (#178) have nothing to connect: both are read through the
// Claude Code / Codex sign-in of this computer.
function isAccountConnected(account: AccountConfig): boolean {
  return account.provider === 'copilot' && !!account.credentials.username;
}

function connectionLabel(account: AccountConfig): string {
  if (account.provider === 'claude') return t('settings.accounts.statusClaudeSignIn');
  if (account.provider === 'codex') return t('settings.accounts.statusCodexSignIn');
  if (!isAccountConnected(account)) return t('settings.accounts.statusNotConnected');
  return account.credentials.username
    ? t('settings.accounts.statusConnectedAs', { user: account.credentials.username })
    : t('settings.accounts.statusConnected');
}

function actionButton(labelKey: MessageKey, action: string, id: AccountId): HTMLButtonElement {
  const btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'btn-secondary btn-small';
  btn.textContent = t(labelKey);
  btn.dataset.action = action;
  btn.dataset.accountId = id;
  return btn;
}

// Accounts table (issue #4): one row per account + an expandable detail row,
// cloned from the provider template. Detail fields become
// data-field="accounts.<index>.<field>": getPath/setPath already work on array
// indices, so draft/Save/Cancel stay the generic ones of the rest of the form.
function renderAccountsTable(): void {
  const tbody = byId('accounts-tbody', HTMLTableSectionElement);
  tbody.innerHTML = '';
  byId('accounts-empty', HTMLElement).hidden = accounts().length > 0;

  accounts().forEach((account, index) => {
    const isExpanded = expandedAccountId === account.id;
    const row = document.createElement('tr');
    row.dataset.accountId = account.id;
    // Zebra computed here rather than with :nth-child: open detail rows would
    // shift the alternation.
    row.className = `account-row${index % 2 ? ' zebra' : ''}${isExpanded ? ' expanded' : ''}`;

    const nameCell = document.createElement('td');
    nameCell.textContent = account.label;
    const providerCell = document.createElement('td');
    providerCell.textContent = PROVIDER_LABELS[account.provider];
    const statusCell = document.createElement('td');
    const status = document.createElement('span');
    status.className = 'connection-status';
    status.dataset.role = 'row-status';
    status.textContent = connectionLabel(account);
    status.classList.toggle('connected', isAccountConnected(account));
    statusCell.appendChild(status);

    const enabledCell = document.createElement('td');
    const enabled = document.createElement('input');
    enabled.type = 'checkbox';
    enabled.dataset.field = `accounts.${index}.enabled`;
    enabled.setAttribute('aria-label', t('settings.accounts.enabledAria', { name: account.label }));
    enabledCell.appendChild(enabled);

    const actionsCell = document.createElement('td');
    actionsCell.className = 'account-actions';
    actionsCell.appendChild(actionButton(isExpanded ? 'settings.accounts.close' : 'settings.accounts.configure', 'toggle-detail', account.id));
    if (isAccountConnected(account)) {
      actionsCell.appendChild(actionButton('settings.accounts.disconnect', 'disconnect', account.id));
    }
    actionsCell.appendChild(actionButton('settings.accounts.remove', 'remove', account.id));

    row.append(nameCell, providerCell, statusCell, enabledCell, actionsCell);
    tbody.appendChild(row);

    if (isExpanded) {
      const detailRow = document.createElement('tr');
      detailRow.className = 'account-detail-row';
      detailRow.dataset.accountId = account.id;
      const cell = document.createElement('td');
      cell.colSpan = 5;
      const tpl = byId(`tpl-detail-${account.provider}`, HTMLTemplateElement);
      const templateRoot = tpl.content.firstElementChild;
      if (!(templateRoot instanceof HTMLElement)) throw new Error(`Empty detail template: ${account.provider}`);
      const detail = templateRoot.cloneNode(true) as HTMLElement;
      detail.dataset.accountId = account.id;
      fillScheduleSlot(detail);
      applyTranslations(detail);
      const title = document.createElement('h3');
      title.className = 'account-detail-title';
      title.textContent = account.id === newAccountId
        ? t('settings.accounts.detailTitleNew', { provider: PROVIDER_LABELS[account.provider] })
        : t('settings.accounts.detailTitle', { name: account.label });
      detail.prepend(title);
      detail.querySelectorAll<HTMLElement>('[data-account-field]').forEach((el) => {
        el.dataset.field = `accounts.${index}.${el.dataset.accountField}`;
      });
      detail.querySelectorAll<HTMLElement>('[data-action]').forEach((el) => { el.dataset.accountId = account.id; });
      cell.appendChild(detail);
      detailRow.appendChild(cell);
      tbody.appendChild(detailRow);
    }
  });
}

function renderTrayBadgeOptions(): void {
  const select = byId('tray-badge-select', HTMLSelectElement);
  select.innerHTML = '';
  const entries = [...(trayView?.entries ?? [])];
  const choice = settings?.ui.trayBadge.view;
  const value = JSON.stringify(choice);
  if (choice && !entries.some(e => JSON.stringify(e.view) === value)) {
    entries.push({ view: choice, enabled: false, checked: true });
  }
  for (const entry of entries) {
    const option = document.createElement('option');
    option.value = JSON.stringify(entry.view);
    option.disabled = !entry.enabled;
    option.textContent = entry.view.mode === 'off' ? t('settings.appearance.trayBadgeOff')
      : entry.view.mode === 'auto' ? t('settings.appearance.trayBadgeAuto')
      : entry.window && entry.provider ? `${entry.accountLabel ?? ''} · ${windowLabel(entry.window, entry.provider)}`
      : t('settings.appearance.trayBadgeUnavailable', { account: entry.accountLabel ?? entry.view.accountId,
        window: entry.view.windowId ?? t('settings.appearance.trayBadgePending') });
    select.appendChild(option);
  }
  select.value = value;
  byId('tray-badge-critical', HTMLInputElement).disabled = choice?.mode === 'off';
}

function populateForm(): void {
  renderAccountsTable();
  renderTrayBadgeOptions();
  fieldElements().forEach((el) => {
    const value = getPath(settings, el.dataset.field as string);
    if (el instanceof HTMLInputElement && el.type === 'checkbox') {
      if (typeof value === 'boolean') el.checked = value;
    } else if (typeof value === 'string' || typeof value === 'number') {
      el.value = String(value);
    }
  });
  accounts().forEach((_account, index) => { applyAccountDetailState(index); });
}

// When an account's work schedule is disabled (e.g. a personal account with no fixed
// days/hours — user feedback), its day selectors and hours/day no longer affect pacing
// (see budget.getDayUnit): they are disabled instead of editable without effect.
function applyScheduleLock(account: AccountConfig, detail: HTMLElement): void {
  const enabled = account.workSchedule.enabled;
  const summary = detail.querySelector<HTMLElement>('[data-role="schedule-summary"]');
  if (summary) summary.textContent = summarizeWorkSchedule(account.workSchedule);
  const hint = detail.querySelector<HTMLElement>('[data-role="schedule-disabled-hint"]');
  if (hint) hint.hidden = enabled;
  detail
    .querySelectorAll<HTMLInputElement | HTMLSelectElement>('[data-field*=".workSchedule.days."], [data-field$=".workSchedule.hoursPerDay"]')
    .forEach((el) => { el.disabled = !enabled; });
}

// The renewal day entered by hand is only a fallback (main.ts resolvePeriodBounds):
// when the provider reports the reset of every paced window (Claude 5h/7d windows,
// Copilot quotas and billing report) the field is locked and shows the date read; it
// stays editable when some window has no date anywhere (e.g. the Claude company spend
// limit) or before the first successful sync.
function applyRenewalLock(account: AccountConfig, detail: HTMLElement): void {
  const source = renewalFromProvider(settings?.history.lastGood?.[account.id]);
  const input = detail.querySelector<HTMLInputElement>('[data-account-field="subscription.renewalRule.day"]');
  if (input) input.disabled = !source.needsManual;
  // The explanation is an info tooltip next to the label, shown only when there is one.
  const hint = detail.querySelector<HTMLElement>('[data-role="renewal-hint"]');
  const hintText = detail.querySelector<HTMLElement>('[data-role="renewal-hint-text"]');
  if (!hint || !hintText) return;
  const hasData = settings?.history.lastGood?.[account.id] !== undefined;
  if (!source.needsManual && source.next) {
    hintText.textContent = t('settings.account.renewalFromProvider', { date: `${formatDate(source.next)} ${formatTime(source.next)}` });
    hint.hidden = false;
  } else if (source.needsManual && hasData) {
    hintText.textContent = t('settings.account.renewalManual');
    hint.hidden = false;
  } else {
    hint.hidden = true;
  }
}

function detailElement(id: AccountId): HTMLElement | null {
  return document.querySelector<HTMLElement>(`.account-detail[data-account-id="${CSS.escape(id)}"]`);
}

// The GitHub domain as typed in the detail (possibly not saved yet): connecting uses
// it, and the main process validates it (only github.com or <tenant>.ghe.com).
function githubHostInput(id: AccountId): string {
  return detailElement(id)?.querySelector<HTMLInputElement>('[data-role="github-host"]')?.value.trim() || 'github.com';
}

// Dynamic state of an account detail, re-applied on every change of its fields:
// - every provider: work schedule fields locked while the schedule is disabled;
// - Copilot: company-seat note shown for the company scope, and PAT/OAuth share the
//   same credentials slot, so only one panel is visible.
function applyAccountDetailState(index: number): void {
  const account = accounts()[index];
  if (!account) return;
  const detail = detailElement(account.id);
  if (!detail) return;
  applyScheduleLock(account, detail);
  applyRenewalLock(account, detail);
  if (account.provider !== 'copilot') return;
  const isOrg = account.accountScope === 'organization';
  (detail.querySelector('[data-role="org-warning"]') as HTMLElement).hidden = !isOrg;
  // The manual quota is read only by the personal-scope fallback (GitHub billing
  // report, which has no total): a company seat always gets it from GitHub.
  (detail.querySelector('[data-role="manual-quota"]') as HTMLElement).hidden = isOrg;
  (detail.querySelector('[data-role="pat-panel"]') as HTMLElement).hidden = account.authMethod === 'oauth';
  (detail.querySelector('[data-role="oauth-panel"]') as HTMLElement).hidden = account.authMethod !== 'oauth';
}

// Local Claude Code sessions do not say which account they belong to: the flag
// can be on one Claude account at a time (the same constraint is enforced by the
// main process, enforceSingleLocalInsights in store/migrate.ts).
function enforceSingleLocalInsightsDraft(keepIndex: number): void {
  accounts().forEach((account, index) => {
    if (index === keepIndex || account.provider !== 'claude' || !account.localInsights) return;
    account.localInsights = false;
    const el = document.querySelector<HTMLInputElement>(`[data-field="accounts.${index}.localInsights"]`);
    if (el) el.checked = false;
  });
}

// Providers read through the one sign-in of this computer (SIGN_IN_PROVIDERS in
// store/migrate.ts): Codex (#178) and Claude Code (#180).
const SIGN_IN_PROVIDERS: readonly ProviderId[] = ['codex', 'claude'];

// The draft merged with a fresh store copy can hold two enabled accounts of a sign-in
// provider (one enabled in the draft, one just added): same rule as
// enforceSingleSignInAccounts in store/migrate.ts (the Claude account with the local
// insights, else the first enabled one, is kept). The renderer project cannot import
// store/, hence the copy.
function keepFirstEnabledSignIn(list: AccountConfig[]): AccountConfig[] {
  const keepers = new Map<ProviderId, AccountId>();
  for (const a of list) {
    if (a.provider === 'claude' && a.enabled && a.localInsights) keepers.set('claude', a.id);
  }
  for (const a of list) {
    if (SIGN_IN_PROVIDERS.includes(a.provider) && a.enabled && !keepers.has(a.provider)) keepers.set(a.provider, a.id);
  }
  return list.map((a) => (SIGN_IN_PROVIDERS.includes(a.provider) && a.enabled && keepers.get(a.provider) !== a.id ? { ...a, enabled: false } : a));
}

// At most one enabled Codex and one enabled Claude account: the core reads the one
// sign-in of this computer (enforceSingleSignInAccounts in store/migrate.ts does the
// same on save).
function enforceSingleSignInDraft(keepIndex: number): void {
  const provider = accounts()[keepIndex]?.provider;
  if (provider === undefined || !SIGN_IN_PROVIDERS.includes(provider)) return;
  accounts().forEach((account, index) => {
    if (index === keepIndex || account.provider !== provider || !account.enabled) return;
    account.enabled = false;
    const el = document.querySelector<HTMLInputElement>(`[data-field="accounts.${index}.enabled"]`);
    if (el) el.checked = false;
  });
}

// After an action performed by the main process (connect/disconnect/add/remove)
// the real store state is needed for the touched account — but unsaved draft
// changes on OTHER accounts and sections must not be lost (the form used to be
// reloaded from scratch, discarding them).
async function reloadAfterAccountAction(actedOn: AccountId | null): Promise<void> {
  const fresh = await window.aipermiler.getSettings();
  const draft = settings;
  savedSettings = structuredClone(fresh);
  if (draft) {
    const draftAccounts = new Map(accounts().map((a) => [a.id, a]));
    fresh.accounts = keepFirstEnabledSignIn(fresh.accounts.map((a) => (a.id !== actedOn ? draftAccounts.get(a.id) ?? a : a)));
    for (const [key, value] of Object.entries(draft)) {
      if (key !== 'accounts') (fresh as unknown as PlainRecord)[key] = value;
    }
  }
  settings = fresh;
  populateForm();
}

function setDetailStatus(id: AccountId, text: string): void {
  const el = detailElement(id)?.querySelector<HTMLElement>('[data-role="detail-status"]');
  if (el) el.textContent = text;
  const rowStatus = document.querySelector<HTMLElement>(`tr[data-account-id="${CSS.escape(id)}"] [data-role="row-status"]`);
  if (rowStatus) rowStatus.textContent = text;
}

async function runAccountAction(action: string, id: AccountId, button: HTMLButtonElement): Promise<void> {
  const account = accounts().find((a) => a.id === id);
  if (!account) return;
  const name = account.label;

  if (action === 'toggle-detail') {
    expandedAccountId = expandedAccountId === id ? null : id;
    if (expandedAccountId === null) newAccountId = null;
    captureDraftFromForm();
    populateForm();
    return;
  }

  button.disabled = true;
  try {
    if (action === 'connect-copilot-pat') {
      const input = detailElement(id)?.querySelector<HTMLInputElement>('[data-role="token-input"]');
      const token = input?.value.trim() ?? '';
      if (!token) {
        setDetailStatus(id, t('settings.accounts.pasteToken'));
        return;
      }
      setDetailStatus(id, t('settings.accounts.checkingToken'));
      const result = await window.aipermiler.connectCopilot(id, token, githubHostInput(id));
      await reloadAfterAccountAction(id);
      showSaveStatus(t('settings.accounts.connectedAs', { name, user: result.username }));
    } else if (action === 'connect-copilot-oauth') {
      const detail = detailElement(id);
      const clientId = detail?.querySelector<HTMLInputElement>('[data-role="oauth-client-id"]')?.value.trim() ?? '';
      const clientSecret = detail?.querySelector<HTMLInputElement>('[data-role="oauth-secret-input"]')?.value.trim() ?? '';
      if (!clientId || !clientSecret) {
        setDetailStatus(id, t('settings.accounts.oauthMissing'));
        return;
      }
      setDetailStatus(id, t('settings.accounts.oauthAuthorize'));
      const result = await window.aipermiler.connectCopilotOAuth(id, clientId, clientSecret, githubHostInput(id));
      await reloadAfterAccountAction(id);
      showSaveStatus(t('settings.accounts.connectedOauthAs', { name, user: result.username }));
    } else if (action === 'disconnect') {
      await window.aipermiler.disconnectAccount(id);
      await reloadAfterAccountAction(id);
      showSaveStatus(t('settings.accounts.disconnected', { name }));
    } else if (action === 'remove') {
      if (!window.confirm(t('settings.accounts.removeConfirm', { name }))) return;
      await window.aipermiler.removeAccount(id);
      if (expandedAccountId === id) expandedAccountId = null;
      await reloadAfterAccountAction(id);
      showSaveStatus(t('settings.accounts.removed', { name }));
    }
  } catch (err) {
    setDetailStatus(id, t('settings.operationFailed', { error: errorMessage(err) }));
  } finally {
    if (button.isConnected) button.disabled = false;
  }
}

// Reads every field currently in the DOM back into the draft: needed before
// re-rendering the table (open/close detail), otherwise a value typed but not yet
// "changed" (input still focused) would be lost.
function captureDraftFromForm(): Set<string> {
  const touchedKeys = new Set<string>();
  fieldElements().forEach((el) => {
    const field = el.dataset.field as string;
    setPath(settings as unknown as PlainRecord, field, readFieldValue(el));
    touchedKeys.add(topLevelKey(field));
  });
  return touchedKeys;
}

// --- Updates card --------------------------------------------------------------------

// "Updates" card (issue #5): state read from settings.updates, written only by
// the main process (services/updates.ts). Not part of the form draft: it updates
// live even when the result of an automatic check arrives.
function renderUpdatesCard(updates: UpdateSettings | undefined): void {
  const status = byId('updates-status', HTMLElement);
  const download = byId('btn-download-update', HTMLButtonElement);
  const hint = byId('updates-download-hint', HTMLElement);
  const available = updates?.available ?? null;

  status.classList.remove('connected', 'update-available', 'update-error');
  if (available) {
    status.textContent = `${t('settings.updates.available', { version: available.version })} `;
    status.classList.add('update-available');
    const notes = document.createElement('a');
    notes.href = '#';
    notes.textContent = t('settings.updates.releaseNotes');
    notes.addEventListener('click', (event) => {
      event.preventDefault();
      // Goes through the main process, which only opens URLs of the project repository.
      runGuarded(window.aipermiler.openReleaseNotes());
    });
    status.append(notes);
  } else if (updates?.lastError) {
    status.textContent = t('settings.updates.failed', { error: updates.lastError });
    status.classList.add('update-error');
  } else if (updates?.lastCheckedAt) {
    status.textContent = t('settings.updates.upToDate', { time: formatDateTime(updates.lastCheckedAt) });
    status.classList.add('connected');
  } else {
    status.textContent = t('settings.updates.neverChecked');
  }

  download.hidden = !available;
  hint.hidden = !available;
  if (available) {
    download.textContent = t('settings.updates.download', { version: available.version });
    hint.textContent = available.assetName
      ? t('settings.updates.downloadHintAsset', { asset: available.assetName })
      : t('settings.updates.downloadHintPage');
  }
}

// --- Status bar and error handling ---------------------------------------------------

function showSaveStatus(text: string): void {
  const el = byId('save-status', HTMLElement);
  el.textContent = text;
  if (saveTimer) clearTimeout(saveTimer);
  saveTimer = setTimeout(() => { el.textContent = ''; }, 2500);
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

// No promise left on its own: an error (rejected IPC, main process throwing) ends
// up in the status bar instead of the console — a failing "Add account"/"Save"
// used to show nothing to the user.
function runGuarded(task: Promise<unknown>): void {
  task.catch((err: unknown) => {
    console.error('[settings]', err);
    showSaveStatus(t('settings.operationFailed', { error: errorMessage(err) }));
  });
}

function guarded(handler: () => Promise<void>): () => void {
  return () => { runGuarded(handler()); };
}

async function persist(key: string): Promise<AppSettings> {
  return window.aipermiler.setSettings({ [key]: (settings as unknown as PlainRecord)[key] });
}

// --- Events ----------------------------------------------------------------------------

function bindEvents(): void {
  // Delegated on document: account table rows and details are recreated on every
  // populateForm(), per-element listeners would be lost.
  document.addEventListener('change', (event) => {
    const el = event.target;
    if (!(el instanceof HTMLInputElement || el instanceof HTMLSelectElement) || !el.dataset.field) return;
    const field = el.dataset.field;
    const value = readFieldValue(el);
    setPath(settings as unknown as PlainRecord, field, value);
    if (el.id === 'tray-badge-select') renderTrayBadgeOptions();
    const accountMatch = /^accounts\.(\d+)\.(.+)$/.exec(field);
    if (accountMatch) {
      const index = Number(accountMatch[1]);
      if (accountMatch[2] === 'localInsights' && value === true) enforceSingleLocalInsightsDraft(index);
      if (accountMatch[2] === 'enabled' && value === true) enforceSingleSignInDraft(index);
      applyAccountDetailState(index);
    }
    // No saving or side effect here: the change stays a draft in the form until
    // the user presses "Save" (or "Cancel" to discard it) — saving on every field
    // used to clash with the "Save settings" button (user feedback).
  });

  document.addEventListener('click', (event) => {
    const target = event.target instanceof Element ? event.target.closest<HTMLButtonElement>('button[data-action]') : null;
    if (!target?.dataset.accountId) return;
    runGuarded(runAccountAction(target.dataset.action as string, target.dataset.accountId, target));
  });

  byId('btn-add-account').addEventListener('click', guarded(async () => {
    const provider = byId('add-account-provider', HTMLSelectElement).value as ProviderId;
    captureDraftFromForm();
    const id = await window.aipermiler.addAccount(provider);
    expandedAccountId = id;
    newAccountId = id;
    await reloadAfterAccountAction(id);
  }));

  // Info tooltips (ⓘ) open to the right of their icon: one near the right edge is
  // shifted left so it stays inside the window.
  const placeTooltip = (event: Event): void => {
    const icon = event.target instanceof Element ? event.target.closest('.info-icon') : null;
    const tooltip = icon?.querySelector<HTMLElement>('.tooltip');
    if (!tooltip) return;
    tooltip.style.left = '0px';
    const overflow = tooltip.getBoundingClientRect().right - (document.documentElement.clientWidth - 8);
    if (overflow > 0) tooltip.style.left = `${String(-Math.ceil(overflow))}px`;
  };
  document.addEventListener('mouseover', placeTooltip);
  document.addEventListener('focusin', placeTooltip);

  // Diagnostics: report file of every connected account + issue draft, after the
  // confirmation shown by the main process (it lists what goes in the file).
  byId('btn-report-responses').addEventListener('click', guarded(async () => {
    const button = byId('btn-report-responses', HTMLButtonElement);
    const status = byId('report-responses-status', HTMLElement);
    button.disabled = true;
    status.textContent = '';
    try {
      const result = await window.aipermiler.createDiagnosticReport();
      status.textContent = result.created && result.fileName
        ? t('settings.diagnostics.reportCreated', { file: result.fileName })
        : '';
    } catch (err) {
      status.textContent = t('settings.operationFailed', { error: errorMessage(err) });
    } finally {
      button.disabled = false;
    }
  }));

  byId('btn-save').addEventListener('click', guarded(async () => {
    // Explicitly re-reads every field (even those without a 'change' event yet,
    // e.g. a focused number input) and saves everything at once.
    const touchedKeys = captureDraftFromForm();
    for (const key of touchedKeys) {
      const stored = await persist(key);
      // The main process normalizes the accounts (e.g. one enabled Codex account): the
      // form shows what was really saved, not the draft.
      if (key === 'accounts' && settings) {
        settings.accounts = structuredClone(stored.accounts);
        populateForm();
      }
    }

    // Side effects that need a dedicated main-process action: applied only now
    // that the user confirmed with Save, and only if the value really changed
    // (otherwise every Save would recreate the window for an unrelated change).
    const newStyle = getPath(settings, 'ui.windowStyle') as AppSettings['ui']['windowStyle'];
    const newAlwaysOnTop = getPath(settings, 'ui.alwaysOnTop') as boolean;
    const newLanguage = getPath(settings, 'ui.language') as AppSettings['ui']['language'];
    if (newStyle !== savedSettings?.ui.windowStyle) {
      await window.aipermiler.setWindowStyle(newStyle);
    }
    if (newAlwaysOnTop !== savedSettings?.ui.alwaysOnTop) {
      await window.aipermiler.setAlwaysOnTop(newAlwaysOnTop);
    }
    if (newLanguage !== savedSettings?.ui.language) {
      // This window too switches language at once; the draft is kept.
      applyLanguage(newLanguage);
      populateForm();
      renderUpdatesCard(settings?.updates);
    }

    // A field affecting the budget computation (plan, renewal day, manual quota,
    // work schedule…) must not wait for the next automatic refresh (up to 30 min).
    if (touchedKeys.has('accounts')) {
      window.aipermiler.requestUsageRefresh();
    }

    savedSettings = structuredClone(settings);
    newAccountId = null;
    showSaveStatus(t('settings.saved'));
  }));

  byId('btn-check-updates').addEventListener('click', guarded(async () => {
    const btn = byId('btn-check-updates', HTMLButtonElement);
    const status = byId('updates-status', HTMLElement);
    btn.disabled = true;
    status.textContent = t('settings.updates.checking');
    try {
      renderUpdatesCard(await window.aipermiler.checkForUpdates());
    } catch (err) {
      status.textContent = t('settings.updates.failed', { error: errorMessage(err) });
    } finally {
      btn.disabled = false;
    }
  }));

  byId('btn-open-guide').addEventListener('click', guarded(async () => {
    await window.aipermiler.openGuide();
  }));

  byId('btn-download-update').addEventListener('click', guarded(async () => {
    try {
      await window.aipermiler.downloadUpdate();
    } catch (err) {
      showSaveStatus(t('settings.updates.downloadFailed', { error: errorMessage(err) }));
    }
  }));

  byId('btn-cancel').addEventListener('click', guarded(async () => {
    // Discards unsaved changes: reloads the persisted state and repopulates the form.
    settings = await window.aipermiler.getSettings();
    savedSettings = structuredClone(settings);
    populateForm();
    showSaveStatus(t('settings.cancelled'));
  }));
}

async function init(): Promise<void> {
  // The loading placeholders (settings.html) in the system language right away: the
  // saved language is known only once the main process answers.
  applyLanguage('auto');
  settings = await window.aipermiler.getSettings();
  savedSettings = structuredClone(settings);
  window.aipermiler.onViewUpdate((updated) => {
    const clean = JSON.stringify(settings?.ui.trayBadge) === JSON.stringify(savedSettings?.ui.trayBadge);
    trayView = updated;
    if (clean && settings && savedSettings) {
      settings.ui.trayBadge = structuredClone(updated.selection);
      savedSettings.ui.trayBadge = structuredClone(updated.selection);
      byId('tray-badge-critical', HTMLInputElement).checked = updated.selection.critical;
    }
    renderTrayBadgeOptions();
  });
  trayView = await window.aipermiler.getView();
  settings.ui.trayBadge = structuredClone(trayView.selection);
  savedSettings.ui.trayBadge = structuredClone(trayView.selection);
  applyLanguage(settings.ui.language);
  populateForm();
  bindEvents();
  byId('app-version', HTMLElement).textContent = await window.aipermiler.getAppVersion();
  // Not awaited: running the core takes a moment and must not delay the form.
  runGuarded(window.aipermiler.getCoreStatus().then((status) => {
    byId('core-status', HTMLElement).textContent = status.ok ? status.version : t('settings.diagnostics.coreUnavailable', { error: status.error });
  }));
  renderUpdatesCard(settings.updates);
  // Disabled in the markup until the settings are loaded and the handlers bound.
  for (const id of ['btn-add-account', 'btn-cancel', 'btn-save', 'btn-report-responses']) byId(id, HTMLButtonElement).disabled = false;
  // Only the updates state (owned by the main process): the rest of the form stays
  // a draft, a full repopulation would discard unsaved changes.
  window.aipermiler.onSettingsUpdate((updated) => {
    if (settings) {
      settings.updates = { ...updated.updates, autoCheck: settings.updates.autoCheck };
    }
    renderUpdatesCard(updated.updates);
  });
}

document.addEventListener('DOMContentLoaded', () => { runGuarded(init()); });
