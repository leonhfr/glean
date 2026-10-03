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

func TestReadLSPCombinesDefaultFileAndInline(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{
		claude.ManifestPath: {Data: []byte(`{"name":"tools","lspServers":["./extra.json",{"ts":{"command":"typescript-language-server","args":["--stdio"],"extensionToLanguage":{".ts":"typescript"},"transport":"socket"}}]}`)},
		".lsp.json": {
			Data: []byte(
				`{"go":{"command":"${CLAUDE_PLUGIN_ROOT}/bin/gopls","args":["serve"],"extensionToLanguage":{".go":"go"},"env":{"DATA":"${CLAUDE_PLUGIN_DATA}"},"workspaceFolder":"${CLAUDE_PROJECT_DIR}","initializationOptions":{"features":["navigation"]},"settings":{"gopls":{"analyses":{"unusedparams":true}}},"startupTimeout":1000,"shutdownTimeout":2000,"requestTimeout":3000,"restartOnCrash":false,"maxRestarts":2,"diagnostics":true}}`,
			),
		},
		"extra.json": {Data: []byte(`{"python":{"command":"pyright-langserver","extensionToLanguage":{".py":"python"},"args":["--stdio"]}}`)},
	}
	inventory, err := claude.ReadLSP(agentSystem(source), "requested", "package")
	require.NoError(t, err)
	assert.Equal(t, "tools", inventory.Package.Name)
	assert.Equal(t, model.PackageID("package"), inventory.Package.ID)
	require.Len(t, inventory.Capabilities, 3)
	goCapability := inventory.Capabilities[0]
	assert.Equal(t, "claude:lsp:go", goCapability.ID.String())
	assert.Equal(t, inventory.Package.ID, goCapability.PackageID)
	assert.Equal(t, []string{".lsp.json"}, goCapability.Payload.Entrypoints)
	assert.Empty(t, goCapability.Payload.Assets)
	definition, ok := goCapability.Definition.(claudemodel.LSP)
	require.True(t, ok)
	assert.Equal(t, model.KindClaudeLSP, definition.Kind())
	assert.Equal(t, "${CLAUDE_PLUGIN_ROOT}/bin/gopls", definition.Command)
	assert.Equal(t, map[string]string{".go": "go"}, definition.ExtensionToLanguage)
	assert.Equal(t, claudemodel.LSPStdio, definition.Transport)
	assert.NotContains(t, definition.Fields, "transport")
	assert.JSONEq(t, `{"DATA":"${CLAUDE_PLUGIN_DATA}"}`, string(definition.Fields["env"]))
	assert.JSONEq(t, `{"gopls":{"analyses":{"unusedparams":true}}}`, string(definition.Fields["settings"]))
	assert.JSONEq(t, `false`, string(definition.Fields["restartOnCrash"]))
	assert.JSONEq(t, `3000`, string(definition.Fields["requestTimeout"]))
	assert.Equal(t, []model.Location{{Path: ".lsp.json", Pointer: "/go"}}, goCapability.Provenance.Declarations)
	assert.Equal(t, []model.Location{{Path: "extra.json", Pointer: "/python"}, {Path: claude.ManifestPath, Pointer: "/lspServers/0"}}, inventory.Capabilities[1].Provenance.Declarations)
	ts := inventory.Capabilities[2]
	assert.Empty(t, ts.Payload.Entrypoints)
	definition, ok = ts.Definition.(claudemodel.LSP)
	require.True(t, ok)
	assert.Equal(t, claudemodel.LSPSocket, definition.Transport)
	assert.Equal(t, []model.Location{{Path: claude.ManifestPath, Pointer: "/lspServers/1/ts"}}, ts.Provenance.Declarations)
}

func TestReadLSPEquivalentDeclarationsDeduplicate(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{
		claude.ManifestPath: {Data: []byte(`{"name":"tools","lspServers":["./.lsp.json","./other.json",{"go":{"extensionToLanguage":{".go":"go"},"command":"gopls","settings":{"B":2,"A":1}}}]}`)},
		".lsp.json":         {Data: []byte(`{"go":{"command":"gopls","extensionToLanguage":{".go":"go"},"settings":{"A":1,"B":2}}}`)},
		"other.json":        {Data: []byte(`{"go":{"settings":{"B":2,"A":1},"command":"gopls","extensionToLanguage":{".go":"go"}}}`)},
	}
	inventory, err := claude.ReadLSP(agentSystem(source), "requested", "package")
	require.NoError(t, err)
	require.Len(t, inventory.Capabilities, 1)
	assert.Len(t, inventory.Capabilities[0].Provenance.Declarations, 5)
	assert.Equal(t, []string{".lsp.json", "other.json"}, inventory.Capabilities[0].Payload.Entrypoints)
}

