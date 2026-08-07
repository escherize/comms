// Package core is the functional core: pure decision logic with no clock, no
// IO, and no network. Everything here is a total function over values.
//
// The shell parses untrusted input into these types exactly once (parse, don't
// validate), authenticates it, and appends whatever Decide returns. Every
// domain refusal lives here; nothing that could refuse a well-formed command
// for a domain reason lives outside.
package core

// Kind identifies an event type. Past tense throughout: an event is a fact that
// already happened and can never be invalid.
type Kind string

const (
	KindChat     Kind = "chat"
	KindFinding  Kind = "finding"
	KindQuestion Kind = "question"
	KindAnswer   Kind = "answer"
	KindTIL      Kind = "til"
	KindHandoff  Kind = "handoff"
	KindStatus   Kind = "status"
	KindPRLink   Kind = "pr.link"
	KindDigest   Kind = "digest"
	KindRedact   Kind = "redact"
)

// Lane is how an event competes for human attention. It is a static property of
// the Kind: nothing an author writes inside an event changes its lane. Severity
// is an author-set claim, so routing by it would hand the addressed lane to
// whichever agent claims p0 most often.
type Lane int

const (
	Ambient Lane = iota
	Addressed
)

// LaneOf is the whole attention classification. Kind decides; author opinion
// never does. Escalation (priced separately, in the shell) is the only crossing.
func LaneOf(k Kind) Lane {
	switch k {
	case KindQuestion, KindAnswer, KindHandoff, KindDigest:
		return Addressed
	default:
		return Ambient
	}
}

// Actor identifies a human or an agent. Agents are actors in exactly the same
// sense humans are.
type Actor string

// IsAgent reports whether the actor is an agent. Agent-authored commands face
// gates human-authored ones do not.
func (a Actor) IsAgent() bool {
	return len(a) > 6 && a[:6] == "agent:"
}

// Attachment references stored content by hash. Artifacts are content
// addressed, so an event carries a pointer, never a payload — a 100KB report
// does not become a 100KB row.
type Attachment struct {
	Hash  string
	Title string
}

// Command is a request that may be refused. Commands never appear in the log.
type Command struct {
	Room        string
	Author      Actor
	Kind        Kind
	Body        map[string]any
	Refs        []string
	Idem        string
	Recipient   Actor // required when LaneOf(Kind) == Addressed
	Attachments []Attachment
}

// Event is an accepted fact awaiting a seq from the shell. The shell assigns
// seq and server_ts; the core never invents either, because it has no clock and
// no counter.
type Event struct {
	Room        string
	Author      Actor
	Kind        Kind
	Body        map[string]any
	Refs        []string
	Recipient   Actor
	Lane        Lane
	Attachments []Attachment
}

// Rejection names the invariant that failed. An agent self-corrects against
// Invariant; a human reads Detail.
type Rejection struct {
	Invariant string
	Detail    string
}

func (r Rejection) Error() string { return r.Invariant + ": " + r.Detail }

// State is the decision-projection view the decider reads. It is always current
// — updated in the same transaction as the append — because a lagging view
// would let two claims both observe an open task and both be accepted.
type State struct {
	// KnownKinds gates unknown kinds without a registry lookup in the shell.
	RoomExists func(room string) bool
	// EventKind returns the kind of a prior event by its ref, and whether it exists.
	EventKind func(ref string) (Kind, bool)
	// ArtifactExists reports whether content is stored under this hash.
	ArtifactExists func(hash string) bool
	// EventAuthor returns who authored a prior event, and whether it exists.
	EventAuthor func(ref string) (Actor, bool)
	// EventRoom returns the room a prior event was posted in.
	EventRoom func(ref string) (string, bool)
	// IsRedacted reports whether an event is already suppressed.
	IsRedacted func(ref string) bool
}

// Decide is the whole domain. state × command → events | rejection.
//
// It is total, deterministic, and clockless: the same inputs always produce the
// same outputs, which is what makes the table-driven tests below exhaustive
// rather than illustrative.
func Decide(s State, c Command) ([]Event, *Rejection) {
	if c.Room == "" {
		return nil, &Rejection{"room.required", "every command names a room"}
	}
	if s.RoomExists != nil && !s.RoomExists(c.Room) {
		return nil, &Rejection{"room.unknown", "no such room: " + c.Room}
	}
	if c.Author == "" {
		return nil, &Rejection{"author.required", "every command names an author"}
	}
	if c.Idem == "" {
		return nil, &Rejection{"idem.required", "every command carries an idempotency key"}
	}
	if !knownKind(c.Kind) {
		return nil, &Rejection{"kind.unknown", "unknown kind: " + string(c.Kind)}
	}

	if r := checkBody(c); r != nil {
		return nil, r
	}

	// An answer must point at a question, checked before the recipient rules:
	// an answer with a bad ref reported as "name a recipient" sends the agent
	// to fix the wrong thing.
	if c.Kind == KindAnswer {
		if r := checkAnswersAQuestion(s, c); r != nil {
			return nil, r
		}
		// The recipient is the question's author. A domain rule, so it lives
		// here and every client gets it — the browser composer and the agent
		// CLI both, rather than one of them reimplementing it.
		if c.Recipient == "" {
			if to, ok := answerRecipient(s, c); ok {
				c.Recipient = to
			}
		}
	}

	// Addressed events must name a recipient. An addressed event nobody is
	// addressed to would render inline and interrupt everyone, which is the
	// flood the lane split exists to prevent.
	lane := LaneOf(c.Kind)
	if lane == Addressed && c.Recipient == "" {
		return nil, &Rejection{"recipient.required",
			"kind " + string(c.Kind) + " is addressed and must name a recipient"}
	}
	if lane == Ambient && c.Recipient != "" {
		return nil, &Rejection{"recipient.forbidden",
			"kind " + string(c.Kind) + " is ambient; it cannot name a recipient"}
	}

	if c.Kind == KindRedact {
		if r := checkRedaction(s, c); r != nil {
			return nil, r
		}
	}

	if r := checkAttachments(s, c); r != nil {
		return nil, r
	}

	return []Event{{
		Room:        c.Room,
		Author:      c.Author,
		Kind:        c.Kind,
		Body:        c.Body,
		Refs:        c.Refs,
		Recipient:   c.Recipient,
		Lane:        lane,
		Attachments: c.Attachments,
	}}, nil
}

