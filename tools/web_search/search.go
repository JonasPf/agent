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
