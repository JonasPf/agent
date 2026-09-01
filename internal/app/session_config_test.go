package app

import (
	"encoding/json"
	"testing"
)

func TestSessionConfigSameAs(t *testing.T) {
	base := SessionConfig{Model: "a/b", EnabledTools: []string{"bash"}}
	cases := []struct {
		name string
		cfg  SessionConfig
		want bool
	}{
		{"identical", SessionConfig{Model: "a/b", EnabledTools: []string{"bash"}}, true},
		{"other model", SessionConfig{Model: "a/c", EnabledTools: []string{"bash"}}, false},
		{"other tools", SessionConfig{Model: "a/b", EnabledTools: []string{"read"}}, false},
		{"all tools is not one tool", SessionConfig{Model: "a/b"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := base.sameAs(c.cfg); got != c.want {
				t.Errorf("sameAs = %v, want %v", got, c.want)
			}
		})
	}
}

// Every tool enabled and no tool enabled are different configurations, and the
// difference is nil versus empty. Confusing them would silently disable
// everything, so it is worth pinning.
func TestSameSetDistinguishesNilFromEmpty(t *testing.T) {
	if sameSet(nil, []string{}) {
		t.Error("nil (all) and empty (none) compared equal")
	}
	if !sameSet(nil, nil) {
		t.Error("nil and nil compared unequal")
	}
	if !sameSet([]string{}, []string{}) {
		t.Error("empty and empty compared unequal")
	}
}

func TestDescribeConfigChange(t *testing.T) {
	from := SessionConfig{Model: "a/b"}
	to := SessionConfig{Model: "a/c", EnabledTools: []string{"bash", "read"}}
	got := describeConfigChange(from, to)
	want := "model a/b → a/c; tools all → bash, read"
	if got != want {
		t.Errorf("describeConfigChange = %q, want %q", got, want)
	}
	if s := describeConfigChange(from, from); s != "" {
		t.Errorf("unchanged config described as %q, want empty", s)
	}
}

// A set arrives in three states and they must stay distinct: absent inherits,
// null means every one, a list means exactly those.
func TestConfigRequestApplyTo(t *testing.T) {
	base := SessionConfig{Model: "a/b", EnabledTools: []string{"bash"}, EnabledSkills: []string{"x"}}
	decode := func(body string) configRequest {
		var c configRequest
		if err := json.Unmarshal([]byte(body), &c); err != nil {
			t.Fatalf("decode %s: %v", body, err)
		}
		return c
	}
	cases := []struct {
		name string
		body string
		want SessionConfig
	}{
		{"empty body inherits everything", `{}`, base},
		{"model alone keeps the sets",
			`{"model":"a/c"}`,
			SessionConfig{Model: "a/c", EnabledTools: []string{"bash"}, EnabledSkills: []string{"x"}}},
		{"null means every tool",
			`{"enabled_tools":null}`,
			SessionConfig{Model: "a/b", EnabledTools: nil, EnabledSkills: []string{"x"}}},
		{"empty list means no tool",
			`{"enabled_tools":[]}`,
			SessionConfig{Model: "a/b", EnabledTools: []string{}, EnabledSkills: []string{"x"}}},
		{"a list replaces the set",
			`{"enabled_tools":["read","write"]}`,
			SessionConfig{Model: "a/b", EnabledTools: []string{"read", "write"}, EnabledSkills: []string{"x"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := decode(c.body).applyTo(base); !got.sameAs(c.want) {
				t.Errorf("applyTo = %+v, want %+v", got, c.want)
			}
		})
	}
}
