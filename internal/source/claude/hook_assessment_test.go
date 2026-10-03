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

func TestHookFieldAssessment(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, handler, field, code string
		status                     model.FindingStatus
	}{
		{"missing command", `{"type":"command"}`, "command", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"null command", `{"type":"command","command":null}`, "command", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"numeric command", `{"type":"command","command":123}`, "command", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"blank prompt", `{"type":"prompt","prompt":" "}`, "prompt", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"missing agent prompt", `{"type":"agent"}`, "prompt", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"missing url", `{"type":"http"}`, "url", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"missing server", `{"type":"mcp_tool","tool":"check"}`, "server", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"missing tool", `{"type":"mcp_tool","server":"db"}`, "tool", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"negative timeout", `{"type":"command","command":"true","timeout":-1}`, "timeout", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"zero timeout", `{"type":"command","command":"true","timeout":0}`, "timeout", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"null timeout", `{"type":"command","command":"true","timeout":null}`, "timeout", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"null args", `{"type":"command","command":"true","args":null}`, "args", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"nonstring args", `{"type":"command","command":"true","args":[1]}`, "args", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"async type", `{"type":"command","command":"true","async":"true"}`, "async", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"asyncRewake type", `{"type":"command","command":"true","asyncRewake":null}`, "asyncRewake", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"shell enum", `{"type":"command","command":"true","shell":"fish"}`, "shell", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"model type", `{"type":"agent","prompt":"Check","model":false}`, "model", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"headers type", `{"type":"http","url":"https://example.test","headers":{"Authorization":null}}`, "headers", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"env names type", `{"type":"http","url":"https://example.test","allowedEnvVars":[1]}`, "allowedEnvVars", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"tool input type", `{"type":"mcp_tool","server":"db","tool":"check","input":[]}`, "input", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"once type", `{"type":"command","command":"true","once":null}`, "once", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"plugin once", `{"type":"command","command":"true","once":true}`, "once", "INAPPLICABLE_NATIVE_FIELD", model.FindingUnsupported},
		{"if type", `{"type":"command","command":"true","if":[]}`, "if", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"status type", `{"type":"command","command":"true","statusMessage":1}`, "statusMessage", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"cross handler field", `{"type":"http","url":"https://example.test","async":true}`, "async", "INAPPLICABLE_NATIVE_FIELD", model.FindingUnsupported},
		{"cross command field", `{"type":"command","command":"true","prompt":"secret-value"}`, "prompt", "INAPPLICABLE_NATIVE_FIELD", model.FindingUnsupported},
		{"unknown field", `{"type":"command","command":"true","future/key~":"secret-value"}`, "future/key~", "UNKNOWN_NATIVE_FIELD", model.FindingUnknown},
		{"shell substitution", `{"type":"command","command":"echo ${user_config.token}"}`, "command", "UNSUPPORTED_NATIVE_REFERENCE", model.FindingUnsupported},
		{"escaped substitution", `{"type":"command","command":"echo \u0024{user_config.token}"}`, "command", "UNSUPPORTED_NATIVE_REFERENCE", model.FindingUnsupported},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source := fstest.MapFS{claude.ManifestPath: {Data: []byte(`{"name":"tools","hooks":{"PreToolUse":[{"hooks":[` + test.handler + `]}]}}`)}}
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
			field := test.field
			if field == "future/key~" {
				field = "future~1key~0"
			}

			assert.Equal(t, model.Location{Path: claude.ManifestPath, Pointer: "/hooks/PreToolUse/0/hooks/0/" + field}, matches[0].Locations[0])
			encoded, err := json.Marshal(plugin.Findings)
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), "secret-value")
		})
	}
}

func TestHookValidShapesRemainUnverified(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{claude.ManifestPath: {Data: []byte(`{"name":"tools","userConfig":{"token":{"type":"string"}},"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"node","args":["${user_config.token}"],"async":false,"asyncRewake":true,"shell":"powershell","timeout":0.5,"if":"Bash(git *)","once":false,"statusMessage":""},{"type":"prompt","prompt":"Check","model":"sonnet"},{"type":"agent","prompt":"Check"},{"type":"http","url":"http://localhost:8080","headers":{"Authorization":"Bearer $TOKEN"},"allowedEnvVars":["TOKEN"]},{"type":"mcp_tool","server":"external","tool":"check","input":{"file":"${tool_input.file_path}"}}]}]}}`)}}
	plugin, err := claude.ReadPlugin(agentSystem(source), "requested", "resolved")
	require.NoError(t, err)
	count := 0
	for _, finding := range plugin.Findings {
		assert.NotEqual(t, "INVALID_NATIVE_FIELD", finding.Code)
		assert.NotEqual(t, "INAPPLICABLE_NATIVE_FIELD", finding.Code)
		assert.NotEqual(t, "UNKNOWN_NATIVE_FIELD", finding.Code)
		assert.NotEqual(t, "UNSUPPORTED_NATIVE_REFERENCE", finding.Code)
		if finding.Code == "NATIVE_HOOK_SUPPORT" {
			count++
			assert.Equal(t, model.FindingUnknown, finding.Status)
		}
	}

	assert.Equal(t, 5, count)
}

