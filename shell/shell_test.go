package shell

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/escherize/comms/core"
	"github.com/escherize/comms/store"
)

// newServer drives the real system through its highest seam: HTTP in, SSE out,
// a real store on a temp file. Nothing is mocked past the interface.
func newServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	return newServerEvery(t, time.Millisecond)
}

// newServerEvery builds a server whose clock advances by step on every read.
// A test about a budget has to choose its own rate of time: an agent that
// paces itself trips the posting budget and never the rate limiter, and that
// is the regime the budget exists for.
func newServerEvery(t *testing.T, step time.Duration) (*httptest.Server, *store.Store) {
	t.Helper()
	srv, st, _ := newServerFull(t, step)
	return srv, st
}

// newServerFull also hands back the Server, for tests about machinery that has
// no HTTP surface of its own — the embedder's watermark, for one.
func newServerFull(t *testing.T, step time.Duration) (*httptest.Server, *store.Store, *Server) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "shell.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.EnsureRoom("core"); err != nil {
		t.Fatal(err)
	}

	// The clock is deterministic and advances. A clock that returns one instant
	// forever makes every event in a test carry the same server_ts, which reads
	// as a tie to anything that orders by time — and a guard written against
	// that ordering then drops every write after the first, failing three tests
	// away from its cause.
	tick := time.Date(2026, 8, 6, 14, 0, 0, 0, time.UTC)
	var clockMu sync.Mutex
	sv := New(st, func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		tick = tick.Add(step)
		return tick
	})
	// Signatures are enforced by default. These tests exercise the command
	// surface and rendering, so they opt out explicitly; authentication itself
	// is covered end to end in auth_test.go against a signing server.
	sv.RequireSignature = false
	srv := httptest.NewServer(sv.Routes())
	t.Cleanup(srv.Close)
	return srv, st, sv
}

// newSigningServer keeps the production default: every command must be signed.
func newSigningServer(t *testing.T) (*httptest.Server, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "signed.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.EnsureRoom("core"); err != nil {
		t.Fatal(err)
	}
	fixed := time.Date(2026, 8, 6, 12, 30, 0, 0, time.UTC)
	srv := httptest.NewServer(New(st, func() time.Time { return fixed }).Routes())
	t.Cleanup(srv.Close)
	return srv, st
}

func post(t *testing.T, srv *httptest.Server, body string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(srv.URL+"/commands", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

func cmd(kind, text, idem string) string {
	return `{"room":"core","author":"human:bcm","kind":"` + kind +
		`","body":{"text":"` + text + `"},"idem":"` + idem + `"}`
}

func TestPostChatIsAcceptedAndReturnsSeq(t *testing.T) {
	srv, _ := newServer(t)

	code, out := post(t, srv, cmd("chat", "morning", "i1"))
	if code != http.StatusOK {
		t.Fatalf("want 200, got %d (%v)", code, out)
	}
	if seq, _ := out["seq"].(float64); seq <= 0 {
		t.Errorf("expected a positive seq, got %v", out["seq"])
	}
	if applied, _ := out["applied"].(bool); !applied {
		t.Error("first post must report applied")
	}
}

// Malformed JSON is refused at the parse boundary, before the decider sees it.
func TestMalformedCommandIsRejectedAtParse(t *testing.T) {
	srv, _ := newServer(t)

	code, out := post(t, srv, `{"room":"core",`)
	if code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", code)
	}
	if out["invariant"] != "parse.failed" {
		t.Errorf("want parse.failed, got %v", out["invariant"])
	}

	// Unknown fields are a parse failure too: an agent guessing at the shape
	// should hear about it rather than have the field silently dropped.
	code, out = post(t, srv,
		`{"room":"core","author":"human:bcm","kind":"chat","body":{"text":"x"},"idem":"i1","urgency":"high"}`)
	if code != http.StatusBadRequest {
		t.Errorf("unknown field must fail parse, got %d (%v)", code, out)
	}
}

// A rejection must carry the invariant and the schema so an agent self-corrects
// without a human.
func TestRejectionCarriesInvariantAndSchema(t *testing.T) {
	srv, _ := newServer(t)

	code, out := post(t, srv,
		`{"room":"core","author":"agent:c1","kind":"finding","body":{"text":"x"},"idem":"i1"}`)
	if code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d (%v)", code, out)
	}
	if out["invariant"] != "body.severity.invalid" {
		t.Errorf("want body.severity.invalid, got %v", out["invariant"])
	}
	if s, _ := out["schema"].(string); !strings.Contains(s, "severity") {
		t.Errorf("rejection must carry the schema, got %q", s)
	}
	if d, _ := out["detail"].(string); d == "" {
		t.Error("rejection must carry detail")
	}
}

