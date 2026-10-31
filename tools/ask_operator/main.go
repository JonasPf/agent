// ask_operator records what the agent wants to ask and returns a link to the
// screen that asks it. The screen is this tool's panel, in ui/panel.js. Nothing
// waits here: the turn ends, and the answer arrives as the operator's next
// message. The panel also calls this tool to save what a page hands it, which is
// the one way a page can write anything (ADR-074).
package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"agent/internal/tool"
)

// maxPage is the largest page the panel is asked to load. A page carries its
// own images, so it can be large, and it is read whole into the interface.
const maxPage = 16 << 20

type question struct {
	Name     string   `json:"name"`
	Label    string   `json:"label,omitempty"`
	Type     string   `json:"type"`
	Options  []string `json:"options,omitempty"`
	Default  string   `json:"default,omitempty"`
	Required bool     `json:"required,omitempty"`
}

type ask struct {
	Message   string     `json:"message"`
	Questions []question `json:"questions,omitempty"`
	Page      string     `json:"page,omitempty"`
}

type args struct {
	ask
	// What the panel sends to save a file for a page. The model is not told of
	// these: it has its own tools for writing files.
	Ask     string  `json:"ask,omitempty"`
	Save    string  `json:"save,omitempty"`
	Content *string `json:"content,omitempty"`
}

var (
	validID = regexp.MustCompile(`^[0-9a-z-]+$`)
	types   = map[string]bool{"text": true, "long_text": true, "number": true,
		"yes_no": true, "choice": true, "choices": true}
)

// check says why an ask cannot be shown. A page's path has already been resolved
// to page, the file on disk.
func check(a ask, page string) error {
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
		case !types[q.Type]:
			return fmt.Errorf("question %q has type %q; the types are text, long_text, number, yes_no, choice, and choices", q.Name, q.Type)
		case (q.Type == "choice" || q.Type == "choices") && len(q.Options) == 0:
			return fmt.Errorf("question %q is a %s with no options", q.Name, q.Type)
		}
		seen[q.Name] = true
	}
	if a.Page == "" {
		return nil
	}
	if !strings.EqualFold(filepath.Ext(a.Page), ".html") {
		return fmt.Errorf("page must be an .html file, not %s", a.Page)
	}
	info, err := os.Stat(page)
	if err != nil || info.IsDir() {
		return fmt.Errorf("%s is not a file in the working directory: write it first", a.Page)
	}
	if info.Size() > maxPage {
		return fmt.Errorf("%s is %d MB, over the %d MB a page can be: use smaller images", a.Page, info.Size()>>20, maxPage>>20)
	}
	return nil
}

// newID names an ask. Its time keeps a folder of them in order.
func newID(now time.Time) string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return now.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b)
}

// record stores an ask where the panel reads it, in the working directory: it is
// the agent's own words, in the place the operator can already see.
func record(root, id string, a ask) error {
	dir := filepath.Join(root, "asks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, id+".json"), b, 0o644)
}

// saveBeside writes what a page handed the panel, in the page's own folder and
// nowhere else.
func saveBeside(root, id, name string, content string) (string, error) {
	if !validID.MatchString(id) {
		return "", fmt.Errorf("no ask %q", id)
	}
	raw, err := os.ReadFile(filepath.Join(root, "asks", id+".json"))
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
	dir := filepath.Join(root, filepath.Dir(a.Page))
	if filepath.IsAbs(name) {
		return "", fmt.Errorf("path %q is outside the page's folder", name)
	}
	full, err := tool.ResolveInside(dir, name)
	if err != nil {
		return "", fmt.Errorf("path %q is outside the page's folder", name)
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, full)
	if err != nil {
		return "", err
	}
	return rel, nil
}

func main() {
	var a args
	tool.Args(&a)

	// Resolved, because a path checked against the workspace comes back resolved.
	root, err := filepath.EvalSymlinks(tool.Workspace)
	if err != nil {
		tool.Failf("%v", err)
	}
	if a.Save != "" {
		if a.Content == nil {
			tool.Failf("content is required")
		}
		saved, err := saveBeside(root, a.Ask, a.Save, *a.Content)
		if err != nil {
			tool.Failf("%v", err)
		}
		tool.OK(saved)
	}

	page := ""
	if a.Page != "" {
		page = tool.InsideWorkspace(a.Page)
		rel, err := filepath.Rel(root, page)
		if err != nil {
			tool.Failf("%v", err)
		}
		a.Page = rel
	}
	if err := check(a.ask, page); err != nil {
		tool.Failf("%v", err)
	}
	id := newID(time.Now())
	if err := record(root, id, a.ask); err != nil {
		tool.Failf("could not record the question: %v", err)
	}
	link := "#toolpanel/ask_operator/" + tool.Session + "/" + id
	tool.OKf("Ready. Put this link in your reply and end your turn: [Open](%s)\n"+
		"Nothing is waiting here. The operator's answer arrives as their next message.", link)
}
