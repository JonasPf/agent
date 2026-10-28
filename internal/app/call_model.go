package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
	"time"
)

// call_model makes one call to another model for the conversation that asked
// (ADR-070). The tool names the files; the agent reads them from that
// conversation's directory, calls the gateway with its own key, writes audio
// back, and adds the cost to the conversation's.

// maxModelFiles is the most a call carries in files. They go to the gateway
// inline, as base64 in the request.
const maxModelFiles = 20 << 20

// listRows is the most models one listing names.
const listRows = 40

type modelCall struct {
	Model      string          `json:"model"`
	Prompt     string          `json:"prompt"`
	Files      []string        `json:"files"`
	Voice      string          `json:"voice"`
	Output     string          `json:"output"`
	Language   string          `json:"language"`
	Timestamps bool            `json:"timestamps"`
	Reasoning  string          `json:"reasoning"`
	Questions  json.RawMessage `json:"questions"`
	List       string          `json:"list"`
}

func (a *App) tModel(w http.ResponseWriter, r *http.Request) {
	var c modelCall
	if err := readJSON(r, &c); err != nil {
		fail(w, 400, "%v", err)
		return
	}
	id := caller(r).SessionID
	content, cost, err := a.callModel(r.Context(), id, c)
	if err != nil {
		fail(w, 400, "%v", err)
		return
	}
	if cost > 0 {
		_, _ = a.store.UpdateSession(id, func(x *Session) { x.Cost += cost })
		a.hub.Broadcast(wsEvent{Kind: "sessions"})
	}
	writeJSON(w, 200, map[string]string{"content": content})
}

// callModel does the call and says what came of it. A cost below zero is one
// the gateway did not report.
func (a *App) callModel(ctx context.Context, id string, c modelCall) (string, float64, error) {
	catalog, err := a.or.Catalog(ctx)
	if err != nil {
		return "", 0, fmt.Errorf("the model list could not be read: %v", err)
	}
	if c.List != "" {
		return listModels(catalog, c.List, c.Prompt), 0, nil
	}
	if c.Model == "" {
		return "", 0, fmt.Errorf("name a model, or set list to a kind to find one")
	}
	var m *CatalogModel
	for i := range catalog {
		if catalog[i].ID == c.Model {
			m = &catalog[i]
		}
	}
	if m == nil {
		return "", 0, fmt.Errorf("no model named %s; set list to text, speech, transcription, or decisions to find one", c.Model)
	}
	files, err := a.modelFiles(id, c.Files)
	if err != nil {
		return "", 0, err
	}
	switch m.Kind {
	case "text":
		return a.callWriter(ctx, m, c, files)
	case "speech":
		return a.callSpeaker(ctx, id, m, c, files)
	case "transcription":
		return a.callScribe(ctx, m, c, files)
	case "decisions":
		return a.callDecider(ctx, m, c, files)
	}
	return "", 0, fmt.Errorf("%s produces %s, which call_model does not call", m.ID, m.Kind)
}

// modelFile is a file a call sends: what it is, and its bytes.
type modelFile struct {
	Path, Kind, Ext string
	Data            []byte
}

// fileKinds is what a file is by its extension, as the gateway takes it.
var fileKinds = map[string]string{
	"png": "image", "jpg": "image", "jpeg": "image", "gif": "image", "webp": "image",
	"wav": "audio", "mp3": "audio", "flac": "audio", "m4a": "audio", "ogg": "audio", "aac": "audio", "aiff": "audio",
	"mp4": "video", "mov": "video", "webm": "video", "mpeg": "video", "mpg": "video", "mkv": "video", "avi": "video",
	"pdf": "file",
	"txt": "text", "md": "text", "csv": "text", "tsv": "text", "json": "text", "srt": "text", "vtt": "text",
	"yaml": "text", "yml": "text", "xml": "text", "html": "text", "log": "text",
}

var mediaTypes = map[string]string{
	"png": "image/png", "jpg": "image/jpeg", "jpeg": "image/jpeg", "gif": "image/gif", "webp": "image/webp",
	"mp4": "video/mp4", "mov": "video/quicktime", "webm": "video/webm", "mpeg": "video/mpeg", "mpg": "video/mpeg",
	"mkv": "video/x-matroska", "avi": "video/x-msvideo",
}

