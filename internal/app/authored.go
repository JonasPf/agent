package app

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// An authored document is prose the operator writes: a skill, or a persona. Both
// are a Markdown file with a frontmatter block naming it, and both are read from
// two roots.
//
// The first root ships in the image and is read-only: replacing the image
// replaces what is in it, so a document written there would be lost on the next
// deployment without anything saying so. The second is beside the data, which is
// the one directory a deployment keeps, and is what the interface writes.
//
// Neither root is writable by a tool. The sandbox grants a tool its own session's
// directory and the database's, and nothing else — so "the operator writes these,
// the agent does not" is the boundary that already exists rather than a new rule
// asking to be trusted (ADR-053).
type authored struct {
	Name        string
	Description string
	Body        string
	// Fields is the frontmatter as written, so a kind that reads more than a
	// name and a description does not need its own parser.
	Fields map[string]string
	Bytes  int
	Path   string
	// Editable is whether this document came from the operator's root. A shipped
	// one is refused an edit rather than silently copied into the writable root,
	// because two documents of the same name is the one state nothing on screen
	// could explain.
	Editable bool
}

// parseFrontmatter splits a document into its frontmatter fields and its body. A
// file that does not open with a frontmatter block is not a document of this
// kind, which is different from one whose fields are wrong.
func parseFrontmatter(text string) (map[string]string, string, error) {
	if !strings.HasPrefix(text, "---") {
		return nil, "", errFrontmatter
	}
	rest := text[3:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return nil, "", errFrontmatter
	}
	head, body := rest[:end], strings.TrimPrefix(rest[end+4:], "\n")
	fields := map[string]string{}
	for _, line := range strings.Split(head, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields[strings.TrimSpace(k)] = strings.TrimSpace(v)
	}
	return fields, body, nil
}

// renderAuthored writes a document back out in the form it is read in, so a file
// the interface saved and a file the operator typed are the same file.
func renderAuthored(name, description string, fields map[string]string, body string) []byte {
	var sb strings.Builder
	sb.WriteString("---\nname: " + name + "\ndescription: " + description + "\n")
	// Sorted, so saving a document twice without changing it produces the same
	// bytes and a diff says nothing happened.
	keys := make([]string, 0, len(fields))
	for k := range fields {
		if k != "name" && k != "description" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		sb.WriteString(k + ": " + fields[k] + "\n")
	}
	sb.WriteString("---\n\n" + strings.TrimLeft(body, "\n"))
	if !strings.HasSuffix(sb.String(), "\n") {
		sb.WriteString("\n")
	}
	return []byte(sb.String())
}

// loadAuthoredDir reads every document in one root. A file that will not parse is
// skipped and the failure stays visible, so a document that is not there is
// explained on screen rather than simply missing.
func loadAuthoredDir(dir string, editable bool) ([]*authored, []LoadFailure) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		// A root that does not exist holds no documents, which is the ordinary
		// state of the operator's root before anything is written to it.
		return nil, nil
	}
	var docs []*authored
	var failures []LoadFailure
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			failures = append(failures, LoadFailure{Dir: e.Name(), Reason: err.Error()})
			continue
		}
		fields, body, err := parseFrontmatter(string(b))
		if err != nil {
			failures = append(failures, LoadFailure{Dir: e.Name(), Reason: err.Error()})
			continue
		}
		if fields["name"] == "" || fields["description"] == "" {
			failures = append(failures, LoadFailure{Dir: e.Name(), Reason: errFrontmatter.Error()})
			continue
		}
		docs = append(docs, &authored{
			Name: fields["name"], Description: fields["description"], Body: body,
			Fields: fields, Bytes: len(b), Path: path, Editable: editable,
		})
	}
	return docs, failures
}

type frontmatterError struct{}

func (frontmatterError) Error() string { return "malformed frontmatter: needs name and description" }

var errFrontmatter = frontmatterError{}

// validAuthoredName is what may become a file name in the operator's root. The
// name is the identifier, the file is named after it, and a name that could
// climb out of the directory would write anywhere the agent can.
func validAuthoredName(name string) error {
	if name == "" {
		return fmt.Errorf("a name is required")
	}
	if len(name) > 64 {
		return fmt.Errorf("a name is at most 64 characters")
	}
	for _, r := range name {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-' || r == '_') {
			return fmt.Errorf("a name holds letters, digits, hyphens, and underscores; %q does not", name)
		}
	}
	return nil
}

// saveAuthored writes one document into the operator's root.
func saveAuthored(dir, name, description string, fields map[string]string, body string) error {
	if err := validAuthoredName(name); err != nil {
		return err
	}
	if strings.TrimSpace(description) == "" {
		return fmt.Errorf("a description is required: it is the one line that says when to use this")
	}
	if strings.ContainsAny(description, "\r\n") {
		return fmt.Errorf("a description is a single line")
	}
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("a body is required")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, name+".md")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, renderAuthored(name, description, fields, body), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// deleteAuthored removes one document from the operator's root.
func deleteAuthored(dir, name string) error {
	if err := validAuthoredName(name); err != nil {
		return err
	}
	return os.Remove(filepath.Join(dir, name+".md"))
}
