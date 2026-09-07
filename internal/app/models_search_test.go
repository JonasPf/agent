package app

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

var searchCatalogue = []ModelInfo{
	{ID: "anthropic/claude-opus-4.1", Name: "Anthropic: Claude Opus 4.1"},
	{ID: "anthropic/claude-sonnet-4.5", Name: "Anthropic: Claude Sonnet 4.5"},
	{ID: "google/gemini-2.5-pro", Name: "Google: Gemini 2.5 Pro"},
	{ID: "openai/gpt-5", Name: "OpenAI: GPT-5"},
}

func ids(ms []ModelInfo) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ID
	}
	return out
}

func TestFilterModels(t *testing.T) {
	cases := []struct {
		name string
		q    string
		want []string
	}{
		{"empty query keeps everything", "", []string{
			"anthropic/claude-opus-4.1", "anthropic/claude-sonnet-4.5",
			"google/gemini-2.5-pro", "openai/gpt-5"}},
		{"blank query keeps everything", "   ", []string{
			"anthropic/claude-opus-4.1", "anthropic/claude-sonnet-4.5",
			"google/gemini-2.5-pro", "openai/gpt-5"}},
		{"matches the id", "sonnet", []string{"anthropic/claude-sonnet-4.5"}},
		{"ignores case", "SONNET", []string{"anthropic/claude-sonnet-4.5"}},
		{"matches the provider prefix", "anthropic/", []string{
			"anthropic/claude-opus-4.1", "anthropic/claude-sonnet-4.5"}},
		{"matches the display name", "gpt", []string{"openai/gpt-5"}},
		{"terms are anded across id and name", "claude 4.5", []string{"anthropic/claude-sonnet-4.5"}},
		{"terms may match in any order", "opus anthropic", []string{"anthropic/claude-opus-4.1"}},
		{"no match yields nothing", "llama", nil},
		{"result keeps catalogue order", "e", []string{
			"anthropic/claude-opus-4.1", "anthropic/claude-sonnet-4.5",
			"google/gemini-2.5-pro", "openai/gpt-5"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ids(filterModels(searchCatalogue, c.q))
			if len(got) != len(c.want) {
				t.Fatalf("filterModels(%q) = %v, want %v", c.q, got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("filterModels(%q) = %v, want %v", c.q, got, c.want)
				}
			}
		})
	}
}

// Filtering must not hand out the caller's slice: the catalogue is a shared
// cache, and a filtered view that aliases it would let one request's result
// be rewritten by the next.
func TestFilterModelsDoesNotAliasCatalogue(t *testing.T) {
	in := []ModelInfo{{ID: "a/one"}, {ID: "a/two"}}
	got := filterModels(in, "")
	if len(got) != 2 {
		t.Fatalf("got %d models, want 2", len(got))
	}
	got[0].ID = "mutated"
	if in[0].ID != "a/one" {
		t.Errorf("filtering aliased the catalogue: in[0].ID = %q", in[0].ID)
	}
}

// The catalogue is large enough that the model picker needs to search it, so
// GET /models accepts the query as ?q= and answers with the narrowed list.
func TestHandleModelsQuery(t *testing.T) {
	or := NewOpenRouter("test-key")
	or.models, or.fetched = searchCatalogue, time.Now() // seed the cache; no network
	a := &App{or: or}

	for _, c := range []struct {
		name string
		url  string
		want []string
	}{
		{"no query returns the catalogue", "/models", []string{
			"anthropic/claude-opus-4.1", "anthropic/claude-sonnet-4.5",
			"google/gemini-2.5-pro", "openai/gpt-5"}},
		{"query narrows the catalogue", "/models?q=sonnet", []string{"anthropic/claude-sonnet-4.5"}},
		{"query with no match is empty, not an error", "/models?q=llama", []string{}},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			a.hModels(w, httptest.NewRequest("GET", c.url, nil))
			if w.Code != 200 {
				t.Fatalf("status = %d, want 200 (%s)", w.Code, w.Body.String())
			}
			var got []ModelInfo
			if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode: %v (%s)", err, w.Body.String())
			}
			if g := ids(got); len(g) != len(c.want) {
				t.Fatalf("ids = %v, want %v", g, c.want)
			} else {
				for i := range g {
					if g[i] != c.want[i] {
						t.Fatalf("ids = %v, want %v", g, c.want)
					}
				}
			}
		})
	}
}