// modelFiles reads the files a call names from the conversation's directory,
// through a root, so a path that climbs out or a link that leads out names
// nothing. Their sizes are added up before anything is read.
func (a *App) modelFiles(id string, paths []string) ([]modelFile, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	root, err := a.workspaceRoot(id)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	var total int64
	var out []modelFile
	for _, p := range paths {
		rel, err := workspacePath(id, p)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", p, err)
		}
		info, err := root.Stat(rel)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("%s is not a file in this conversation's directory", rel)
		}
		ext := strings.ToLower(strings.TrimPrefix(path.Ext(rel), "."))
		kind := fileKinds[ext]
		if kind == "" {
			return nil, fmt.Errorf("%s: a .%s file is not something a model is sent; convert it first", rel, ext)
		}
		total += info.Size()
		out = append(out, modelFile{Path: rel, Kind: kind, Ext: ext})
	}
	if total > maxModelFiles {
		return nil, fmt.Errorf("the files come to %.1f MB, and a call carries at most 20 MB; "+
			"send a recording as its audio, extracted with ffmpeg, or cut it into parts", float64(total)/(1<<20))
	}
	for i := range out {
		f, err := root.Open(out[i].Path)
		if err != nil {
			return nil, err
		}
		out[i].Data, err = io.ReadAll(f)
		f.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func refuseFile(m *CatalogModel, f modelFile) error {
	return fmt.Errorf("%s accepts %s, and %s is %s", m.ID, strings.Join(m.Inputs, ", "), f.Path, article(f.Kind))
}

func article(kind string) string {
	switch kind {
	case "image":
		return "an image"
	case "audio":
		return "audio"
	case "file":
		return "a PDF"
	}
	return "a " + kind
}

func costLine(model string, cost float64) string {
	if cost < 0 {
		return "\n\n— " + model + " · cost not reported"
	}
	return fmt.Sprintf("\n\n— %s · $%.4f", model, cost)
}

func (a *App) callWriter(ctx context.Context, m *CatalogModel, c modelCall, files []modelFile) (string, float64, error) {
	if strings.TrimSpace(c.Prompt) == "" && len(files) == 0 {
		return "", 0, fmt.Errorf("%s writes, and is sent nothing but this call: put the whole request in prompt", m.ID)
	}
	parts := []map[string]any{}
	if c.Prompt != "" {
		parts = append(parts, map[string]any{"type": "text", "text": c.Prompt})
	}
	for _, f := range files {
		if f.Kind != "text" && !contains(m.Inputs, f.Kind) {
			return "", 0, refuseFile(m, f)
		}
		b64 := base64.StdEncoding.EncodeToString(f.Data)
		switch f.Kind {
		case "image":
			parts = append(parts, map[string]any{"type": "image_url",
				"image_url": map[string]string{"url": "data:" + mediaTypes[f.Ext] + ";base64," + b64}})
		case "audio":
			parts = append(parts, map[string]any{"type": "input_audio",
				"input_audio": map[string]string{"data": b64, "format": f.Ext}})
		case "video":
			parts = append(parts, map[string]any{"type": "video_url",
				"video_url": map[string]string{"url": "data:" + mediaTypes[f.Ext] + ";base64," + b64}})
		case "file":
			parts = append(parts, map[string]any{"type": "file",
				"file": map[string]string{"filename": path.Base(f.Path), "file_data": "data:application/pdf;base64," + b64}})
		case "text":
			parts = append(parts, map[string]any{"type": "text", "text": "Contents of " + f.Path + ":\n\n" + string(f.Data)})
		}
	}
	req := ChatRequest{Model: m.ID, Messages: []ChatMessage{{Role: "user", Content: parts}}}
	switch c.Reasoning {
	case "":
	case "low", "medium", "high":
		req.Reasoning = &ReasoningOpt{Effort: c.Reasoning}
	default:
		return "", 0, fmt.Errorf("reasoning is low, medium, or high, not %q", c.Reasoning)
	}
	res, err := a.or.Chat(ctx, req, nil)
	if err != nil {
		return "", 0, err
	}
	return res.Text + costLine(m.ID, res.Usage.Cost), res.Usage.Cost, nil
}

func (a *App) callSpeaker(ctx context.Context, id string, m *CatalogModel, c modelCall, files []modelFile) (string, float64, error) {
	if len(files) > 0 {
		return "", 0, fmt.Errorf("%s speaks the prompt and is sent no files", m.ID)
	}
	if strings.TrimSpace(c.Prompt) == "" {
		return "", 0, fmt.Errorf("a model that speaks needs the words to say, in prompt")
	}
	if c.Voice != "" && len(m.Voices) > 0 && !contains(m.Voices, c.Voice) {
		return "", 0, fmt.Errorf("%s has no voice %q; its voices are %s", m.ID, c.Voice, strings.Join(m.Voices, ", "))
	}
	audio, format, gen, err := a.or.Speak(ctx, m.ID, c.Prompt, c.Voice)
	if err != nil {
		return "", 0, err
	}
	// The file is named for what came back: a model that gives raw samples
	// only is written as a WAV, under the name asked for with its extension.
	out := c.Output
	if out == "" {
		out = "speech-" + time.Now().UTC().Format("20060102-150405") + "." + format
	} else if ext := path.Ext(out); format != "mp3" && strings.EqualFold(ext, ".mp3") {
		out = strings.TrimSuffix(out, ext) + "." + format
	}
	landed, n, err := a.writeWorkspaceFile(id, out, bytes.NewReader(audio))
	if err != nil {
		return "", 0, err
	}
	go a.addCostWhenReported(id, gen)
	return fmt.Sprintf("Wrote %s (%d bytes, %s).", landed, n, format) +
		"\n\n— " + m.ID + " · cost added to this conversation's once the gateway reports it, in about 15 seconds", 0, nil
}

// addCostWhenReported adds what a generation cost to a conversation's, once
// the gateway can say: within two minutes, or not at all.
func (a *App) addCostWhenReported(id, gen string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if cost := a.or.GenerationCost(ctx, gen); cost > 0 {
		_, _ = a.store.UpdateSession(id, func(x *Session) { x.Cost += cost })
		a.hub.Broadcast(wsEvent{Kind: "sessions"})
	}
}

func (a *App) callScribe(ctx context.Context, m *CatalogModel, c modelCall, files []modelFile) (string, float64, error) {
	if len(files) != 1 || files[0].Kind != "audio" {
		return "", 0, fmt.Errorf("%s transcribes one audio file; send a video as its audio, extracted with ffmpeg", m.ID)
	}
	t, err := a.or.Transcribe(ctx, m.ID, files[0].Data, files[0].Ext, c.Language, c.Timestamps)
	if err != nil {
		return "", 0, err
	}
	var b strings.Builder
	if c.Timestamps && len(t.Segments) > 0 {
		for _, s := range t.Segments {
			fmt.Fprintf(&b, "[%.2f–%.2f] %s\n", s.Start, s.End, strings.TrimSpace(s.Text))
		}
	} else {
		b.WriteString(t.Text)
		if t.Untimed || c.Timestamps {
			b.WriteString("\n\n(no times: the provider that answered gives none)")
		}
	}
	return strings.TrimRight(b.String(), "\n") + costLine(m.ID, t.Cost), t.Cost, nil
}

func (a *App) callDecider(ctx context.Context, m *CatalogModel, c modelCall, files []modelFile) (string, float64, error) {
	if len(files) > 0 {
		return "", 0, fmt.Errorf("%s decides on the prompt and is sent no files", m.ID)
	}
	if strings.TrimSpace(c.Prompt) == "" || len(bytes.TrimSpace(c.Questions)) == 0 {
		return "", 0, fmt.Errorf("a model that decides needs the state, in prompt, and questions")
	}
	answers, cost, err := a.or.Decide(ctx, m.ID, c.Prompt, c.Questions)
	if err != nil {
		return "", 0, err
	}
	var pretty bytes.Buffer
	if json.Indent(&pretty, answers, "", "  ") != nil {
		pretty.Reset()
		pretty.Write(answers)
	}
	return pretty.String() + costLine(m.ID, cost), cost, nil
}

// listModels names the models of a kind, narrowed by every word of query
// appearing in the id or name, with what they cost and, for speech, their
// voices. Prices are as the gateway lists them, per million of what it
// counts: tokens, or for most speech models characters.
func listModels(catalog []CatalogModel, kind, query string) string {
	terms := strings.Fields(strings.ToLower(query))
	var rows []string
	for _, m := range catalog {
		if m.Kind != kind || !matchesAll(strings.ToLower(m.ID+" "+m.Name), terms) {
			continue
		}
		row := fmt.Sprintf("%s — %s · takes %s · $%.2f in, $%.2f out per million",
			m.ID, m.Name, strings.Join(m.Inputs, ", "), m.PromptPrice*1e6, m.CompPrice*1e6)
		if len(m.Voices) > 0 {
			row += " · voices: " + strings.Join(m.Voices, ", ")
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return fmt.Sprintf("No %s models match. The kinds are text, speech, transcription, and decisions.", kind)
	}
	more := ""
	if len(rows) > listRows {
		more = fmt.Sprintf("\n… and %d more; narrow the list with words in prompt.", len(rows)-listRows)
		rows = rows[:listRows]
	}
	return strings.Join(rows, "\n") + more
}
