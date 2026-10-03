package claude_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/leonhfr/glean/internal/model"
	"github.com/leonhfr/glean/internal/source/claude"
	"github.com/leonhfr/glean/internal/system"
)

func TestPluginAssessmentComponentsAndMetadata(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{
		claude.ManifestPath: {Data: []byte(`{"name":"tools","description":"ordinary metadata","outputStyles":"./custom","experimental":{"themes":"./themes"},"futureField":true}`)},
		"bin/tool":          {Data: []byte("binary")},
		"settings.json":     {Data: []byte(`{}`)},
	}
	plugin, err := claude.ReadPlugin(agentSystem(source), "requested", "resolved")
	require.NoError(t, err)
	var references []string
	for _, finding := range plugin.Findings {
		references = append(references, finding.Reference)
		assert.Empty(t, finding.Capability)
		assert.NotEmpty(t, finding.Locations)
	}

	assert.ElementsMatch(t, []string{"experimental.themes", "futureField", "outputStyles", "bin", "settings.json"}, references)
	assert.Empty(t, plugin.Inventory.Capabilities)
}

func TestPluginAssessmentReferencesDoNotExportValues(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{
		claude.ManifestPath: {Data: []byte(`{"name":"tools","userConfig":{"token":{"type":"string","sensitive":true,"default":"do-not-export-this"}},"mcpServers":{"docs":{"command":"node","env":{"TOKEN":"${user_config.token}","OTHER":"${user_config.missing}","RAW":"do-not-export-that","DATA":"${CLAUDE_PLUGIN_DATA}"}}}}`)},
	}
	plugin, err := claude.ReadPlugin(agentSystem(source), "requested", "resolved")
	require.NoError(t, err)
	findings := map[string]model.Finding{}
	for _, finding := range plugin.Findings {
		findings[finding.Code+":"+finding.Reference] = finding
	}

	assert.Equal(t, model.FindingUnknown, findings["USER_CONFIG_REFERENCE:token"].Status)
	assert.Equal(t, model.FindingMissing, findings["USER_CONFIG_REFERENCE:missing"].Status)
	assert.Equal(t, model.FindingUnknown, findings["PERSISTENT_DATA:CLAUDE_PLUGIN_DATA"].Status)
	assert.Equal(t, model.CapabilityID{Kind: model.KindClaudeMCP, Name: "docs"}, findings["USER_CONFIG_REFERENCE:missing"].Capability)
	assert.Equal(t, model.FindingUnknown, findings["EXECUTABLE:command"].Status)
	encoded, err := json.Marshal(plugin.Findings)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "do-not-export-this")
	assert.NotContains(t, string(encoded), "do-not-export-that")
}

func TestPluginAssessmentMarkdownRequirements(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{
		claude.ManifestPath:      {Data: []byte(`{"name":"tools","userConfig":{"tone":{"type":"string"}}}`)},
		"skills/review/SKILL.md": {Data: []byte("Use ${user_config.tone} twice ${user_config.tone} and ${CLAUDE_PLUGIN_DATA}.")},
	}
	plugin, err := claude.ReadPlugin(agentSystem(source), "requested", "resolved")
	require.NoError(t, err)
	count := 0
	for _, finding := range plugin.Findings {
		if finding.Code == "USER_CONFIG_REFERENCE" {
			count++
			assert.Equal(t, "tone", finding.Reference)
			assert.Equal(t, model.KindSkill, finding.Capability.Kind)
		}
	}

	assert.Equal(t, 1, count)
}

func TestPluginAssessmentDependenciesStayUnverified(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{claude.ManifestPath: {Data: []byte(`{"name":"tools","dependencies":["shared@examples",{"name":"companion","marketplace":"other","version":"1"},null,"https://secret:token@example.test"]}`)}}
	plugin, err := claude.ReadPlugin(agentSystem(source), "requested", "resolved")
	require.NoError(t, err)
	require.Len(t, plugin.Findings, 4)
	assert.Equal(t, "shared@examples", plugin.Findings[0].Reference)
	assert.Equal(t, model.FindingUnknown, plugin.Findings[0].Status)
	assert.Equal(t, model.FindingUnknown, plugin.Findings[1].Status)
	assert.Equal(t, model.FindingMissing, plugin.Findings[2].Status)
	assert.Empty(t, plugin.Findings[3].Reference)
}

func TestPluginAssessmentHookCompanions(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{claude.ManifestPath: {Data: []byte(`{"name":"tools","mcpServers":{"db":{"command":"node"}},"hooks":{"PostToolUse":[{"hooks":[{"type":"mcp_tool","server":"plugin:tools:db","tool":"check"},{"type":"mcp_tool","server":"plugin:tools:absent","tool":"check"},{"type":"mcp_tool","server":"external","tool":"check"},{"type":"future_handler"}]}]}}`)}}
	plugin, err := claude.ReadPlugin(agentSystem(source), "requested", "resolved")
	require.NoError(t, err)
	statuses := map[string]model.FindingStatus{}
	for _, finding := range plugin.Findings {
		if finding.Code == "MCP_COMPANION" {
			statuses[finding.Reference] = finding.Status
		}

		if finding.Code == "NATIVE_HOOK_SUPPORT" && finding.Reference == "type" {
			assert.Equal(t, model.FindingUnsupported, finding.Status)
		}
	}

	assert.Equal(t, map[string]model.FindingStatus{"db": model.FindingVerified, "absent": model.FindingMissing, "external server": model.FindingUnknown}, statuses)
}

