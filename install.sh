#!/bin/sh
# install.sh — fetch the klainmain binary for this machine from the GitHub
# release assets and put it in a user-writable directory (TDD-00179).
#
#   curl -fsSL https://raw.githubusercontent.com/memphisx/KlainMainLang/main/install.sh | sh
#
# Environment:
#   KLAINMAIN_VERSION      release to install, e.g. v0.64.0 (default: latest)
#   KLAINMAIN_INSTALL_DIR  where to put the binary (default: $HOME/.local/bin)
#   KLAINMAIN_BASE_URL     alternate download base (testing; default: GitHub releases)
#
# The binary is the compiler only: it drives `clang` at compile time and the
# programs it produces link against the optional libraries listed in the
# README's Requirements section — those are installed with the OS package
# manager, not by this script.
set -eu

repo="memphisx/KlainMainLang"
version="${KLAINMAIN_VERSION:-latest}"
install_dir="${KLAINMAIN_INSTALL_DIR:-$HOME/.local/bin}"

os="$(uname -s)"
arch="$(uname -m)"
case "$os" in
  Linux)  os_name=linux ;;
  Darwin) os_name=macos ;;
  *) echo "install.sh: unsupported OS '$os' (Linux and macOS only; on Windows use install.ps1)" >&2; exit 1 ;;
esac
case "$arch" in
  x86_64|amd64)  arch_name=x64 ;;
  aarch64|arm64) arch_name=arm64 ;;
  *) echo "install.sh: unsupported architecture '$arch' (x64 and arm64 only)" >&2; exit 1 ;;
esac
platform="$os_name-$arch_name"

if [ -n "${KLAINMAIN_BASE_URL:-}" ]; then
  base="$KLAINMAIN_BASE_URL"
elif [ "$version" = latest ]; then
  base="https://github.com/$repo/releases/latest/download"
else
  case "$version" in v*) ;; *) version="v$version" ;; esac
  base="https://github.com/$repo/releases/download/$version"
fi

fetch() { # fetch URL DEST
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL --retry 3 -o "$2" "$1"
  elif command -v wget >/dev/null 2>&1; then
    wget -q -O "$2" "$1"
  else
    echo "install.sh: need curl or wget" >&2; exit 1
  fi
}

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "klainmain: fetching checksums from $base ..."
if ! fetch "$base/checksums.txt" "$tmp/checksums.txt"; then
  echo "install.sh: no release found at $base (does the release exist, and is the version spelled like v0.64.0?)" >&2; exit 1
fi
# (a checksums line is "<sha256>  <name>"; a "*" before the name is sha256sum's
# binary-mode marker on some hosts, tolerated here)
asset="$(sed -n "s/^[0-9a-f]\{64\}  *\*\{0,1\}\(klainmain-v[^ ]*-$platform\)\$/\1/p" "$tmp/checksums.txt" | head -n 1)"
if [ -z "$asset" ]; then
  echo "install.sh: this release has no binary for $platform." >&2
  echo "  A platform is left out of a release when its test lane did not pass for that version;" >&2
  echo "  it returns with the next release that is green there. Assets in this release:" >&2
  sed 's/^[0-9a-f]* *//' "$tmp/checksums.txt" | sed 's/^/    /' >&2
  exit 2
fi

echo "klainmain: downloading $asset ..."
fetch "$base/$asset" "$tmp/$asset"

expected="$(grep -E " \*?$asset\$" "$tmp/checksums.txt" | cut -d' ' -f1)"
if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "$tmp/$asset" | cut -d' ' -f1)"
else
  actual="$(shasum -a 256 "$tmp/$asset" | cut -d' ' -f1)"
fi
if [ "$expected" != "$actual" ]; then
  echo "install.sh: checksum mismatch for $asset (expected $expected, got $actual)" >&2; exit 1
fi

mkdir -p "$install_dir"
install -m 0755 "$tmp/$asset" "$install_dir/klainmain"
echo "klainmain: installed $("$install_dir/klainmain" --version) to $install_dir/klainmain"

case ":$PATH:" in
  *":$install_dir:"*) ;;
  *) echo "note: $install_dir is not on your PATH — add it, e.g.:  export PATH=\"$install_dir:\$PATH\"" ;;
esac
if ! command -v clang >/dev/null 2>&1; then
  echo "note: 'clang' is not on your PATH; klainmain needs it to build programs (see the README's Requirements section)."
fi
