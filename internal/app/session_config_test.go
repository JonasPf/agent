package app

import "testing"

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
