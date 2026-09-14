package main

import (
	"strings"
	"testing"
)

// The whole reason a cheap fetch is safe to offer is that it can tell when it
// was the wrong tool. Its predecessor could not: it returned the shell a page
// ships as, and whoever asked could not tell that from a page that simply says
// little. So the one thing this must get right is noticing.
func TestAPageWhoseTextIsWrittenByScriptsIsRecognised(t *testing.T) {
	for _, c := range []struct {
		name string
		html string
	}{
		{"an empty root a framework fills", `<html><head><title>Shop</title>
<script src="/bundle.js"></script></head><body><div id="root"></div></body></html>`},
		{"a placeholder waiting to be replaced", `<html><body><div id="out">Loading…</div>
<script>fetch('/api').then(r => r.json()).then(d => out.textContent = d.text)</script></body></html>`},
	} {
		t.Run(c.name, func(t *testing.T) {
			if !NeedsScripts(c.html) {
				t.Error("a page assembled by its scripts was not recognised as one")
			}
		})
	}
}

// The opposite matters just as much. A warning on a page that is genuinely
// finished teaches whoever reads it to ignore warnings, and sends them to a
// browser that costs a hundred times as much for nothing.
func TestAPageThatIsAlreadyReadableIsNotSecondGuessed(t *testing.T) {
	for _, c := range []struct {
		name string
		html string
	}{
		{"prose with a script beside it", `<html><head><script src="/analytics.js"></script></head><body>
<h1>Widgets</h1><p>` + strings.Repeat("The widget is a small device that does a job. ", 20) +
			`</p><p>` + strings.Repeat("It is sold in boxes of ten. ", 20) + `</p></body></html>`},
		{"plain prose, no scripts at all", `<html><body><h1>Notes</h1><p>` +
			strings.Repeat("A sentence that carries some actual meaning. ", 12) + `</p></body></html>`},
		{"a short page with nothing dynamic about it", `<html><body><p>Service is up.</p></body></html>`},
	} {
		t.Run(c.name, func(t *testing.T) {
			if NeedsScripts(c.html) {
				t.Error("a page that was already readable was reported as needing a browser")
			}
		})
	}
}

// A body that is not a document is the case this tool is best at, and the one
// where a browser is pure cost. It is never second-guessed.
func TestANonDocumentIsNeverSentToABrowser(t *testing.T) {
	if NeedsScripts(`{"stock": 4, "sku": "W-1"}`) {
		t.Error("a JSON body was reported as needing a browser")
	}
}
