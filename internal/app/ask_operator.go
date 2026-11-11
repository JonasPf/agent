package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ask_operator records what the agent wants to ask and returns a link to the
// screen that asks it, web/panels/ask_operator.js. Nothing waits here: the turn
// ends, and the answer arrives as the operator's next message. The panel also
// calls this tool to save what a page hands it, which is the one way a page can
// write anything (ADR-074).
//
// It runs inside the agent (ADR-086), so no sandbox stands between it and the
// rest of the disk. Every file it touches is reached through the conversation's
// directory opened as a root, which refuses a path, or a link on the way, that
// leads out.

// maxAskPage is the largest page the panel is asked to load. A page carries its
// own images, so it can be large, and it is read whole into the interface.
const maxAskPage = 16 << 20

type askQuestion struct {
	Name     string   `json:"name"`
	Label    string   `json:"label,omitempty"`
	Type     string   `json:"type"`
	Options  []string `json:"options,omitempty"`
	Default  string   `json:"default,omitempty"`
	Required bool     `json:"required,omitempty"`
}

type ask struct {
	Message   string        `json:"message"`
	Questions []askQuestion `json:"questions,omitempty"`
	Page      string        `json:"page,omitempty"`
}

var (
	validAskID = regexp.MustCompile(`^[0-9a-z-]+$`)
	askTypes   = map[string]bool{"text": true, "long_text": true, "number": true,
		"yes_no": true, "choice": true, "choices": true}
)

func (a *App) askOperatorTool(ctx context.Context, tc *ToolCtx, args json.RawMessage) (any, error) {
	var in struct {
		ask
		// What the panel sends to save a file for a page. The model is not told
		// of these: it has its own tools for writing files.
		Ask     string  `json:"ask,omitempty"`
		Save    string  `json:"save,omitempty"`
		Content *string `json:"content,omitempty"`
	}
	if err := toolArgs(args, &in); err != nil {
		return nil, err
	}
	root, err := a.workspaceRoot(tc.SessionID)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	defer a.workspaceChanged(tc.SessionID)

	if in.Save != "" {
		if in.Content == nil {
			return nil, fmt.Errorf("content is required")
		}
		return saveBesideAsk(root, in.Ask, in.Save, *in.Content)
	}

	if in.Page != "" {
		rel, err := a.askPage(tc.SessionID, in.Page)
		if err != nil {
			return nil, err
		}
		in.Page = rel
	}
	if err := checkAsk(root, in.ask); err != nil {
		return nil, err
	}
	id := newAskID(time.Now())
	body, err := json.MarshalIndent(in.ask, "", "  ")
	if err == nil {
		err = root.MkdirAll("asks", 0o755)
	}
	if err == nil {
		err = root.WriteFile(path.Join("asks", id+".json"), body, 0o644)
	}
	if err != nil {
		return nil, fmt.Errorf("could not record the question: %v", err)
	}
	link := "#toolpanel/ask_operator/" + tc.SessionID + "/" + id
	return fmt.Sprintf("Ready. Put this link in your reply and end your turn: [Open](%s)\n"+
		"Nothing is waiting here. The operator's answer arrives as their next message.", link), nil
}

// askPage is a page as the model named it, relative to the conversation's
// directory. The model may give the path it wrote the file at, which is
// absolute; one that is not under the directory is refused.
func (a *App) askPage(sessionID, page string) (string, error) {
	outside := fmt.Errorf("%s is outside the working directory", page)
	if filepath.IsAbs(page) {
		dir := absOr(a.ensureWorkspace(sessionID))
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			dir = real
		}
		rel, err := filepath.Rel(dir, filepath.Clean(page))
		if err != nil || !filepath.IsLocal(rel) {
			return "", outside
		}
		return filepath.ToSlash(rel), nil
	}
	if !filepath.IsLocal(page) {
		return "", outside
	}
	return path.Clean(filepath.ToSlash(page)), nil
}

// checkAsk says why an ask cannot be shown.
func checkAsk(root *os.Root, a ask) error {
	if strings.TrimSpace(a.Message) == "" {
		return fmt.Errorf("message is required: say what you need and why")
	}
	if (len(a.Questions) == 0) == (a.Page == "") {
		return fmt.Errorf("give questions or page, one of the two")
	}
	seen := map[string]bool{}
	for i, q := range a.Questions {
		switch {
		case strings.TrimSpace(q.Name) == "":
			return fmt.Errorf("question %d has no name", i+1)
		case seen[q.Name]:
			return fmt.Errorf("two questions are named %q", q.Name)
		case !askTypes[q.Type]:
			return fmt.Errorf("question %q has type %q; the types are text, long_text, number, yes_no, choice, and choices", q.Name, q.Type)
		case (q.Type == "choice" || q.Type == "choices") && len(q.Options) == 0:
			return fmt.Errorf("question %q is a %s with no options", q.Name, q.Type)
		}
		seen[q.Name] = true
	}
	if a.Page == "" {
		return nil
	}
	if !strings.EqualFold(path.Ext(a.Page), ".html") {
		return fmt.Errorf("page must be an .html file, not %s", a.Page)
	}
	// Through the root: a link that leads out of the directory is not a file
	// in it, whatever it points at.
	info, err := root.Stat(a.Page)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a file in the working directory: write it first", a.Page)
	}
	if info.Size() > maxAskPage {
		return fmt.Errorf("%s is %d MB, over the %d MB a page can be: use smaller images", a.Page, info.Size()>>20, maxAskPage>>20)
	}
	return nil
}

// newAskID names an ask. Its time keeps a folder of them in order.
func newAskID(now time.Time) string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return now.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}

// saveBesideAsk writes what a page handed the panel, in the page's own folder
// and nowhere else.
func saveBesideAsk(root *os.Root, id, name, content string) (string, error) {
	if !validAskID.MatchString(id) {
		return "", fmt.Errorf("no ask %q", id)
	}
	raw, err := root.ReadFile(path.Join("asks", id+".json"))
	if err != nil {
		return "", fmt.Errorf("no ask %q", id)
	}
	var a ask
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", err
	}
	if a.Page == "" {
		return "", fmt.Errorf("ask %s has no page to save beside", id)
	}
	if !filepath.IsLocal(name) {
		return "", fmt.Errorf("path %q is outside the page's folder", name)
	}
	full := path.Join(path.Dir(a.Page), filepath.ToSlash(name))
	if err := root.MkdirAll(path.Dir(full), 0o755); err != nil {
		return "", err
	}
	if err := root.WriteFile(full, []byte(content), 0o644); err != nil {
		return "", err
	}
	return full, nil
}
