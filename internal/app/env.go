package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// A conversation holds its own environment variables (ADR-068): names and
// values the operator sets, which its tools and checks receive. The values sit
// beside the transcript, in a directory no tool's sandbox reaches, and leave it
// only for a subprocess of that conversation. Nothing returns one: what the
// session says, and what an export carries, is which names are set.

// toolContractEnv is set by the agent on every tool call, over whatever else
// the environment held, so a conversation defining one of these names would
// either be overwritten or break the call.
var toolContractEnv = map[string]bool{
	"AGENT_DB": true, "AGENT_DB_PREFIX": true, "AGENT_URL": true, "AGENT_WORKSPACE": true,
	"AGENT_SESSION": true, "AGENT_JOB": true, "AGENT_TOKEN": true, "TMPDIR": true,
	"HOME": true,
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// maxEnvValue bounds one value. A credential is a line; anything far longer is
// a file, and belongs in the working directory.
const maxEnvValue = 64 << 10

// checkEnvChange refuses a change that cannot be applied as asked, naming the
// variable, so a refused request is refused whole and changes nothing.
func checkEnvChange(change map[string]*string) error {
	names := make([]string, 0, len(change))
	for name := range change {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		switch {
		case !envName.MatchString(name):
			return fmt.Errorf("%q is not a variable name: letters, digits, and _, not starting with a digit", name)
		case toolContractEnv[name]:
			return fmt.Errorf("%s is set by the agent on every tool call, and cannot be defined", name)
		case slices.Contains(toolPassthrough, name):
			return fmt.Errorf("%s is already given to every tool, and cannot be defined", name)
		}
		if v := change[name]; v != nil {
			if strings.ContainsRune(*v, 0) {
				return fmt.Errorf("the value of %s holds a NUL byte, which no environment can carry", name)
			}
			if len(*v) > maxEnvValue {
				return fmt.Errorf("the value of %s is longer than %d bytes; put it in a file instead", name, maxEnvValue)
			}
		}
	}
	return nil
}

// describeEnvChange says what a change did, by name, for the conversation's
// own record of it. Never a value.
func describeEnvChange(before map[string]string, change map[string]*string) string {
	var set, replaced, removed []string
	for name, v := range change {
		_, had := before[name]
		switch {
		case v == nil && had:
			removed = append(removed, name)
		case v == nil:
		case had:
			replaced = append(replaced, name)
		default:
			set = append(set, name)
		}
	}
	var parts []string
	for _, p := range []struct {
		verb  string
		names []string
	}{{"set", set}, {"replaced", replaced}, {"removed", removed}} {
		if len(p.names) > 0 {
			slices.Sort(p.names)
			parts = append(parts, p.verb+" "+strings.Join(p.names, ", "))
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return "variables " + strings.Join(parts, "; ")
}

// sessionVars is a conversation's variables as a subprocess environment, in a
// stable order.
func sessionVars(vars map[string]string) []string {
	names := envNames(vars)
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, name+"="+vars[name])
	}
	return out
}

func envNames(vars map[string]string) []string {
	names := make([]string, 0, len(vars))
	for name := range vars {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

func (s *Store) envPath(id string) string { return filepath.Join(s.sessionDir(id), "env.json") }

func readEnvJSON(path string) map[string]string {
	vars := map[string]string{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &vars)
	}
	return vars
}

// SessionEnv is what a session's tools and checks are given. A session that is
// not there holds nothing, which is the safe direction: a credential is
// withheld rather than handed out on a lookup that found nothing.
func (s *Store) SessionEnv(id string) map[string]string {
	if !validSessionID(id) {
		return map[string]string{}
	}
	s.envMu.Lock()
	defer s.envMu.Unlock()
	return readEnvJSON(s.envPath(id))
}

// ChangeSessionEnv sets each name given a value and removes each given null,
// and answers with what the session held before. The values are written for
// the agent alone; the session records only the names.
func (s *Store) ChangeSessionEnv(id string, change map[string]*string) (map[string]string, error) {
	if len(change) == 0 {
		return s.SessionEnv(id), nil
	}
	s.envMu.Lock()
	defer s.envMu.Unlock()
	before := readEnvJSON(s.envPath(id))
	vars := make(map[string]string, len(before))
	for k, v := range before {
		vars[k] = v
	}
	for name, v := range change {
		if v == nil {
			delete(vars, name)
		} else {
			vars[name] = *v
		}
	}
	if err := s.writeEnv(id, vars); err != nil {
		return nil, err
	}
	if _, err := s.UpdateSession(id, func(x *Session) { x.Env = envNames(vars) }); err != nil {
		return nil, err
	}
	return before, nil
}

// writeEnv replaces the file whole, so a reader never sees half a change.
func (s *Store) writeEnv(id string, vars map[string]string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.deleted[id] {
		return errSessionDeleted
	}
	path := s.envPath(id)
	if len(vars) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	b, _ := json.MarshalIndent(vars, "", "  ")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// adoptGrants turns the names a session was granted before ADR-068 into
// variables of its own, holding what the agent's environment has for them now,
// so a conversation that could reach a credential yesterday still can. The
// model key was never delivered by a grant and is not now.
func (s *Store) adoptGrants(id string) error {
	s.mu.RLock()
	cur := s.sessions[id]
	s.mu.RUnlock()
	if cur == nil || len(cur.LegacyGrants) == 0 {
		return nil
	}
	change := map[string]*string{}
	for _, name := range cur.LegacyGrants {
		v, ok := os.LookupEnv(name)
		if !ok || name == "OPENROUTER_API_KEY" {
			continue
		}
		// A name no conversation can define now was never delivered then.
		if checkEnvChange(map[string]*string{name: &v}) == nil {
			change[name] = &v
		}
	}
	if _, err := s.ChangeSessionEnv(id, change); err != nil {
		return err
	}
	_, err := s.UpdateSession(id, func(x *Session) { x.LegacyGrants = nil })
	return err
}
