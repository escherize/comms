package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// .commsrc pins a seat to a project. The first omp study found the gap in one
// try: join names your seat and then every verb still wants --as — "a
// completed join means ready to use, not ready after one more setup step."
// The file is personal (the key it names lives on this machine), so join
// keeps it out of the repo's history via .git/info/exclude, never the shared
// .gitignore.

// rcSeat reads the pinned seat from the nearest .commsrc, walking up from the
// working directory the way git finds its root. Empty when there is none.
func rcSeat() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if raw, err := os.ReadFile(filepath.Join(dir, ".commsrc")); err == nil {
			var rc struct {
				Seat string `json:"seat"`
			}
			if json.Unmarshal(raw, &rc) == nil {
				return rc.Seat
			}
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// writeRC pins the seat in the working directory and, in a git repo, excludes
// the file locally so it cannot be committed into a teammate's identity.
func writeRC(seat string) error {
	raw, err := json.MarshalIndent(map[string]string{"seat": seat}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(".commsrc", append(raw, '\n'), 0o644); err != nil {
		return err
	}
	if fi, err := os.Stat(".git"); err == nil && fi.IsDir() {
		exclude := filepath.Join(".git", "info", "exclude")
		cur, _ := os.ReadFile(exclude)
		if !strings.Contains(string(cur), ".commsrc") {
			_ = os.MkdirAll(filepath.Dir(exclude), 0o755)
			out := string(cur)
			if out != "" && !strings.HasSuffix(out, "\n") {
				out += "\n"
			}
			_ = os.WriteFile(exclude, []byte(out+".commsrc\n"), 0o644)
		}
	}
	return nil
}
