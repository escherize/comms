package shell

import (
	"strings"
	"testing"
)

// TestRenderMarkdownStripsScriptVectors is the security test for the display
// boundary. Each case is a real way an agent could try to get script into a
// human's authenticated session; none may survive the pipeline.
func TestRenderMarkdownStripsScriptVectors(t *testing.T) {
	vectors := []struct {
		name   string
		src    string
		banned []string
	}{
		{
			name:   "raw script tag",
			src:    "hello\n\n<script>fetch('/commands',{method:'POST'})</script>\n",
			banned: []string{"<script", "fetch("},
		},
		{
			name:   "img onerror handler",
			src:    "<img src=x onerror=\"alert(document.cookie)\">",
			banned: []string{"onerror", "alert(", "<img"},
		},
		{
			name:   "javascript: url in markdown link",
			src:    "[click me](javascript:alert(1))",
			banned: []string{"javascript:"},
		},
		{
			name:   "iframe",
			src:    "<iframe src=\"https://evil.example\"></iframe>",
			banned: []string{"<iframe"},
		},
		{
			name:   "style block",
			src:    "<style>body{display:none}</style>",
			banned: []string{"<style", "display:none"},
		},
		{
			name:   "svg with onload",
			src:    "<svg onload=alert(1)></svg>",
			banned: []string{"<svg", "onload"},
		},
		{
			name:   "form posting elsewhere",
			src:    "<form action=\"https://evil.example\"><input name=\"x\"></form>",
			banned: []string{"<form", "action="},
		},
		{
			name:   "object embed",
			src:    "<object data=\"evil.swf\"></object>",
			banned: []string{"<object"},
		},
		{
			name:   "data uri link",
			src:    "[x](data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==)",
			banned: []string{"data:text/html"},
		},
		{
			name:   "onclick on an anchor",
			src:    "<a href=\"#\" onclick=\"alert(1)\">x</a>",
			banned: []string{"onclick"},
		},
	}

	for _, v := range vectors {
		t.Run(v.name, func(t *testing.T) {
			got := strings.ToLower(string(RenderMarkdown([]byte(v.src))))
			for _, banned := range v.banned {
				if strings.Contains(got, strings.ToLower(banned)) {
					t.Errorf("rendered output must not contain %q\ngot: %s", banned, got)
				}
			}
		})
	}
}

// The GFM features agent output actually uses must survive the sanitizer,
// otherwise the format choice buys nothing.
func TestRenderMarkdownKeepsGFM(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "table of results",
			src:  "| pkg | status |\n|---|---|\n| auth | fail |\n",
			want: []string{"<table", "<th", "<td", "auth", "fail"},
		},
		{
			name: "task list",
			src:  "- [x] repro confirmed\n- [ ] fix written\n",
			want: []string{"<ul", "<li", "repro confirmed"},
		},
		{
			name: "fenced code",
			src:  "```go\nfunc main() {}\n```\n",
			want: []string{"<pre", "<code", "func main"},
		},
		{
			name: "strikethrough",
			src:  "~~wrong~~ right",
			want: []string{"<del", "right"},
		},
		{
			name: "headings and emphasis",
			src:  "## Findings\n\n**p1** and *context*\n",
			want: []string{"<h2", "<strong", "<em", "Findings"},
		},
		{
			name: "safe link is preserved",
			src:  "[the PR](https://github.com/bcm/agent_comms/pull/12)",
			want: []string{"<a", "href", "github.com"},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := string(RenderMarkdown([]byte(c.src)))
			for _, w := range c.want {
				if !strings.Contains(got, w) {
					t.Errorf("rendered output must contain %q\ngot: %s", w, got)
				}
			}
		})
	}
}

// Content that looks like markup but is not must be shown, not executed.
func TestRenderMarkdownEscapesRatherThanDrops(t *testing.T) {
	got := string(RenderMarkdown([]byte("the parser choked on `<div>` in the fixture")))
	if !strings.Contains(got, "&lt;div&gt;") {
		t.Errorf("literal markup in prose must be escaped and visible, got: %s", got)
	}
}

func TestRenderMarkdownHandlesEmptyAndHuge(t *testing.T) {
	if out := RenderMarkdown(nil); len(out) != 0 && !strings.Contains(string(out), "<") {
		t.Errorf("empty input should render empty-ish, got %q", out)
	}
	big := strings.Repeat("word ", 50000)
	if out := RenderMarkdown([]byte(big)); len(out) == 0 {
		t.Error("large input must still render")
	}
}
