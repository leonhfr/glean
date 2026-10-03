package model_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/leonhfr/glean/internal/model"
)

func TestParseCapabilityID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		value string
		kind  model.CapabilityKind
		name  string
	}{
		{"skill:review", model.KindSkill, "review"},
		{"claude:agent:reviewer", model.KindClaudeAgent, "reviewer"},
		{"claude:command:tools/review", model.KindClaudeCommand, "tools/review"},
		{"claude:hook:PostToolUse", model.KindClaudeHook, "PostToolUse"},
		{"claude:mcp:docs.api:remote", model.KindClaudeMCP, "docs.api:remote"},
		{"claude:lsp:go", model.KindClaudeLSP, "go"},
	}
	for _, tc := range cases {
		t.Run(tc.value, func(t *testing.T) {
			t.Parallel()
			id, err := model.ParseCapabilityID(tc.value)
			require.NoError(t, err)
			assert.Equal(t, tc.kind, id.Kind)
			assert.Equal(t, tc.name, id.Name)
			assert.Equal(t, tc.value, id.String())
		})
	}
}

func TestParseCapabilityIDRejectsUnqualifiedOrUnsupportedIDs(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"", "review", "agent:review", "claude:unknown:review", "skill:", "claude:hook:  "} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			id, err := model.ParseCapabilityID(value)
			require.ErrorIs(t, err, model.ErrInvalidCapabilityID)
			assert.Zero(t, id)
		})
	}
}
