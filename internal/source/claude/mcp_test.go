package claude_test

import (
	"errors"
	"io/fs"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/leonhfr/glean/internal/model"
	claudemodel "github.com/leonhfr/glean/internal/model/claude"
	"github.com/leonhfr/glean/internal/source/claude"
	"github.com/leonhfr/glean/internal/system"
)

func TestReadMCPCombinesWrappedBareAndInline(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{
		claude.ManifestPath: {Data: []byte(`{"name":"tools","mcpServers":["./extra.json",{"remote":{"type":"http","url":"https://example.test/${ENDPOINT}","headers":{"Authorization":"Bearer ${TOKEN}"},"oauth":{"clientId":"${user_config.client}"}}}]}`)},
		".mcp.json":         {Data: []byte(`{"mcpServers":{"local":{"command":"node","args":["${CLAUDE_PLUGIN_ROOT}/server.js"],"env":{"DATA":"${CLAUDE_PLUGIN_DATA}","OPTION":"${user_config.option}"},"cwd":"${CLAUDE_PROJECT_DIR}"}}}`)},
		"extra.json":        {Data: []byte(`{"extra":{"type":"stdio","command":"python","args":["script.py"],"futureField":true}}`)},
	}
	inventory, err := claude.ReadMCP(agentSystem(source), "requested", "package")
	require.NoError(t, err)
	assert.Equal(t, "tools", inventory.Package.Name)
	require.Len(t, inventory.Capabilities, 3)
	assert.Equal(t, "extra", inventory.Capabilities[0].ID.Name)
	local := inventory.Capabilities[1]
	assert.Equal(t, "claude:mcp:local", local.ID.String())
	assert.Equal(t, model.PackageID("package"), local.PackageID)
	assert.Equal(t, []string{".mcp.json"}, local.Payload.Entrypoints)
	assert.Empty(t, local.Payload.Assets)
	definition, ok := local.Definition.(claudemodel.MCP)
	require.True(t, ok)
	assert.Equal(t, model.KindClaudeMCP, definition.Kind())
	assert.Equal(t, claudemodel.MCPStdio, definition.Transport)
	assert.NotContains(t, definition.Fields, "type")
	assert.JSONEq(t, `["${CLAUDE_PLUGIN_ROOT}/server.js"]`, string(definition.Fields["args"]))
	assert.Equal(t, []model.Location{{Path: ".mcp.json", Pointer: "/mcpServers/local"}}, local.Provenance.Declarations)
	assert.Equal(t, []model.Location{{Path: "extra.json", Pointer: "/extra"}, {Path: claude.ManifestPath, Pointer: "/mcpServers/0"}}, inventory.Capabilities[0].Provenance.Declarations)
	remote := inventory.Capabilities[2]
	assert.Empty(t, remote.Payload.Entrypoints)
	definition, ok = remote.Definition.(claudemodel.MCP)
	require.True(t, ok)
	assert.Equal(t, claudemodel.MCPHTTP, definition.Transport)
	assert.JSONEq(t, `{"Authorization":"Bearer ${TOKEN}"}`, string(definition.Fields["headers"]))
	assert.JSONEq(t, `{"clientId":"${user_config.client}"}`, string(definition.Fields["oauth"]))
}

func TestReadMCPEquivalentDuplicatesRetainLocations(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{
		claude.ManifestPath: {Data: []byte(`{"name":"tools","mcpServers":["./.mcp.json","./other.json",{"db":{"env":{"B":"2","A":"1"},"command":"node"}}]}`)},
		".mcp.json":         {Data: []byte(`{"db":{"command":"node","env":{"A":"1","B":"2"}}}`)},
		"other.json":        {Data: []byte(`{"mcpServers":{"db":{"command":"node","env":{"B":"2","A":"1"}}}}`)},
	}
	inventory, err := claude.ReadMCP(agentSystem(source), "requested", "package")
	require.NoError(t, err)
	require.Len(t, inventory.Capabilities, 1)
	assert.Len(t, inventory.Capabilities[0].Provenance.Declarations, 5)
	assert.Equal(t, []string{".mcp.json", "other.json"}, inventory.Capabilities[0].Payload.Entrypoints)
}

