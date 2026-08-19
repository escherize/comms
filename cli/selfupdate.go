package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// Self-update keeps a release client byte-identical to the hub it talks to,
// so version skew — the class where a stale binary refuses verbs the docs
// promise — heals itself instead of waiting for a human to re-run an
// installer. The hub serves its own executable at /comms with an
// X-Comms-Build hash; when ours differs, we adopt the hub's copy and re-exec
// the same argv. The agent never knows it happened.
//
// It never runs for a source build (Version == ""): a developer's dirty-tree
// binary must not be clobbered by an older hub. And it never blocks a verb —
// any failure falls through silently to running what we have.

// selfUpdateEvery is how often a client bothers to ask. Timothy's rule:
// "stale by 10 seconds" is fine, so stale by minutes certainly is.
const selfUpdateEvery = 5 * time.Minute

// maybeSelfUpdate is called once per invocation, before verb dispatch.
func maybeSelfUpdate(e *Env, verb string) {
	if Version == "" || runtime.GOOS == "windows" {
		return // source builds own their bytes; windows cannot rename a running exe
	}
	switch verb {
	case "serve", "version", "--version", "-version", "-h", "--help", "help":
		return // the hub is the source of truth, and help must never dial out
	}
	if v, _ := e.getenv("COMMS_NO_SELFUPDATE"); v != "" {
		return
	}
	if v, _ := e.getenv("COMMS_SELFUPDATED"); v != "" {
		return // we are the fresh copy; checking again would loop on a flapping hub
	}
	if !selfUpdateDue() {
		return
	}
	selfUpdate(e)
}

// selfUpdateDue debounces via one file's mtime. Touch first, check after: two
// racing invocations at worst both check, which is harmless.
func selfUpdateDue() bool {
	dir := filepath.Join(configDir(), "state")
	if os.Getenv("COMMS_HOME") != "" {
		dir = filepath.Join(os.Getenv("COMMS_HOME"), "state")
	}
	stamp := filepath.Join(dir, "selfupdate.checked")
	if fi, err := os.Stat(stamp); err == nil && time.Since(fi.ModTime()) < selfUpdateEvery {
		return false
	}
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(stamp, nil, 0o600)
	return true
}

func selfUpdate(e *Env) {
	// Trust boundary: adopting the hub's bytes repeats the trust decision the
	// install already made (curl <hub>/comms), continuously. Over loopback, an
	// attacker who could tamper with the reply is already on the box. Over
	// https, TLS carries integrity. Plain http across a network is the one
	// lane where an on-path attacker could swap the binary for their own — so
	// that lane never self-updates. The hash check below is fetch integrity
	// (torn or proxied download), not authenticity.
	// ponytail: signed releases (/comms.sig verified against an embedded
	// public key) are the upgrade path if hubs stop being team-trusted infra.
	if !updateChannelTrusted(e.Server) {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	base := strings.TrimRight(e.Server, "/")

	head := &http.Client{Timeout: 2 * time.Second}
	resp, err := head.Head(base + "/comms")
	if err != nil || resp.StatusCode != http.StatusOK {
		return
	}
	resp.Body.Close()
	if resp.Header.Get("X-Comms-Platform") != runtime.GOOS+"/"+runtime.GOARCH {
		return // the hub's binary would not even exec here
	}
	want := resp.Header.Get("X-Comms-Build")
	if want == "" {
		return
	}
	self, err := fileSHA256(exe)
	if err != nil || self == want {
		return
	}

	get := &http.Client{Timeout: 60 * time.Second}
	dl, err := get.Get(base + "/comms")
	if err != nil || dl.StatusCode != http.StatusOK {
		return
	}
	defer dl.Body.Close()
	// Same directory as the running binary, so the rename is atomic.
	tmp, err := os.CreateTemp(filepath.Dir(exe), ".comms-update-*")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, dl.Body); err != nil {
		tmp.Close()
		return
	}
	tmp.Close()
	// The bytes must hash to what the HEAD promised: a truncated or proxied
	// download must not become the binary.
	if got, err := fileSHA256(tmp.Name()); err != nil || got != want {
		return
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return
	}
	if err := os.Rename(tmp.Name(), exe); err != nil {
		return
	}
	e.Out.Note("self-updated to the hub's build (%s); re-running", want[:12])
	env := append(os.Environ(), "COMMS_SELFUPDATED=1")
	_ = syscall.Exec(exe, os.Args, env) // on failure, the old process carries on
}

// updateChannelTrusted says whether a server URL may feed us executable
// bytes: https, or loopback.
func updateChannelTrusted(server string) bool {
	u, err := url.Parse(server)
	if err != nil {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	if u.Scheme != "http" {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
