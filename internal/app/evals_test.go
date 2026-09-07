package app

import (
	"os"
	"path/filepath"
	"testing"
)

// A tool's description is the surface a model programmes against, and nothing
// deterministic can tell whether it reads correctly. So every tool ships eval
// cases, and this is what makes "every" true: add a tool without them and the
// test run says so, rather than the omission being noticed months later.
func TestEveryToolShipsEvalCases(t *testing.T) {
	suites, err := LoadEvalSuites("../../tools")
	if err != nil {
		t.Fatalf("%v\n\nevery tool directory needs an %s; see specs/tools.html", err, evalFile)
	}
	dirs, err := os.ReadDir("../../tools")
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, d := range dirs {
		if d.IsDir() {
			want = append(want, d.Name())
		}
	}
	if len(suites) != len(want) {
		t.Fatalf("%d suites for %d tools", len(suites), len(want))
	}
	for _, s := range suites {
		if len(s.Cases) == 0 {
			t.Errorf("%s ships no eval cases", s.Tool)
		}
	}
}

func TestLoadEvalSuiteRejectsIncompleteFiles(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"no cases", `{"cases":[]}`},
		{"case without a prompt", `{"cases":[{"name":"x"}]}`},
		{"case without a name", `{"cases":[{"prompt":"do a thing"}]}`},
		{"not json", `{`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "atool")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, evalFile), []byte(c.body), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := loadEvalSuite(dir); err == nil {
				t.Error("want an error, got none")
			}
		})
	}
}

func TestMissingEvalFileIsAnError(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "bare")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := loadEvalSuite(dir); err == nil {
		t.Error("a tool directory with no eval.json must be an error")
	}
}

// The expectation language decides whether an eval passed, so it is worth being
// sure about. These run without a model.
func TestExpectCheck(t *testing.T) {
	rec := TurnRecord{
		Calls: []RecordedCall{
			{Name: "clock", Args: map[string]any{"offset": "2m"}},
			{Name: "schedule", Args: map[string]any{"action": "create", "schedule": "10m", "repeat": false}},
		},
	}
	dirty := TurnRecord{Calls: rec.Calls, Errors: []string{"schedule: floor"}}
	probed := TurnRecord{Calls: rec.Calls, Errors: []string{"bash: exit 1"}}

	cases := []struct {
		name string
		exp  Expect
		rec  TurnRecord
		ok   bool
	}{
		{"empty expectation passes", Expect{}, rec, true},
		{"tool called", Expect{Tool: "schedule"}, rec, true},
		{"tool not called", Expect{Tool: "memory"}, rec, false},
		{"exact arg", Expect{Tool: "schedule", Args: map[string]any{"action": "create"}}, rec, true},
		{"wrong arg", Expect{Tool: "schedule", Args: map[string]any{"action": "delete"}}, rec, false},
		{"bool arg", Expect{Tool: "schedule", Args: map[string]any{"repeat": false}}, rec, true},
		{"missing arg", Expect{Tool: "schedule", Args: map[string]any{"check": "x"}}, rec, false},
		{"regexp arg", Expect{Tool: "schedule", ArgsMatch: map[string]string{"schedule": `^\d+m$`}}, rec, true},
		{"regexp fails", Expect{Tool: "schedule", ArgsMatch: map[string]string{"schedule": `^\d{4}-`}}, rec, false},
		{"forbidden tool", Expect{NotTools: []string{"clock"}}, rec, false},
		{"clean turn", Expect{Clean: true}, rec, true},
		{"turn with a rejection", Expect{Clean: true}, dirty, false},
		{"a rejection is only caught when clean is set", Expect{Tool: "schedule"}, dirty, true},
		{"clean is scoped to the tool under test", Expect{Tool: "schedule", Clean: true}, probed, true},
		{"an unscoped clean catches every failure", Expect{Clean: true}, probed, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := c.exp.Check(c.rec)
			if c.ok != (len(got) == 0) {
				t.Errorf("Check = %v, want pass=%v", got, c.ok)
			}
		})
	}
}

// Several calls to the same tool: one that satisfies the expectation is enough,
// because a model may probe before it commits.
func TestExpectAcceptsAnyMatchingCall(t *testing.T) {
	rec := TurnRecord{Calls: []RecordedCall{
		{Name: "schedule", Args: map[string]any{"action": "list"}},
		{Name: "schedule", Args: map[string]any{"action": "create"}},
	}}
	if got := (Expect{Tool: "schedule", Args: map[string]any{"action": "create"}}).Check(rec); len(got) != 0 {
		t.Errorf("Check = %v, want pass", got)
	}
}
