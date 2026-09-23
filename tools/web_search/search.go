package main

import (
	"encoding/base64"
	"html"
	"net/url"
	"regexp"
	"strings"
)

// Reading results out of a rendered search page. Pure functions, so the parsing
// can be tested against a saved page without a browser or a network.

// Bing marks an organic result <li class="b_algo">; ads and sidebars carry
// other classes, so matching the class is what keeps paid placements out.
var (
	itemRE    = regexp.MustCompile(`(?i)<li[^>]*>`)
	algoRE    = regexp.MustCompile(`(?i)<li[^>]*\bclass="[^"]*\bb_algo\b[^"]*"[^>]*>`)
	linkRE    = regexp.MustCompile(`(?si)<h2[^>]*>.*?<a[^>]+href="([^"]+)"[^>]*>(.*?)</a>`)
	captionRE = regexp.MustCompile(`(?si)<(?:p|div)[^>]*\bclass="[^"]*b_caption[^"]*"[^>]*>(.*?)</(?:p|div)>`)
	paraRE    = regexp.MustCompile(`(?si)<p[^>]*>(.*?)</p>`)
	tagRE     = regexp.MustCompile(`(?si)<(script|style)[^>]*>.*?</(?:script|style)>|<[^>]+>`)
	spaceRE   = regexp.MustCompile(`\s+`)
)

// refusals are the phrases a search engine uses to say it will not serve an
// automated query. The tool reports these; it never tries to get past one.
var refusals = []string{
	"challenge", "captcha", "unusual traffic", "are you a robot",
	"verify you", "automated queries", "access denied",
	"sending automated", "not a bot",
}

// Result is one organic hit.
type Result struct {
	Title   string
	URL     string
	Snippet string
}

// TextOf flattens a fragment of HTML to its readable text.
func TextOf(fragment string) string {
	return strings.TrimSpace(html.UnescapeString(
		spaceRE.ReplaceAllString(tagRE.ReplaceAllString(fragment, " "), " ")))
}

// organicBlocks returns the inner HTML of each organic result. A result runs
// until the next list item or the end of the list; Go's regexp has no lookahead,
// and a pattern that consumed the boundary would eat every second result.
func organicBlocks(page string) []string {
	items := itemRE.FindAllStringIndex(page, -1)
	var out []string
	for i, loc := range items {
		if !algoRE.MatchString(page[loc[0]:loc[1]]) {
			continue
		}
		end := len(page)
		if i+1 < len(items) {
			end = items[i+1][0]
		}
		if j := strings.Index(page[loc[1]:end], "</ol>"); j >= 0 {
			end = loc[1] + j
		}
		out = append(out, page[loc[1]:end])
	}
	return out
}

// ParseResults returns the organic results on the page, in order.
func ParseResults(page string) []Result {
	var out []Result
	for _, block := range organicBlocks(page) {
		link := linkRE.FindStringSubmatch(block)
		if link == nil {
			continue
		}
		title := TextOf(link[2])
		if title == "" {
			continue
		}
		snippet := ""
		if cap := captionRE.FindStringSubmatch(block); cap != nil {
			snippet = TextOf(cap[1])
		}
		if snippet == "" {
			for _, p := range paraRE.FindAllStringSubmatch(block, -1) {
				if t := TextOf(p[1]); t != "" {
					snippet = t
					break
				}
			}
		}
		out = append(out, Result{Title: title, URL: CleanURL(link[1]), Snippet: snippet})
	}
	return out
}

// CleanURL unwraps a Bing click-tracking redirect back to the page it points
// at. The real URL rides in the u parameter, prefixed "a1" and base64url
// encoded. A wrapper that will not decode is returned untouched: a link that
// works is better than no link.
func CleanURL(raw string) string {
	raw = html.UnescapeString(raw)
	if !strings.Contains(raw, "bing.com/ck/a") {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	enc := u.Query().Get("u")
	if !strings.HasPrefix(enc, "a1") {
		return raw
	}
	enc = enc[2:]
	if pad := len(enc) % 4; pad != 0 {
		enc += strings.Repeat("=", 4-pad)
	}
	decoded, err := base64.URLEncoding.DecodeString(enc)
	if err != nil {
		return raw
	}
	s := string(decoded)
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		return s
	}
	return raw
}

// Refusal names the phrase by which the engine declined, or the empty string if
// it served us. An engine that will not answer and a query with no answers are
// different results, and only one of them is worth changing the query over.
func Refusal(text string) string {
	low := strings.ToLower(text)
	for _, phrase := range refusals {
		if strings.Contains(low, phrase) {
			return phrase
		}
	}
	return ""
}

// A search engine that will not serve an automated client usually says so, and
// Refusal above is how that is told from a query with no matches. Bing does
// something else: it answers. The page carries the query in its title, holds
// ten well-formed organic results, and every one of them is about something
// unrelated — gift cards, hospital listings, Manhattan attractions — with a
// different set on each request. Nothing in the parsed output distinguishes
// that from a real answer, so the model reasons from it as though it were one.
//
// The test is the weakest one that catches it: across every result, does any
// part of the query appear anywhere at all. A genuine result set clears that
// bar on its first hit; a page of filler clears it on none.

// searchStopWords are the words a query can be full of and a result would not
// be expected to repeat. A query that has nothing else in it cannot judge an
// answer, and says so by yielding no terms.
var searchStopWords = map[string]bool{
	"the": true, "a": true, "an": true, "and": true, "or": true, "but": true,
	"of": true, "for": true, "to": true, "in": true, "on": true, "at": true,
	"by": true, "is": true, "are": true, "was": true, "were": true, "be": true,
	"how": true, "what": true, "why": true, "when": true, "where": true,
	"which": true, "who": true, "with": true, "from": true, "into": true,
	"not": true, "no": true, "do": true, "does": true, "did": true,
	"can": true, "could": true, "should": true, "would": true, "will": true,
	"it": true, "its": true, "my": true, "me": true, "you": true, "your": true,
	"this": true, "that": true, "these": true, "those": true, "there": true,
	"any": true, "all": true, "get": true, "have": true, "has": true,
	"about": true, "best": true, "new": true, "use": true, "using": true,
}

var wordRE = regexp.MustCompile(`[\p{L}\p{N}]+`)

// QueryTerms are the words of a query worth looking for in an answer:
// lower-cased, without punctuation and quoting, without the words any page
// might carry, and without the very short ones a substring test would match by
// accident.
func QueryTerms(query string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range wordRE.FindAllString(strings.ToLower(query), -1) {
		if len(w) < 3 || searchStopWords[w] || seen[w] {
			continue
		}
		seen[w] = true
		out = append(out, w)
	}
	return out
}

// Unrelated reports that a set of results answers some other question. It is
// deliberately hard to trigger: one term, in one title, URL or snippet, out of
// every result on the page, is enough to call the answer genuine. With nothing
// to look for, or nothing to look in, it judges nothing.
func Unrelated(query string, results []Result) bool {
	terms := QueryTerms(query)
	if len(terms) == 0 || len(results) == 0 {
		return false
	}
	for _, r := range results {
		hay := strings.ToLower(r.Title + " " + r.URL + " " + r.Snippet)
		for _, t := range terms {
			if strings.Contains(hay, t) {
				return false
			}
		}
	}
	return true
}
