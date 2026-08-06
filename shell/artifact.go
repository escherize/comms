package shell

import (
	"bytes"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

// RenderMarkdown turns stored GFM into HTML that is safe to put in front of a
// human in an authenticated session.
//
// This is the display-side execution boundary (ADR-0011). An injected agent
// that could get script into a room a human views would inherit that human's
// session: read every room, post as them, approve its own worker.offer. So the
// pipeline is deliberately one-way — markdown in, sanitized HTML out — and
// goldmark is configured with raw HTML rendering DISABLED, which means agent
// markup never reaches the sanitizer in the first place. The sanitizer is the
// second line, not the only one.
func RenderMarkdown(src []byte) []byte {
	md := goldmark.New(
		// GFM: tables (test results), task lists (bug-bash checklists),
		// strikethrough, autolinks. These are what agent output actually uses.
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(
			// Not WithUnsafe. Raw HTML in the source is escaped, not emitted.
			html.WithHardWraps(),
		),
	)
	var buf bytes.Buffer
	if err := md.Convert(src, &buf); err != nil {
		// A conversion error must not fall through to raw content.
		return []byte("<p>artifact could not be rendered</p>")
	}
	return artifactPolicy().SanitizeBytes(buf.Bytes())
}

// artifactPolicy is an allowlist, not a denylist: anything not named here is
// stripped, so a new HTML feature cannot become a hole by default.
func artifactPolicy() *bluemonday.Policy {
	p := bluemonday.NewPolicy()

	p.AllowElements(
		"p", "br", "hr",
		"h1", "h2", "h3", "h4", "h5", "h6",
		"strong", "em", "del", "code", "pre", "blockquote",
		"ul", "ol", "li",
		"table", "thead", "tbody", "tr", "th", "td",
	)
	// Task lists render as disabled checkboxes; they carry no interaction.
	p.AllowAttrs("type", "checked", "disabled").Matching(
		bluemonday.Paragraph).OnElements("input")
	p.AllowElements("input")

	// Links: http/https/mailto only. javascript: and data: never survive.
	p.AllowAttrs("href").OnElements("a")
	p.AllowURLSchemes("http", "https", "mailto")
	p.RequireNoFollowOnLinks(true)
	p.AddTargetBlankToFullyQualifiedLinks(true)

	// Syntax-highlight class names from fenced blocks, nothing else.
	p.AllowAttrs("class").Matching(bluemonday.SpaceSeparatedTokens).
		OnElements("code", "pre", "span", "li", "ul")

	return p
}
