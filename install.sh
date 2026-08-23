#!/bin/sh
# Install prohairesis from a GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/Sh1n1230/prohairesis/main/install.sh | sh
#
# Read this script before you run it. That advice is not a formality here: this
# project's own documentation argues that you should not trust what you have not
# checked, and it would be incoherent to exempt itself.
#
# What it does:
#   - downloads one release archive and the release checksum file
#   - verifies the archive against the checksum, and refuses to continue if it
#     does not match
#   - unpacks a single binary into a directory you own
#
# What it does not do: it never uses sudo, never writes outside the install
# prefix, and never touches your shell configuration. If the install directory
# is not on your PATH it tells you and stops short of editing anything.
#
# Options (environment or flags):
#   --prefix DIR / PROHAIRESIS_PREFIX   install directory (default ~/.local/bin)
#   --version TAG / PROHAIRESIS_VERSION release tag (default: latest)
set -eu

REPO="Sh1n1230/prohairesis"
BIN="prohairesis"
PREFIX="${PROHAIRESIS_PREFIX:-$HOME/.local/bin}"
VERSION="${PROHAIRESIS_VERSION:-latest}"

while [ $# -gt 0 ]; do
  case "$1" in
    --prefix)  PREFIX="$2"; shift 2 ;;
    --prefix=*) PREFIX="${1#*=}"; shift ;;
    --version) VERSION="$2"; shift 2 ;;
    --version=*) VERSION="${1#*=}"; shift ;;
    -h|--help) sed -n '2,25p' "$0"; exit 0 ;;
    *) echo "install: unknown option $1" >&2; exit 2 ;;
  esac
done

say()  { printf '%s\n' "$*"; }
die()  { printf 'install: %s\n' "$*" >&2; exit 2; }

for tool in curl tar; do
  command -v "$tool" >/dev/null 2>&1 || die "$tool is required"
done

# --- platform ---------------------------------------------------------------
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) die "unsupported architecture: $arch" ;;
esac
case "$os" in
  darwin|linux) ;;
  *) die "unsupported operating system: $os (build from source instead)" ;;
esac

# --- resolve the release ----------------------------------------------------
if [ "$VERSION" = latest ]; then
  VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
            | sed -n 's/.*"tag_name" *: *"\([^"]*\)".*/\1/p' | head -1)
  [ -n "$VERSION" ] || die "could not determine the latest release"
fi

archive="${BIN}_${VERSION}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$VERSION"

say "prohairesis $VERSION ($os/$arch)"

tmp=$(mktemp -d) || die "could not create a temporary directory"
trap 'rm -rf "$tmp"' EXIT INT TERM

say "  downloading $archive"
curl -fsSL -o "$tmp/$archive" "$base/$archive" \
  || die "no release asset $archive at $VERSION"
curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" \
  || die "release $VERSION publishes no checksums.txt; refusing to install unverified"

# --- verify -----------------------------------------------------------------
# A download that is not checked is a download you have to trust. The whole
# point of this project is to not have to.
expected=$(grep " $archive\$" "$tmp/checksums.txt" | awk '{print $1}' | head -1)
[ -n "$expected" ] || die "checksums.txt does not list $archive"

if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$tmp/$archive" | awk '{print $1}')
elif command -v shasum >/dev/null 2>&1; then
  actual=$(shasum -a 256 "$tmp/$archive" | awk '{print $1}')
else
  die "no sha256sum or shasum available; refusing to install unverified"
fi

if [ "$expected" != "$actual" ]; then
  die "checksum mismatch for $archive
     expected $expected
     actual   $actual
   The download does not match what the release publishes. Do not use it."
fi
say "  checksum ok"

# Stronger verification is available and is not performed here, because it needs
# a tool this script will not install for you. If you want it, the release page
# documents how to verify the build provenance attestation with the GitHub CLI:
#   gh attestation verify <archive> --repo Sh1n1230/prohairesis

# --- install ----------------------------------------------------------------
tar -xzf "$tmp/$archive" -C "$tmp" || die "could not unpack $archive"
[ -f "$tmp/$BIN" ] || die "$archive does not contain a $BIN binary"

mkdir -p "$PREFIX" || die "could not create $PREFIX"
[ -w "$PREFIX" ] || die "$PREFIX is not writable by you.
   This installer will not use sudo. Choose a directory you own:
     --prefix \$HOME/.local/bin"

mv "$tmp/$BIN" "$PREFIX/$BIN"
chmod +x "$PREFIX/$BIN"
say "  installed $PREFIX/$BIN"

# --- PATH -------------------------------------------------------------------
case ":$PATH:" in
  *":$PREFIX:"*) ;;
  *)
    say ""
    say "  $PREFIX is not on your PATH."
    say "  Add this to your shell profile yourself; this script will not edit it:"
    say ""
    say "    export PATH=\"$PREFIX:\$PATH\""
    ;;
esac

say ""
say "Next:"
say "  $BIN doctor          what is and is not in effect"
say "  $BIN session start   begin checkpointing a repository"
