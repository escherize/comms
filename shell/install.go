package shell

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"sync"
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

// getBinary serves the hub's own executable. This exists because agents
// (correctly) refuse to pipe a remote script into a shell: downloading one
// file to a user directory is an act their safety rules allow, and the bytes
// are the exact build the hub runs, so version skew cannot happen through
// this door. Public for the same reason /install is. A client on a different
// OS/arch gets a binary that fails loudly at exec; the header names the
// platform so a careful caller can check first.
func (s *Server) getBinary(w http.ResponseWriter, r *http.Request) {
	exe, err := os.Executable()
	if err != nil {
		http.Error(w, "the hub cannot find its own binary", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Comms-Platform", runtime.GOOS+"/"+runtime.GOARCH)
	// The build hash is what lets a client answer "am I this hub's build?"
	// with one HEAD — the self-update check rides on it.
	if h := binarySHA(exe); h != "" {
		w.Header().Set("X-Comms-Build", h)
	}
	http.ServeFile(w, r, exe)
}

// binarySHA hashes the served executable once per process. The binary cannot
// change under a running hub (an upgrade is a new process), so once is right.
var (
	binaryHashOnce sync.Once
	binaryHash     string
)

func binarySHA(exe string) string {
	binaryHashOnce.Do(func() {
		f, err := os.Open(exe)
		if err != nil {
			return
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return
		}
		binaryHash = hex.EncodeToString(h.Sum(nil))
	})
	return binaryHash
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
  *) echo "note: $DEST is not on your PATH. For this session:" >&2
     echo "  export PATH=\"$DEST:\$PATH\"" >&2
     echo "add that line to your shell rc to make it stick" >&2 ;;
esac
`
