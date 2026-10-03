package claude

import (
	"encoding/json"

	"github.com/leonhfr/glean/internal/model"
)

// Command preserves native Markdown and optional manifest overrides.
// Declaration is nil for path-based commands and retains all named-entry fields.
// Inline commands have no filesystem entrypoint; their declaration owns content.
type Command struct {
	Document    string
	Description *string
	Declaration map[string]json.RawMessage
}

// Kind implements model.Definition.
func (Command) Kind() model.CapabilityKind { return model.KindClaudeCommand }
