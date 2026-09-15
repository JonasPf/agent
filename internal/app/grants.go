package app

import (
	"net/http"
	"os"
	"sort"
	"strings"
)

// toolContractEnv is set by the agent on every tool call, over whatever the
// environment held, so granting one of these names would deliver nothing.
var toolContractEnv = map[string]bool{
	"AGENT_DB": true, "AGENT_DB_PREFIX": true, "AGENT_URL": true, "AGENT_WORKSPACE": true,
	"AGENT_SESSION": true, "AGENT_JOB": true, "TMPDIR": true,
}

type grantableName struct {
	Name string `json:"name"`
	// FromEnvFile says the name is written in the agent's .env, which is where an
	// operator puts a credential on purpose; the rest arrived with the process.
	FromEnvFile bool `json:"from_env_file"`
}

// grantableNames is every variable a session could be granted and a tool would
// actually receive: the names in the agent's environment and its .env, less the
// model key, what every tool already gets, and what each call overwrites.
//
// Names only. The operator holds the values already; what the interface needs
// is the list to tick from, and a name is safe to put on a screen.
func grantableNames(envFile string) []grantableName {
	fromFile := map[string]bool{}
	if lines, err := readEnvFile(envFile); err == nil {
		for _, l := range lines {
			fromFile[l.key] = true
		}
	}
	names := map[string]bool{}
	for name := range fromFile {
		names[name] = true
	}
	for _, kv := range os.Environ() {
		if name, _, _ := strings.Cut(kv, "="); name != "" {
			names[name] = true
		}
	}
	given := map[string]bool{}
	for _, name := range toolPassthrough {
		given[name] = true
	}
	out := []grantableName{}
	for name := range names {
		if neverGranted[name] || given[name] || toolContractEnv[name] {
			continue
		}
		out = append(out, grantableName{Name: name, FromEnvFile: fromFile[name]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (a *App) hEnv(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, grantableNames(a.cfg.EnvFile))
}
