#!/bin/sh
# Purpose: export the "Sous Dev Signing" identity (certificate + private key) as a
#          base64-encoded .p12 for the release workflow's repository secrets, so
#          published darwin binaries are signed with the same identity as local
#          builds (decision 0026). macOS only; see the "发布产物签名" section of
#          docs/guides/macos-signing-and-permissions.md.
# Usage:   export SOUS_SIGN_CERT_P12_PASSWORD="$(openssl rand -hex 24)"
#          sh scripts/export-signing-cert.sh | gh secret set SOUS_SIGN_CERT_P12
#          printf '%s' "$SOUS_SIGN_CERT_P12_PASSWORD" | gh secret set SOUS_SIGN_CERT_P12_PASSWORD
#          (or paste the output into GitHub's "New repository secret" page)
# Env:     SOUS_SIGN_CERT_P12_PASSWORD  password for the .p12 (required, never printed)
#          SOUS_SIGN_IDENTITY  certificate name (default: Sous Dev Signing)
#          SOUS_SIGN_KEYCHAIN  keychain file (default: ~/Library/Keychains/sous-signing.keychain-db)
# Output:  stdout: the .p12 as one line of base64, nothing else.
#          stderr: the certificate's SHA-1 and the next steps.
# Needs:   /usr/bin/security, /usr/bin/openssl, base64 (all ship with macOS).
#
# The .p12 only exists in a private temporary directory under $TMPDIR (never in
# the repository) and is deleted on exit. macOS may ask for permission to export
# the private key; the dialog wants the "sous-signing" keychain's password, which
# `security find-generic-password -s sous-signing-keychain -w` shows.
set -eu

NAME="${SOUS_SIGN_IDENTITY:-Sous Dev Signing}"
KC="${SOUS_SIGN_KEYCHAIN:-$HOME/Library/Keychains/sous-signing.keychain-db}"
SVC="sous-signing-keychain"

die() { echo "error: $*" >&2; exit 1; }

[ "$(uname -s)" = Darwin ] || die "this script is for macOS only"
[ -n "${SOUS_SIGN_CERT_P12_PASSWORD:-}" ] || die "set SOUS_SIGN_CERT_P12_PASSWORD first, for example:
  export SOUS_SIGN_CERT_P12_PASSWORD=\"\$(openssl rand -hex 24)\"
(it becomes the secret SOUS_SIGN_CERT_P12_PASSWORD, so keep the same shell open)"
[ -f "$KC" ] || die "keychain $KC does not exist; create the identity with: wok run core setup-signing"

# After a reboot the dedicated keychain is locked; its password is in the login keychain.
if ! security show-keychain-info "$KC" >/dev/null 2>&1; then
  pw=$(security find-generic-password -a "$USER" -s "$SVC" -w 2>/dev/null) \
    || die "keychain $KC is locked and its password is not in the login keychain (service $SVC)"
  security unlock-keychain -p "$pw" "$KC" 2>/dev/null || die "cannot unlock $KC"
  unset pw
fi

count=$(security find-identity -p codesigning "$KC" 2>/dev/null | grep -c "\"$NAME\"" || true)
[ "$count" -ge 1 ] || die "no code signing identity named \"$NAME\" in $KC"
[ "$count" -eq 1 ] || die "$KC holds $count identities named \"$NAME\"; keep only one"

umask 077
tmp=$(mktemp -d "${TMPDIR:-/tmp}/sous-export.XXXXXX")
trap 'rm -rf "$tmp"' EXIT
trap 'rm -rf "$tmp"; exit 130' INT TERM HUP

# -t identities exports every certificate that has a private key; the dedicated
# keychain holds only this one (checked above and again below).
security export -k "$KC" -t identities -f pkcs12 -P "$SOUS_SIGN_CERT_P12_PASSWORD" \
  -o "$tmp/id.p12" >/dev/null || die "security could not export the identity from $KC"

# Read it back with the password to make sure the file is what the workflow expects.
certs=$(/usr/bin/openssl pkcs12 -in "$tmp/id.p12" -nokeys -clcerts \
  -passin env:SOUS_SIGN_CERT_P12_PASSWORD 2>/dev/null) \
  || die "the exported .p12 cannot be read back with SOUS_SIGN_CERT_P12_PASSWORD"
n=$(printf '%s\n' "$certs" | grep -c 'BEGIN CERTIFICATE' || true)
[ "$n" -eq 1 ] || die "the export holds $n identities; expected only \"$NAME\""
subject=$(printf '%s\n' "$certs" | /usr/bin/openssl x509 -noout -subject)
case "$subject" in
  *"CN=$NAME"*|*"CN = $NAME"*) ;;
  *) die "the exported certificate is \"$subject\", not \"$NAME\"" ;;
esac
sha1=$(printf '%s\n' "$certs" | /usr/bin/openssl x509 -noout -fingerprint -sha1 \
  | sed -e 's/^.*=//' -e 's/://g' | tr 'a-f' 'A-F')
unset certs

base64 -i "$tmp/id.p12" | tr -d '\n'
echo

cat >&2 <<EOF

exported "$NAME" (certificate SHA-1 $sha1) as a base64 .p12 to stdout.
The release workflow only accepts the certificate whose SHA-1 is in
SOUS_SIGN_CERT_SHA1 in .github/workflows/release.yml; check that it matches.

Next steps (GitHub → the repository → Settings → Secrets and variables → Actions):
  1. New repository secret SOUS_SIGN_CERT_P12: the base64 line printed above.
     With the GitHub CLI, run this script again piped into it instead:
       sh scripts/export-signing-cert.sh | gh secret set SOUS_SIGN_CERT_P12
  2. New repository secret SOUS_SIGN_CERT_P12_PASSWORD: the value of
     \$SOUS_SIGN_CERT_P12_PASSWORD in this shell. With the GitHub CLI:
       printf '%s' "\$SOUS_SIGN_CERT_P12_PASSWORD" | gh secret set SOUS_SIGN_CERT_P12_PASSWORD
The .p12 must have been exported with that same password. Do not save the
.p12 or the password in the repository.
EOF
