package shell

import (
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/escherize/comms/core"
	"github.com/escherize/comms/store"
)

// kindCode is the ledger's posting reference: a short fixed-width code, the way
// a journal abbreviates an account.
func kindCode(k core.Kind) string {
	switch k {
	case core.KindChat:
		return "💬"
	case core.KindFinding:
		return "🔍"
	case core.KindQuestion:
		return "❓"
	case core.KindAnswer:
		return "💡"
	case core.KindTIL:
		return "🎓"
	case core.KindHandoff:
		return "🤝"
	case core.KindStatus:
		return "🛠️"
	case core.KindPRLink:
		return "🔗"
	case core.KindDigest:
		return "📰"
	case core.KindRedact:
		return "✂️"
	}
	return "·"
}

// kindGlyph wraps the symbol so hover answers what it means and a screen
// reader says the kind, not the codepoint. data-tip renders as an instant CSS
// tooltip — the native title needs a second and a half of held hover, which
// reads as broken.
func kindGlyph(k core.Kind) string {
	name := html.EscapeString(string(k))
	return `<span role="img" aria-label="` + name + `" data-tip="` + name + `">` +
		kindCode(k) + `</span>`
}

// agentChip marks agent-authored rows with a drawn glyph rather than a word:
// the namespace was already stripped from the name for width, and provenance
// is exactly the thing that must survive the stripping.
const agentChip = `<svg class="chip" viewBox="0 0 12 12" aria-hidden="true">` +
	`<rect x="2.5" y="2.5" width="7" height="7" rx="1.2" fill="none" stroke="currentColor"/>` +
	`<rect x="5" y="5" width="2" height="2" fill="currentColor"/>` +
	`<path d="M4.5 0v2M7.5 0v2M4.5 10v2M7.5 10v2M0 4.5h2M0 7.5h2M10 4.5h2M10 7.5h2" stroke="currentColor"/></svg>`

// authorCell is the seat, ellipsized to the column, with the full actor
// string — namespace included — in the tooltip the column has no room for.
func authorCell(a core.Actor) string {
	chip := ""
	if a.IsAgent() {
		chip = agentChip
	}
	full := html.EscapeString(string(a))
	return `<span aria-label="` + full + `" data-tip="` + full + `">` + chip +
		html.EscapeString(shortActor(a)) + `</span>`
}

