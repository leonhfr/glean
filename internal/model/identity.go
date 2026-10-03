package model

import (
	"errors"
	"fmt"
	"strings"
)

// ErrInvalidCapabilityID identifies an unsupported kind or empty native name.
var ErrInvalidCapabilityID = errors.New("invalid capability ID")

// CapabilityID identifies a kind and exact native name within a package.
// Names remain opaque, including dots, colons and path qualification.
// The struct is comparable and can be used as an inventory map key.
type CapabilityID struct {
	Kind CapabilityKind
	Name string
}

// ParseCapabilityID decodes a qualified identifier for a supported kind.
// It consumes the known kind prefix and preserves the complete remaining name.
func ParseCapabilityID(value string) (CapabilityID, error) {
	for _, kind := range []CapabilityKind{
		KindSkill, KindClaudeAgent, KindClaudeCommand, KindClaudeHook, KindClaudeMCP, KindClaudeLSP,
	} {
		if name, ok := strings.CutPrefix(value, string(kind)+":"); ok {
			if strings.TrimSpace(name) == "" {
				return CapabilityID{}, fmt.Errorf("%w: require a nonblank native name", ErrInvalidCapabilityID)
			}

			return CapabilityID{Kind: kind, Name: name}, nil
		}
	}

	return CapabilityID{}, fmt.Errorf("%w: require a supported qualified kind", ErrInvalidCapabilityID)
}

// String returns the qualified CLI/JSON identifier. Package identity is separate.
func (id CapabilityID) String() string {
	return string(id.Kind) + ":" + id.Name
}
