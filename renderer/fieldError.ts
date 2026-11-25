// renderer/fieldError.ts — the Settings input a service validation error is about (#337):
// main maps the error's JSON path to an account id and a form path
// (main/serviceSettings.ts fieldOfInvalidSettings); here it becomes the input's
// data-field, `accounts.<index>.<path>` by the account's current position in the form.

import type { CoreSettingsFieldError } from '../types/ipc';

/** The reason of a service validation message without its leading JSON path (`accounts[1].renewalDay: …`). */
export function withoutJsonPath(message: string): string {
  return message.replace(/^[A-Za-z][\w.[\]]*: /, '');
}

export function dataFieldOf(field: CoreSettingsFieldError['field'], accountIds: readonly string[]): string | null {
  if (field === null) return null;
  if (field.accountId === null) return field.path;
  const index = accountIds.indexOf(field.accountId);
  return index === -1 ? null : `accounts.${String(index)}.${field.path}`;
}