// renderRow is one ledger entry. Addressed rows break the band with an accent
// gutter rule and drop the tick column; redactions render struck through with
// their hash still attested.
func renderRow(r store.Record) string {
	classes := []string{"row"}
	if r.Lane == core.Addressed {
		classes = append(classes, "addressed")
	}
	if r.BodyErased || r.Redacted || r.Kind == core.KindRedact {
		classes = append(classes, "struck")
	}

	var body strings.Builder
	switch {
	case r.BodyErased:
		body.WriteString(`<span class="erased">body erased · hash attested ` +
			html.EscapeString(short(r.BodyHash)) + `</span>`)
	case r.Redacted:
		body.WriteString(`<span class="erased">redacted by ` +
			html.EscapeString(shortActor(core.Actor(r.RedactedBy))) +
			` · hash attested ` + html.EscapeString(short(r.BodyHash)) + `</span>`)
	default:
		if about := r.About(); about != "" {
			body.WriteString(`<span class="about">` + html.EscapeString(about) + `</span> `)
		}
		if sev := r.Severity(); sev != "" {
			body.WriteString(`<span class="sev sev-` + html.EscapeString(sev) + `">` +
				html.EscapeString(strings.ToUpper(sev)) + `</span> `)
		}
		if r.Recipient != "" {
			body.WriteString(`<span class="to">` + html.EscapeString(string(r.Recipient)) + `</span> `)
		}
		if r.Kind == core.KindStatus {
			if step, of := r.Step(), r.Of(); of > 0 {
				body.WriteString(fmt.Sprintf(`<span class="step">%d/%d</span> `, step, of))
			}
		}
		if txt := r.Text(); txt != "" {
			body.WriteString(renderEntryText(txt, r.Seq))
		} else if u := r.URL(); u != "" {
			// A pr.link carries a url, not text. Rendering r.Text() alone left
			// the entry column blank.
			body.WriteString(`<a href="` + html.EscapeString(u) + `">` +
				html.EscapeString(u) + `</a>`)
		}
		// Attachments render as titles, never as content: a 100KB report must
		// not become a 100KB row. Inside the default branch, so a redacted row
		// links to nothing — the blob is gone and a link to it would 404 while
		// still naming what was attached.
		for _, a := range r.Attach {
			body.WriteString(fmt.Sprintf(
				` <a class="att" href="/a/%s">▤ %s</a>`,
				html.EscapeString(a.Hash), html.EscapeString(a.Title)))
		}
	}

	tick := `<div class="tick">✓</div>`
	if r.Lane == core.Addressed {
		tick = ""
	}

	return fmt.Sprintf(
		`<div class="%s" data-seq="%d">`+
			`<div class="folio">%d</div>`+
			`<div class="author">%s</div>`+
			`<div class="kind">%s</div>`+
			`<div class="body">%s</div>`+
			`%s</div>`,
		strings.Join(classes, " "), r.Seq, r.Seq,
		authorCell(r.Author), kindGlyph(r.Kind), body.String(), tick)
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// shortActor drops the agent: prefix — the column is narrow and the prefix is
// the same on every agent row, so it carries no information there.
// shortActor drops the namespace for display. The namespace decides how a post
// is read — provenance, lane budgets — but the column is 8 characters wide and
// every row would spend six of them on the same two words.
func shortActor(a core.Actor) string {
	s := strings.TrimPrefix(string(a), "agent:")
	return strings.TrimPrefix(s, "human:")
}

func (s *Server) roomPage(w http.ResponseWriter, r *http.Request) {
	room := r.URL.Query().Get("room")
	if room == "" {
		room = "core"
	}
	// A non-member and a nonexistent room both 404: content and existence hidden
	// alike, so a seat cannot probe for a room it was scoped away from.
	if !s.canRead(reader(r), room) || !s.st.RoomExists(room) {
		http.NotFound(w, r)
		return
	}

	recs, err := s.st.Latest(room, roomPageRows)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// The nav lists only the reader's own rooms, so it never advertises a room
	// the seat cannot open.
	allRooms, _ := s.st.Rooms()
	rooms := s.visibleRooms(reader(r), allRooms)

	var rows strings.Builder
	// Consecutive ambient rows collapse into a carried-forward line — the
	// ledger's own page-break convention doing the attention work.
	run := 0
	group := 0
	// The collapsed rows are rendered hidden rather than dropped, so expanding
	// is a class toggle and not a fetch. The control is a real button with
	// aria-expanded wired to its state.
	flush := func(hidden []store.Record) {
		if run < carryThreshold {
			return
		}
		group++
		id := fmt.Sprintf("cf%d", group)
		rows.WriteString(fmt.Sprintf(
			`<button class="carried" type="button" aria-expanded="false" aria-controls="%s">`+
				`<span class="folio">·</span>`+
				`<span class="cf">carried forward — %d entries</span></button>`+
				`<div class="carried-body" id="%s" hidden>`, id, run, id))
		for _, h := range hidden {
			rows.WriteString(renderRow(h))
		}
		rows.WriteString(`</div>`)
		run = 0
	}
	var pending []store.Record
	for _, rec := range recs {
		if rec.Lane == core.Ambient {
			pending = append(pending, rec)
			run++
			continue
		}
		if run >= carryThreshold {
			flush(pending)
			pending = nil
		} else {
			for _, p := range pending {
				rows.WriteString(renderRow(p))
			}
			pending, run = nil, 0
		}
		rows.WriteString(renderRow(rec))
	}
	if run >= carryThreshold {
		flush(pending)
	} else {
		for _, p := range pending {
			rows.WriteString(renderRow(p))
		}
	}

	ambient, addressed, head := tally(recs)

	var nav strings.Builder
	for _, rm := range rooms {
		sel := ""
		if rm == room {
			sel = ` class="sel"`
		}
		nav.WriteString(fmt.Sprintf(`<a href="/?room=%s"%s>%s</a>`,
			html.EscapeString(rm), sel, html.EscapeString(rm)))
	}

	page := strings.NewReplacer(
		"{{ROOM}}", html.EscapeString(room),
		"{{NAV}}", nav.String(),
		"{{ROWS}}", rows.String(),
		"{{AMBIENT}}", fmt.Sprint(ambient),
		"{{ADDRESSED}}", fmt.Sprint(addressed),
		"{{HEAD}}", fmt.Sprint(head),
		"{{PROGRESS}}", s.renderProgress(room),
		"{{SIGNING}}", fmt.Sprint(s.RequireSignature),
	).Replace(roomHTML)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, page)
}

// carryThreshold is how many consecutive ambient entries collapse. Below it the
// rows are cheap enough to show; above it they are a flood.
const carryThreshold = 3

// stallWindow is how long an actor may go quiet before the room says so.
// Evaluated against the server clock, never a client timestamp.
const stallWindow = store.StallWindow

// roomPageRows is how much of the tail the page renders. The tail, not the
// head: a reader opening the room wants what just happened.
const roomPageRows = 500

