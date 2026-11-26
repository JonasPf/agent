package app

import (
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
)

// outputSchema is the schema a bundle's tool declares for its structured
// result. MCP says a client should check a result against it; a result that
// does not match is still the model's, with that said beside it, because its
// text may be right where its structure is not.
type outputSchema struct {
	resolved *jsonschema.Resolved
}

// outputSchemaOf reads the output schema a tool listed, or none.
func outputSchemaOf(declared any) (*outputSchema, error) {
	if declared == nil {
		return nil, nil
	}
	raw, err := json.Marshal(declared)
	if err != nil {
		return nil, err
	}
	if string(raw) == "null" {
		return nil, nil
	}
	var s jsonschema.Schema
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	r, err := s.Resolve(nil)
	if err != nil {
		return nil, err
	}
	return &outputSchema{resolved: r}, nil
}

// check says what is wrong with a structured result against the schema its
// tool declared, or nothing.
func (o *outputSchema) check(tool string, structured any) string {
	if o == nil {
		return ""
	}
	if structured == nil {
		return fmt.Sprintf("(%s declares an output schema and sent no structured result)", tool)
	}
	// What the SDK decoded is checked as JSON reads it, whatever Go type it
	// came in.
	raw, err := json.Marshal(structured)
	if err != nil {
		return fmt.Sprintf("(the structured result could not be read: %v)", err)
	}
	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		return fmt.Sprintf("(the structured result could not be read: %v)", err)
	}
	if err := o.resolved.Validate(instance); err != nil {
		return fmt.Sprintf("(the structured result does not match the output schema %s declares: %v)", tool, err)
	}
	return ""
}
