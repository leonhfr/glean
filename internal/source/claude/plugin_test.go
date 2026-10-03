package claude_test

import (
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

func TestReadPluginSixKindsSingleBoundary(t *testing.T) {
	t.Parallel()
	source := pluginSource()
	source[claude.ManifestPath] = &fstest.MapFile{Data: []byte(`{"name":"tools","dependencies":["shared@examples"],"userConfig":{"token":{"type":"string","sensitive":true}},"outputStyles":"./styles","customMetadata":{"label":"retain"}}`)}
	opened, closed, manifestReads, payloadWalks := 0, 0, 0, 0
	sys := &system.Fake{OpenRootHandler: func(directory string) (system.Root, error) {
		opened++
		assert.Equal(t, "requested", directory)
		return system.FakeRoot{Path: "/source/tools", Source: pluginCountingFS{MapFS: source, manifestReads: &manifestReads, payloadWalks: &payloadWalks}, CloseHandler: func() error { closed++; return nil }}, nil
	}}
	plugin, err := claude.ReadPlugin(sys, "requested", "resolved")
	require.NoError(t, err)
	assert.Equal(t, 1, opened)
	assert.Equal(t, 1, closed)
	assert.Equal(t, 1, manifestReads)
	assert.Equal(t, 1, payloadWalks)
	assert.Equal(t, model.Package{ID: "resolved", Name: "tools", Root: "/source/tools"}, plugin.Inventory.Package)
	assert.True(t, plugin.Manifest.Present)
	assert.Equal(t, plugin.Inventory.Package.Root, plugin.Manifest.Root)
	assert.JSONEq(t, `["shared@examples"]`, string(plugin.Manifest.Fields["dependencies"]))
	assert.JSONEq(t, `{"token":{"type":"string","sensitive":true}}`, string(plugin.Manifest.Fields["userConfig"]))
	assert.JSONEq(t, `"./styles"`, string(plugin.Manifest.Fields["outputStyles"]))
	assert.JSONEq(t, `{"label":"retain"}`, string(plugin.Manifest.Fields["customMetadata"]))
	var ids []string
	for _, capability := range plugin.Inventory.Capabilities {
		ids = append(ids, capability.ID.String())
		assert.Equal(t, plugin.Inventory.Package.ID, capability.PackageID)
		assert.Equal(t, plugin.Manifest.Root, capability.Provenance.Root)
		require.NotNil(t, capability.Definition)
		assert.Equal(t, capability.ID.Kind, capability.Definition.Kind())
	}

	assert.Equal(t, []string{
		"claude:agent:review", "claude:command:review", "claude:hook:PostToolUse", "claude:lsp:review", "claude:mcp:review", "skill:review",
	}, ids)
}

func TestReadPluginEachComponentFailureDiscardsCompleteResult(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, field string
		cause       error
	}{
		{name: "skills", field: `"skills":null`, cause: claude.ErrInvalidSkills},
		{name: "agents", field: `"agents":null`, cause: claude.ErrInvalidAgents},
		{name: "commands", field: `"commands":null`, cause: claude.ErrInvalidCommands},
		{name: "hooks", field: `"hooks":null`, cause: claude.ErrInvalidHooks},
		{name: "MCP", field: `"mcpServers":null`, cause: claude.ErrInvalidMCP},
		{name: "LSP", field: `"lspServers":null`, cause: claude.ErrInvalidLSP},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source := pluginSource()
			source[claude.ManifestPath] = &fstest.MapFile{Data: []byte(`{"name":"tools",` + test.field + `}`)}
			closed := 0
			closeFailure := errors.New("close failed")
			sys := &system.Fake{OpenRootHandler: func(string) (system.Root, error) {
				return system.FakeRoot{Path: "/source/tools", Source: source, CloseHandler: func() error { closed++; return closeFailure }}, nil
			}}
			plugin, err := claude.ReadPlugin(sys, "requested", "resolved")
			require.ErrorIs(t, err, claude.ErrInvalidPlugin)
			require.ErrorIs(t, err, test.cause)
			require.ErrorIs(t, err, closeFailure)
			assert.Empty(t, plugin)
			assert.Equal(t, 1, closed)
		})
	}
}