// renderProgress folds each working actor's latest status into one live line
// for the balance foot, so a human can see where the agents are without
// expanding anything. Progress is a projection: the room shows current state,
// not the status events that produced it. It belongs to the room, not to any
// one run of collapsed rows, so it renders once.
func (s *Server) renderProgress(room string) string {
	ps, err := s.st.ProgressFor(room)
	if err != nil || len(ps) == 0 {
		return ""
	}
	now := s.now()
	var parts []string
	for _, p := range ps {
		label := shortActor(core.Actor(p.Author))
		switch {
		case p.Stalled(now, stallWindow):
			parts = append(parts, fmt.Sprintf(
				`<span class="stall">%s stalled %s</span>`,
				html.EscapeString(label), since(now, p.Updated)))
		case p.Of > 0:
			parts = append(parts, fmt.Sprintf(`%s step %d/%d (%s)`,
				html.EscapeString(label), p.Step, p.Of, since(now, p.Updated)))
		default:
			parts = append(parts, fmt.Sprintf(`%s %s (%s)`,
				html.EscapeString(label), html.EscapeString(truncate(p.Note, 40)),
				since(now, p.Updated)))
		}
	}
	return `<span>working <b>` + strings.Join(parts, "</b></span> <span><b>") + `</b></span>`
}

func since(now, then time.Time) string {
	d := now.Sub(then)
	if d < time.Minute {
		return "just now"
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	return fmt.Sprintf("%dh", int(d.Hours()))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func tally(recs []store.Record) (ambient, addressed int, head int64) {
	for _, r := range recs {
		if r.Lane == core.Addressed {
			addressed++
		} else {
			ambient++
		}
		if r.Seq > head {
			head = r.Seq
		}
	}
	return
}

func (s *Server) searchPage(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))

	// An empty query is a rejection, not an empty result. Filters alone match
	// nothing, and returning zero hits would read as "the room does not know
	// this" when nothing was actually asked.
	if q == "" && wantsJSON(r) {
		writeJSONL(w, http.StatusUnprocessableEntity, nil, map[string]any{
			"ok": false, "outcome": "rejected", "exit": 3,
			"invariant": "query.required",
			"detail":    "search needs a query; filters alone match nothing",
			"next":      "add words to search for, then filter with room=, kind=, author=, since=",
		})
		return
	}

	if wantsJSON(r) {
		fused, lanes, err := s.searchBoth(r.Context(), q, r.URL.Query().Get("room"),
			r.URL.Query().Get("kind"), r.URL.Query().Get("author"),
			r.URL.Query().Get("since"), s.readerRooms(reader(r)), 100)
		if err != nil {
			writeJSONL(w, http.StatusInternalServerError, nil, map[string]any{
				"ok": false, "outcome": "internal", "exit": 1,
				"invariant": "search.failed", "detail": err.Error(),
			})
			return
		}
		hits := make([]store.Record, 0, len(fused))
		ranks := map[int64]map[string]any{}
		for _, f := range fused {
			hits = append(hits, f.Rec)
			// Both ranks, per hit. A result that ranked 1st lexically and 40th
			// semantically is a different fact than one that ranked 20th in
			// each, and a single fused number throws that away.
			ranks[f.Rec.Seq] = map[string]any{
				"lexical": f.LexRank, "vector": f.VecRank,
				"similarity": f.VecScore, "fused": f.Score,
			}
		}
		watermark, at, stale := s.lagFor()
		dead, _ := s.st.DeadLettered()
		writeJSONL(w, http.StatusOK, hits, map[string]any{
			"ok": true, "outcome": "read", "count": len(hits),
			"lanes": lanes, "query": q, "ranks": ranks,
			"vector_index": map[string]any{
				"embedded_through_seq": watermark,
				"current_to":           at.UTC().Format(time.RFC3339),
				"stale":                stale,
				"dead_lettered":        len(dead),
			},
		})
		return
	}

	var rows strings.Builder
	var n int
	var highest int64
	var lanes []store.LaneStatus
	if q != "" {
		fused, laneStatus, err := s.searchBoth(r.Context(), q, r.URL.Query().Get("room"),
			r.URL.Query().Get("kind"), r.URL.Query().Get("author"),
			r.URL.Query().Get("since"), s.readerRooms(reader(r)), 100)
		lanes = laneStatus
		if err != nil {
			rows.WriteString(`<div class="row"><div class="folio">!</div>` +
				`<div class="author">—</div><div class="kind">ERR</div>` +
				`<div class="body">` + html.EscapeString(err.Error()) + `</div></div>`)
		}
		for _, f := range fused {
			rows.WriteString(fusedRow(f))
			if f.Rec.Seq > highest {
				highest = f.Rec.Seq
			}
		}
		n = len(fused)
	}

	// The live stream resumes after the highest hit already rendered, so nothing
	// on the page arrives again. Zero when nothing matched, because then
	// everything is new from here.
	head := highest
	room := r.URL.Query().Get("room")
	if room == "" {
		room = "core"
	}
	page := strings.NewReplacer(
		"{{Q}}", html.EscapeString(q),
		"{{ROWS}}", rows.String(),
		"{{N}}", fmt.Sprint(n),
		"{{ROOM}}", html.EscapeString(room),
		"{{HEAD}}", fmt.Sprint(head),
		"{{LANES}}", laneFoot(lanes),
	).Replace(searchHTML)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, page)
}