func TestReadMCPConflictingDuplicateBlocks(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{
		claude.ManifestPath: {Data: []byte(`{"name":"tools","mcpServers":{"db":{"command":"second"}}}`)},
		".mcp.json":         {Data: []byte(`{"db":{"command":"first"}}`)},
	}
	inventory, err := claude.ReadMCP(agentSystem(source), "requested", "package")
	require.ErrorIs(t, err, claude.ErrInvalidMCP)
	require.ErrorContains(t, err, "conflicting server")
	require.ErrorContains(t, err, ".mcp.json")
	require.ErrorContains(t, err, claude.ManifestPath)
	assert.Empty(t, inventory)
}

func TestReadMCPDuplicateComparisonPreservesNumbersAndArrayOrder(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, first, second string }{
		{name: "large numbers", first: `{"command":"node","option":9007199254740992}`, second: `{"command":"node","option":9007199254740993}`},
		{name: "argument order", first: `{"command":"node","args":["first","second"]}`, second: `{"command":"node","args":["second","first"]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			source := fstest.MapFS{
				claude.ManifestPath: {Data: []byte(`{"name":"tools","mcpServers":{"db":` + test.second + `}}`)},
				".mcp.json":         {Data: []byte(`{"db":` + test.first + `}`)},
			}
			inventory, err := claude.ReadMCP(agentSystem(source), "requested", "package")
			require.ErrorIs(t, err, claude.ErrInvalidMCP)
			require.ErrorContains(t, err, "conflicting server")
			assert.Empty(t, inventory)
		})
	}
}

func TestReadMCPNativeTransportsRemainObservable(t *testing.T) {
	t.Parallel()
	for _, transport := range []string{"stdio", "http", "sse", "streamable-http", "future"} {
		t.Run(transport, func(t *testing.T) {
			t.Parallel()
			source := fstest.MapFS{claude.ManifestPath: {Data: []byte(`{"name":"tools","mcpServers":{"a~/b":{"type":"` + transport + `","futureField":true}}}`)}}
			inventory, err := claude.ReadMCP(agentSystem(source), "requested", "package")
			require.NoError(t, err)
			require.Len(t, inventory.Capabilities, 1)
			definition, ok := inventory.Capabilities[0].Definition.(claudemodel.MCP)
			require.True(t, ok)
			assert.Equal(t, claudemodel.MCPTransport(transport), definition.Transport)
			assert.Equal(t, []model.Location{{Path: claude.ManifestPath, Pointer: "/mcpServers/a~0~1b"}}, inventory.Capabilities[0].Provenance.Declarations)
		})
	}
}

func TestReadMCPEmptyDeclarationsKeepDefault(t *testing.T) {
	t.Parallel()
	for _, declaration := range []string{`[]`, `{}`} {
		t.Run(declaration, func(t *testing.T) {
			t.Parallel()
			source := fstest.MapFS{
				claude.ManifestPath: {Data: []byte(`{"name":"tools","mcpServers":` + declaration + `}`)},
				".mcp.json":         {Data: []byte(`{"db":{"command":"node"}}`)},
			}
			inventory, err := claude.ReadMCP(agentSystem(source), "requested", "package")
			require.NoError(t, err)
			assert.Len(t, inventory.Capabilities, 1)
		})
	}

	inventory, err := claude.ReadMCP(agentSystem(fstest.MapFS{}), "requested", "package")
	require.NoError(t, err)
	assert.Empty(t, inventory.Capabilities)
}

func TestReadMCPRejectsInvalidDeclarations(t *testing.T) {
	t.Parallel()
	for _, declaration := range []string{
		`null`, `4`, `true`, `[null]`, `[[]]`, `"missing.json"`, `"./../missing.json"`, `"./.git/config.json"`, `"./missing.json"`, `"./file.txt"`, `"./directory.json"`,
		`{"db":null}`, `{"db":[]}`, `{"db":{"type":null}}`, `{"db":{"type":false}}`, `{"db":{"type":" "}}`, `{" ":{}}`,
		`{"db":{},"db":{}}`, `{"db":{"command":"a","command":"b"}}`, `{"db":{"env":{"A":"1","A":"2"}}}`, `"./malformed.json"`, `"./invalid-wrapper.json"`,
	} {
		t.Run(declaration, func(t *testing.T) {
			t.Parallel()
			source := fstest.MapFS{
				claude.ManifestPath: {Data: []byte(`{"name":"tools","mcpServers":` + declaration + `}`)},
				"file.txt":          {Data: []byte(`{}`)}, "directory.json": {Mode: fs.ModeDir},
				"malformed.json": {Data: []byte(`{} {}`)}, "invalid-wrapper.json": {Data: []byte(`{"mcpServers":null}`)},
			}
			inventory, err := claude.ReadMCP(agentSystem(source), "requested", "package")
			require.ErrorIs(t, err, claude.ErrInvalidMCP)
			assert.Empty(t, inventory)
		})
	}
}

func TestReadMCPRejectsBundlesWithoutAcquisition(t *testing.T) {
	t.Parallel()
	for _, reference := range []string{"./server.mcpb", "./server.dxt", "https://example.test/server.mcpb", "https://example.test/server.dxt?download=1"} {
		t.Run(reference, func(t *testing.T) {
			t.Parallel()
			source := fstest.MapFS{claude.ManifestPath: {Data: []byte(`{"name":"tools","mcpServers":"` + reference + `"}`)}}
			inventory, err := claude.ReadMCP(agentSystem(source), "requested", "package")
			require.ErrorIs(t, err, claude.ErrUnsupportedMCPBundle)
			require.ErrorIs(t, err, claude.ErrInvalidMCP)
			assert.Empty(t, inventory)
		})
	}
}

func TestReadMCPFailures(t *testing.T) {
	t.Parallel()
	failure := errors.New("read failed")
	closeFailure := errors.New("close failed")
	closed := 0
	sys := &system.Fake{OpenRootHandler: func(string) (system.Root, error) {
		return system.FakeRoot{Path: "/source/tools", Source: mcpReadFailureFS{MapFS: fstest.MapFS{".mcp.json": {Data: []byte(`{}`)}}, failure: failure}, CloseHandler: func() error { closed++; return closeFailure }}, nil
	}}
	inventory, err := claude.ReadMCP(sys, "requested", "package")
	require.ErrorIs(t, err, failure)
	require.ErrorIs(t, err, closeFailure)
	assert.Empty(t, inventory)
	assert.Equal(t, 1, closed)
	_, err = claude.ReadMCP(&system.Fake{}, "requested", " ")
	require.ErrorIs(t, err, claude.ErrInvalidMCP)
	_, err = claude.ReadMCP(agentSystem(fstest.MapFS{claude.ManifestPath: {Data: []byte(`{"name":"tools","mcpServers":"./missing.json"}`)}}), "requested", "package")
	require.ErrorIs(t, err, fs.ErrNotExist)
	sys.OpenRootHandler = func(string) (system.Root, error) { return nil, failure }

	_, err = claude.ReadMCP(sys, "requested", "package")
	require.ErrorIs(t, err, failure)
	sys.OpenRootHandler = func(string) (system.Root, error) {
		return system.FakeRoot{Path: "/source/tools", Source: fstest.MapFS{".mcp.json": {Data: []byte(`{"db":{"command":"node"}}`)}}, CloseHandler: func() error { return closeFailure }}, nil
	}

	inventory, err = claude.ReadMCP(sys, "requested", "package")
	require.ErrorIs(t, err, closeFailure)
	assert.Empty(t, inventory)
}

type mcpReadFailureFS struct {
	fstest.MapFS
	failure error
}

func (f mcpReadFailureFS) ReadFile(filename string) ([]byte, error) {
	if filename == ".mcp.json" {
		return nil, f.failure
	}

	return fs.ReadFile(f.MapFS, filename)
}
