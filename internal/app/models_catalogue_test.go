package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// A model is chosen on what it costs, how much it holds, and how capable it is.
// The gateway reports all three, so the catalogue carries them through rather
// than leaving the operator to look each one up.

const catalogueFixture = `{"data":[
 {"id":"anthropic/claude-fable-5.1","name":"Anthropic: Claude Fable 5.1","created":1788285838,
  "description":"Improves agentic coding.","context_length":1000000,
  "architecture":{"input_modalities":["text","image","file"],"output_modalities":["text"]},
  "pricing":{"prompt":"0.00001","completion":"0.00005","input_cache_read":"0.00000025"},
  "top_provider":{"context_length":1000000,"max_completion_tokens":128000},
  "supported_parameters":["reasoning","tools"],
  "benchmarks":{"artificial_analysis":{"intelligence_index":53.4,"coding_index":81.6,"agentic_index":58}}},
 {"id":"small/unmeasured","name":"Small: Unmeasured","context_length":32000,
  "pricing":{"prompt":"0.0000001","completion":"0.0000002"},
  "supported_parameters":["tools"]},
 {"id":"chat/only","name":"Chat: Only","context_length":8000,
  "pricing":{"prompt":"0","completion":"0"},"supported_parameters":["temperature"]}
]}`

func TestTheCatalogueCarriesWhatAModelIsChosenBy(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(catalogueFixture))
	}))
	defer gateway.Close()
	or := NewOpenRouter("k")
	or.base = gateway.URL
	a := &App{or: or}

	w := httptest.NewRecorder()
	a.routes().ServeHTTP(w, httptest.NewRequest("GET", "/models", nil))
	if w.Code != 200 {
		t.Fatalf("GET /models: %d %s", w.Code, w.Body.String())
	}
	var got []map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d models, want the 2 that call tools", len(got))
	}
	byID := map[string]map[string]any{}
	for _, m := range got {
		byID[m["id"].(string)] = m
	}

	fable := byID["anthropic/claude-fable-5.1"]
	for field, want := range map[string]any{
		"name":                  "Anthropic: Claude Fable 5.1",
		"description":           "Improves agentic coding.",
		"created":               1788285838.0,
		"context_length":        1000000.0,
		"max_completion_tokens": 128000.0,
		"prompt_price":          0.00001,
		"completion_price":      0.00005,
		"cache_read_price":      0.00000025,
		"reasoning":             true,
		"intelligence_index":    53.4,
		"coding_index":          81.6,
		"agentic_index":         58.0,
	} {
		if fable[field] != want {
			t.Errorf("%s = %v, want %v", field, fable[field], want)
		}
	}
	if mods, _ := fable["input_modalities"].([]any); len(mods) != 3 {
		t.Errorf("input_modalities = %v, want text, image, file", fable["input_modalities"])
	}

	// An index the gateway does not report is absent, never zero: a zero would
	// rank an unmeasured model as the least capable one there is.
	small := byID["small/unmeasured"]
	for _, field := range []string{"intelligence_index", "coding_index", "agentic_index"} {
		if v, ok := small[field]; ok {
			t.Errorf("unmeasured model reports %s = %v, want it absent", field, v)
		}
	}
}