// checkRedaction is the authorization the log's irreversibility demands. A
// redact is permanent and instant, so an unauthorized one is unrecoverable —
// and without this, any actor could erase any other actor's event at wire speed.
//
// Only the original author may suppress their own event. Someone else's leaked
// secret is an operator action through the CLI, which holds the database, not a
// command any actor can send.
func checkRedaction(s State, c Command) *Rejection {
	if s.EventAuthor == nil {
		return nil
	}
	author, ok := s.EventAuthor(c.Refs[0])
	if !ok {
		// A redact naming nothing must not report success. Silently accepting
		// it tells an agent the secret is gone when it is still readable. A seq
		// not yet assigned lands here too, so nothing can be pre-redacted.
		return &Rejection{"refs.target_unknown",
			"no event at " + c.Refs[0] + "; a redact that names nothing would report success and do nothing"}
	}

	// The target must live in the room the redact is posted to, so the record
	// of the suppression sits where the suppressed thing was.
	if s.EventRoom != nil {
		if room, ok := s.EventRoom(c.Refs[0]); ok && room != c.Room {
			return &Rejection{"refs.target_unknown",
				"event " + c.Refs[0] + " is in room " + room + ", not " + c.Room}
		}
	}

	// Re-redacting is refused rather than absorbed: silently accepting it would
	// report success for an act that changed nothing.
	if s.IsRedacted != nil && s.IsRedacted(c.Refs[0]) {
		return &Rejection{"redact.already_redacted",
			"event " + c.Refs[0] + " is already redacted; use purge to erase the body permanently"}
	}
	if author != c.Author {
		return &Rejection{"redact.not_author",
			"only " + string(author) + " may redact their own event; erasure is permanent, " +
				"so someone else's paste is an operator action, not a command"}
	}
	return nil
}

// checkAttachments refuses an event that points at content nobody stored. A
// dangling attachment would render as a broken link forever, and the log is
// append-only, so there is no later repair.
func checkAttachments(s State, c Command) *Rejection {
	for _, a := range c.Attachments {
		if a.Title == "" {
			return &Rejection{"attachment.title.required",
				"each attachment needs a title; the row shows the title, not the content"}
		}
		if s.ArtifactExists != nil && !s.ArtifactExists(a.Hash) {
			return &Rejection{"attachment.unknown",
				"no artifact stored under hash " + a.Hash + "; POST /artifacts first"}
		}
	}
	return nil
}

func knownKind(k Kind) bool {
	switch k {
	case KindChat, KindFinding, KindQuestion, KindAnswer, KindTIL,
		KindHandoff, KindStatus, KindPRLink, KindDigest, KindRedact:
		return true
	}
	return false
}

// checkBody enforces the per-kind schema. A rejection returns the field that
// failed so an agent can correct itself without a human.
func checkBody(c Command) *Rejection {
	text, _ := c.Body["text"].(string)

	switch c.Kind {
	case KindFinding:
		if text == "" {
			return &Rejection{"body.text.required", "finding requires text"}
		}
		sev, _ := c.Body["severity"].(string)
		if !validSeverity(sev) {
			return &Rejection{"body.severity.invalid",
				"finding requires severity in p0|p1|p2|p3, got: " + sev}
		}
	case KindChat, KindQuestion, KindAnswer, KindTIL, KindStatus, KindDigest:
		if text == "" {
			return &Rejection{"body.text.required", string(c.Kind) + " requires text"}
		}
	case KindHandoff:
		if text == "" {
			return &Rejection{"body.text.required", "handoff requires text"}
		}
	case KindPRLink:
		if u, _ := c.Body["url"].(string); u == "" {
			return &Rejection{"body.url.required", "pr.link requires url"}
		}
	case KindRedact:
		if len(c.Refs) != 1 {
			return &Rejection{"refs.exactly_one",
				"redact must reference exactly one event"}
		}
	}
	return nil
}

func validSeverity(s string) bool {
	switch s {
	case "p0", "p1", "p2", "p3":
		return true
	}
	return false
}

// answerRecipient finds the author of the question this answer refs.
func answerRecipient(s State, c Command) (Actor, bool) {
	if s.EventKind == nil || s.EventAuthor == nil {
		return "", false
	}
	for _, ref := range c.Refs {
		if k, ok := s.EventKind(ref); ok && k == KindQuestion {
			if who, ok := s.EventAuthor(ref); ok && who != "" {
				return who, true
			}
		}
	}
	return "", false
}

func checkAnswersAQuestion(s State, c Command) *Rejection {
	if len(c.Refs) == 0 {
		return &Rejection{"refs.question_required",
			"answer must reference the question it answers"}
	}
	if s.EventKind == nil {
		return nil
	}
	for _, ref := range c.Refs {
		if k, ok := s.EventKind(ref); ok && k == KindQuestion {
			return nil
		}
	}
	return &Rejection{"refs.question_required",
		"answer must reference an event of kind question"}
}
