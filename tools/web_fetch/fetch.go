package main

import (
	"regexp"
	"strings"

	"agent/internal/tool"
)

// Deciding whether the bytes a server sent are the page, or only the shell it
// ships while its scripts build the page. Pure, so it can be tested directly.

var (
	hasScript = regexp.MustCompile(`(?is)<script\b`)
	// The roots a client-side framework mounts into. An element that exists only
	// to be filled says plainly that the page is not finished.
	emptyRoot = regexp.MustCompile(`(?is)<(?:div|main|section)[^>]*\bid=["']?(?:root|app|__next|application)["']?[^>]*>\s*</(?:div|main|section)>`)
	// Text a page shows while it waits. On its own this means nothing; with
	// almost no other text around it, it is the whole visible page.
	waiting = regexp.MustCompile(`(?i)\b(loading|please wait|just a moment|one moment)\b`)
)

// thin is the amount of readable text below which a document with scripts in it
// is more likely a shell than a short page. A genuinely brief page — a status
// line, a 404 — sits under this too, which is why a script tag is also required:
// a page with no scripts cannot be waiting on any.
const thin = 400

// NeedsScripts reports whether a fetched body looks like a page whose text has
// not been written yet — the case where these bytes answer a different question
// from the one that was asked, and web_browse should be used instead.
//
// It is deliberately conservative. A false positive sends the reader to a
// browser that costs a hundred times as much and returns the same thing; worse,
// a warning that fires on finished pages is a warning nobody reads. So it fires
// only when a page both runs scripts and has next to nothing to say without
// them.
func NeedsScripts(body string) bool {
	if !hasScript.MatchString(body) {
		return false
	}
	if emptyRoot.MatchString(body) {
		return true
	}
	text := tool.ToText(body)
	if len(text) >= thin {
		return false
	}
	// Under the threshold, with scripts present: a page that is visibly waiting
	// is a shell, and a page that says almost nothing at all probably is too.
	return waiting.MatchString(text) || len(strings.TrimSpace(text)) == 0
}
