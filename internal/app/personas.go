package app

import (
	"fmt"
	"sort"
	"sync"
)

// A persona is the opening section of the system prompt: who the agent is and
// how it works. There is one built in, and the operator may write others.
//
// Choosing one is a whole replacement, not a layer. The built-in persona carries
// the working rules as well as the character — how to turn a request for later
// into a job, that a wake is unattended, that the agent cannot modify itself —
// so a persona that leaves them out gets an agent that does not know them. That
// is the point of choosing one, and it is said on the screen that chooses.
//
// The persona is fixed for a session's life, like the model and the tools: it is
// the first thing in the cacheable prefix, and a conversation whose character
// changed halfway through would have no single answer to what it had been told.
type Persona struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Bytes       int    `json:"bytes"`
	// Editable is whether the operator wrote this one. The built-in is not.
	Editable bool   `json:"editable"`
	Body     string `json:"-"`
}

// defaultPersona is the name of the one built in. A session that chose no
// persona runs on it, which is every session that existed before personas did.
const defaultPersona = "default"

const defaultPersonaDescription = "The agent as it ships: long-lived conversations, scheduled work, and memory."

type Personas struct {
	dir      string
	mu       sync.RWMutex
	personas map[string]*Persona
	failures []LoadFailure
}

func NewPersonas(dir string) *Personas {
	p := &Personas{dir: dir, personas: map[string]*Persona{}}
	p.Load()
	return p
}

// Dir is the root the operator's personas are written to.
func (p *Personas) Dir() string { return p.dir }

func (p *Personas) Load() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.personas = map[string]*Persona{
		defaultPersona: {Name: defaultPersona, Description: defaultPersonaDescription,
			Body: persona, Bytes: len(persona)},
	}
	docs, failures := loadAuthoredDir(p.dir, true)
	p.failures = failures
	for _, d := range docs {
		// The built-in is always there, so a file claiming its name would make
		// the choice on screen mean two different things.
		if d.Name == defaultPersona {
			p.failures = append(p.failures, LoadFailure{Dir: d.Path,
				Reason: fmt.Sprintf("%q is the built-in persona; choose another name", defaultPersona)})
			continue
		}
		p.personas[d.Name] = &Persona{Name: d.Name, Description: d.Description,
			Body: d.Body, Bytes: d.Bytes, Editable: true}
	}
}

// Get resolves a name to a persona. An empty name is the built-in one, which is
// what a session that chose nothing runs on. A set that was never loaded answers
// for the built-in too: every session has a persona, and there is no arrangement
// of this program in which that stops being true.
func (p *Personas) Get(name string) *Persona {
	if name == "" {
		name = defaultPersona
	}
	if p == nil {
		if name == defaultPersona {
			return &Persona{Name: defaultPersona, Description: defaultPersonaDescription,
				Body: persona, Bytes: len(persona)}
		}
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.personas[name]
}

func (p *Personas) All() []*Persona {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*Persona, 0, len(p.personas))
	for _, v := range p.personas {
		out = append(out, v)
	}
	// The built-in first, then the operator's by name: the list is read as "the
	// one you get unless you choose" followed by the choices.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Editable != out[j].Editable {
			return !out[i].Editable
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (p *Personas) Failures() []LoadFailure {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return append([]LoadFailure(nil), p.failures...)
}

// Text is the prompt section for a chosen persona. A session naming one that is
// no longer on disk falls back to the built-in rather than running with no
// persona at all, and says so where it is read.
func (p *Personas) Text(name string) string {
	if got := p.Get(name); got != nil {
		return got.Body
	}
	return persona + "\n\n(The persona named " + name + " is no longer on disk; this is the built-in one.)"
}

func (p *Personas) Save(name, description, body string) error {
	if name == defaultPersona {
		return fmt.Errorf("%q is the built-in persona and cannot be edited here", defaultPersona)
	}
	if err := saveAuthored(p.dir, name, description, nil, body); err != nil {
		return err
	}
	p.Load()
	return nil
}

func (p *Personas) Delete(name string) error {
	existing := p.Get(name)
	if existing == nil {
		return fmt.Errorf("no persona named %q", name)
	}
	if !existing.Editable {
		return fmt.Errorf("%q is the built-in persona and cannot be deleted here", name)
	}
	if err := deleteAuthored(p.dir, name); err != nil {
		return err
	}
	p.Load()
	return nil
}
