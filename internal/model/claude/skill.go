// Package claude defines native Claude capabilities.
package claude

import "github.com/leonhfr/glean/internal/model"

// Skill retains a native skill document and its authored display metadata.
// Document is unchanged, including frontmatter, embedded hooks and substitutions.
// Nil metadata means the field was absent or native parsing could not read it.
type Skill struct {
	Document    string
	Name        *string
	Description *string
}

// Kind implements model.Definition.
func (Skill) Kind() model.CapabilityKind {
	return model.KindSkill
}
