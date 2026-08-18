package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
)

// distinctiveTerms picks the words worth searching from a natural sentence.
// Searching the raw question matches on its stopwords, which are exactly the
// words every other question also contains.
func distinctiveTerms(sentence string) []string {
	stop := map[string]bool{
		"a": true, "an": true, "and": true, "are": true, "as": true, "at": true,
		"be": true, "but": true, "by": true, "can": true, "did": true, "do": true,
		"does": true, "for": true, "from": true, "has": true, "have": true,
		"how": true, "i": true, "if": true, "in": true, "is": true, "it": true,
		"its": true, "of": true, "on": true, "or": true, "should": true,
		"so": true, "that": true, "the": true, "then": true, "there": true,
		"this": true, "to": true, "was": true, "we": true, "what": true,
		"when": true, "where": true, "which": true, "why": true, "will": true,
		"with": true, "would": true, "you": true, "your": true, "safe": true,
		"any": true, "some": true, "just": true, "still": true, "get": true,
	}
	word := regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9._:/\-]*`)
	seen := map[string]bool{}
	var terms []string
	for _, w := range word.FindAllString(sentence, -1) {
		lower := strings.ToLower(w)
		if stop[lower] || len(lower) < 3 || seen[lower] {
			continue
		}
		seen[lower] = true
		terms = append(terms, w)
	}
	// Longer words carry more signal than short ones.
	sort.SliceStable(terms, func(i, j int) bool { return len(terms[i]) > len(terms[j]) })
	if len(terms) > 6 {
		terms = terms[:6]
	}
	return terms
}

// searchHit is what the JSON search lane returns per event.
type searchHit struct {
	Seq  int64          `json:"seq"`
	Kind string         `json:"kind"`
	Body map[string]any `json:"body"`
	Rank float64        `json:"rank"`
}

// searchFor runs the JSON search lane. A search failure is not a reason to
// refuse the post: attaching context is a convenience, never a gate.
func searchFor(e *Env, room, query string, limit int) []searchHit {
	q := url.Values{}
	q.Set("q", query)
	q.Set("room", room)
	resp, err := doRead(e, nil, func() (*http.Request, error) {
		req, err := http.NewRequest("GET", e.Server+"/search?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		return req, nil
	})
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	var hits []searchHit
	dec := json.NewDecoder(resp.Body)
	for {
		var m map[string]any
		if err := dec.Decode(&m); err != nil {
			break
		}
		if m["type"] != "event" {
			continue
		}
		var h searchHit
		raw, _ := json.Marshal(m)
		if json.Unmarshal(raw, &h) == nil {
			hits = append(hits, h)
		}
		if len(hits) >= limit {
			break
		}
	}
	return hits
}

// errUnreachable marks an upload that never reached the hub, so callers can
// answer "wait and retry" instead of misreporting transport as a refusal.
var errUnreachable = errors.New("unreachable")

// uploadArtifact stores content and returns its hash. It sends text/markdown
// and sniffs nothing: ADR-0011 puts the boundary at the renderer, and a file
// extension is not evidence about bytes.
func uploadArtifact(e *Env, content []byte) (string, int, error) {
	resp, err := doRead(e, nil, func() (*http.Request, error) {
		req, err := http.NewRequest("POST", e.Server+"/artifacts", bytes.NewReader(content))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "text/markdown")
		return req, nil
	})
	if err != nil {
		return "", 0, fmt.Errorf("%w: %v", errUnreachable, err)
	}
	defer resp.Body.Close()

	var out struct {
		Hash      string `json:"hash"`
		Size      int    `json:"size"`
		Invariant string `json:"invariant"`
		Detail    string `json:"detail"`
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("%s: %s", out.Invariant, out.Detail)
	}
	return out.Hash, out.Size, nil
}

// readContent takes a file, or stdin for "-".
func readContent(e *Env, path string) ([]byte, error) {
	if path == "-" {
		if e.Stdin == nil {
			return nil, fmt.Errorf("no stdin")
		}
		return io.ReadAll(e.Stdin)
	}
	return os.ReadFile(path)
}

// stdinClaim tracks who took stdin, so two flags cannot both consume it.
type stdinClaim struct{ takenBy string }

var claimed stdinClaim

// resolveText takes the text from a flag, a file, or stdin. Only one of them
// may claim stdin: --text - and --attach - both reading it would give the
// second an empty read and no explanation.
func resolveText(e *Env, text, textFile, _ string) (string, int) {
	switch {
	case text == "-" && textFile != "":
		return "", e.Out.Fail(ExitUsage, "usage", "text.contested",
			"--text - and --text-file both supply the text; use one")
	case text == "-":
		if claimed.takenBy != "" {
			return "", e.Out.Fail(ExitUsage, "usage", "stdin.contested",
				"stdin is already claimed by "+claimed.takenBy+"; a second reader would get nothing")
		}
		claimed.takenBy = "--text -"
		b, err := readContent(e, "-")
		if err != nil {
			return "", e.Out.Fail(ExitUsage, "usage", "text.unreadable", err.Error())
		}
		return strings.TrimRight(string(b), "\n"), 0
	case textFile == "-":
		if claimed.takenBy != "" {
			return "", e.Out.Fail(ExitUsage, "usage", "stdin.contested",
				"stdin is already claimed by "+claimed.takenBy+"; a second reader would get nothing")
		}
		claimed.takenBy = "--text-file -"
		b, err := readContent(e, "-")
		if err != nil {
			return "", e.Out.Fail(ExitUsage, "usage", "text.unreadable", err.Error())
		}
		return strings.TrimRight(string(b), "\n"), 0
	case textFile != "":
		b, err := readContent(e, textFile)
		if err != nil {
			return "", e.Out.Fail(ExitUsage, "usage", "text.unreadable", err.Error())
		}
		return strings.TrimRight(string(b), "\n"), 0
	}
	return text, 0
}

// claimStdinForAttach reserves stdin for an attachment.
func claimStdinForAttach(e *Env) int {
	if claimed.takenBy != "" {
		return e.Out.Fail(ExitUsage, "usage", "stdin.contested",
			"stdin is already claimed by "+claimed.takenBy+"; a second reader would get nothing")
	}
	claimed.takenBy = "--attach -"
	return 0
}

// send posts one built command and reports the outcome under the exit contract.
func send(e *Env, c *Client, cmd map[string]any, what string, retry func(string) string) int {
	sent, err := c.Post(cmd)
	if err != nil {
		return spoolOrFail(e, c, cmd, sent, err)
	}
	exit, outcome := statusToExit(sent.Status, sent.Body.Invariant)
	if exit != ExitOK {
		if sent.Body.Invariant == "key.revoked" || sent.Body.Invariant == "key.compromised" {
			// Same rule as post: a dead seat must not keep a queue of signed
			// bytes that lands the moment somebody re-enrols it. Every write
			// verb that spools also drops on revocation.
			if actor, ok := cmd["author"].(string); ok {
				DropSpool(actor)
			}
		}
		if sent.Body.Exit != 0 && stricter(sent.Body.Exit, exit) {
			exit = sent.Body.Exit
			outcome = "refused"
		}
		r := Result{
			Outcome: outcome, Exit: exit,
			Invariant: sent.Body.Invariant, Detail: sent.Body.Detail, Schema: sent.Body.Schema,
			RetryAfterMS: sent.Body.RetryAfterMS, Attempts: sent.Body.Attempts,
		}
		if retry != nil {
			r.Retry = retry(sent.Body.Invariant)
		}
		if sent.Body.Next != "" {
			r.Next = sent.Body.Next
		}
		return e.Out.FailWith(r)
	}
	applied := sent.Body.Applied
	if applied {
		e.Out.Note("posted %s at %d", what, sent.Body.Seq)
	} else {
		e.Out.Note("replayed %s at %d", what, sent.Body.Seq)
	}
	name := "accepted"
	if !applied {
		name = "replayed"
	}
	return e.Out.Succeed(Result{Outcome: name, Seq: sent.Body.Seq, Applied: &applied})
}

// multiFlag collects a repeatable flag in order.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func defaultTitle(path string) string {
	if path == "-" {
		return "stdin.md"
	}
	parts := strings.Split(path, "/")
	return parts[len(parts)-1]
}

// isLongForm is the nudge threshold: more than four lines, or any fenced block.
func isLongForm(s string) bool {
	if strings.Contains(s, "```") {
		return true
	}
	return strings.Count(s, "\n") >= 4
}
