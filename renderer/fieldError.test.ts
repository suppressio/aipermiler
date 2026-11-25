import { test } from 'node:test';
import assert from 'node:assert/strict';
import { dataFieldOf, withoutJsonPath } from './fieldError.js';

test('withoutJsonPath drops only a leading JSON path', () => {
  assert.equal(withoutJsonPath('accounts[1].renewalDay: must be 0 (none) or 1-31'), 'must be 0 (none) or 1-31');
  assert.equal(withoutJsonPath('settings.refreshIntervalMinutes: must be 5-1440'), 'must be 5-1440');
  assert.equal(withoutJsonPath('request body is not JSON'), 'request body is not JSON');
});

test('an account field becomes the input of that account in the form, by its current index', () => {
  assert.equal(dataFieldOf({ accountId: 'b', path: 'subscription.renewalRule.day' }, ['a', 'b']), 'accounts.1.subscription.renewalRule.day');
});

test('a global field keeps its path; an unknown account or no field gives no input', () => {
  assert.equal(dataFieldOf({ accountId: null, path: 'ui.refreshIntervalMinutes' }, []), 'ui.refreshIntervalMinutes');
  assert.equal(dataFieldOf({ accountId: 'gone', path: 'label' }, ['a']), null);
  assert.equal(dataFieldOf(null, ['a']), null);
});
