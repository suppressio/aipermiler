// services/updates.test.ts — new-version check (issue #5), mocked fetch, no network.
// Asset names taken from a real release produced by CI.

import { test, afterEach } from 'node:test';
import assert from 'node:assert/strict';
import { compareVersions, fetchLatestUpdate, guideUrl, pickDownloadAsset, pickLatestRelease, RELEASES_API_URL, TRUSTED_DOWNLOAD_PREFIX } from './updates';
import type { GithubRelease, GithubReleaseAsset } from './updates';

const originalFetch = globalThis.fetch;

afterEach(() => {
  globalThis.fetch = originalFetch;
});

function asset(name: string): GithubReleaseAsset {
  return { name, browser_download_url: `https://github.com/suppressio/aipermiler/releases/download/v0.3.0-beta/${name}` };
}

const CI_ASSETS = [
  asset('aipermiler-0.3.0-beta-arm64.dmg'),
  asset('aipermiler-0.3.0-beta-arm64.dmg.blockmap'),
  asset('aipermiler-0.3.0-beta.AppImage'),
  asset('aipermiler-Setup-0.3.0-beta.exe'),
  asset('aipermiler-Setup-0.3.0-beta.exe.blockmap'),
  asset('aipermiler_0.3.0-beta_amd64.deb'),
  asset('latest.yml'),
];

function release(tag: string, overrides: Partial<GithubRelease> = {}): GithubRelease {
  return {
    tag_name: tag,
    html_url: `https://github.com/suppressio/aipermiler/releases/tag/${tag}`,
    draft: false,
    prerelease: false,
    published_at: '2026-10-01T10:00:00Z',
    assets: CI_ASSETS,
    ...overrides,
  };
}

test('compareVersions: semver with pre-release and v prefix', () => {
  assert.ok(compareVersions('0.2.1-beta', '0.2.0-beta') > 0);
  assert.ok(compareVersions('0.2.1-beta', '0.2.1') < 0);
  assert.ok(compareVersions('v0.3.0-beta', '0.2.9') > 0);
  assert.ok(compareVersions('0.10.0', '0.9.0') > 0);
  assert.ok(compareVersions('1.0.0-beta.2', '1.0.0-beta.10') < 0);
  assert.equal(compareVersions('v0.2.1-beta', '0.2.1-beta'), 0);
  assert.throws(() => compareVersions('latest', '0.2.1'), /Invalid version/);
});

test('pickLatestRelease: the highest one, drafts and non-semver tags excluded', () => {
  const releases = [release('v0.2.1-beta'), release('v0.4.0-beta', { draft: true }), release('v0.3.0-beta'), release('nightly')];
  assert.equal(pickLatestRelease(releases, '0.2.1-beta')?.tag_name, 'v0.3.0-beta');
});

test('pickLatestRelease: no newer version → null', () => {
  assert.equal(pickLatestRelease([release('v0.2.1-beta'), release('v0.2.0-beta')], '0.2.1-beta'), null);
  assert.equal(pickLatestRelease([], '0.2.1-beta'), null);
});

test('pickLatestRelease: pre-releases only for who runs a pre-release', () => {
  const releases = [release('v0.4.0-beta', { prerelease: true }), release('v0.3.0-beta'), release('v0.2.9-beta', { prerelease: true })];
  // On a normal release: the test build is skipped.
  assert.equal(pickLatestRelease(releases, '0.2.1-beta')?.tag_name, 'v0.3.0-beta');
  assert.equal(pickLatestRelease(releases, '0.3.0-beta'), null);
  // On a pre-release: newer pre-releases are offered too.
  assert.equal(pickLatestRelease(releases, '0.2.9-beta')?.tag_name, 'v0.4.0-beta');
  // Not in the list (dev build): treated as a normal release.
  assert.equal(pickLatestRelease(releases, '0.2.5-beta')?.tag_name, 'v0.3.0-beta');
});