func TestHookAssessmentGroupAndHandlerProvenance(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{
		claude.ManifestPath: {Data: []byte(`{"name":"tools","hooks":["./hooks/custom.json","./hooks/custom.json"]}`)},
		"hooks/custom.json": {Data: []byte(`{"hooks":{"PreToolUse":[{"futureGroup":"secret-value","hooks":[{"type":"command","command":"echo ok"},{"type":"command","command":null}]},{"hooks":[{"type":"mcp_tool","server":"external","tool":"check"}]}]}}`)},
	}
	plugin, err := claude.ReadPlugin(agentSystem(source), "requested", "resolved")
	require.NoError(t, err)
	require.Len(t, plugin.Inventory.Capabilities, 1)
	definition, ok := plugin.Inventory.Capabilities[0].Definition.(native.Hook)
	require.True(t, ok)
	require.Len(t, definition.Groups, 2)
	assert.Contains(t, string(definition.Groups[0].Document), "secret-value")
	var fields, supports, companions []model.Finding
	for _, finding := range plugin.Findings {
		switch finding.Code {
		case "INVALID_NATIVE_FIELD", "UNKNOWN_NATIVE_FIELD":
			fields = append(fields, finding)

		case "NATIVE_HOOK_SUPPORT":
			supports = append(supports, finding)

		case "MCP_COMPANION":
			companions = append(companions, finding)
		}
	}

	require.Len(t, fields, 2)
	assert.Equal(t, "/hooks/PreToolUse/0/futureGroup", fields[0].Locations[0].Pointer)
	assert.Equal(t, "/hooks/PreToolUse/0/hooks/1/command", fields[1].Locations[0].Pointer)
	assert.Equal(t, []model.Location{{Path: "hooks/custom.json", Pointer: "/hooks/PreToolUse/0/hooks/1/command"}, {Path: claude.ManifestPath, Pointer: "/hooks/0"}, {Path: claude.ManifestPath, Pointer: "/hooks/1"}}, fields[1].Locations)
	require.Len(t, supports, 3)
	assert.Equal(t, "/hooks/PreToolUse/0/hooks/1/type", supports[1].Locations[0].Pointer)
	require.Len(t, companions, 1)
	assert.Equal(t, "/hooks/PreToolUse/1/hooks/0/server", companions[0].Locations[0].Pointer)
}

func TestHookUnknownFormsRemainVisible(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{claude.ManifestPath: {Data: []byte(`{"name":"tools","hooks":{"FutureEvent":[{"hooks":[{"type":"future","command":null,"future":"secret-value"},{"type":"command","command":"true"}]}]}}`)}}
	plugin, err := claude.ReadPlugin(agentSystem(source), "requested", "resolved")
	require.NoError(t, err)
	require.Len(t, plugin.Inventory.Capabilities, 1)
	assert.Equal(t, "FutureEvent", plugin.Inventory.Capabilities[0].ID.Name)
	require.Len(t, plugin.Findings, 2)
	assert.Equal(t, model.FindingUnsupported, plugin.Findings[0].Status)
	assert.Equal(t, model.FindingUnknown, plugin.Findings[1].Status)
	encoded, err := json.Marshal(plugin.Findings)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "secret-value")
}

func TestPluginAssessmentPreservesArrayDeclarations(t *testing.T) {
	t.Parallel()
	manifest := `{"name":"tools","hooks":["./hooks/custom.json",{"PreToolUse":[{"hooks":[{"type":"command","command":"true"}]}]}],"mcpServers":["./servers.json",{"other":{"command":"node"}}],"lspServers":["./languages.json",{"other":{"command":"gopls","extensionToLanguage":{".go":"go"}}}]}`
	source := fstest.MapFS{
		claude.ManifestPath: {Data: []byte(manifest)},
		"hooks/custom.json": {Data: []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"true"}]}]}}`)},
		"servers.json":      {Data: []byte(`{"db":{"command":"node"}}`)},
		"languages.json":    {Data: []byte(`{"go":{"command":"gopls","extensionToLanguage":{".go":"go"}}}`)},
	}
	plugin, err := claude.ReadPlugin(agentSystem(source), "requested", "resolved")
	require.NoError(t, err)
	require.Len(t, plugin.Inventory.Capabilities, 6)
	encoded, err := json.Marshal(plugin.Manifest.Fields)
	require.NoError(t, err)
	assert.JSONEq(t, manifest, string(encoded))
	assert.Equal(t, manifest, string(source[claude.ManifestPath].Data))
}
