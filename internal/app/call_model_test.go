package app

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// The gateway is the one thing these tests stand in for. It lists a model of
// each kind call_model can reach, answers each endpoint the way the gateway
// does, and keeps every request so a test can read what was sent.

type gatewayCall struct {
	Method, Path, Query, Auth string
	Body                      map[string]any
}

type fakeGateway struct {
	mu    sync.Mutex
	calls []gatewayCall
}

func (g *fakeGateway) sent(path string) []gatewayCall {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []gatewayCall
	for _, c := range g.calls {
		if c.Path == path {
			out = append(out, c)
		}
	}
	return out
}

func (g *fakeGateway) posts() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := 0
	for _, c := range g.calls {
		if c.Method == "POST" {
			n++
		}
	}
	return n
}

const gatewayModels = `{"data":[
 {"id":"x/writer","name":"X Writer","architecture":{"input_modalities":["text","image","audio"],"output_modalities":["text"]},
  "supported_parameters":["tools","reasoning"],"pricing":{"prompt":"0.000001","completion":"0.000004"}},
 {"id":"x/textonly","name":"X Text","architecture":{"input_modalities":["text"],"output_modalities":["text"]},
  "supported_parameters":["tools"],"pricing":{"prompt":"0.0000001","completion":"0.0000002"}},
 {"id":"x/voice","name":"X Voice","architecture":{"input_modalities":["text"],"output_modalities":["speech"]},
  "supported_voices":["Kore","Puck"],"pricing":{"prompt":"0.0000005","completion":"0.000009"}},
 {"id":"x/pcmvoice","name":"X PCM Voice","architecture":{"input_modalities":["text"],"output_modalities":["speech"]},
  "supported_voices":["Kore"],"pricing":{"prompt":"0.0000005","completion":"0.000009"}},
 {"id":"x/scribe","name":"X Scribe","architecture":{"input_modalities":["audio"],"output_modalities":["transcription"]},
  "pricing":{"prompt":"0.00006","completion":"0"}},
 {"id":"x/judge","name":"X Judge","architecture":{"input_modalities":["text","image"],"output_modalities":["decisions"]},
  "pricing":{"prompt":"0.00000002","completion":"0"}}
]}`

