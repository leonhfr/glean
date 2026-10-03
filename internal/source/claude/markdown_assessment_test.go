package claude_test

import (
	"encoding/json"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/leonhfr/glean/internal/model"
	native "github.com/leonhfr/glean/internal/model/claude"
	"github.com/leonhfr/glean/internal/source/claude"
)

func TestMarkdownFieldAssessment(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, file, metadata, field, code string
		status                            model.FindingStatus
	}{
		{"skill boolean", "skills/review/SKILL.md", "user-invocable: []", "user-invocable", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"skill null", "skills/review/SKILL.md", "disable-model-invocation: null", "disable-model-invocation", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"tools invalid", "skills/review/SKILL.md", "allowed-tools: [Read, 42]", "allowed-tools", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"metadata invalid", "skills/review/SKILL.md", "metadata: text", "metadata", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"unknown field", "skills/review/SKILL.md", "future: secret-value", "future", "UNKNOWN_NATIVE_FIELD", model.FindingUnknown},
		{"malformed YAML", "skills/review/SKILL.md", "hooks: [", "frontmatter", "NATIVE_FRONTMATTER_FALLBACK", model.FindingUnknown},
		{"duplicate YAML", "skills/review/SKILL.md", "user-invocable: true\nuser-invocable: false", "frontmatter", "NATIVE_FRONTMATTER_FALLBACK", model.FindingUnknown},
		{"nonmap YAML", "skills/review/SKILL.md", "- value", "frontmatter", "NATIVE_FRONTMATTER_FALLBACK", model.FindingUnknown},
		{"hooks nonobject", "skills/review/SKILL.md", "hooks: []", "hooks", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"hooks bad structure", "skills/review/SKILL.md", "hooks:\n  PreToolUse: text", "hooks", "INVALID_EMBEDDED_HOOK", model.FindingMissing},
		{"embedded command missing", "skills/review/SKILL.md", "hooks:\n  PreToolUse:\n    - hooks:\n        - type: command", "hooks.command", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"embedded once invalid", "skills/review/SKILL.md", "hooks:\n  Stop:\n    - hooks:\n        - type: command\n          command: echo ok\n          once: text", "hooks.once", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"agent maxTurns", "agents/review.md", "maxTurns: 0", "maxTurns", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"agent hooks ignored", "agents/review.md", "hooks: {Stop: []}", "hooks", "INAPPLICABLE_NATIVE_FIELD", model.FindingUnsupported},
		{"agent permission ignored", "agents/review.md", "permissionMode: bypassPermissions", "permissionMode", "INAPPLICABLE_NATIVE_FIELD", model.FindingUnsupported},
		{"agent MCP ignored", "agents/review.md", "mcpServers: [db]", "mcpServers", "INAPPLICABLE_NATIVE_FIELD", model.FindingUnsupported},
		{"command name ignored", "commands/review.md", "name: other", "name", "INAPPLICABLE_NATIVE_FIELD", model.FindingUnsupported},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			document := "---\n" + test.metadata + "\n---\nInstructions.\n"
			source := fstest.MapFS{test.file: {Data: []byte(document)}}
			plugin, err := claude.ReadPlugin(agentSystem(source), "requested", "resolved")
			require.NoError(t, err)
			require.Len(t, plugin.Inventory.Capabilities, 1)
			var matches []model.Finding
			for _, finding := range plugin.Findings {
				if finding.Code == test.code && finding.Reference == test.field {
					matches = append(matches, finding)
				}
			}

			require.Len(t, matches, 1)
			assert.Equal(t, test.status, matches[0].Status)
			assert.Equal(t, plugin.Inventory.Capabilities[0].ID, matches[0].Capability)
			assert.Equal(t, model.Location{Path: test.file}, matches[0].Locations[0])
			assert.Equal(t, document, string(source[test.file].Data))
			encoded, err := json.Marshal(plugin.Findings)
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), "secret-value")
		})
	}
}

func TestMarkdownPreservesNativeBooleanFormsAndEmbeddedOnce(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"true", "false", "yes", "NO", "on", "OFF", "1", "0", `"TRUE"`} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			document := "\xef\xbb\xbf---\r\ndisable-model-invocation: " + value + "\r\nallowed-tools: [Read, Bash]\r\nhooks:\r\n  Stop:\r\n    - hooks:\r\n        - type: command\r\n          command: echo ok\r\n          once: true\r\n---\r\nInstructions.\r\n"
			source := fstest.MapFS{"skills/review/SKILL.md": {Data: []byte(document)}}
			plugin, err := claude.ReadPlugin(agentSystem(source), "requested", "resolved")
			require.NoError(t, err)
			require.Len(t, plugin.Inventory.Capabilities, 1)
			definition, ok := plugin.Inventory.Capabilities[0].Definition.(native.Skill)
			require.True(t, ok)
			assert.Equal(t, document, definition.Document)
			for _, finding := range plugin.Findings {
				assert.NotEqual(t, "INVALID_NATIVE_FIELD", finding.Code)
				assert.NotEqual(t, "INAPPLICABLE_NATIVE_FIELD", finding.Code)
				if finding.Code == "NATIVE_HOOK_SUPPORT" {
					assert.Equal(t, model.FindingUnknown, finding.Status)
				}
			}
		})
	}
}

func TestMarkdownBodyFeaturesAndCommandOverrides(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{claude.ManifestPath: {Data: []byte(`{"name":"tools","commands":{"run":{"content":"Use !\u0060secret-value\u0060 twice !\u0060another\u0060 with $ARGUMENTS and $1 at ${CLAUDE_SKILL_DIR} in ${CLAUDE_SESSION_ID}.","future":"secret-value"}}}`)}}
	plugin, err := claude.ReadPlugin(agentSystem(source), "requested", "resolved")
	require.NoError(t, err)
	require.Len(t, plugin.Inventory.Capabilities, 1)
	var features []string
	for _, finding := range plugin.Findings {
		if finding.Code == "NATIVE_MARKDOWN_FEATURE" {
			features = append(features, finding.Reference)
			assert.Equal(t, model.FindingUnknown, finding.Status)
		}

		if finding.Code == "UNKNOWN_NATIVE_FIELD" {
			assert.Equal(t, "future", finding.Reference)
			assert.Equal(t, model.Location{Path: claude.ManifestPath, Pointer: "/commands/run/future"}, finding.Locations[0])
		}
	}

	assert.ElementsMatch(t, []string{"dynamic-command", "arguments", "skill-directory", "session-id"}, features)
	encoded, err := json.Marshal(plugin.Findings)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "secret-value")
}

func TestMarkdownBodyAssessmentExcludesFrontmatter(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{"skills/review/SKILL.md": {Data: []byte("---\ndescription: 'Use $ARGUMENTS'\n---\nPlain instructions.\n")}}
	plugin, err := claude.ReadPlugin(agentSystem(source), "requested", "resolved")
	require.NoError(t, err)
	for _, finding := range plugin.Findings {
		assert.NotEqual(t, "NATIVE_MARKDOWN_FEATURE", finding.Code)
	}
}
