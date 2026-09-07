package main

import "testing"

const page = `
<html><body>
<ol id="b_results">
  <li class="b_algo"><h2><a href="https://go.dev/doc">The Go Programming Language</a></h2>
    <div class="b_caption"><p>Get started with Go, the language.</p></div></li>
  <li class="b_algo"><h2><a href="https://sqlite.org/wal.html">SQLite WAL mode</a></h2>
    <div class="b_caption"><p>Write-ahead logging &amp; its tradeoffs.</p></div></li>
  <li class="b_ad"><h2><a href="https://ad.example/x">Buy Go</a></h2></li>
</ol>
</body></html>
`

func TestParseResults(t *testing.T) {
	got := ParseResults(page)
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2 (a paid placement is not a result): %+v", len(got), got)
	}
	if got[0].Title != "The Go Programming Language" || got[0].URL != "https://go.dev/doc" {
		t.Errorf("first result = %+v", got[0])
	}
	if got[0].Snippet != "Get started with Go, the language." {
		t.Errorf("snippet = %q", got[0].Snippet)
	}
	if got[1].Snippet != "Write-ahead logging & its tradeoffs." {
		t.Errorf("entities were not decoded: %q", got[1].Snippet)
	}
}

func TestAResultWithoutASnippetStillCounts(t *testing.T) {
	got := ParseResults(`<li class="b_algo"><h2><a href="https://x.test/">Bare</a></h2></li>`)
	if len(got) != 1 || got[0].Snippet != "" {
		t.Errorf("got %+v, want one result with no snippet", got)
	}
}

func TestNoResultsIsEmptyNotAnError(t *testing.T) {
	if got := ParseResults("<html><body>nothing here</body></html>"); len(got) != 0 {
		t.Errorf("got %+v, want nothing", got)
	}
}

// Bing wraps every result in a click-tracking redirect that carries the real URL
// base64'd in its u parameter. A wrapper is useless to the model: it cannot be
// read, and web_fetch on it just bounces.
func TestCleanURL(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"unwraps a redirect",
			"https://www.bing.com/ck/a?!&&p=04&ptn=3&u=a1aHR0cHM6Ly9nby5kZXYv&ntb=1",
			"https://go.dev/"},
		{"unwraps when entities are still encoded",
			"https://www.bing.com/ck/a?p=04&amp;u=a1aHR0cHM6Ly9nby5kZXYv&amp;ntb=1",
			"https://go.dev/"},
		{"leaves a plain URL alone",
			"https://sqlite.org/wal.html", "https://sqlite.org/wal.html"},
		{"keeps the wrapper when it cannot be decoded",
			"https://www.bing.com/ck/a?u=a1not-valid-base64!!",
			"https://www.bing.com/ck/a?u=a1not-valid-base64!!"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := CleanURL(c.in); got != c.want {
				t.Errorf("CleanURL(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// A search engine that declines to serve an automated query must be reported as
// exactly that. Never dress a block up as an empty result, and never try to get
// around it.
func TestRefusal(t *testing.T) {
	for _, in := range []string{
		"Please complete the following challenge",
		"Verifying you're not a bot",
		"your network appears to be sending automated queries",
		"UNUSUAL TRAFFIC from your computer network",
	} {
		if Refusal(in) == "" {
			t.Errorf("Refusal(%q) found nothing", in)
		}
	}
	if got := Refusal("golang sqlite wal - Search. About 40,400 results"); got != "" {
		t.Errorf("an ordinary page was read as a refusal: %q", got)
	}
}
