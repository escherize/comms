package cli

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// colorEnabled reports whether stderr is a terminal a person is reading and
// nobody asked for plain output.
func colorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	fi, err := os.Stderr.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

const (
	ansiBold  = "\033[1m"
	ansiCyan  = "\033[36m"
	ansiGreen = "\033[32m"
	ansiReset = "\033[0m"
)

var flagToken = regexp.MustCompile(`--[a-z][a-z0-9-]*`)

// colorize styles help text for a terminal: the usage line bold, example
// invocations green, flag names cyan. Help is authored plain; color is a
// rendering concern, so JSON and piped output never see an escape code.
func colorize(text string) string {
	lines := strings.Split(text, "\n")
	styledFirst := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case !styledFirst && trimmed != "":
			lines[i] = ansiBold + line + ansiReset
			styledFirst = true
		case strings.HasPrefix(trimmed, "--"):
			lines[i] = flagToken.ReplaceAllString(line, ansiCyan+"$0"+ansiReset)
		case strings.HasPrefix(line, "  ") && strings.Contains(line, "comms "):
			lines[i] = ansiGreen + line + ansiReset
		default:
			lines[i] = flagToken.ReplaceAllString(line, ansiCyan+"$0"+ansiReset)
		}
	}
	return strings.Join(lines, "\n")
}

// FlagsHelp renders a flag set as a help section in double-dash form, aligned,
// with defaults worth knowing. Rendered from the FlagSet itself so the table
// cannot drift from the code. Exported for package main, whose operator flags
// live on flag.CommandLine.
func FlagsHelp(fs *flag.FlagSet) string {
	type row struct{ head, usage string }
	var rows []row
	width := 0
	fs.VisitAll(func(f *flag.Flag) {
		name, usage := flag.UnquoteUsage(f)
		head := "--" + f.Name
		if name != "" {
			head += " <" + name + ">"
		}
		switch f.DefValue {
		case "", "false", "0", "0s", "-1":
		default:
			usage += " (default " + f.DefValue + ")"
		}
		if len(head) > width {
			width = len(head)
		}
		rows = append(rows, row{head, usage})
	})
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("flags\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "  %-*s  %s\n", width, r.head, r.usage)
	}
	return strings.TrimRight(b.String(), "\n")
}

// HelpFS answers --help for one verb: the prose, then the verb's flags.
func (o *Out) HelpFS(fs *flag.FlagSet, format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	if block := FlagsHelp(fs); block != "" {
		text += "\n\n" + block
	}
	o.Help("%s", text)
}
