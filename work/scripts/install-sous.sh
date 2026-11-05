#!/bin/sh
# Purpose: install or update the sous client (macOS, Linux) from GitHub Releases.
# Usage:   curl -fsSL https://raw.githubusercontent.com/XiaoXianThis/Sous/main/work/scripts/install-sous.sh | sh
#          ... | sh -s -- pair https://agent.wxian.cc 123456 --name laptop   (install, then run `sous <args>`)
# Env:     SOUS_VERSION      tag to install, such as v0.1.1 (default: latest)
#          SOUS_INSTALL_DIR  where to put the binary (default: where `sous` already is, else ~/.local/bin)
# Needs:   curl, tar, and shasum or sha256sum. Windows: install-sous.ps1.
set -eu

REPO="XiaoXianThis/Sous"

die() { echo "error: $*" >&2; exit 1; }

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux) os=linux ;;
  *) die "unsupported system $(uname -s); on Windows use install-sous.ps1" ;;
esac
case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) die "unsupported architecture $(uname -m)" ;;
esac

if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum "$1" | cut -d' ' -f1; }
elif command -v shasum >/dev/null 2>&1; then
  sha256() { shasum -a 256 "$1" | cut -d' ' -f1; }
else
  die "need sha256sum or shasum to verify the download"
fi

tag="${SOUS_VERSION:-}"
if [ -z "$tag" ]; then
  # /releases/latest redirects to /releases/tag/<tag>; this avoids the API rate limit.
  url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest") \
    || die "cannot reach GitHub"
  tag="${url##*/}"
fi
case "$tag" in v*) ;; *) tag="v$tag" ;; esac
ver="${tag#v}"

# Install where sous already lives so an update replaces it.
existing=$(command -v sous 2>/dev/null || true)
if [ -n "${SOUS_INSTALL_DIR:-}" ]; then
  dir="$SOUS_INSTALL_DIR"
elif [ -n "$existing" ]; then
  dir=$(dirname "$existing")
else
  dir="$HOME/.local/bin"
fi
mkdir -p "$dir" || die "cannot create $dir"

if [ -n "$existing" ] && [ "$("$existing" version 2>/dev/null || true)" = "$tag" ]; then
  echo "sous $tag is already installed at $existing"
else
  name="sous_${ver}_${os}_${arch}.tar.gz"
  base="https://github.com/$REPO/releases/download/$tag"
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT

  echo "downloading sous $tag ($os/$arch)..."
  curl -fsSL -o "$tmp/$name" "$base/$name" || die "download failed: $base/$name"
  curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" || die "cannot download checksums.txt"
  want=$(grep " $name\$" "$tmp/checksums.txt" | cut -d' ' -f1)
  [ -n "$want" ] || die "$name is not in checksums.txt"
  [ "$(sha256 "$tmp/$name")" = "$want" ] || die "checksum mismatch for $name; not installing"
  tar -xzf "$tmp/$name" -C "$tmp" sous

  # Stage next to the target and rename, so a running daemon is replaced atomically.
  cp "$tmp/sous" "$dir/.sous.new" && chmod 755 "$dir/.sous.new" && mv -f "$dir/.sous.new" "$dir/sous" \
    || die "cannot write $dir/sous"
  echo "installed sous $tag to $dir/sous"

  # An updated binary does nothing until the service restarts.
  if [ -n "$existing" ] && "$dir/sous" service status 2>/dev/null | grep -q "运行中"; then
    "$dir/sous" service restart && echo "restarted the sous service"
  fi
fi

case ":$PATH:" in
  *":$dir:"*) ;;
  *) echo "note: $dir is not in PATH; add this to your shell profile:  export PATH=\"$dir:\$PATH\"" ;;
esac

if [ "$#" -gt 0 ]; then
  exec "$dir/sous" "$@"
fi
echo "next: sous pair <hub-url> <code>   (the Machines panel on the web shows the command)"
