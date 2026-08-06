package shell

import (
	"fmt"
	"html"
	"net/http"
	"strings"

	"github.com/bcm/agent_comms/core"
	"github.com/bcm/agent_comms/store"
)

// kindCode is the ledger's posting reference: a short fixed-width code, the way
// a journal abbreviates an account.
func kindCode(k core.Kind) string {
	switch k {
	case core.KindChat:
		return "CHT"
	case core.KindFinding:
		return "FND"
	case core.KindQuestion:
		return "Q"
	case core.KindAnswer:
		return "ANS"
	case core.KindTIL:
		return "TIL"
	case core.KindHandoff:
		return "HND"
	case core.KindStatus:
		return "STA"
	case core.KindPRLink:
		return "PR"
	case core.KindDigest:
		return "DIG"
	case core.KindRedact:
		return "RDC"
	}
	return "—"
}

// renderRow is one ledger entry. Addressed rows break the band with an accent
// gutter rule and drop the tick column; redactions render struck through with
// their hash still attested.
func renderRow(r store.Record) string {
	classes := []string{"row"}
	if r.Lane == core.Addressed {
		classes = append(classes, "addressed")
	}
	if r.BodyErased || r.Kind == core.KindRedact {
		classes = append(classes, "struck")
	}

	var body strings.Builder
	if r.BodyErased {
		body.WriteString(`<span class="erased">body erased · hash attested ` +
			html.EscapeString(short(r.BodyHash)) + `</span>`)
	} else {
		if sev := r.Severity(); sev != "" {
			body.WriteString(`<span class="sev sev-` + html.EscapeString(sev) + `">` +
				html.EscapeString(strings.ToUpper(sev)) + `</span> `)
		}
		if r.Recipient != "" {
			body.WriteString(`<span class="to">` + html.EscapeString(string(r.Recipient)) + `</span> `)
		}
		if txt := r.Text(); txt != "" {
			body.WriteString(html.EscapeString(txt))
		} else if u := r.URL(); u != "" {
			// A pr.link carries a url, not text. Rendering r.Text() alone left
			// the entry column blank.
			body.WriteString(`<a href="` + html.EscapeString(u) + `">` +
				html.EscapeString(u) + `</a>`)
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
		html.EscapeString(shortActor(r.Author)), kindCode(r.Kind), body.String(), tick)
}

func short(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// shortActor drops the agent: prefix — the column is narrow and the prefix is
// the same on every agent row, so it carries no information there.
func shortActor(a core.Actor) string {
	return strings.TrimPrefix(string(a), "agent:")
}

func (s *Server) roomPage(w http.ResponseWriter, r *http.Request) {
	room := r.URL.Query().Get("room")
	if room == "" {
		room = "core"
	}
	if !s.st.RoomExists(room) {
		http.NotFound(w, r)
		return
	}

	recs, err := s.st.Since(room, 0, 500)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rooms, _ := s.st.Rooms()

	var rows strings.Builder
	// Consecutive ambient rows collapse into a carried-forward line — the
	// ledger's own page-break convention doing the attention work.
	run := 0
	flush := func() {
		if run >= carryThreshold {
			rows.WriteString(fmt.Sprintf(
				`<div class="carried" tabindex="0" role="button" aria-expanded="false">`+
					`<div class="folio">·</div>`+
					`<div class="cf">carried forward — %d entries</div></div>`, run))
		}
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
			flush()
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
		flush()
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
	).Replace(roomHTML)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, page)
}

// carryThreshold is how many consecutive ambient entries collapse. Below it the
// rows are cheap enough to show; above it they are a flood.
const carryThreshold = 3

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
	q := r.URL.Query().Get("q")
	var rows strings.Builder
	var n int
	if q != "" {
		hits, err := s.st.Search(q, r.URL.Query().Get("room"),
			r.URL.Query().Get("kind"), r.URL.Query().Get("author"), 100)
		if err != nil {
			rows.WriteString(`<div class="row"><div class="folio">!</div>` +
				`<div class="author">—</div><div class="kind">ERR</div>` +
				`<div class="body">` + html.EscapeString(err.Error()) + `</div></div>`)
		}
		for i, hit := range hits {
			rows.WriteString(searchRow(i+1, hit))
		}
		n = len(hits)
	}

	page := strings.NewReplacer(
		"{{Q}}", html.EscapeString(q),
		"{{ROWS}}", rows.String(),
		"{{N}}", fmt.Sprint(n),
	).Replace(searchHTML)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, page)
}

// searchRow shows the lexical rank as its own column. The vector rank column
// ships with M2; the grammar already has a slot for it.
func searchRow(rank int, r store.Record) string {
	return fmt.Sprintf(
		`<div class="row srow">`+
			`<div class="folio">%d</div>`+
			`<div class="rank">%d</div>`+
			`<div class="rank vec">—</div>`+
			`<div class="author">%s</div>`+
			`<div class="kind">%s</div>`+
			`<div class="body"><a href="/?room=%s#%d">%s</a></div></div>`,
		r.Seq, rank, html.EscapeString(shortActor(r.Author)), kindCode(r.Kind),
		html.EscapeString(r.Room), r.Seq, html.EscapeString(r.Text()))
}