func TestUnknownRoomIsRejected(t *testing.T) {
	srv, _ := newServer(t)
	code, out := post(t, srv,
		`{"room":"ghost","author":"human:bcm","kind":"chat","body":{"text":"x"},"idem":"i1"}`)
	if code != http.StatusUnprocessableEntity || out["invariant"] != "room.unknown" {
		t.Errorf("want 422 room.unknown, got %d %v", code, out)
	}
}

// The retry contract: same idem key twice yields one event and the original seq.
func TestIdempotentRetryReturnsSameSeqAndOneEvent(t *testing.T) {
	srv, st := newServer(t)

	_, first := post(t, srv, cmd("chat", "hello", "same"))
	code, second := post(t, srv, cmd("chat", "hello", "same"))

	if code != http.StatusOK {
		t.Fatalf("retry must succeed, got %d", code)
	}
	if first["seq"] != second["seq"] {
		t.Errorf("retry must return the original seq: %v vs %v", first["seq"], second["seq"])
	}
	if applied, _ := second["applied"].(bool); applied {
		t.Error("a replayed idempotency key must report applied=false")
	}

	recs, _ := st.Since("core", 0, 100)
	if len(recs) != 1 {
		t.Errorf("retry created a duplicate: %d events", len(recs))
	}
}

// The room page must render the ledger grammar the direction commits to.
func TestRoomPageRendersLedgerGrammar(t *testing.T) {
	srv, _ := newServer(t)
	post(t, srv, cmd("chat", "first entry", "i1"))
	post(t, srv, `{"room":"core","author":"agent:c2","kind":"question",`+
		`"body":{"text":"safe to reorder?"},"recipient":"human:bcm","idem":"i2"}`)

	resp, err := http.Get(srv.URL + "/?room=core")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	html := buf.String()

	for _, want := range []string{
		`class="folio"`,    // the ledger's left margin
		`class="kind"`,     // posting reference column
		`addressed`,        // the addressed row breaks the band
		`balance at folio`, // the running balance foot
		"first entry",
		"safe to reorder?",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("room page missing %q", want)
		}
	}
	// The direction refuses avatars and bubbles. Assert on markup, not on the
	// words — the direction contract in the page comment names them precisely
	// because it is rejecting them.
	body := html[strings.Index(html, `id="ledger-body"`):]
	for _, banned := range []string{`class="avatar`, `class="av`, `class="bubble`, `<img`} {
		if strings.Contains(body, banned) {
			t.Errorf("ledger body must not contain %q", banned)
		}
	}
}

// Consecutive ambient entries collapse; the addressed one never does. This is
// the attention model rendered.
func TestAmbientRunCollapsesAddressedDoesNot(t *testing.T) {
	srv, st := newServer(t)
	for i, txt := range []string{"a", "b", "c", "d", "e"} {
		post(t, srv, cmd("chat", txt, "amb"+string(rune('0'+i))))
	}
	seedActor(t, st, "agent:c3")
	post(t, srv, `{"room":"core","author":"agent:c2","kind":"handoff",`+
		`"body":{"text":"retry path is yours"},"recipient":"agent:c3","idem":"h1"}`)

	resp, err := http.Get(srv.URL + "/?room=core")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	html := buf.String()

	if !strings.Contains(html, "carried forward — 5 entries") {
		t.Error("a run of 5 ambient entries must collapse to a carried-forward row")
	}
	if !strings.Contains(html, "retry path is yours") {
		t.Error("an addressed entry must always render inline, never collapse")
	}
}

