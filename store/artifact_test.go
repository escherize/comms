package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/escherize/comms/core"
)

// The artifact_ref index maps a hash to the rooms that reference it, is
// backfilled from existing attachments on open, and loses a row when the
// referencing event is redacted — so /a/<hash> access can be decided by
// membership without a raw hash bypassing room scope.
func TestArtifactRefIndex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ref.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureRoom("comms"); err != nil {
		t.Fatal(err)
	}
	hash, _ := s.PutArtifact([]byte("# report\n"), kt0)
	seq, err := s.Append(core.Event{Room: "comms", Author: "human:bcm",
		Kind: core.KindFinding, Body: map[string]any{"text": "see", "severity": "p2"},
		Attachments: []core.Attachment{{Hash: hash, Title: "r.md"}},
		Lane:        core.LaneOf(core.KindFinding)}, "ref1", kt0)
	if err != nil {
		t.Fatal(err)
	}

	// Appending recorded the reference.
	if got := s.ArtifactRooms(hash); len(got) != 1 || got[0] != "comms" {
		t.Errorf("append must index the artifact reference, got %v", got)
	}

	// Redacting the event drops the reference — the artifact is served to nobody.
	redactSeq, _ := s.Append(core.Event{Room: "comms", Author: "human:bcm",
		Kind: core.KindRedact, Refs: []string{itoa(seq)},
		Body: map[string]any{"text": "leak"}, Lane: core.LaneOf(core.KindRedact)}, "red1", kt0)
	if err := s.ApplyRedaction(seq, redactSeq, "human:bcm", kt0); err != nil {
		t.Fatal(err)
	}
	if got := s.ArtifactRooms(hash); len(got) != 0 {
		t.Errorf("a redacted event must stop referencing its artifact, got %v", got)
	}
	s.Close()

	// A second artifact referenced before an upgrade: simulate by writing an
	// envelope row without the index, then reopen to backfill.
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	h2, _ := s2.PutArtifact([]byte("# other\n"), kt0)
	if _, err := s2.Append(core.Event{Room: "comms", Author: "human:bcm",
		Kind: core.KindFinding, Body: map[string]any{"text": "x", "severity": "p2"},
		Attachments: []core.Attachment{{Hash: h2, Title: "o.md"}},
		Lane:        core.LaneOf(core.KindFinding)}, "ref2", kt0); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.db.Exec(`DELETE FROM artifact_ref WHERE hash = ?`, h2); err != nil {
		t.Fatal(err)
	}
	s2.Close()
	s3, err := Open(path) // reopen backfills the missing ref
	if err != nil {
		t.Fatal(err)
	}
	defer s3.Close()
	if got := s3.ArtifactRooms(h2); len(got) != 1 || got[0] != "comms" {
		t.Errorf("reopening must backfill a missing artifact_ref, got %v", got)
	}
}

// Purge must drop the artifact_ref rows too, or a purged event keeps granting
// /a/<hash> to its rooms — the read-grant surviving the body it came from.
func TestPurgeDropsArtifactRefGrant(t *testing.T) {
	s := newStore(t)
	h, err := s.PutArtifact([]byte("# secret report\n"), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	seq, err := s.Append(core.Event{Room: "core", Author: "agent:a", Kind: core.KindFinding,
		Body:        map[string]any{"text": "see attached", "severity": "p2"},
		Attachments: []core.Attachment{{Hash: h, Title: "report.md"}},
		Lane:        core.Ambient}, "pa1", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if rooms := s.ArtifactRooms(h); len(rooms) == 0 {
		t.Fatal("the attachment must grant its room before purge")
	}
	if err := s.Purge(seq); err != nil {
		t.Fatal(err)
	}
	if rooms := s.ArtifactRooms(h); len(rooms) != 0 {
		t.Errorf("purge must revoke the read grant, still granted to %v", rooms)
	}
}
