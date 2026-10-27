package app

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The gateway serves more than models that write. A model that speaks, one
// that transcribes, and one that decides each have an endpoint of their own,
// and call_model reaches them through these (ADR-070).

// CatalogModel is a model of any kind the gateway lists. Kind is what it
// produces, and decides how it is called: text, speech, transcription,
// decisions, or another kind call_model does not call.
type CatalogModel struct {
	ID          string
	Name        string
	Kind        string
	Inputs      []string
	Voices      []string
	PromptPrice float64
	CompPrice   float64
}

// kindOf reads what a model produces as the one kind it is called as. A model
// that writes and also speaks in a chat (gpt-audio) is a model that writes.
func kindOf(outputs []string) string {
	for _, k := range []string{"speech", "transcription", "decisions", "text"} {
		if contains(outputs, k) {
			return k
		}
	}
	if len(outputs) > 0 {
		return outputs[0]
	}
	return "unknown"
}

// Catalog returns every model the gateway lists, cached and refreshed daily.
func (o *OpenRouter) Catalog(ctx context.Context) ([]CatalogModel, error) {
	o.mu.Lock()
	if time.Since(o.catalogAt) < 24*time.Hour && len(o.catalog) > 0 {
		c := o.catalog
		o.mu.Unlock()
		return c, nil
	}
	o.mu.Unlock()
	resp, err := o.do(ctx, "GET", "/models?output_modalities=all", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, gatewayError(resp)
	}
	var payload struct {
		Data []struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			Architecture struct {
				InputModalities  []string `json:"input_modalities"`
				OutputModalities []string `json:"output_modalities"`
			} `json:"architecture"`
			Voices  []string `json:"supported_voices"`
			Pricing struct {
				Prompt     string `json:"prompt"`
				Completion string `json:"completion"`
			} `json:"pricing"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	out := make([]CatalogModel, 0, len(payload.Data))
	for _, m := range payload.Data {
		p, _ := strconv.ParseFloat(m.Pricing.Prompt, 64)
		c, _ := strconv.ParseFloat(m.Pricing.Completion, 64)
		out = append(out, CatalogModel{ID: m.ID, Name: m.Name, Kind: kindOf(m.Architecture.OutputModalities),
			Inputs: m.Architecture.InputModalities, Voices: m.Voices, PromptPrice: p, CompPrice: c})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	o.mu.Lock()
	o.catalog, o.catalogAt = out, time.Now()
	o.mu.Unlock()
	return out, nil
}

// gatewayError reads a refusal: the gateway's own words where it gave some.
func gatewayError(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil && e.Error.Message != "" {
		return fmt.Errorf("the gateway refused (%d): %s", resp.StatusCode, e.Error.Message)
	}
	return fmt.Errorf("the gateway refused (%d): %s", resp.StatusCode, strings.TrimSpace(string(b)))
}

// Speak has a model that speaks say text, and returns the audio, its format
// (mp3 or wav), and the generation it names. It asks for mp3; a model that
// gives raw samples only refuses that by naming pcm, and is asked again for
// them, which come back described by their media type and are wrapped as a
// WAV. The audio comes back bare: what it cost is asked for afterwards, by the
// generation (GenerationCost).
func (o *OpenRouter) Speak(ctx context.Context, model, text, voice string) ([]byte, string, string, error) {
	audio, format, gen, err := o.speakAs(ctx, model, text, voice, "mp3")
	if err != nil && strings.Contains(err.Error(), "pcm") {
		audio, format, gen, err = o.speakAs(ctx, model, text, voice, "pcm")
	}
	return audio, format, gen, err
}

func (o *OpenRouter) speakAs(ctx context.Context, model, text, voice, format string) ([]byte, string, string, error) {
	body := map[string]any{"model": model, "input": text, "response_format": format}
	if voice != "" {
		body["voice"] = voice
	}
	resp, err := o.do(ctx, "POST", "/audio/speech", body)
	if err != nil {
		return nil, "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, "", "", gatewayError(resp)
	}
	audio, err := io.ReadAll(io.LimitReader(resp.Body, maxUpload))
	if err != nil {
		return nil, "", "", err
	}
	gen := resp.Header.Get("X-Generation-Id")
	if format != "pcm" {
		return audio, format, gen, nil
	}
	rate, channels := pcmFormat(resp.Header.Get("Content-Type"))
	return wavOf(audio, rate, channels), "wav", gen, nil
}

// pcmFormat reads the sample rate and channels from a raw-audio media type,
// audio/pcm;rate=24000;channels=1. What it leaves out is what the speech
// endpoints default to: 24 kHz, one channel.
func pcmFormat(mediaType string) (rate, channels int) {
	rate, channels = 24000, 1
	_, params, err := mime.ParseMediaType(mediaType)
	if err != nil {
		return rate, channels
	}
	if n, err := strconv.Atoi(params["rate"]); err == nil && n > 0 {
		rate = n
	}
	if n, err := strconv.Atoi(params["channels"]); err == nil && n > 0 {
		channels = n
	}
	return rate, channels
}

// wavOf wraps 16-bit little-endian samples in the header that makes them a
// WAV file anything plays.
func wavOf(pcm []byte, rate, channels int) []byte {
	const bits = 16
	block := channels * bits / 8
	h := make([]byte, 44)
	le32 := func(at int, v int) { binary.LittleEndian.PutUint32(h[at:], uint32(v)) }
	le16 := func(at int, v int) { binary.LittleEndian.PutUint16(h[at:], uint16(v)) }
	copy(h[0:], "RIFF")
	le32(4, 36+len(pcm))
	copy(h[8:], "WAVEfmt ")
	le32(16, 16)
	le16(20, 1)
	le16(22, channels)
	le32(24, rate)
	le32(28, rate*block)
	le16(32, block)
	le16(34, bits)
	copy(h[36:], "data")
	le32(40, len(pcm))
	return append(h, pcm...)
}

// GenerationCost asks what a generation cost. The gateway records speech some
// fifteen seconds after answering, so one not found yet is asked about again
// until ctx ends. A cost it never reports is unknown, and negative, rather
// than free.
func (o *OpenRouter) GenerationCost(ctx context.Context, id string) float64 {
	if id == "" {
		return -1
	}
	for attempt := 0; ; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return -1
			case <-time.After(o.retryBase):
			}
		}
		resp, err := o.do(ctx, "GET", "/generation?id="+url.QueryEscape(id), nil)
		if err != nil {
			return -1
		}
		// The gateway answers with the cost under "usage"; "total_cost" is
		// read too, as the name its documentation has used.
		var g struct {
			Data struct {
				Usage     *float64 `json:"usage"`
				TotalCost *float64 `json:"total_cost"`
			} `json:"data"`
		}
		ok := resp.StatusCode == 200 && json.NewDecoder(resp.Body).Decode(&g) == nil
		resp.Body.Close()
		if ok && g.Data.Usage != nil {
			return *g.Data.Usage
		}
		if ok && g.Data.TotalCost != nil {
			return *g.Data.TotalCost
		}
	}
}

// Segment is a stretch of a transcript and when it was said, in seconds.
type Segment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

type Transcript struct {
	Text     string
	Segments []Segment
	Cost     float64
	// Untimed is set when times were asked for and the provider the gateway
	// chose does not give them, so the text came back without.
	Untimed bool
}

// Transcribe has a model that transcribes write down a recording. Asked for
// times, it asks for segments; a provider that has none refuses that, and
// the transcript is fetched again without them rather than not at all.
func (o *OpenRouter) Transcribe(ctx context.Context, model string, audio []byte, format, language string, timed bool) (*Transcript, error) {
	body := map[string]any{"model": model,
		"input_audio": map[string]string{"data": base64.StdEncoding.EncodeToString(audio), "format": format}}
	if language != "" {
		body["language"] = language
	}
	if timed {
		body["response_format"] = "verbose_json"
		body["timestamp_granularities"] = []string{"segment"}
	}
	resp, err := o.do(ctx, "POST", "/audio/transcriptions", body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 400 && timed {
		t, err := o.Transcribe(ctx, model, audio, format, language, false)
		if t != nil {
			t.Untimed = true
		}
		return t, err
	}
	if resp.StatusCode != 200 {
		return nil, gatewayError(resp)
	}
	var r struct {
		Text     string    `json:"text"`
		Segments []Segment `json:"segments"`
		Usage    struct {
			Cost float64 `json:"cost"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	return &Transcript{Text: r.Text, Segments: r.Segments, Cost: r.Usage.Cost}, nil
}

// Decide puts typed questions about a state to a model that decides, and
// returns its answers as the gateway gave them.
func (o *OpenRouter) Decide(ctx context.Context, model, state string, questions json.RawMessage) (json.RawMessage, float64, error) {
	endpoint := strings.TrimSuffix(o.base, "/v1") + "/alpha/decisions"
	resp, err := o.doURL(ctx, "POST", endpoint, map[string]any{"model": model, "state": state, "questions": questions})
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, 0, gatewayError(resp)
	}
	var r struct {
		Answers json.RawMessage `json:"answers"`
		Usage   struct {
			Cost float64 `json:"cost"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, 0, err
	}
	return r.Answers, r.Usage.Cost, nil
}