func TestReadLSPConflictingDefinitionsBlock(t *testing.T) {
	t.Parallel()
	for _, replacement := range []string{
		`{"command":"other","extensionToLanguage":{".go":"go"}}`,
		`{"command":"gopls","extensionToLanguage":{".go":"other"}}`,
		`{"command":"gopls","extensionToLanguage":{".go":"go"},"transport":"stdio"}`,
	} {
		t.Run(replacement, func(t *testing.T) {
			t.Parallel()
			source := fstest.MapFS{
				claude.ManifestPath: {Data: []byte(`{"name":"tools","lspServers":{"go":` + replacement + `}}`)},
				".lsp.json":         {Data: []byte(`{"go":{"command":"gopls","extensionToLanguage":{".go":"go"}}}`)},
			}
			inventory, err := claude.ReadLSP(agentSystem(source), "requested", "package")
			require.ErrorIs(t, err, claude.ErrInvalidLSP)
			require.ErrorContains(t, err, "conflicting server")
			require.ErrorContains(t, err, ".lsp.json")
			require.ErrorContains(t, err, claude.ManifestPath)
			assert.Empty(t, inventory)
		})
	}
}

func TestReadLSPEmptyDeclarationsKeepDefaults(t *testing.T) {
	t.Parallel()
	for _, declaration := range []string{`[]`, `{}`} {
		t.Run(declaration, func(t *testing.T) {
			t.Parallel()
			source := fstest.MapFS{
				claude.ManifestPath: {Data: []byte(`{"name":"tools","lspServers":` + declaration + `}`)},
				".lsp.json":         {Data: []byte(`{"go":{"command":"gopls","extensionToLanguage":{".go":"go"}}}`)},
			}
			inventory, err := claude.ReadLSP(agentSystem(source), "requested", "package")
			require.NoError(t, err)
			assert.Len(t, inventory.Capabilities, 1)
		})
	}

	inventory, err := claude.ReadLSP(agentSystem(fstest.MapFS{}), "requested", "package")
	require.NoError(t, err)
	assert.Empty(t, inventory.Capabilities)
}

func TestReadLSPRequiredFieldsAndShapes(t *testing.T) {
	t.Parallel()
	for _, declaration := range []string{
		`null`, `true`, `3`, `[null]`, `[[]]`, `"missing.json"`, `"./../missing.json"`, `"./.git/servers.json"`, `"./missing.json"`, `"./file.txt"`, `"./directory.json"`, `"./wrapped.json"`, `"./malformed.json"`,
		`{"go":null}`, `{"go":[]}`, `{" ":{}}`, `{"go":{}}`,
		`{"go":{"command":null,"extensionToLanguage":{".go":"go"}}}`, `{"go":{"command":" ","extensionToLanguage":{".go":"go"}}}`,
		`{"go":{"command":"gopls"}}`, `{"go":{"command":"gopls","extensionToLanguage":null}}`, `{"go":{"command":"gopls","extensionToLanguage":[]}}`,
		`{"go":{"command":"gopls","extensionToLanguage":{}}}`, `{"go":{"command":"gopls","extensionToLanguage":{"go":"go"}}}`,
		`{"go":{"command":"gopls","extensionToLanguage":{".go":null}}}`, `{"go":{"command":"gopls","extensionToLanguage":{".go":" "}}}`,
		`{"go":{"command":"gopls","extensionToLanguage":{".go":"go"},"transport":null}}`, `{"go":{"command":"gopls","extensionToLanguage":{".go":"go"},"transport":true}}`,
		`{"go":{"command":"gopls","extensionToLanguage":{".go":"go",".go":"other"}}}`,
		`{"go":{"command":"gopls","command":"other","extensionToLanguage":{".go":"go"}}}`,
		`{"go":{"command":"gopls","extensionToLanguage":{".go":"go"},"settings":{"x":1,"x":2}}}`,
	} {
		t.Run(declaration, func(t *testing.T) {
			t.Parallel()
			source := fstest.MapFS{
				claude.ManifestPath: {Data: []byte(`{"name":"tools","lspServers":` + declaration + `}`)},
				"file.txt":          {Data: []byte(`{}`)}, "directory.json": {Mode: fs.ModeDir},
				"wrapped.json":   {Data: []byte(`{"lspServers":{"go":{"command":"gopls","extensionToLanguage":{".go":"go"}}}}`)},
				"malformed.json": {Data: []byte(`{} {}`)},
			}
			inventory, err := claude.ReadLSP(agentSystem(source), "requested", "package")
			require.ErrorIs(t, err, claude.ErrInvalidLSP)
			assert.Empty(t, inventory)
		})
	}
}

