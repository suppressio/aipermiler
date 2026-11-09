#!/bin/sh
# migrate-legacy-settings.sh - moves the settings of IA Hypermiler (v0.4.x) to
# AIpermiler v0.6.0 or later, for whoever skipped the v0.5.0 bridge release (#260).
# Linux and macOS. Windows: migrate-legacy-settings.ps1.
#
# What it does, the app closed:
#   1. copies <config>/ia-hypermiler/ia-hypermiler-config.json to
#      <config>/AIpermiler/aipermiler-config.json (accounts, settings, history,
#      the Copilot token of v0.4.x: same file, same store key);
#   2. if AIpermiler already has settings, asks first and keeps them as
#      aipermiler-config.json.bak-<date>;
#   3. once the copy is verified, deletes the old ia-hypermiler folder.
# <config> is ~/.config (or $XDG_CONFIG_HOME) on Linux and
# ~/Library/Application Support on macOS.
set -eu

fail() { echo "Error: $*" >&2; exit 1; }

case "$(uname -s)" in
  Darwin) config="$HOME/Library/Application Support" ;;
  *) config="${XDG_CONFIG_HOME:-$HOME/.config}" ;;
esac
legacy_dir="$config/ia-hypermiler"
legacy_store="$legacy_dir/ia-hypermiler-config.json"
new_dir="$config/AIpermiler"
new_store="$new_dir/aipermiler-config.json"

[ -f "$legacy_store" ] || fail "no IA Hypermiler settings found in $legacy_dir: nothing to migrate."

if pgrep -ix 'aipermiler|ia-hypermiler|ia hypermiler' >/dev/null 2>&1; then
  fail "AIpermiler or IA Hypermiler is running. Quit it (tray menu, Quit) and run this script again."
fi

if [ -f "$new_store" ]; then
  echo "AIpermiler already has settings in $new_dir."
  echo "They will be replaced by the IA Hypermiler ones and kept as a backup."
  printf "Continue? [y/N] "
  read -r answer || answer=""
  case "$answer" in
    y|Y|yes|YES) ;;
    *) echo "Nothing changed."; exit 0 ;;
  esac
  backup="$new_store.bak-$(date +%Y%m%d-%H%M%S)"
  mv "$new_store" "$backup"
  echo "Current settings kept as $backup"
fi

mkdir -p "$new_dir"
tmp="$new_store.tmp"
rm -f "$tmp"
cp "$legacy_store" "$tmp"
mv "$tmp" "$new_store"
cmp -s "$legacy_store" "$new_store" || fail "the copy in $new_store differs from the original: the old folder is kept."
echo "Settings copied to $new_store"

rm -rf "$legacy_dir"
echo "Old folder $legacy_dir deleted."
echo "Done: start AIpermiler."