// SSE: a subscriber sees a post that happens after it connected.
func TestStreamDeliversNewEvents(t *testing.T) {
	srv, _ := newServer(t)

	req, _ := http.NewRequest("GET", srv.URL+"/stream?room=core", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("want text/event-stream, got %q", ct)
	}

	go func() {
		time.Sleep(40 * time.Millisecond)
		http.Post(srv.URL+"/commands", "application/json",
			strings.NewReader(cmd("chat", "live delivery", "live1")))
	}()

	if !scanFor(t, resp, "live delivery", 3*time.Second) {
		t.Error("subscriber did not receive the posted event")
	}
}

// Resume: reconnecting with Last-Event-ID replays what was missed, with no gaps
// and no duplicates. This is the everyday failure the seq contract prevents.
func TestStreamResumesFromLastEventID(t *testing.T) {
	srv, _ := newServer(t)

	_, a := post(t, srv, cmd("chat", "alpha", "r1"))
	post(t, srv, cmd("chat", "bravo", "r2"))
	post(t, srv, cmd("chat", "charlie", "r3"))

	seqA := int64(a["seq"].(float64))

	req, _ := http.NewRequest("GET", srv.URL+"/stream?room=core", nil)
	req.Header.Set("Last-Event-ID", itoa(seqA))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	got := readAvailable(t, resp, 600*time.Millisecond)
	if strings.Contains(got, "alpha") {
		t.Error("resume must not replay the event the client already had")
	}
	if !strings.Contains(got, "bravo") || !strings.Contains(got, "charlie") {
		t.Errorf("resume must replay everything after Last-Event-ID; got:\n%s", got)
	}
}

