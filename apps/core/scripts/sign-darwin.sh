#!/bin/sh
# Purpose: sign macOS binaries with the fixed "Sous Dev Signing" identity, so their
#          designated requirement is "identifier + certificate" instead of a cdhash
#          and macOS privacy grants (TCC) survive rebuilds. The `build` task and the
#          goreleaser build hook run it; run it by hand after installing or updating
#          a release binary. See docs/guides/macos-signing-and-permissions.md.
# Usage:   sh scripts/sign-darwin.sh <binary>...
#          Files that are not Mach-O (Linux or Windows builds) are skipped. On other
#          systems the script does nothing.
# Env:     SOUS_SIGN           require (default): fail when the identity is missing;
#                              auto: warn and keep the linker's ad-hoc signature; off: skip.
#                              When CI is set (GitHub Actions) the default is off;
#                              the release workflow sets require.
#          SOUS_SIGN_DRY_RUN   1: print the codesign command instead of running it
#          SOUS_SIGN_IDENTITY  certificate name (default: Sous Dev Signing)
#          SOUS_SIGN_KEYCHAIN  keychain file (default: ~/Library/Keychains/sous-signing.keychain-db)
#          SOUS_SIGN_ID_PREFIX identifier prefix (default: cc.wxian.); the identifier is
#                              <prefix><file name>, such as cc.wxian.sous
# Needs:   codesign, security (both ship with macOS). Create the identity once with
#          scripts/setup-macos-signing.sh.
# Note:    while signing, the keychain is appended to the user's keychain search
#          list (codesign cannot find the identity otherwise) and the original list
#          is restored on exit. Dry runs leave the list alone.
set -eu

[ "$(uname -s)" = Darwin ] || exit 0

if [ -n "${SOUS_SIGN:-}" ]; then
  MODE="$SOUS_SIGN"
elif [ -n "${CI:-}" ]; then
  MODE=off # CI machines have no local identity; the release workflow sets SOUS_SIGN=require
else
  MODE=require
fi
NAME="${SOUS_SIGN_IDENTITY:-Sous Dev Signing}"
KC="${SOUS_SIGN_KEYCHAIN:-$HOME/Library/Keychains/sous-signing.keychain-db}"
PREFIX="${SOUS_SIGN_ID_PREFIX:-cc.wxian.}"
DRY="${SOUS_SIGN_DRY_RUN:-}"
SVC="sous-signing-keychain"

case "$MODE" in
  off) exit 0 ;;
  auto|require) ;;
  *) echo "error: SOUS_SIGN must be require, auto or off (got \"$MODE\")" >&2; exit 2 ;;
esac
[ $# -gt 0 ] || { echo "usage: $0 <binary>..." >&2; exit 2; }

# Keep only Mach-O files, so cross builds never need the identity.
status=0
for f in "$@"; do
  shift
  if [ ! -f "$f" ]; then
    echo "error: no such file: $f" >&2
    status=1
    continue
  fi
  case "$(file -b "$f")" in
    *Mach-O*) set -- "$@" "$f" ;;
    *) echo "skip $f (not a macOS binary)" ;;
  esac
done
[ $# -gt 0 ] || exit $status

# missing explains why nothing was signed: an error in require mode, a warning in auto mode.
missing() {
  if [ "$MODE" = require ]; then
    echo "error: cannot sign with \"$NAME\": $*" >&2
    echo "error: without it macOS asks for file access again after every rebuild." >&2
  else
    echo "warning: NOT signed with \"$NAME\": $*" >&2
    echo "warning: the binaries keep the linker's ad-hoc signature (designated requirement =" >&2
    echo "warning: cdhash), so macOS asks for file access again after every rebuild." >&2
  fi
  echo "  fix:   wok run core setup-signing   (= sh scripts/setup-macos-signing.sh in apps/core)" >&2
  echo "         (docs/guides/macos-signing-and-permissions.md)" >&2
  if [ "$MODE" = require ]; then
    echo "  or:    SOUS_SIGN=auto to build anyway with a warning, SOUS_SIGN=off to skip signing" >&2
    exit 1
  fi
  echo "  quiet: SOUS_SIGN=off" >&2
  exit $status
}

[ -f "$KC" ] || missing "keychain $KC does not exist"

# After a reboot the dedicated keychain is locked; its password is in the login keychain.
if ! security show-keychain-info "$KC" >/dev/null 2>&1; then
  pw=$(security find-generic-password -a "$USER" -s "$SVC" -w 2>/dev/null) \
    || missing "keychain $KC is locked and its password is not in the login keychain (service $SVC)"
  security unlock-keychain -p "$pw" "$KC" 2>/dev/null || missing "cannot unlock $KC"
  unset pw
fi

hash=$(security find-identity -p codesigning "$KC" 2>/dev/null \
  | awk -v n="\"$NAME\"" 'index($0, n) { print $2; exit }')
[ -n "$hash" ] || missing "no code signing identity named \"$NAME\" in $KC"

# codesign only finds an identity in a keychain on the user's search list;
# --keychain alone reports "no identity found". Append ours while signing and put
# the original list back afterwards, also when signing fails or is interrupted.
# Only the search list changes: no keychain contents, no trust settings.
search_list() { security list-keychains -d user | sed -e 's/^[[:space:]]*"//' -e 's/"[[:space:]]*$//'; }
set_search_list() { # $1: newline-separated keychain paths
  old_ifs=$IFS
  IFS='
'
  # shellcheck disable=SC2086 # split on newlines only, so paths may contain spaces
  security list-keychains -d user -s $1
  IFS=$old_ifs
}
restore_search_list() {
  [ -n "${orig_list+set}" ] || return 0
  set_search_list "$orig_list" || echo "error: could not restore the keychain search list to: $orig_list" >&2
  unset orig_list
}
if [ -z "$DRY" ]; then
  list=$(search_list) || missing "cannot read the keychain search list"
  if ! printf '%s\n' "$list" | grep -Fqx "$KC"; then
    trap restore_search_list EXIT
    trap 'restore_search_list; exit 130' INT TERM HUP
    orig_list=$list
    set_search_list "$list
$KC" || missing "cannot add $KC to the keychain search list"
  fi
fi

for f in "$@"; do
  dir=$(dirname "$f")
  base=$(basename "$f")
  if [ -n "$DRY" ]; then
    echo "dry run: codesign --force --sign $hash --keychain $KC --identifier $PREFIX$base --timestamp=none $f"
    continue
  fi
  # Sign a copy and rename it over the original: a running process keeps its
  # pages and the file is never half-written.
  tmp="$dir/.$base.signing.$$"
  cp -p "$f" "$tmp"
  if codesign --force --sign "$hash" --keychain "$KC" --identifier "$PREFIX$base" \
      --timestamp=none "$tmp" 2>"$tmp.err" && mv -f "$tmp" "$f"; then
    rm -f "$tmp.err"
    echo "signed $f as $PREFIX$base with \"$NAME\""
  else
    sed 's/^/  codesign: /' "$tmp.err" >&2
    rm -f "$tmp" "$tmp.err"
    echo "error: codesign failed for $f" >&2
    status=1
  fi
done
exit $status
