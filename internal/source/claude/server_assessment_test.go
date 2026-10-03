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

func TestServerFieldAssessment(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, kind, definition, field, code string
		status                              model.FindingStatus
	}{
		{"missing command", "mcpServers", `{}`, "command", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"null command", "mcpServers", `{"command":null}`, "command", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"numeric command", "mcpServers", `{"command":42}`, "command", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"blank command", "mcpServers", `{"command":" "}`, "command", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"null args", "mcpServers", `{"command":"node","args":null}`, "args", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"nonstring args", "mcpServers", `{"command":"node","args":[null]}`, "args", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"nonstring env", "mcpServers", `{"command":"node","env":{"TOKEN":123}}`, "env", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"missing url", "mcpServers", `{"type":"http"}`, "url", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"relative url", "mcpServers", `{"type":"http","url":"/mcp"}`, "url", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"wrong scheme", "mcpServers", `{"type":"http","url":"file:///mcp"}`, "url", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"dynamic url", "mcpServers", `{"type":"http","url":"${ENDPOINT}"}`, "url", "NATIVE_URL", model.FindingUnknown},
		{"url without type", "mcpServers", `{"command":"node","url":"https://example.test"}`, "url", "INAPPLICABLE_NATIVE_FIELD", model.FindingUnsupported},
		{"remote command", "mcpServers", `{"type":"http","url":"https://example.test","command":"node"}`, "command", "INAPPLICABLE_NATIVE_FIELD", model.FindingUnsupported},
		{"header null", "mcpServers", `{"type":"http","url":"https://example.test","headers":{"Authorization":null}}`, "headers", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"oauth unresolved", "mcpServers", `{"type":"http","url":"https://example.test","oauth":{"clientSecret":"secret-value"}}`, "oauth", "UNKNOWN_NATIVE_FIELD", model.FindingUnknown},
		{"sse deferred", "mcpServers", `{"type":"sse","url":"https://example.test"}`, "type", "UNSUPPORTED_TRANSPORT", model.FindingUnsupported},
		{"future transport", "mcpServers", `{"type":"future"}`, "type", "UNSUPPORTED_TRANSPORT", model.FindingUnsupported},
		{"future field", "mcpServers", `{"command":"node","future/key~":true}`, "future/key~", "UNKNOWN_NATIVE_FIELD", model.FindingUnknown},
		{"negative timeout", "lspServers", `{"startupTimeout":-1}`, "startupTimeout", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"zero timeout", "lspServers", `{"shutdownTimeout":0}`, "shutdownTimeout", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"fractional timeout", "lspServers", `{"startupTimeout":1.5}`, "startupTimeout", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"null restart", "lspServers", `{"restartOnCrash":null}`, "restartOnCrash", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"negative restarts", "lspServers", `{"maxRestarts":-1}`, "maxRestarts", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"string diagnostics", "lspServers", `{"diagnostics":"true"}`, "diagnostics", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"workspace type", "lspServers", `{"workspaceFolder":42}`, "workspaceFolder", "INVALID_NATIVE_FIELD", model.FindingMissing},
		{"strict unknown", "lspServers", `{"future":true}`, "future", "UNKNOWN_NATIVE_FIELD", model.FindingUnknown},
		{"newer version", "lspServers", `{"requestTimeout":1000}`, "requestTimeout", "UNKNOWN_NATIVE_FIELD", model.FindingUnknown},
		{"lsp transport", "lspServers", `{"transport":"future"}`, "transport", "UNSUPPORTED_TRANSPORT", model.FindingUnsupported},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			definition := test.definition
			if test.kind == "lspServers" {
				definition = `{"command":"gopls","extensionToLanguage":{".go":"go"},` + definition[1:]
			}

			source := fstest.MapFS{claude.ManifestPath: {Data: []byte(`{"name":"tools","` + test.kind + `":{"server":` + definition + `}}`)}}
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
			pointer := "/" + test.kind + "/server/" + test.field
			if test.field == "future/key~" {
				pointer = "/mcpServers/server/future~1key~0"
			}

			assert.Equal(t, model.Location{Path: claude.ManifestPath, Pointer: pointer}, matches[0].Locations[0])
			encoded, err := json.Marshal(plugin.Findings)
			require.NoError(t, err)
			assert.NotContains(t, string(encoded), "secret-value")
		})
	}
}

func TestServerValidShapesRemainRuntimeUnverified(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{
		claude.ManifestPath: {Data: []byte(`{"name":"tools","mcpServers":{"local":{"command":"node","args":["","server.js"],"env":{"TOKEN":""}},"remote":{"type":"streamable-http","url":"https://example.test/mcp","headers":{},"headersHelper":"get-headers"}},"lspServers":{"go":{"command":"/path with spaces/gopls","extensionToLanguage":{".go":"go"},"transport":"socket","args":[],"env":{},"workspaceFolder":"","initializationOptions":{"x":[true]},"settings":{},"startupTimeout":1e3,"shutdownTimeout":1.0,"maxRestarts":0,"restartOnCrash":false,"diagnostics":true}}}`)},
	}
	plugin, err := claude.ReadPlugin(agentSystem(source), "requested", "resolved")
	require.NoError(t, err)
	require.Len(t, plugin.Inventory.Capabilities, 3)
	count := 0
	for _, finding := range plugin.Findings {
		assert.NotEqual(t, "INVALID_NATIVE_FIELD", finding.Code)
		assert.NotEqual(t, "UNKNOWN_NATIVE_FIELD", finding.Code)
		assert.NotEqual(t, "INAPPLICABLE_NATIVE_FIELD", finding.Code)
		if finding.Code == "NATIVE_SERVER_SUPPORT" {
			count++
			assert.Equal(t, model.FindingUnknown, finding.Status)
		}
	}

	assert.Equal(t, 3, count)
}

func TestServerFieldSidecarProvenanceAndPreservation(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{
		claude.ManifestPath: {Data: []byte(`{"name":"tools","mcpServers":"./servers.json"}`)},
		"servers.json":      {Data: []byte(`{"mcpServers":{"db":{"command":"node","env":{"TOKEN":42}}}}`)},
	}
	plugin, err := claude.ReadPlugin(agentSystem(source), "requested", "resolved")
	require.NoError(t, err)
	require.Len(t, plugin.Inventory.Capabilities, 1)
	definition, ok := plugin.Inventory.Capabilities[0].Definition.(native.MCP)
	require.True(t, ok)
	assert.JSONEq(t, `{"TOKEN":42}`, string(definition.Fields["env"]))
	var invalid []model.Finding
	for _, finding := range plugin.Findings {
		if finding.Code == "INVALID_NATIVE_FIELD" {
			invalid = append(invalid, finding)
			assert.Equal(t, []model.Location{{Path: "servers.json", Pointer: "/mcpServers/db/env"}, {Path: claude.ManifestPath, Pointer: "/mcpServers"}}, finding.Locations)
		}
	}

	require.Len(t, invalid, 1)
}
