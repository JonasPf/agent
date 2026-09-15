package app

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
)

// fallbackModel is what a conversation starts on when nothing has been chosen
// and no conversation exists to take a model from: the first start of a fresh
// agent, and nothing else.
const fallbackModel = "anthropic/claude-sonnet-5"

// Preferences is what the operator chose that should outlive the choice. It is
// a file in the data directory rather than a setting: it changes when a person
// picks something on screen, never by editing a file and restarting.
type Preferences struct {
	// Model is the one last chosen for a new conversation, or for a fork onto
	// a different model.
	Model string `json:"model"`
}

func (s *Store) preferencesPath() string { return filepath.Join(s.dir, "preferences.json") }

// Preferences reads what is stored. A missing or unreadable file is no
// preference, which every caller already has an answer for.
func (s *Store) Preferences() Preferences {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var p Preferences
	if b, err := os.ReadFile(s.preferencesPath()); err == nil {
		_ = json.Unmarshal(b, &p)
	}
	return p
}

// PutPreferences replaces the file whole, through a rename, so a crash leaves
// the old choice or the new one and never half of either.
func (s *Store) PutPreferences(p Preferences) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.preferencesPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.preferencesPath())
}

// startingModel is the model a new conversation starts on: the one last chosen,
// else the most recent conversation's, else the fallback.
func (a *App) startingModel() string {
	if m := a.store.Preferences().Model; m != "" {
		return m
	}
	if recent := a.store.Sessions(); len(recent) > 0 && recent[0].Model != "" {
		return recent[0].Model
	}
	return fallbackModel
}

func (a *App) rememberModel(model string) {
	p := a.store.Preferences()
	if model == "" || p.Model == model {
		return
	}
	p.Model = model
	_ = a.store.PutPreferences(p)
}

func (a *App) hPreferences(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]string{"model": a.startingModel()})
}
