package claude

import (
	"fmt"
	"slices"

	"github.com/leonhfr/glean/internal/model"
)

func includeMarkdownCapability(capabilities []model.Capability, candidate model.Capability, invalidErr error) ([]model.Capability, error) {
	for i, existing := range capabilities {
		if existing.ID != candidate.ID {
			continue
		}

		if existing.Payload.Entrypoints[0] != candidate.Payload.Entrypoints[0] {
			return nil, fmt.Errorf("%w: invocation %s is declared by both %s and %s", invalidErr,
				candidate.ID, existing.Payload.Entrypoints[0], candidate.Payload.Entrypoints[0])
		}

		for _, location := range candidate.Provenance.Declarations {
			if !slices.Contains(existing.Provenance.Declarations, location) {
				existing.Provenance.Declarations = append(existing.Provenance.Declarations, location)
			}
		}

		capabilities[i] = existing
		return capabilities, nil
	}

	return append(capabilities, candidate), nil
}