// searchRow shows the lexical rank as its own column — the bm25 score, not the
// row's position, so a reader can see how much better the first hit is than the
// second. The vector rank column ships with ticket 07; the grammar has its slot.
func searchRow(r store.Record) string {
	return fmt.Sprintf(
		`<div class="row srow">`+
			`<div class="folio">%d</div>`+
			`<div class="rank">%.1f</div>`+
			`<div class="rank vec">—</div>`+
			`<div class="author">%s</div>`+
			`<div class="kind">%s</div>`+
			`<div class="body"><a href="/?room=%s#%d">%s</a></div></div>`,
		r.Seq, r.Rank, authorCell(r.Author), kindGlyph(r.Kind),
		html.EscapeString(r.Room), r.Seq, html.EscapeString(r.Text()))
}

// entryLineCeiling is how much of a body a row shows before it folds. A ledger
// is a thing you scan by folio, and a row with no ceiling destroys that: a
// fourteen-line stack trace is 850px of a 981px viewport, and a fifty-line one
// is a page with a single entry on it. The overflow is rendered, not dropped —
// folding is the ledger's own page-break convention, the same one the
// carried-forward line uses, so it reuses that control verbatim.
const entryLineCeiling = 12

// renderEntryText writes a body, folding everything past the ceiling behind the
// same button the collapsed ambient run uses.
func renderEntryText(txt string, seq int64) string {
	lines := strings.Split(txt, "\n")
	if len(lines) <= entryLineCeiling {
		return html.EscapeString(txt)
	}

	id := fmt.Sprintf("more%d", seq)
	shown := strings.Join(lines[:entryLineCeiling], "\n")
	rest := strings.Join(lines[entryLineCeiling:], "\n")

	return html.EscapeString(shown) +
		`<button class="carried more" type="button" aria-expanded="false" aria-controls="` +
		id + `"><span class="cf">` +
		fmt.Sprintf("%d more line(s)", len(lines)-entryLineCeiling) +
		`</span></button><span class="more-body" id="` + id + `" hidden>` +
		html.EscapeString("\n"+rest) + `</span>`
}

// fusedRow renders one hit with both ranks. An em dash in a rank column means
// that lane did not return this event, which is information: it is how a reader
// sees that a hit is lexical-only, or that the semantic lane found something
// the words did not.
func fusedRow(f Fused) string {
	lex, vec := "—", "—"
	if f.LexRank > 0 {
		lex = fmt.Sprintf("%d", f.LexRank)
	}
	if f.VecRank > 0 {
		vec = fmt.Sprintf("%d", f.VecRank)
	}
	r := f.Rec
	return fmt.Sprintf(
		`<div class="row srow">`+
			`<div class="folio">%d</div>`+
			`<div class="rank">%s</div>`+
			`<div class="rank vec">%s</div>`+
			`<div class="author">%s</div>`+
			`<div class="kind">%s</div>`+
			`<div class="body"><a href="/?room=%s#%d">%s</a></div></div>`,
		r.Seq, lex, vec,
		authorCell(r.Author), kindGlyph(r.Kind),
		html.EscapeString(r.Room), r.Seq,
		html.EscapeString(truncate(r.Text(), 160)))
}

// laneFoot states what each lane actually did. A lexical-only result over an
// absent or stale semantic lane is a true result an agent draws a false
// conclusion from, so the page says which it is rather than implying both ran.
func laneFoot(lanes []store.LaneStatus) string {
	if len(lanes) == 0 {
		return ""
	}
	var b strings.Builder
	for _, l := range lanes {
		b.WriteString(`<span>` + html.EscapeString(l.Name) + ` <b>` +
			html.EscapeString(l.State) + `</b>`)
		if l.Detail != "" {
			b.WriteString(` — ` + html.EscapeString(l.Detail))
		}
		b.WriteString(`</span>`)
	}
	return b.String()
}
