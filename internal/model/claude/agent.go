package claude

import "github.com/leonhfr/glean/internal/model"

// Agent retains a plugin agent document and its authored display metadata.
// Document preserves frontmatter, instructions and references without rewriting.
// Nil metadata means the field was absent or native parsing could not read it.
type Agent struct {
	Document    string
	Name        *string
	Description *string
}

// Kind implements model.Definition.
func (Agent) Kind() model.CapabilityKind {
	return model.KindClaudeAgent
}
