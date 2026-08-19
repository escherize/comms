// Package core is the functional core: pure decision logic with no clock, no
// IO, and no network. Everything here is a total function over values.
//
// The shell parses untrusted input into these types exactly once (parse, don't
// validate), authenticates it, and appends whatever Decide returns. Every
// domain refusal lives here; nothing that could refuse a well-formed command
// for a domain reason lives outside.
package core

import "strings"

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
	// KindDecline answers a handoff with "not me". Without it, an agent that
	// will not take the work has no way to say so, and divergence is
	// indistinguishable from silence — which is what happened when a
	// coordinator handed two slices out and found six minutes later that both
	// agents were working a third.
	KindDecline Kind = "decline"
	// KindPresence is a seat arriving: the check-in join posts. It exists
	// because join used to check in as chat — exactly the shrug the skill
	// tells agents never to post, filed as an inconsistency by a study agent.
	// Presence is a fact about the roster, not a thing anyone said.
	KindPresence Kind = "presence"
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
	case KindQuestion, KindAnswer, KindHandoff, KindDigest, KindDecline:
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

// CapDigest is the one capability today: the right to post an addressed
// summary nobody asked for.
const CapDigest = "digest"

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
	// HasCapability reports whether a seat holds a named capability. It is the
	// same shape as the author check on redact: authorization inside the
	// decider, not a privileged write path around it.
	HasCapability func(a Actor, capability string) bool
	// ActorEnrolled reports whether a seat has ever held a key. An addressed
	// event to a seat that does not exist is worse than a rejection: it is
	// accepted, addressed to nobody, permanently, and its author waits.
	ActorEnrolled func(a Actor) bool
	// IsMember reports whether a seat may act in a room. It is the room-scope
	// decision projection, the same shape as HasCapability: authorization
	// inside the decider, not a privileged path around it. A '*'-scoped seat is
	// a member of every room. When nil (a hub with no scoping wired), every
	// author is treated as a member — backward compatible with a hub that
	// predates room scoping.
	IsMember func(a Actor, room string) bool
	// MemberRooms lists the rooms a seat holds, for the room.not_a_member
	// refusal to name — the seat's OWN rooms only, so the error helps the
	// author without confirming whether the room they were refused from exists.
	MemberRooms func(a Actor) []string
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
	if c.Author == "" {
		return nil, &Rejection{"author.required", "every command names an author"}
	}
	// Membership is checked before room existence, and this order is a security
	// property, not a preference. room.unknown reveals that a room does not
	// exist; a seat scoped away from a room must not be able to probe for it, so
	// a non-member is refused room.not_a_member — which names only the seat's
	// own rooms and says nothing about the target — before RoomExists could
	// leak. A '*' member is a member of every room and falls through to the
	// existence check, where room.unknown is not a leak because they can see
	// every room anyway.
	if s.IsMember != nil && !s.IsMember(c.Author, c.Room) {
		return nil, &Rejection{"room.not_a_member", notAMemberDetail(s, c.Author)}
	}
	if s.RoomExists != nil && !s.RoomExists(c.Room) {
		return nil, &Rejection{"room.unknown", "no such room: " + c.Room}
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
	if c.Kind == KindDecline {
		if len(c.Refs) != 1 {
			return nil, &Rejection{"refs.exactly_one",
				"a decline names the one handoff it is refusing"}
		}
		if s.EventKind != nil {
			k, ok := s.EventKind(c.Refs[0])
			if !ok {
				return nil, &Rejection{"refs.unknown",
					"no event at " + c.Refs[0] + "; check the seq you were handed"}
			}
			if k != KindHandoff {
				return nil, &Rejection{"refs.handoff_required",
					"a decline refuses a handoff; " + c.Refs[0] + " is a " + string(k)}
			}
		}
		// Back to whoever handed it over, for the same reason an answer goes
		// back to whoever asked: the person who needs to know is the one who
		// thought the work was covered.
		if c.Recipient == "" {
			if to, ok := authorOfReferenced(s, c, KindHandoff); ok {
				c.Recipient = to
			}
		}
	}

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
	// A digest is addressed by definition (ADR-0008), so an agent that could
	// post one could interrupt everyone, for free, on a loop. The capability
	// lives with the digest bot, which authenticates with its own key and whose
	// commands travel this same path.
	if c.Kind == KindDigest {
		if s.HasCapability == nil || !s.HasCapability(c.Author, CapDigest) {
			return nil, &Rejection{"digest.not_authorized",
				"digest is addressed by definition, so posting one interrupts everyone " +
					"without spending a budget. The capability belongs to the digest bot; " +
					"post a til or a finding instead"}
		}
	}

	// A recipient nobody enrolled as is a typo the log keeps forever. The check
	// is here rather than in the shell because it decides whether an event is
	// admissible, and both clients must get the same answer.
	if c.Recipient != "" && s.ActorEnrolled != nil && !s.ActorEnrolled(c.Recipient) {
		return nil, &Rejection{"recipient.unknown",
			"no seat " + string(c.Recipient) + " is enrolled; addressing an event to a " +
				"seat that does not exist waits for an answer nobody was asked for. " +
				"Run: comms room"}
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

// notAMemberDetail is the room.not_a_member message. It names the author's own
// rooms and nothing else — never the room they were refused from, never
// whether that room exists — so the refusal helps a legitimate author who
// mistyped a room they hold, and tells a prober nothing. A seat with no rooms
// at all is told exactly that, which is also existence-independent.
func notAMemberDetail(s State, author Actor) string {
	var mine []string
	if s.MemberRooms != nil {
		for _, r := range s.MemberRooms(author) {
			if r == "*" {
				// A '*' member never reaches this refusal, but if wiring ever
				// let one through, do not print the wildcard as a room name.
				continue
			}
			mine = append(mine, r)
		}
	}
	if len(mine) == 0 {
		return "you are not a member of that room, and hold no rooms to post in; " +
			"ask whoever runs the hub for an invite scoped to it"
	}
	return "you are not a member of that room; you can post in: " + strings.Join(mine, ", ")
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
		KindHandoff, KindStatus, KindPRLink, KindDigest, KindRedact, KindDecline,
		KindPresence:
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
	case KindChat, KindQuestion, KindAnswer, KindTIL, KindStatus, KindDigest, KindDecline, KindPresence:
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
	return authorOfReferenced(s, c, KindQuestion)
}

// authorOfReferenced finds who wrote the referenced event of a given kind. Two
// kinds derive their recipient this way for the same reason: a reply goes to
// whoever spoke, because the person who needs it is the one who is waiting on
// it. An answer goes to whoever asked; a decline goes to whoever thought the
// work was covered.
func authorOfReferenced(s State, c Command, want Kind) (Actor, bool) {
	if s.EventKind == nil || s.EventAuthor == nil {
		return "", false
	}
	for _, ref := range c.Refs {
		if k, ok := s.EventKind(ref); ok && k == want {
			if who, ok := s.EventAuthor(ref); ok && who != "" {
				return who, true
			}
		}
	}
	return "", false
}

// checkAnswersAQuestion distinguishes the two ways an answer's ref can be
// wrong. redact already tells them apart, and an agent that cannot tell "I
// named the wrong seq" from "I named a chat" retries the same mistake.
func checkAnswersAQuestion(s State, c Command) *Rejection {
	if len(c.Refs) == 0 {
		return &Rejection{"refs.question_required",
			"answer must reference the question it answers"}
	}
	if s.EventKind == nil {
		return nil
	}
	var sawSomething bool
	for _, ref := range c.Refs {
		k, ok := s.EventKind(ref)
		if !ok {
			continue
		}
		sawSomething = true
		if k == KindQuestion {
			return nil
		}
	}
	if !sawSomething {
		// A seq that is not there and a seq that is the wrong kind call for
		// different corrections: look up the right number, versus point at a
		// different event. redact already tells these apart.
		return &Rejection{"refs.unknown",
			"no event at " + strings.Join(c.Refs, ", ") + " in this room; " +
				"check the seq you read it from"}
	}
	return &Rejection{"refs.question_required",
		"answer must reference an event of kind question; that seq is a different kind"}
}

// KindDoc is what a kind means and what it needs, so the binary can answer
// "what are the kinds" instead of a person having to. Three documents listed
// 8, 8 and 26 kinds while this list held the answer and would not say it.
type KindDoc struct {
	Kind     Kind
	Lane     Lane
	Means    string
	Requires string
	Agent    bool // may an ordinary agent seat post it
}

// AllKinds is the enumeration every switch in this package must handle. Go has
// no exhaustive matching, so a Kind added without updating knownKind, LaneOf or
// checkBody would fall through a default and ship.
//
// It is derived from Kinds() rather than written out beside it. It used to be a
// second list, in a _test.go file — which meant no production code could read
// it, so `comms kinds` could not exist and three documents each kept
// their own copy. It also meant the guard could rot: `decline` was added and
// the list was not updated, so the check against forgetting a kind had itself
// forgotten one. One list, and adding a kind is one edit.
var AllKinds = allKinds()

func allKinds() []Kind {
	out := make([]Kind, 0, len(Kinds()))
	for _, k := range Kinds() {
		out = append(out, k.Kind)
	}
	return out
}

// Kinds describes every kind, in the order an agent should consider them: the
// ladder from "something is wrong" down to "it still needs saying".
func Kinds() []KindDoc {
	return []KindDoc{
		{KindFinding, LaneOf(KindFinding), "a defect, gotcha or surprise worth keeping", "--text, --severity p0|p1|p2|p3", true},
		{KindTIL, LaneOf(KindTIL), "a lesson the team can reuse (today I learned)", "--text", true},
		{KindQuestion, LaneOf(KindQuestion), "a decision or fact you need from a person", "--text, --to", true},
		{KindAnswer, LaneOf(KindAnswer), "a reply, pointed at the question", "--text, --to-question", true},
		{KindHandoff, LaneOf(KindHandoff), "transfer of responsibility, with context", "--text, --to", true},
		{KindDecline, LaneOf(KindDecline), "refusing a handoff, out loud", "<seq>, --why", true},
		{KindStatus, LaneOf(KindStatus), "progress on work in flight", "--text, optional --step/--of", true},
		{KindPRLink, LaneOf(KindPRLink), "a PR that exists", "--url", true},
		{KindChat, LaneOf(KindChat), "everything else, and a shrug of an answer", "--text", true},
		{KindPresence, LaneOf(KindPresence), "a seat arriving — join posts it for you", "--text; join's check-in, not for chatter", true},
		{KindRedact, LaneOf(KindRedact), "suppress a body you should not have posted", "redact <seq> --why", true},
		{KindDigest, LaneOf(KindDigest), "a periodic summary of a window", "operator capability; the digest bot's", false},
	}
}