func TestPluginAssessmentInvalidUserConfigRejectsResult(t *testing.T) {
	t.Parallel()
	plugin, err := claude.ReadPlugin(agentSystem(fstest.MapFS{claude.ManifestPath: {Data: []byte(`{"name":"tools","userConfig":null}`)}}), "requested", "resolved")
	require.ErrorIs(t, err, claude.ErrInvalidPlugin)
	assert.Empty(t, plugin)
}

func TestPluginAssessmentEmptyDefaultDirectories(t *testing.T) {
	t.Parallel()
	plugin, err := claude.ReadPlugin(agentSystem(fstest.MapFS{"bin": {Mode: fs.ModeDir}}), "requested", "resolved")
	require.NoError(t, err)
	assert.Empty(t, plugin.Findings)
}

func TestPluginAssessmentModulesWithoutEventGroups(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{"hooks/hooks.json": {Data: []byte(`{"hooks":{},"modules":["./module.js"]}`)}, "module.js": {Data: []byte("export default {}")}}
	plugin, err := claude.ReadPlugin(agentSystem(source), "requested", "resolved")
	require.NoError(t, err)
	assert.Empty(t, plugin.Inventory.Capabilities)
	require.Len(t, plugin.Findings, 1)
	assert.Equal(t, model.FindingUnsupported, plugin.Findings[0].Status)
	assert.Equal(t, []model.Location{{Path: "hooks/hooks.json", Pointer: "/modules"}}, plugin.Findings[0].Locations)
}

func TestPluginAssessmentBundledExecutablePresence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		exists bool
		status model.FindingStatus
	}{
		{"existing", true, model.FindingVerified}, {"missing", false, model.FindingMissing},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source := fstest.MapFS{claude.ManifestPath: {Data: []byte(`{"name":"tools","mcpServers":{"server":{"command":"${CLAUDE_PLUGIN_ROOT}/server.js"}}}`)}}
			if test.exists {
				source["server.js"] = &fstest.MapFile{Data: []byte("code")}
			}

			plugin, err := claude.ReadPlugin(agentSystem(source), "requested", "resolved")
			require.NoError(t, err)
			var found bool
			for _, finding := range plugin.Findings {
				if finding.Code == "LOCAL_EXECUTABLE" {
					found = true
					assert.Equal(t, test.status, finding.Status)
				}
			}

			assert.True(t, found)
		})
	}
}

func TestPluginAssessmentReusesInventoryFileBytes(t *testing.T) {
	t.Parallel()
	reads := 0
	source := assessmentReadOnceFS{MapFS: fstest.MapFS{"hooks/hooks.json": {Data: []byte(`{"hooks":{},"modules":["./module.js"]}`)}}, reads: &reads}
	plugin, err := claude.ReadPlugin(agentSystem(source), "requested", "resolved")
	require.NoError(t, err)
	assert.Equal(t, 1, reads)
	require.Len(t, plugin.Findings, 1)
	assert.Equal(t, "modules", plugin.Findings[0].Reference)
}

func TestPluginAssessmentFilesystemFailureDiscardsResult(t *testing.T) {
	t.Parallel()
	failure := errors.New("component inspection failed")
	closed := 0
	sys := &system.Fake{OpenRootHandler: func(string) (system.Root, error) {
		return system.FakeRoot{Path: "/source/tools", Source: assessmentStatFailureFS{MapFS: pluginSource(), failure: failure}, CloseHandler: func() error { closed++; return nil }}, nil
	}}
	plugin, err := claude.ReadPlugin(sys, "requested", "resolved")
	require.ErrorIs(t, err, failure)
	assert.Empty(t, plugin)
	assert.Equal(t, 1, closed)
}

type assessmentReadOnceFS struct {
	fstest.MapFS
	reads *int
}

func (f assessmentReadOnceFS) ReadFile(name string) ([]byte, error) {
	if name == "hooks/hooks.json" {
		*f.reads++
		if *f.reads > 1 {
			return nil, errors.New("unexpected repeated source read")
		}
	}

	return fs.ReadFile(f.MapFS, name)
}

type assessmentStatFailureFS struct {
	fstest.MapFS
	failure error
}

func (f assessmentStatFailureFS) Stat(name string) (fs.FileInfo, error) {
	if name == "settings.json" {
		return nil, f.failure
	}

	return fs.Stat(f.MapFS, name)
}

func TestPluginAssessmentIgnoresOverriddenComponentDefaults(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{
		claude.ManifestPath:      {Data: []byte(`{"name":"tools","outputStyles":[],"experimental":{"themes":[],"monitors":[]}}`)},
		"output-styles/style.md": {Data: []byte("Style")},
		"themes/theme.json":      {Data: []byte(`{}`)},
		"monitors/monitors.json": {Data: []byte(`[]`)},
	}
	plugin, err := claude.ReadPlugin(agentSystem(source), "requested", "resolved")
	require.NoError(t, err)
	var references []string
	for _, finding := range plugin.Findings {
		references = append(references, finding.Reference)
	}

	assert.ElementsMatch(t, []string{"outputStyles", "experimental.themes", "experimental.monitors"}, references)
}
