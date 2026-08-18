package shell

import (
	"fmt"
	"net/http"
)

// InstallVersion is the client version this hub matches — main copies the
// release stamp here at startup. The installer the hub serves pins downloads
// to it, so a client installed through the hub is the build the hub is
// running: the version-skew class (a stale binary refusing verbs the docs
// promise) cannot happen through this door. Empty means a source build; the
// script then takes the newest release.
var InstallVersion = ""

// getInstall serves the installer script. It is deliberately public — an
// uninstalled client cannot hold a session, and the script contains no hub
// data beyond the version string. curl -fsSL <hub>/install | sh
func (s *Server) getInstall(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	fmt.Fprintf(w, installScript, InstallVersion)
}

// No %% anywhere below except the one %s: the script goes through Sprintf.
const installScript = `#!/bin/sh
# comms installer — served by the hub itself, pinned to the hub's own version.
set -eu

VER="%s"   # empty: a source-built hub, take the newest release
REPO="https://github.com/escherize/comms"

OS=$(uname -s | tr 'A-Z' 'a-z')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "unsupported arch $ARCH — build from source: $REPO" >&2; exit 1 ;;
esac
case "$OS" in
  linux|darwin) ;;
  *) echo "unsupported OS $OS — binaries: $REPO/releases" >&2; exit 1 ;;
esac

if [ -z "$VER" ]; then
  VER=$(curl -fsSL "https://api.github.com/repos/escherize/comms/releases/latest" \
    | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)
  [ -n "$VER" ] || { echo "could not resolve the latest release" >&2; exit 1; }
fi

URL="$REPO/releases/download/$VER/comms_${VER}_${OS}_${ARCH}.tar.gz"
DEST="${COMMS_INSTALL_DIR:-$HOME/.local/bin}"
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

echo "installing comms $VER ($OS/$ARCH) to $DEST" >&2
curl -fsSL "$URL" | tar -xz -C "$TMP"
mkdir -p "$DEST"
install -m 0755 "$TMP/comms" "$DEST/comms"

"$DEST/comms" --version
case ":$PATH:" in
  *":$DEST:"*) ;;
  *) echo "note: $DEST is not on your PATH — add it, or move the binary" >&2 ;;
esac
`
