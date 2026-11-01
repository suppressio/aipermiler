// services/updates.ts — check for a new app version (issue #5).
// No extra dependency (no electron-updater, the user's explicit choice): it reads the
// project's GitHub Releases list and offers the right package for the current OS, to
// download in the browser and install by hand — works the same on the 3 OSes even
// with unsigned packages.
//
// Endpoint: GET /repos/{owner}/{repo}/releases (official GitHub REST API, no
// authentication). NOT /releases/latest: CI publishes every release as a pre-release
// (`releaseType: "prerelease"` in package.json) and the owner promotes the tested ones
// by hand, so the list is needed to tell the two apart. No user data leaves in the
// request: it is an anonymous GET to a public repository.
//
// Pure functions (compareVersions/pickLatestRelease/pickDownloadAsset) + a single
// network point (fetchLatestUpdate, via services/_http.ts: 10s timeout, explicit errors).

import { fetchJson } from './_http';
import type { UpdateInfo } from '../types/index';

const REPO = 'suppressio/aipermiler';
// 100 = GitHub's page maximum, one request. Beyond it an installed pre-release is no
// longer recognised (it then follows normal releases) and a normal release preceded by
// 100 pre-releases is missed: years of margin at the current pace, no pagination.
export const RELEASES_API_URL = `https://api.github.com/repos/${REPO}/releases?per_page=100`;
/** Every URL opened by the "Download" button must be under this prefix (see main.ts). */
export const TRUSTED_DOWNLOAD_PREFIX = `https://github.com/${REPO}/`;

/** The user guide (README) on GitHub, in the interface language (Settings → Guide). */
export function guideUrl(locale: 'en' | 'it'): string {
  return `${TRUSTED_DOWNLOAD_PREFIX}blob/main/${locale === 'it' ? 'README.it.md' : 'README.md'}`;
}

export interface GithubReleaseAsset {
  name: string;
  browser_download_url: string;
}

export interface GithubRelease {
  tag_name: string;
  html_url: string;
  draft: boolean;
  prerelease: boolean;
  published_at: string | null;
  assets: GithubReleaseAsset[];
}

interface ParsedVersion {
  core: number[];
  pre: string[];
}

function parseVersion(version: string): ParsedVersion | null {
  const match = /^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+.*)?$/.exec(version.trim());
  if (!match) return null;
  return {
    core: [Number(match[1]), Number(match[2]), Number(match[3])],
    pre: match[4] ? match[4].split('.') : [],
  };
}

/**
 * Semver comparison (with pre-releases): < 0 if a < b, 0 if equal, > 0 if a > b.
 * The "v" prefix is ignored. A version without pre-release wins over the same one with
 * a pre-release (0.2.1-beta < 0.2.1). Throws on a non-semver version: better an explicit
 * error than an update offered (or hidden) by mistake.
 */
export function compareVersions(a: string, b: string): number {
  const pa = parseVersion(a);
  const pb = parseVersion(b);
  if (!pa || !pb) throw new Error(`Invalid version: ${!pa ? a : b}`);

  const coreDiff = pa.core.map((value, i) => value - (pb.core[i] ?? 0)).find((diff) => diff !== 0);
  if (coreDiff !== undefined) return coreDiff;
  if (pa.pre.length === 0 || pb.pre.length === 0) return pb.pre.length - pa.pre.length;

  for (let i = 0; i < Math.max(pa.pre.length, pb.pre.length); i++) {
    const x = pa.pre[i];
    const y = pb.pre[i];
    if (x === undefined) return -1;
    if (y === undefined) return 1;
    const xn = /^\d+$/.test(x);
    const yn = /^\d+$/.test(y);
    if (xn && yn && Number(x) !== Number(y)) return Number(x) - Number(y);
    if (xn !== yn) return xn ? -1 : 1;
    if (x !== y) return x < y ? -1 : 1;
  }
  return 0;
}

/**
 * Most recent release (drafts and non-semver tags excluded), or null when it is not
 * newer than `currentVersion`. Pre-releases may hold builds still to test: they are
 * offered only when the installed version is itself a GitHub pre-release; a version
 * missing from the list (dev build, too old) counts as a normal release.
 */
export function pickLatestRelease(releases: GithubRelease[], currentVersion: string): GithubRelease | null {
  const onPrerelease = releases.some((r) => r.prerelease && parseVersion(r.tag_name) && compareVersions(r.tag_name, currentVersion) === 0);
  let best: GithubRelease | null = null;
  for (const release of releases) {
    if (release.draft || (release.prerelease && !onPrerelease) || !parseVersion(release.tag_name)) continue;
    if (!best || compareVersions(release.tag_name, best.tag_name) > 0) best = release;
  }
  if (!best || compareVersions(best.tag_name, currentVersion) <= 0) return null;
  return best;
}

/**
 * Package fitting the current OS, according to the names produced by CI
 * (.github/workflows/build.yml): Windows `.exe` (NSIS), macOS `.dmg` of the same
 * architecture (CI builds only arm64 today: on an Intel Mac no asset fits), Linux
 * `.AppImage` when the app runs as an AppImage, `.deb` otherwise.
 * null = no fitting asset → the caller falls back to the release page.
 */
export function pickDownloadAsset(
  assets: GithubReleaseAsset[],
  platform: string,
  arch: string,
  isAppImage: boolean,
): GithubReleaseAsset | null {
  const byExt = (ext: string) => assets.filter((a) => a.name.toLowerCase().endsWith(ext));
  switch (platform) {
    case 'win32':
      return byExt('.exe')[0] ?? null;
    case 'darwin': {
      const dmgs = byExt('.dmg');
      const archTag = arch === 'arm64' ? 'arm64' : arch === 'x64' ? 'x64' : arch;
      // Without an architecture suffix electron-builder produces the x64 dmg.
      return dmgs.find((a) => a.name.includes(`-${archTag}.dmg`))
        ?? (archTag === 'x64' ? dmgs.find((a) => !/-(arm64|universal)\.dmg$/.test(a.name)) : undefined)
        ?? dmgs.find((a) => a.name.includes('-universal.dmg'))
        ?? null;
    }
    case 'linux': {
      if (isAppImage) return byExt('.appimage')[0] ?? null;
      const debArch = arch === 'x64' ? 'amd64' : arch;
      return byExt('.deb').find((a) => a.name.includes(`_${debArch}.deb`)) ?? null;
    }
    default:
      return null;
  }
}

export interface UpdateEnvironment {
  platform: string;
  arch: string;
  isAppImage: boolean;
}

/** Checks GitHub Releases: UpdateInfo when a newer version exists, null otherwise. Throws on error. */
export async function fetchLatestUpdate(currentVersion: string, env: UpdateEnvironment): Promise<UpdateInfo | null> {
  const releases = await fetchJson(RELEASES_API_URL, {
    headers: { Accept: 'application/vnd.github+json', 'User-Agent': 'aipermiler' },
    label: 'GitHub Releases',
  });
  if (!Array.isArray(releases)) throw new Error('GitHub Releases: unexpected response (not a list)');

  const latest = pickLatestRelease(releases as GithubRelease[], currentVersion);
  if (!latest) return null;

  const asset = pickDownloadAsset(latest.assets, env.platform, env.arch, env.isAppImage);
  return {
    version: latest.tag_name.replace(/^v/, ''),
    publishedAt: latest.published_at,
    releaseUrl: latest.html_url,
    downloadUrl: asset?.browser_download_url ?? latest.html_url,
    assetName: asset?.name ?? null,
  };
}