// Every SSE frame carries id: <seq>, which is what makes resume possible.
func TestStreamFramesCarrySeqAsEventID(t *testing.T) {
	srv, _ := newServer(t)
	_, out := post(t, srv, cmd("chat", "tagged", "t1"))
	seq := itoa(int64(out["seq"].(float64)))

	req, _ := http.NewRequest("GET", srv.URL+"/stream?room=core", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	got := readAvailable(t, resp, 600*time.Millisecond)
	if !strings.Contains(got, "id: "+seq) {
		t.Errorf("frame must carry id: %s; got:\n%s", seq, got)
	}
	if !strings.Contains(got, "event: datastar-patch-elements") {
		t.Error("frame must use the datastar patch protocol")
	}
}

// Search is read-your-writes: an event is findable the moment it is posted.
func TestSearchFindsEventImmediately(t *testing.T) {
	srv, _ := newServer(t)
	post(t, srv, cmd("til", "sqlite-vec rejects long bodies", "s1"))

	resp, err := http.Get(srv.URL + "/search?q=sqlite-vec")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	html := buf.String()

	if !strings.Contains(html, "sqlite-vec rejects long bodies") {
		t.Error("posted event must be searchable immediately")
	}
	if !strings.Contains(html, `class="rank"`) {
		t.Error("search must show rank columns per the spec")
	}
}

func TestThemeTokensAreTheOnlyColourSource(t *testing.T) {
	srv, _ := newServer(t)
	resp, err := http.Get(srv.URL + "/?room=core")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	css := buf.String()

	// Every theme the app ships must define the same token set.
	for _, theme := range []string{`:root {`, `:root[data-theme="light"]`, `:root[data-theme="slate"]`} {
		if !strings.Contains(css, theme) {
			t.Errorf("missing theme block %q", theme)
		}
	}
	// Components reference tokens, never literals: no hex outside the token blocks.
	body := css[strings.Index(css, "* { box-sizing"):]
	if i := strings.Index(body, "#"); i != -1 && !strings.Contains(body[:i], "id=") {
		for _, line := range strings.Split(body, "\n") {
			if strings.Contains(line, ":") && strings.Contains(line, "#") &&
				!strings.Contains(line, "id=") && !strings.Contains(line, "href") &&
				!strings.Contains(line, "ledger-body") && !strings.Contains(line, "composer") {
				t.Errorf("colour literal outside token block: %q", strings.TrimSpace(line))
			}
		}
	}
}

// ---- helpers ----

func scanFor(t *testing.T, resp *http.Response, want string, within time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(within)
	sc := bufio.NewScanner(resp.Body)
	for time.Now().Before(deadline) && sc.Scan() {
		if strings.Contains(sc.Text(), want) {
			return true
		}
	}
	return false
}

// readAvailable collects whatever the stream has produced within a window. An
// SSE body never EOFs, so reading to completion would block forever; the shared
// buffer is what lets the timeout still return real content.
func readAvailable(t *testing.T, resp *http.Response, within time.Duration) string {
	t.Helper()
	var (
		mu sync.Mutex
		sb strings.Builder
	)
	go func() {
		buf := make([]byte, 2048)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				mu.Lock()
				sb.Write(buf[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()
	time.Sleep(within)
	mu.Lock()
	defer mu.Unlock()
	return sb.String()
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// The browser gets `answer` from the same core rule the CLI uses: it sends refs
// and no recipient, so neither client carries its own inference.
func TestComposerAnswerCarriesRefsAndNoRecipient(t *testing.T) {
	srv, _ := newServer(t)
	resp, err := http.Get(srv.URL + "/?room=core")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	page := buf.String()

	if !strings.Contains(page, "answer: function(rest)") {
		t.Fatal("the composer needs an /answer verb")
	}
	// The verb block, from its name to the next verb.
	block := page[strings.Index(page, "answer: function(rest)"):]
	block = block[:strings.Index(block, "handoff:")]
	if !strings.Contains(block, "refs:") {
		t.Error("/answer must send refs so the core can find the question")
	}
	var code string
	for _, line := range strings.Split(block, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "//") {
			code += line + "\n"
		}
	}
	if strings.Contains(code, "recipient") {
		t.Error("the composer must not infer a recipient; the core derives it from the question")
	}
	if !strings.Contains(page, "cmdObj.refs=refs") {
		t.Error("parsed refs must reach the command, not stay in the body")
	}
}

// The placeholder is the only place a slash verb is discoverable, so a verb
// missing from it is a verb nobody finds.
func TestComposerPlaceholderListsEverySlashVerb(t *testing.T) {
	srv, _ := newServer(t)
	resp, err := http.Get(srv.URL + "/?room=core")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	page := buf.String()

	slash := page[strings.Index(page, "var SLASH={"):]
	slash = slash[:strings.Index(slash, "function fail(")]
	i := strings.Index(page, `placeholder="entry`)
	placeholder := page[i : i+strings.Index(page[i:], `"`)+120]

	for _, verb := range []string{"finding", "til", "status", "ask", "answer", "handoff", "pr"} {
		if !strings.Contains(slash, verb+": function(rest)") {
			t.Errorf("SLASH is missing the %q verb", verb)
		}
		if !strings.Contains(placeholder, "/"+verb) {
			t.Errorf("the composer placeholder does not offer /%s", verb)
		}
	}
}

// seedActor makes a seat addressable the way the hub does: by having it post.
// recipient.unknown is checked against the roster, and the roster is what has
// been seen.
func seedActor(t *testing.T, st *store.Store, actor string) {
	t.Helper()
	// Into a side room, so making a seat addressable does not add a row to the
	// ledger under test.
	if err := st.EnsureRoom("roster"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(core.Event{Room: "roster", Author: core.Actor(actor),
		Kind: core.KindChat, Body: map[string]any{"text": "here"},
		Lane: core.LaneOf(core.KindChat)}, "seed-"+actor, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func readFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	return string(b), err
}

// postTo sends a signed-or-not body to a route other than /commands.
func postTo(t *testing.T, srv *httptest.Server, path, body string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(srv.URL+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// The founder flow rides in the room page: a bootstrap (*) setup token gets a
// claim card that enrols on the spot, then offers rooms/invite/just-post. The
// card enrols through the composer's own keyFor, so there is one enrol rule.
func TestRoomPageCarriesClaimFlow(t *testing.T) {
	for _, want := range []string{
		"claimCard(token)",     // the bootstrap branch renders the card
		"claim this hub",       // the explicit claim action
		"window.commsKeyFor",   // claim enrols via the composer's path
		"window.commsSettings", // the rooms/invite doors open real panels
		"just post",            // skipping is always offered
	} {
		if !strings.Contains(roomHTML, want) {
			t.Errorf("room page missing %q", want)
		}
	}
}

// Creating a room pushes a fresh nav to every open page, so viewers see the
// room appear without a reload. The patch replaces the header nav wholesale
// and is rebuilt per subscriber under its own reader's membership.
func TestRoomCreationPushesNavPatch(t *testing.T) {
	srv, _ := newServer(t)

	resp, err := http.Get(srv.URL + "/stream?room=core")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	time.Sleep(100 * time.Millisecond) // let the stream register its nav refresher

	r2, err := http.Post(srv.URL+"/rooms", "application/json",
		strings.NewReader(`{"name":"ops"}`))
	if err != nil {
		t.Fatal(err)
	}
	r2.Body.Close()

	if !scanFor(t, resp, `?room=ops`, 3*time.Second) {
		t.Fatal("stream never delivered a nav patch naming the new room")
	}
}

// The rescue paths a stuck newcomer reaches must exist across script
// boundaries: the composer's no-seat guard calls onboardScript's helpers via
// their window exports (a bare askName there was an uncaught ReferenceError),
// and the unlock page lets a pasted token beat a stale cached key.
func TestRescuePathsAreWired(t *testing.T) {
	for _, want := range []string{
		"window.commsAskName", // exported…
		"window.commsSetActor",
		"window.commsNote",
		"window.commsAskName(function(name)", // …and used by the composer guard
	} {
		if !strings.Contains(roomHTML, want) {
			t.Errorf("room page missing %q", want)
		}
	}
	if !strings.Contains(unlockPage, "token ? enrolThen") {
		t.Error("unlock page must let a pasted token win over a cached key")
	}
	if !strings.Contains(unlockPage, `aria-label="enrolment token"`) {
		t.Error("unlock token input must be labelled")
	}
}

// The brief's surface: identity chip (derived, no header picker), room rail
// with unread marks, human time column, tall composer with markdown attach.
func TestRoomPageCarriesTheRefinedSurface(t *testing.T) {
	for _, want := range []string{
		`id="me"`,                  // identity chip…
		`aria-label="acting seat"`, // …picker demoted to settings
		`class="rail"`,             // room rail
		`data-head=`,               // rail carries heads for unread marks
		`<div>when</div>`,          // human clock column
		`<textarea id="ctext"`,     // tall composer
		`id="cfile"`,               // markdown attach
		"cmdObj.attachments",       // attachments ride the signed command
		"comms.seen.",              // unread bookkeeping
	} {
		if !strings.Contains(roomHTML, want) {
			t.Errorf("room page missing %q", want)
		}
	}
	if strings.Contains(roomHTML, `<nav>{{NAV}}</nav>`) {
		t.Error("the header room strip should be gone; rooms live in the rail")
	}
}
