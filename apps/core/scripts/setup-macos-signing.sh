#!/bin/sh
# Purpose: create (or import) the fixed self-signed code signing identity that
#          scripts/sign-darwin.sh uses, so macOS privacy grants (TCC) for sous and
#          sous-hub survive rebuilds. macOS only; see
#          docs/guides/macos-signing-and-permissions.md.
# Usage:   sh scripts/setup-macos-signing.sh            create the identity if it is missing
#          sh scripts/setup-macos-signing.sh import <file.p12>
#                                                    import a backed-up identity instead
#                                                    (macOS asks for the .p12 password)
#          sh scripts/setup-macos-signing.sh status     show what is installed
# Env:     SOUS_SIGN_IDENTITY  certificate name (default: Sous Dev Signing)
#          SOUS_SIGN_KEYCHAIN  keychain file (default: ~/Library/Keychains/sous-signing.keychain-db)
# Needs:   /usr/bin/security, /usr/bin/openssl (LibreSSL that ships with macOS).
#
# The private key never leaves the dedicated keychain. The keychain's random
# password is stored in the login keychain (service "sous-signing-keychain") so
# sign-darwin.sh can unlock it after a reboot; it is never printed.
#
# The keychain is deliberately NOT added to the user's keychain search list, so
# the identity does not show up in other apps. codesign does need it on the list
# (with --keychain alone it reports "no identity found"), so sign-darwin.sh
# appends it only while signing and restores the original list afterwards. No
# mode of this script changes the search list; "status" changes nothing at all.
set -eu

NAME="${SOUS_SIGN_IDENTITY:-Sous Dev Signing}"
KC="${SOUS_SIGN_KEYCHAIN:-$HOME/Library/Keychains/sous-signing.keychain-db}"
SVC="sous-signing-keychain"
DAYS=7305 # 20 years

die() { echo "error: $*" >&2; exit 1; }

[ "$(uname -s)" = Darwin ] || die "this script is for macOS only"

identity_hash() {
  security find-identity -p codesigning "$KC" 2>/dev/null \
    | awk -v n="\"$NAME\"" 'index($0, n) { print $2; exit }'
}

randpw() { head -c 48 /dev/urandom | base64 | tr -d '/+=\n' | cut -c1-40; }

# Creates the dedicated keychain and remembers its password in the login keychain.
ensure_keychain() {
  if [ -f "$KC" ]; then
    pw=$(security find-generic-password -a "$USER" -s "$SVC" -w 2>/dev/null) \
      || die "$KC exists but its password is not in the login keychain (service $SVC); delete it with: security delete-keychain \"$KC\""
    security unlock-keychain -p "$pw" "$KC" || die "cannot unlock $KC"
    return
  fi
  pw=$(randpw)
  [ ${#pw} -ge 32 ] || die "cannot generate a password"
  security create-keychain -p "$pw" "$KC" || die "cannot create $KC"
  # No -l / -u: the keychain does not lock on sleep or after a timeout.
  security set-keychain-settings "$KC"
  security unlock-keychain -p "$pw" "$KC"
  security add-generic-password -U -a "$USER" -s "$SVC" -l "Sous signing keychain" \
    -T /usr/bin/security -w "$pw" \
    || { security delete-keychain "$KC"; die "cannot store the keychain password in the login keychain"; }
}

# Lets codesign use the key without a GUI prompt.
allow_codesign() {
  security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "$pw" "$KC" >/dev/null \
    || die "cannot set the key's access list"
}

status() {
  echo "keychain: $KC"
  if [ ! -f "$KC" ]; then echo "  missing"; return; fi
  h=$(identity_hash)
  if [ -z "$h" ]; then echo "  no \"$NAME\" identity"; return; fi
  echo "identity: \"$NAME\" sha1=$h"
  pem=$(security find-certificate -c "$NAME" -p "$KC" 2>/dev/null) || pem=""
  if [ -z "$pem" ]; then
    echo "  (cannot read the certificate details)"
    return
  fi
  printf '%s\n' "$pem" | /usr/bin/openssl x509 -noout -subject -dates | sed 's/^/  /'
  printf '%s\n' "$pem" | /usr/bin/openssl x509 -noout -text \
    | grep -A1 'Extended Key Usage' | tail -1 | sed 's/^ */  extended key usage: /'
}

create() {
  if [ -f "$KC" ] && [ -n "$(identity_hash)" ]; then
    echo "\"$NAME\" already exists in $KC; nothing to do"
    status
    return
  fi
  ensure_keychain

  umask 077
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT INT TERM
  cat >"$tmp/req.cnf" <<EOF
[req]
distinguished_name = dn
x509_extensions = ext
prompt = no
[dn]
CN = $NAME
[ext]
basicConstraints = critical, CA:false
keyUsage = critical, digitalSignature
extendedKeyUsage = critical, codeSigning
subjectKeyIdentifier = hash
EOF
  /usr/bin/openssl req -x509 -newkey rsa:2048 -nodes -sha256 -days "$DAYS" \
    -config "$tmp/req.cnf" -keyout "$tmp/key.pem" -out "$tmp/cert.pem" 2>/dev/null \
    || die "openssl could not create the certificate"
  P12PW=$(randpw)
  export P12PW
  /usr/bin/openssl pkcs12 -export -inkey "$tmp/key.pem" -in "$tmp/cert.pem" \
    -name "$NAME" -out "$tmp/id.p12" -passout env:P12PW \
    || die "openssl could not package the identity"
  security import "$tmp/id.p12" -k "$KC" -f pkcs12 -P "$P12PW" \
    -T /usr/bin/codesign >/dev/null \
    || die "cannot import the identity into $KC"
  unset P12PW
  allow_codesign
  rm -rf "$tmp"
  echo "created \"$NAME\" in $KC"
  status
}

import_p12() {
  [ -f "$1" ] || die "no such file: $1"
  ensure_keychain
  # Without -P, macOS asks for the .p12 password in a dialog.
  security import "$1" -k "$KC" -f pkcs12 -T /usr/bin/codesign \
    || die "cannot import $1"
  allow_codesign
  [ -n "$(identity_hash)" ] || die "$1 does not contain a code signing identity named \"$NAME\""
  echo "imported \"$NAME\" into $KC"
  status
}

case "${1:-create}" in
  create) create ;;
  import) [ $# -eq 2 ] || die "usage: $0 import <file.p12>"; import_p12 "$2" ;;
  status) status ;;
  *) die "usage: $0 [create | import <file.p12> | status]" ;;
esac
