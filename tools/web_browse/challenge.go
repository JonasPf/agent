package main

import (
	"regexp"

	"agent/internal/tool"
)

// Telling a bot check from the page it stands in front of. Pure, so it can be
// tested directly.

// A vendor's check leaves a marker in the DOM that its ordinary pages do not
// carry. For Cloudflare that is the challenge's own options and the page script
// it loads, not the challenge-platform path in general: its bot detection
// injects that path into pages it serves normally, and a Turnstile widget on a
// served form loads from challenges.cloudflare.com.
var checks = []struct {
	vendor string
	marker *regexp.Regexp
}{
	{"Cloudflare", regexp.MustCompile(`_cf_chl_opt|/cdn-cgi/challenge-platform/h/[^"']*/orchestrate/chl_page`)},
	{"DataDome", regexp.MustCompile(`captcha-delivery\.com`)},
	{"HUMAN (PerimeterX)", regexp.MustCompile(`\bid=["']?px-captcha\b`)},
}

// thin is the amount of text below which a page carrying a marker is the check
// rather than a page the check let through. A challenge says a few sentences.
const thin = 1000

// BotCheck names the vendor whose check this page is, or the empty string if it
// is a page. It asks for both a marker and next to no text, because being wrong
// in this direction tells the model that a readable page cannot be read.
func BotCheck(dom string) string {
	if len(tool.ToText(dom)) >= thin {
		return ""
	}
	for _, c := range checks {
		if c.marker.MatchString(dom) {
			return c.vendor
		}
	}
	return ""
}
