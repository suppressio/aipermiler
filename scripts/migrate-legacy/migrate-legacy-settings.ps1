# migrate-legacy-settings.ps1 - moves the settings of IA Hypermiler (v0.4.x) to
# AIpermiler v0.6.0 or later, for whoever skipped the v0.5.0 bridge release (#260).
# Windows. Linux and macOS: migrate-legacy-settings.sh.
#
# Run it with:
#   powershell -ExecutionPolicy Bypass -File migrate-legacy-settings.ps1
#
# What it does, the app closed:
#   1. copies %APPDATA%\ia-hypermiler\ia-hypermiler-config.json to
#      %APPDATA%\AIpermiler\aipermiler-config.json (accounts, settings, history,
#      the Copilot token of v0.4.x: same file, same store key);
#   2. if AIpermiler already has settings, asks first and keeps them as
#      aipermiler-config.json.bak-<date>;
#   3. once the copy is verified, deletes the old ia-hypermiler folder.
$ErrorActionPreference = 'Stop'

function Fail([string]$message) {
  Write-Host "Error: $message" -ForegroundColor Red
  exit 1
}

$legacyDir = Join-Path $env:APPDATA 'ia-hypermiler'
$legacyStore = Join-Path $legacyDir 'ia-hypermiler-config.json'
$newDir = Join-Path $env:APPDATA 'AIpermiler'
$newStore = Join-Path $newDir 'aipermiler-config.json'

if (-not (Test-Path -LiteralPath $legacyStore -PathType Leaf)) {
  Fail "no IA Hypermiler settings found in ${legacyDir}: nothing to migrate."
}

if (Get-Process -Name 'AIpermiler', 'IA Hypermiler', 'aipermiler' -ErrorAction SilentlyContinue) {
  Fail 'AIpermiler or IA Hypermiler is running. Quit it (tray menu, Quit) and run this script again.'
}

if (Test-Path -LiteralPath $newStore -PathType Leaf) {
  Write-Host "AIpermiler already has settings in $newDir."
  Write-Host 'They will be replaced by the IA Hypermiler ones and kept as a backup.'
  $answer = Read-Host 'Continue? [y/N]'
  if ($answer -notmatch '^(y|yes)$') {
    Write-Host 'Nothing changed.'
    exit 0
  }
  $backup = "$newStore.bak-$(Get-Date -Format 'yyyyMMdd-HHmmss')"
  Move-Item -LiteralPath $newStore -Destination $backup
  Write-Host "Current settings kept as $backup"
}

New-Item -ItemType Directory -Force -Path $newDir | Out-Null
$tmp = "$newStore.tmp"
Remove-Item -LiteralPath $tmp -Force -ErrorAction SilentlyContinue
Copy-Item -LiteralPath $legacyStore -Destination $tmp
Move-Item -LiteralPath $tmp -Destination $newStore
if ((Get-FileHash -LiteralPath $legacyStore).Hash -ne (Get-FileHash -LiteralPath $newStore).Hash) {
  Fail "the copy in $newStore differs from the original: the old folder is kept."
}
Write-Host "Settings copied to $newStore"

Remove-Item -LiteralPath $legacyDir -Recurse -Force
Write-Host "Old folder $legacyDir deleted."
Write-Host 'Done: start AIpermiler.'
