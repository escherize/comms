package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Lane names a cursor's stream. Two lanes, two cursors: one integer shared
// between read and inbox either makes inbox swallow every ambient event beneath
// the addressed high-water mark, or makes it re-deliver the same answer
// forever — and both failures are silent.
type Lane string

const (
	LaneAll       Lane = "read"
	LaneAddressed Lane = "inbox"
)

// cursorSet is the whole file: one seq per (room, lane) for one seat.
type cursorSet struct {
	Cursors map[string]int64 `json:"cursors"`
}

func cursorPath(actor string) string {
	safe := strings.NewReplacer("/", "_", ":", "_", string(filepath.Separator), "_").Replace(actor)
	return filepath.Join(stateDir(), safe+".cursors.json")
}

func stateDir() string {
	if d := os.Getenv("COMMS_HOME"); d != "" {
		return filepath.Join(d, "state")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "agent_comms", "state")
}

func cursorKey(room string, lane Lane) string { return room + "\x00" + string(lane) }

func loadCursors(actor string) cursorSet {
	cs := cursorSet{Cursors: map[string]int64{}}
	raw, err := os.ReadFile(cursorPath(actor))
	if err != nil {
		return cs
	}
	_ = json.Unmarshal(raw, &cs)
	if cs.Cursors == nil {
		cs.Cursors = map[string]int64{}
	}
	return cs
}

// Cursor returns where this seat last read one lane of one room.
func Cursor(actor, room string, lane Lane) int64 {
	return loadCursors(actor).Cursors[cursorKey(room, lane)]
}

// SaveCursor advances a lane's cursor, never backwards. The write is atomic —
// two sessions under one seat, in two worktrees, must not leave each other a
// half-written file.
func SaveCursor(actor, room string, lane Lane, seq int64) error {
	if err := os.MkdirAll(stateDir(), 0o700); err != nil {
		return err
	}
	cs := loadCursors(actor)
	key := cursorKey(room, lane)
	if seq <= cs.Cursors[key] {
		return nil
	}
	cs.Cursors[key] = seq

	raw, err := json.Marshal(cs)
	if err != nil {
		return err
	}
	// Write to a unique temp file in the same directory, then rename. Rename is
	// atomic within a filesystem, so a concurrent reader sees either the old
	// file or the new one and never a partial write.
	tmp, err := os.CreateTemp(stateDir(), ".cursors-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), cursorPath(actor))
}

// ResetCursor rewinds a lane, for an agent that wants the history again.
func ResetCursor(actor, room string, lane Lane) error {
	cs := loadCursors(actor)
	delete(cs.Cursors, cursorKey(room, lane))
	raw, _ := json.Marshal(cs)
	if err := os.MkdirAll(stateDir(), 0o700); err != nil {
		return err
	}
	return os.WriteFile(cursorPath(actor), raw, 0o600)
}

func (c cursorSet) String() string { return fmt.Sprintf("%v", c.Cursors) }