test('pickDownloadAsset: right package for every OS', () => {
  assert.equal(pickDownloadAsset(CI_ASSETS, 'win32', 'x64', false)?.name, 'aipermiler-Setup-0.3.0-beta.exe');
  assert.equal(pickDownloadAsset(CI_ASSETS, 'darwin', 'arm64', false)?.name, 'aipermiler-0.3.0-beta-arm64.dmg');
  assert.equal(pickDownloadAsset(CI_ASSETS, 'linux', 'x64', true)?.name, 'aipermiler-0.3.0-beta.AppImage');
  assert.equal(pickDownloadAsset(CI_ASSETS, 'linux', 'x64', false)?.name, 'aipermiler_0.3.0-beta_amd64.deb');
});

test('pickDownloadAsset: no fitting package → null (Intel Mac with arm64-only dmg, linux arm64 without deb)', () => {
  assert.equal(pickDownloadAsset(CI_ASSETS, 'darwin', 'x64', false), null);
  assert.equal(pickDownloadAsset(CI_ASSETS, 'linux', 'arm64', false), null);
  assert.equal(pickDownloadAsset(CI_ASSETS, 'freebsd', 'x64', false), null);
});

test('fetchLatestUpdate: queries the release list (not /latest) and offers the package for the OS', async () => {
  let calledUrl = '';
  globalThis.fetch = (async (input: string) => {
    calledUrl = input;
    return new Response(JSON.stringify([release('v0.3.0-beta'), release('v0.2.1-beta')]), { status: 200 });
  }) as typeof fetch;

  const update = await fetchLatestUpdate('0.2.1-beta', { platform: 'win32', arch: 'x64', isAppImage: false });
  assert.equal(calledUrl, RELEASES_API_URL);
  assert.deepEqual(update, {
    version: '0.3.0-beta',
    publishedAt: '2026-10-01T10:00:00Z',
    releaseUrl: 'https://github.com/suppressio/aipermiler/releases/tag/v0.3.0-beta',
    downloadUrl: 'https://github.com/suppressio/aipermiler/releases/download/v0.3.0-beta/aipermiler-Setup-0.3.0-beta.exe',
    assetName: 'aipermiler-Setup-0.3.0-beta.exe',
  });
});

test('fetchLatestUpdate: without a fitting package falls back to the release page', async () => {
  globalThis.fetch = (async () => new Response(JSON.stringify([release('v0.3.0-beta')]), { status: 200 }));
  const update = await fetchLatestUpdate('0.2.1-beta', { platform: 'darwin', arch: 'x64', isAppImage: false });
  assert.equal(update?.downloadUrl, 'https://github.com/suppressio/aipermiler/releases/tag/v0.3.0-beta');
  assert.equal(update.assetName, null);
});

test('fetchLatestUpdate: already up to date → null', async () => {
  globalThis.fetch = (async () => new Response(JSON.stringify([release('v0.2.1-beta')]), { status: 200 }));
  assert.equal(await fetchLatestUpdate('0.2.1-beta', { platform: 'linux', arch: 'x64', isAppImage: true }), null);
});

test('fetchLatestUpdate: HTTP error or unexpected response → explicit error', async () => {
  globalThis.fetch = (async () => new Response('rate limited', { status: 403, statusText: 'Forbidden' }));
  await assert.rejects(fetchLatestUpdate('0.2.1-beta', { platform: 'linux', arch: 'x64', isAppImage: true }), /GitHub Releases answered 403/);
  globalThis.fetch = (async () => new Response(JSON.stringify({ message: 'x' }), { status: 200 }));
  await assert.rejects(fetchLatestUpdate('0.2.1-beta', { platform: 'linux', arch: 'x64', isAppImage: true }), /not a list/);
});

test('guideUrl: the README of the repository in the interface language', () => {
  assert.equal(guideUrl('en'), 'https://github.com/suppressio/aipermiler/blob/main/README.md');
  assert.equal(guideUrl('it'), 'https://github.com/suppressio/aipermiler/blob/main/README.it.md');
  assert.ok(guideUrl('it').startsWith(TRUSTED_DOWNLOAD_PREFIX));
});