func TestReadPluginEmptyManifestlessSource(t *testing.T) {
	t.Parallel()
	plugin, err := claude.ReadPlugin(agentSystem(fstest.MapFS{}), "requested", "resolved")
	require.NoError(t, err)
	assert.False(t, plugin.Manifest.Present)
	assert.Equal(t, "review-tools", plugin.Manifest.Name)
	assert.Equal(t, plugin.Manifest.Name, plugin.Inventory.Package.Name)
	assert.Empty(t, plugin.Inventory.Capabilities)
	_, err = claude.ReadPlugin(&system.Fake{}, "requested", " ")
	require.ErrorIs(t, err, claude.ErrInvalidPlugin)
}

func TestReadPluginAcquisitionManifestAndCloseFailures(t *testing.T) {
	t.Parallel()
	failure := errors.New("injected failure")
	sys := &system.Fake{OpenRootHandler: func(string) (system.Root, error) { return nil, failure }}
	plugin, err := claude.ReadPlugin(sys, "requested", "resolved")
	require.ErrorIs(t, err, failure)
	assert.Empty(t, plugin)
	closed := 0
	source := pluginSource()
	sys.OpenRootHandler = func(string) (system.Root, error) {
		return system.FakeRoot{Path: "/source/tools", Source: source, CloseHandler: func() error { closed++; return failure }}, nil
	}

	plugin, err = claude.ReadPlugin(sys, "requested", "resolved")
	require.ErrorIs(t, err, failure)
	assert.Empty(t, plugin)
	assert.Equal(t, 1, closed)
	source[claude.ManifestPath] = &fstest.MapFile{Data: []byte(`{"name":null}`)}
	plugin, err = claude.ReadPlugin(sys, "requested", "resolved")
	require.ErrorIs(t, err, claude.ErrInvalidManifest)
	require.ErrorIs(t, err, failure)
	assert.Empty(t, plugin)
	assert.Equal(t, 2, closed)
}

func TestReadPluginBoundaryRejectionClosesRoot(t *testing.T) {
	t.Parallel()
	closed := 0
	failure := errors.New("close failed")
	source := pluginSource()
	source["linked"] = &fstest.MapFile{Mode: fs.ModeSymlink, Data: []byte("skills/review")}
	sys := &system.Fake{OpenRootHandler: func(string) (system.Root, error) {
		return system.FakeRoot{Path: "/source/tools", Source: source, CloseHandler: func() error { closed++; return failure }}, nil
	}}
	plugin, err := claude.ReadPlugin(sys, "requested", "resolved")
	require.ErrorIs(t, err, claude.ErrUnsupportedPayload)
	require.ErrorIs(t, err, failure)
	assert.Empty(t, plugin)
	assert.Equal(t, 1, closed)
}

func TestReadPluginPreservesLateFilesystemFailure(t *testing.T) {
	t.Parallel()
	failure := errors.New("LSP read failed")
	closed := 0
	sys := &system.Fake{OpenRootHandler: func(string) (system.Root, error) {
		return system.FakeRoot{Path: "/source/tools", Source: lspReadFailureFS{MapFS: pluginSource(), failure: failure}, CloseHandler: func() error { closed++; return nil }}, nil
	}}
	plugin, err := claude.ReadPlugin(sys, "requested", "resolved")
	require.ErrorIs(t, err, claude.ErrInvalidPlugin)
	require.ErrorIs(t, err, failure)
	assert.Empty(t, plugin)
	assert.Equal(t, 1, closed)
}

type pluginCountingFS struct {
	fstest.MapFS
	manifestReads *int
	payloadWalks  *int
}

func (f pluginCountingFS) ReadFile(filename string) ([]byte, error) {
	if filename == claude.ManifestPath {
		*f.manifestReads++
	}

	return fs.ReadFile(f.MapFS, filename)
}

func (f pluginCountingFS) ReadDir(directory string) ([]fs.DirEntry, error) {
	if directory == "." {
		*f.payloadWalks++
	}

	return fs.ReadDir(f.MapFS, directory)
}

func pluginSource() fstest.MapFS {
	return fstest.MapFS{
		claude.ManifestPath:      {Data: []byte(`{"name":"tools"}`)},
		"skills/review/SKILL.md": {Data: []byte("Review skill")},
		"agents/review.md":       {Data: []byte("Review agent")},
		"commands/review.md":     {Data: []byte("Review command")},
		"hooks/hooks.json":       {Data: []byte(`{"hooks":{"PostToolUse":[{"hooks":[{"type":"command","command":"echo hook"}]}]}}`)},
		".mcp.json":              {Data: []byte(`{"review":{"command":"node"}}`)},
		".lsp.json":              {Data: []byte(`{"review":{"command":"gopls","extensionToLanguage":{".go":"go"}}}`)},
	}
}