func TestReadLSPUnknownOptionsAndEscapedPointersPreserved(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{claude.ManifestPath: {Data: []byte(`{"name":"tools","lspServers":{"a~/b":{"command":"server","extensionToLanguage":{".a":"a"},"transport":"future","futureOption":{"value":1}}}}`)}}
	inventory, err := claude.ReadLSP(agentSystem(source), "requested", "package")
	require.NoError(t, err)
	require.Len(t, inventory.Capabilities, 1)
	definition, ok := inventory.Capabilities[0].Definition.(claudemodel.LSP)
	require.True(t, ok)
	assert.Equal(t, claudemodel.LSPTransport("future"), definition.Transport)
	assert.JSONEq(t, `{"value":1}`, string(definition.Fields["futureOption"]))
	assert.Equal(t, []model.Location{{Path: claude.ManifestPath, Pointer: "/lspServers/a~0~1b"}}, inventory.Capabilities[0].Provenance.Declarations)
}

func TestReadLSPFileDeclarationAndFailureBoundaries(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{
		claude.ManifestPath: {Data: []byte(`{"name":"tools","lspServers":"./custom.json"}`)},
		"custom.json":       {Data: []byte(`{"go":{"command":"gopls","extensionToLanguage":{".go":"go"}}}`)},
	}
	inventory, err := claude.ReadLSP(agentSystem(source), "requested", "package")
	require.NoError(t, err)
	require.Len(t, inventory.Capabilities, 1)
	assert.Equal(t, []model.Location{{Path: "custom.json", Pointer: "/go"}, {Path: claude.ManifestPath, Pointer: "/lspServers"}}, inventory.Capabilities[0].Provenance.Declarations)
	failure := errors.New("read failed")
	closeFailure := errors.New("close failed")
	closed := 0
	sys := &system.Fake{OpenRootHandler: func(string) (system.Root, error) {
		return system.FakeRoot{Path: "/source/tools", Source: lspReadFailureFS{MapFS: fstest.MapFS{".lsp.json": {Data: []byte(`{}`)}}, failure: failure}, CloseHandler: func() error { closed++; return closeFailure }}, nil
	}}
	inventory, err = claude.ReadLSP(sys, "requested", "package")
	require.ErrorIs(t, err, failure)
	require.ErrorIs(t, err, closeFailure)
	assert.Equal(t, 1, closed)
	assert.Empty(t, inventory)
	_, err = claude.ReadLSP(&system.Fake{}, "requested", " ")
	require.ErrorIs(t, err, claude.ErrInvalidLSP)
	_, err = claude.ReadLSP(agentSystem(fstest.MapFS{claude.ManifestPath: {Data: []byte(`{"name":"tools","lspServers":"./missing.json"}`)}}), "requested", "package")
	require.ErrorIs(t, err, fs.ErrNotExist)
	sys.OpenRootHandler = func(string) (system.Root, error) { return nil, failure }

	_, err = claude.ReadLSP(sys, "requested", "package")
	require.ErrorIs(t, err, failure)
	sys.OpenRootHandler = func(string) (system.Root, error) {
		return system.FakeRoot{Path: "/source/tools", Source: source, CloseHandler: func() error { return closeFailure }}, nil
	}

	inventory, err = claude.ReadLSP(sys, "requested", "package")
	require.ErrorIs(t, err, closeFailure)
	assert.Empty(t, inventory)
}

type lspReadFailureFS struct {
	fstest.MapFS
	failure error
}

func (f lspReadFailureFS) ReadFile(filename string) ([]byte, error) {
	if filename == ".lsp.json" {
		return nil, f.failure
	}

	return fs.ReadFile(f.MapFS, filename)
}