func newFakeGateway(t *testing.T) (*fakeGateway, *httptest.Server) {
	t.Helper()
	g := &fakeGateway{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		g.mu.Lock()
		g.calls = append(g.calls, gatewayCall{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Authorization"), body})
		g.mu.Unlock()
		switch r.URL.Path {
		case "/models":
			fmt.Fprint(w, gatewayModels)
		case "/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, `data: {"choices":[{"delta":{"content":"It shows a cat."}}]}`+"\n\n")
			fmt.Fprint(w, `data: {"choices":[],"usage":{"prompt_tokens":900,"completion_tokens":6,"cost":0.002}}`+"\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
		case "/audio/speech":
			// Gemini's speech models give raw samples only, and say so.
			if body["model"] == "x/pcmvoice" && body["response_format"] != "pcm" {
				w.WriteHeader(400)
				fmt.Fprint(w, `{"error":{"message":"Gemini TTS only supports response_format=\"pcm\". Got \"mp3\".","code":400}}`)
				return
			}
			w.Header().Set("X-Generation-Id", "gen-speech-1")
			if body["response_format"] == "pcm" {
				w.Header().Set("Content-Type", "audio/pcm;rate=24000;channels=1")
				w.Write([]byte{1, 0, 2, 0, 3, 0, 4, 0})
				return
			}
			w.Header().Set("Content-Type", "audio/mpeg")
			fmt.Fprint(w, "ID3-not-really-an-mp3")
		case "/generation":
			// What a generation cost is under "usage", as the gateway answers.
			fmt.Fprint(w, `{"data":{"id":"gen-speech-1","model":"x/voice","usage":0.003}}`)
		case "/audio/transcriptions":
			fmt.Fprint(w, `{"text":"hello there. general kenobi","segments":[`+
				`{"start":0,"end":1.2,"text":"hello there."},{"start":1.2,"end":2.75,"text":"general kenobi"}],`+
				`"usage":{"cost":0.001}}`)
		case "/alpha/decisions":
			fmt.Fprint(w, `{"answers":{"is_bug":{"type":"noul","noul":true},`+
				`"severity":{"type":"choice","choice":"high","confidence":0.82}},"usage":{"cost":0.0001}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return g, srv
}

// callModelApp is an agent with the shipped tools loaded, the tool API
// listening, and the gateway faked; call runs call_model as the subprocess it
// is, in one conversation.
func callModelApp(t *testing.T) (*App, *Session, *fakeGateway, func(args string) toolResult) {
	t.Helper()
	dir := t.TempDir()
	a := newTestAppAt(t, dir)
	g, srv := newFakeGateway(t)
	a.or = NewOpenRouter("the-agents-own-key")
	a.or.base = srv.URL
	a.cfg.ToolsDir = filepath.Join("..", "..", "tools")
	a.tools = NewRegistry(a.cfg.ToolsDir, DBPath(dir), a.store.DB())
	a.registerBuiltins()
	if _, failures := a.tools.Load(a); len(failures) > 0 {
		t.Fatalf("tools failed to load: %+v", failures)
	}
	toolAPI(t, a)
	s, err := a.NewSession(SessionConfig{Model: "test/model"}, "")
	if err != nil {
		t.Fatal(err)
	}
	return a, s, g, func(args string) toolResult {
		t.Helper()
		return a.tools.Call(t.Context(), &ToolCtx{App: a, SessionID: s.ID}, "call_model", json.RawMessage(args))
	}
}

func putFile(t *testing.T, a *App, id, rel string, data []byte) {
	t.Helper()
	p := filepath.Join(a.sessionWorkspace(id), rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func sessionCost(t *testing.T, a *App, id string) float64 {
	t.Helper()
	return a.store.Session(id).Cost
}

// "A model that writes text is sent the prompt with each file attached as its
// kind, and the reasoning effort when given; its answer is the result."
func TestCallModelSendsAWritingModelThePromptWithItsFiles(t *testing.T) {
	a, s, g, call := callModelApp(t)
	png := []byte("\x89PNG-pixels")
	wav := []byte("RIFF-samples")
	putFile(t, a, s.ID, "photo.png", png)
	putFile(t, a, s.ID, "audio/clip.wav", wav)

	res := call(`{"model":"x/writer","prompt":"What is in these?","files":["photo.png","audio/clip.wav"],"reasoning":"high"}`)
	if !res.OK {
		t.Fatalf("call failed: %s (stderr %s)", res.Error, res.stderr)
	}
	if !strings.Contains(res.Content, "It shows a cat.") || !strings.Contains(res.Content, "$0.0020") {
		t.Errorf("result = %q, want the answer and what it cost", res.Content)
	}
	sent := g.sent("/chat/completions")
	if len(sent) != 1 {
		t.Fatalf("chat requests = %d, want 1", len(sent))
	}
	body := sent[0].Body
	if body["model"] != "x/writer" {
		t.Errorf("model = %v", body["model"])
	}
	if r, _ := body["reasoning"].(map[string]any); r["effort"] != "high" {
		t.Errorf("reasoning = %v, want effort high", body["reasoning"])
	}
	parts := body["messages"].([]any)[0].(map[string]any)["content"].([]any)
	got := map[string]map[string]any{}
	for _, p := range parts {
		m := p.(map[string]any)
		got[m["type"].(string)] = m
	}
	if got["text"]["text"] != "What is in these?" {
		t.Errorf("text part = %v", got["text"])
	}
	if u := got["image_url"]["image_url"].(map[string]any)["url"]; u != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(png) {
		t.Errorf("image part = %v", u)
	}
	audio := got["input_audio"]["input_audio"].(map[string]any)
	if audio["format"] != "wav" || audio["data"] != base64.StdEncoding.EncodeToString(wav) {
		t.Errorf("audio part = %v", audio)
	}
	if c := sessionCost(t, a, s.ID); c < 0.00199 || c > 0.00201 {
		t.Errorf("conversation cost = %v, want the call's 0.002 added", c)
	}
}

// "A model that speaks is sent the prompt as the words to say, in the voice
// given; the audio is written into the conversation's directory and the result
// names the file." The gateway reports what speech cost only some seconds
// later, so the call does not wait for it: the cost reaches the conversation
// when the gateway has it.
func TestCallModelWritesWhatASpeakingModelSaid(t *testing.T) {
	a, s, g, call := callModelApp(t)
	res := call(`{"model":"x/voice","prompt":"Welcome back. Today: the garden.","voice":"Kore","output":"voice/take1.mp3"}`)
	if !res.OK {
		t.Fatalf("call failed: %s (stderr %s)", res.Error, res.stderr)
	}
	sent := g.sent("/audio/speech")
	if len(sent) != 1 {
		t.Fatalf("speech requests = %d, want 1", len(sent))
	}
	if b := sent[0].Body; b["input"] != "Welcome back. Today: the garden." || b["voice"] != "Kore" || b["response_format"] != "mp3" {
		t.Errorf("speech request = %v", b)
	}
	data, err := os.ReadFile(filepath.Join(a.sessionWorkspace(s.ID), "voice", "take1.mp3"))
	if err != nil || string(data) != "ID3-not-really-an-mp3" {
		t.Fatalf("the audio was not written where asked: %v %q", err, data)
	}
	if !strings.Contains(res.Content, "voice/take1.mp3") || !strings.Contains(res.Content, "cost") {
		t.Errorf("result = %q, want the file and where its cost goes", res.Content)
	}
	deadline := time.Now().Add(5 * time.Second)
	for c := sessionCost(t, a, s.ID); c < 0.00299 || c > 0.00301; c = sessionCost(t, a, s.ID) {
		if time.Now().After(deadline) {
			t.Fatalf("conversation cost = %v, want the call's 0.003 added once the gateway reports it", c)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A model that gives raw samples only is asked again for them, and they are
// written as a WAV the samples' rate and channels describe, under the name
// asked for with .wav in place of .mp3.
func TestCallModelWritesRawSpeechAsAWav(t *testing.T) {
	a, s, _, call := callModelApp(t)
	res := call(`{"model":"x/pcmvoice","prompt":"Hello.","voice":"Kore","output":"hello.mp3"}`)
	if !res.OK {
		t.Fatalf("call failed: %s (stderr %s)", res.Error, res.stderr)
	}
	data, err := os.ReadFile(filepath.Join(a.sessionWorkspace(s.ID), "hello.wav"))
	if err != nil {
		t.Fatalf("no hello.wav: %v (result %q)", err, res.Content)
	}
	if len(data) != 44+8 || string(data[:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		t.Fatalf("not a WAV of the 8 bytes sent: % x", data)
	}
	if rate := uint32(data[24]) | uint32(data[25])<<8 | uint32(data[26])<<16 | uint32(data[27])<<24; rate != 24000 {
		t.Errorf("sample rate = %d, want 24000", rate)
	}
	if channels := int(data[22]); channels != 1 {
		t.Errorf("channels = %d, want 1", channels)
	}
	if !strings.Contains(res.Content, "hello.wav") {
		t.Errorf("result = %q, want it to name hello.wav", res.Content)
	}
	deadline := time.Now().Add(5 * time.Second)
	for sessionCost(t, a, s.ID) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the cost the gateway reported never reached the conversation")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// "A model that transcribes is sent its one audio file; the result is the
// transcript, with each segment's times when timestamps are asked for."
func TestCallModelTranscribesWithTimes(t *testing.T) {
	a, s, g, call := callModelApp(t)
	mp3 := []byte("ID3-speech")
	putFile(t, a, s.ID, "talk.mp3", mp3)
	res := call(`{"model":"x/scribe","files":["talk.mp3"],"timestamps":true,"language":"en"}`)
	if !res.OK {
		t.Fatalf("call failed: %s (stderr %s)", res.Error, res.stderr)
	}
	sent := g.sent("/audio/transcriptions")
	if len(sent) != 1 {
		t.Fatalf("transcription requests = %d, want 1", len(sent))
	}
	b := sent[0].Body
	in, _ := b["input_audio"].(map[string]any)
	if in["format"] != "mp3" || in["data"] != base64.StdEncoding.EncodeToString(mp3) ||
		b["response_format"] != "verbose_json" || b["language"] != "en" {
		t.Errorf("transcription request = %v", b)
	}
	for _, want := range []string{"[0.00–1.20] hello there.", "[1.20–2.75] general kenobi", "$0.0010"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("result lacks %q:\n%s", want, res.Content)
		}
	}
}

// "A model that decides is sent the prompt as the state and the questions as
// given; the result is each question's answer."
func TestCallModelPutsQuestionsToADecidingModel(t *testing.T) {
	_, _, g, call := callModelApp(t)
	res := call(`{"model":"x/judge","prompt":"The nightly build fails on a nil map write in the scheduler.",` +
		`"questions":{"is_bug":{"type":"noul","instructions":"Is this a defect in our code?"},` +
		`"severity":{"type":"choice","instructions":"How bad is it?","criteria":{"low":"cosmetic","high":"data loss or crash"}}}}`)
	if !res.OK {
		t.Fatalf("call failed: %s (stderr %s)", res.Error, res.stderr)
	}
	sent := g.sent("/alpha/decisions")
	if len(sent) != 1 {
		t.Fatalf("decision requests = %d, want 1", len(sent))
	}
	b := sent[0].Body
	q, _ := b["questions"].(map[string]any)
	sev, _ := q["severity"].(map[string]any)
	if b["state"] != "The nightly build fails on a nil map write in the scheduler." || sev["type"] != "choice" {
		t.Errorf("decision request = %v", b)
	}
	for _, want := range []string{`"is_bug"`, `"noul": true`, `"choice": "high"`, "$0.0001"} {
		if !strings.Contains(res.Content, want) {
			t.Errorf("result lacks %s:\n%s", want, res.Content)
		}
	}
}

// "Listing a kind calls no model and returns that kind's models with their
// prices, and for speech their voices."
func TestCallModelListsTheModelsOfAKind(t *testing.T) {
	_, _, g, call := callModelApp(t)
	res := call(`{"list":"speech"}`)
	if !res.OK {
		t.Fatalf("list failed: %s (stderr %s)", res.Error, res.stderr)
	}
	if !strings.Contains(res.Content, "x/voice") || !strings.Contains(res.Content, "Kore") {
		t.Errorf("list = %q, want the speech model and its voices", res.Content)
	}
	if strings.Contains(res.Content, "x/writer") {
		t.Errorf("list = %q, holds a model of another kind", res.Content)
	}
	if n := g.posts(); n != 0 {
		t.Errorf("listing made %d calls", n)
	}
	if res := call(`{"list":"text","prompt":"writer"}`); !strings.Contains(res.Content, "x/writer") || strings.Contains(res.Content, "x/textonly") {
		t.Errorf("narrowed list = %q, want x/writer alone", res.Content)
	}
}

// "A file the model does not accept is refused before anything is sent, and the
// refusal names what the model accepts." And "a path outside the
// conversation's directory, or more than 20 MB of files, is refused before
// anything is sent."
func TestCallModelRefusesWhatItCannotSend(t *testing.T) {
	a, s, g, call := callModelApp(t)
	putFile(t, a, s.ID, "photo.png", []byte("\x89PNG"))
	putFile(t, a, s.ID, "huge.wav", bytes.Repeat([]byte{0}, 21<<20))
	if err := os.Symlink("/etc/hostname", filepath.Join(a.sessionWorkspace(s.ID), "away.txt")); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ args, says string }{
		{`{"model":"x/textonly","prompt":"what is this?","files":["photo.png"]}`, "accepts text"},
		{`{"model":"x/writer","prompt":"read it","files":["away.txt"]}`, "away.txt"},
		{`{"model":"x/writer","prompt":"read it","files":["../../etc/passwd"]}`, "etc/passwd"},
		{`{"model":"x/scribe","files":["huge.wav"]}`, "20 MB"},
		{`{"model":"x/nothing","prompt":"hi"}`, "x/nothing"},
	} {
		res := call(c.args)
		if res.OK || !strings.Contains(res.Error, c.says) {
			t.Errorf("%s: ok=%v error %q, want a refusal saying %q", c.args, res.OK, res.Error, c.says)
		}
	}
	if n := g.posts(); n != 0 {
		t.Errorf("%d calls reached the gateway; every one should have been refused first", n)
	}
}

// "The gateway's key is never given to the tool." The tool asks for no
// variable that holds it, and the call that reaches the gateway is the
// agent's, under its key.
func TestCallModelNeverHoldsTheKey(t *testing.T) {
	a, _, g, call := callModelApp(t)
	for _, name := range a.tools.Get("call_model").Agent.Env {
		if strings.Contains(name, "OPENROUTER") || strings.Contains(name, "KEY") {
			t.Errorf("call_model's manifest asks for %s", name)
		}
	}
	if res := call(`{"model":"x/textonly","prompt":"hi"}`); !res.OK {
		t.Fatalf("call failed: %s", res.Error)
	}
	if got := g.sent("/chat/completions")[0].Auth; got != "Bearer the-agents-own-key" {
		t.Errorf("the gateway was called with %q", got)
	}
}
