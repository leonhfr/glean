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

func TestReadHooksCombinesSourcesInOrder(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{
		claude.ManifestPath: {Data: []byte(`{"name":"tools","hooks":["./custom/hooks.json",{"PostToolUse":[{"matcher":"Write|Edit","hooks":[{"type":"http","url":"https://example.test","headers":{"Authorization":"Bearer $TOKEN"},"allowedEnvVars":["TOKEN"]}]}]}]}`)},
		"hooks/hooks.json": {
			Data: []byte(
				`{"description":"Native hook file","hooks":{"PostToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"${CLAUDE_PLUGIN_ROOT}/scripts/check.sh","async":true,"timeout":42},{"type":"prompt","prompt":"Check $ARGUMENTS","model":"sonnet"}]}],"Stop":[{"hooks":[{"type":"agent","prompt":"Review"}]}]}}`,
			),
		},
		"custom/hooks.json": {Data: []byte(`{"hooks":{"PostToolUse":[{"hooks":[{"type":"mcp_tool","server":"plugin:tools:db","tool":"check","input":{"file":"${tool_input.file_path}"}}]}]}}`)},
		"scripts/check.sh":  {Data: []byte("exit 99")},
	}
	inventory, err := claude.ReadHooks(agentSystem(source), "requested", "package")
	require.NoError(t, err)
	assert.Equal(t, model.PackageID("package"), inventory.Package.ID)
	assert.Equal(t, "tools", inventory.Package.Name)
	require.Len(t, inventory.Capabilities, 2)
	capability := inventory.Capabilities[0]
	assert.Equal(t, "claude:hook:PostToolUse", capability.ID.String())
	assert.Equal(t, inventory.Package.ID, capability.PackageID)
	assert.Equal(t, []string{"hooks/hooks.json", "custom/hooks.json"}, capability.Payload.Entrypoints)
	assert.Empty(t, capability.Payload.Assets)
	definition, ok := capability.Definition.(claudemodel.Hook)
	require.True(t, ok)
	assert.Equal(t, model.KindClaudeHook, definition.Kind())
	require.Len(t, definition.Groups, 3)
	require.Len(t, definition.Groups[0].Handlers, 2)
	assert.Equal(t, claudemodel.HookCommand, definition.Groups[0].Handlers[0].Type)
	assert.Equal(t, claudemodel.HookPrompt, definition.Groups[0].Handlers[1].Type)
	assert.Equal(t, claudemodel.HookMCPTool, definition.Groups[1].Handlers[0].Type)
	assert.Equal(t, claudemodel.HookHTTP, definition.Groups[2].Handlers[0].Type)
	assert.JSONEq(t, `"${CLAUDE_PLUGIN_ROOT}/scripts/check.sh"`, string(definition.Groups[0].Handlers[0].Fields["command"]))
	assert.JSONEq(t, `{"Authorization":"Bearer $TOKEN"}`, string(definition.Groups[2].Handlers[0].Fields["headers"]))
	assert.Equal(t, []model.Location{{Path: "custom/hooks.json", Pointer: "/hooks/PostToolUse/0"}, {Path: claude.ManifestPath, Pointer: "/hooks/0"}}, definition.Groups[1].Declarations)
	assert.Equal(t, []model.Location{{Path: claude.ManifestPath, Pointer: "/hooks/1/PostToolUse/0"}}, definition.Groups[2].Declarations)
	assert.Equal(t, "/source/review-tools", capability.Provenance.Root)
	stop, ok := inventory.Capabilities[1].Definition.(claudemodel.Hook)
	require.True(t, ok)
	assert.Equal(t, claudemodel.HookAgent, stop.Groups[0].Handlers[0].Type)
}

func TestReadHooksRepeatedFilesDeduplicateGroups(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{
		claude.ManifestPath: {Data: []byte(`{"name":"tools","hooks":["./hooks/hooks.json","./hooks/./hooks.json"]}`)},
		"hooks/hooks.json":  {Data: []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo first"}]},{"hooks":[{"type":"command","command":"echo first"}]}]}}`)},
	}
	inventory, err := claude.ReadHooks(agentSystem(source), "requested", "package")
	require.NoError(t, err)
	require.Len(t, inventory.Capabilities, 1)
	definition, ok := inventory.Capabilities[0].Definition.(claudemodel.Hook)
	require.True(t, ok)
	require.Len(t, definition.Groups, 2)
	assert.Len(t, definition.Groups[0].Declarations, 3)
	assert.Len(t, definition.Groups[1].Declarations, 3)
	assert.Len(t, inventory.Capabilities[0].Provenance.Declarations, 4)
	assert.Equal(t, []string{"hooks/hooks.json"}, inventory.Capabilities[0].Payload.Entrypoints)
}

func TestReadHooksInlineAndUnknownFormsRemainObservable(t *testing.T) {
	t.Parallel()
	group := `{ "matcher": "*", "futureGroupOption": true, "hooks": [{"type":"future_handler","futureField":{"value":1}}] }`
	source := fstest.MapFS{claude.ManifestPath: {Data: []byte(`{"name":"tools","hooks":{"Future~/Event":[` + group + `]}}`)}}
	inventory, err := claude.ReadHooks(agentSystem(source), "requested", "package")
	require.NoError(t, err)
	require.Len(t, inventory.Capabilities, 1)
	capability := inventory.Capabilities[0]
	assert.Equal(t, "Future~/Event", capability.ID.Name)
	assert.Empty(t, capability.Payload.Entrypoints)
	assert.Equal(t, []model.Location{{Path: claude.ManifestPath, Pointer: "/hooks/Future~0~1Event/0"}}, capability.Provenance.Declarations)
	definition, ok := capability.Definition.(claudemodel.Hook)
	require.True(t, ok)
	assert.Equal(t, group, string(definition.Groups[0].Document))
	assert.Equal(t, claudemodel.HookHandlerType("future_handler"), definition.Groups[0].Handlers[0].Type)
	assert.JSONEq(t, `{"value":1}`, string(definition.Groups[0].Handlers[0].Fields["futureField"]))
}

func TestReadHooksEmptyDeclarationsKeepDefaults(t *testing.T) {
	t.Parallel()
	for _, declaration := range []string{`[]`, `{}`, `{"Stop":[]}`} {
		t.Run(declaration, func(t *testing.T) {
			t.Parallel()
			source := fstest.MapFS{
				claude.ManifestPath: {Data: []byte(`{"name":"tools","hooks":` + declaration + `}`)},
				"hooks/hooks.json":  {Data: []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo default"}]}]}}`)},
			}
			inventory, err := claude.ReadHooks(agentSystem(source), "requested", "package")
			require.NoError(t, err)
			assert.Len(t, inventory.Capabilities, 1)
		})
	}
}

func TestReadHooksNoHooks(t *testing.T) {
	t.Parallel()
	for _, source := range []fstest.MapFS{
		{},
		{"hooks/hooks.json": {Data: []byte(`{"hooks":{}}`)}},
		{claude.ManifestPath: {Data: []byte(`{"name":"tools","hooks":[]}`)}},
	} {
		t.Run("empty", func(t *testing.T) {
			t.Parallel()
			inventory, err := claude.ReadHooks(agentSystem(source), "requested", "package")
			require.NoError(t, err)
			assert.Empty(t, inventory.Capabilities)
		})
	}
}

func TestReadHooksRejectsMalformedDeclarations(t *testing.T) {
	t.Parallel()
	declarations := []string{
		`null`, `true`, `4`, `[null]`, `[[]]`, `"hooks/hooks.json"`, `"./../hooks.json"`, `"./.git/hooks.json"`, `"./missing.json"`, `"./file.txt"`, `"./directory.json"`,
		`{"Stop":null}`, `{"Stop":{}}`, `{"Stop":[null]}`, `{"Stop":[{}]}`, `{"Stop":[{"hooks":null}]}`,
		`{"Stop":[{"matcher":false,"hooks":[]}]}`, `{"Stop":[{"hooks":[null]}]}`, `{"Stop":[{"hooks":[{}]}]}`,
		`{"Stop":[{"hooks":[{"type":null}]}]}`, `{"Stop":[{"hooks":[{"type":" "}]}]}`,
		`{"Stop":[{"hooks":[{"type":"command","type":"prompt"}]}]}`, `{"Stop":[],"Stop":[]}`,
		`{"Stop":[{"hooks":[],"hooks":[]}]}`, `{" ":[]}`, `"./unwrapped.json"`, `"./bad.json"`,
	}
	for _, declaration := range declarations {
		t.Run(declaration, func(t *testing.T) {
			t.Parallel()
			source := fstest.MapFS{
				claude.ManifestPath: {Data: []byte(`{"name":"tools","hooks":` + declaration + `}`)},
				"file.txt":          {Data: []byte(`{"hooks":{}}`)},
				"directory.json":    {Mode: fs.ModeDir},
				"unwrapped.json":    {Data: []byte(`{"Stop":[]}`)},
				"bad.json":          {Data: []byte(`{"hooks":{}} {}`)},
			}
			inventory, err := claude.ReadHooks(agentSystem(source), "requested", "package")
			require.ErrorIs(t, err, claude.ErrInvalidHooks)
			assert.Empty(t, inventory)
		})
	}
}

func TestReadHooksFileDeclarationsAndDuplicateWrapperKeys(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{
		claude.ManifestPath: {Data: []byte(`{"name":"tools","hooks":"./custom.json"}`)},
		"custom.json":       {Data: []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo stop"}]}]}}`)},
	}
	inventory, err := claude.ReadHooks(agentSystem(source), "requested", "package")
	require.NoError(t, err)
	require.Len(t, inventory.Capabilities, 1)
	assert.Equal(t, []model.Location{{Path: "custom.json", Pointer: "/hooks/Stop/0"}, {Path: claude.ManifestPath, Pointer: "/hooks"}}, inventory.Capabilities[0].Provenance.Declarations)
	source["custom.json"].Data = []byte(`{"hooks":{},"hooks":{}}`)
	_, err = claude.ReadHooks(agentSystem(source), "requested", "package")
	require.ErrorIs(t, err, claude.ErrInvalidHooks)
}

func TestReadHooksFailureBoundaries(t *testing.T) {
	t.Parallel()
	readErr := errors.New("read failed")
	closeErr := errors.New("close failed")
	closed := 0
	source := hookReadFailureFS{MapFS: fstest.MapFS{"hooks/hooks.json": {Data: []byte(`{"hooks":{}}`)}}, failure: readErr}
	sys := &system.Fake{OpenRootHandler: func(string) (system.Root, error) {
		return system.FakeRoot{Path: "/source/tools", Source: source, CloseHandler: func() error { closed++; return closeErr }}, nil
	}}
	inventory, err := claude.ReadHooks(sys, "requested", "package")
	require.ErrorIs(t, err, readErr)
	require.ErrorIs(t, err, closeErr)
	assert.Equal(t, 1, closed)
	assert.Empty(t, inventory)
	_, err = claude.ReadHooks(&system.Fake{}, "requested", " ")
	require.ErrorIs(t, err, claude.ErrInvalidHooks)
	_, err = claude.ReadHooks(agentSystem(fstest.MapFS{claude.ManifestPath: {Data: []byte(`{"name":"tools","hooks":"./missing.json"}`)}}), "requested", "package")
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestReadHooksAcquisitionAndCloseFailures(t *testing.T) {
	t.Parallel()
	failure := errors.New("injected failure")
	sys := &system.Fake{OpenRootHandler: func(string) (system.Root, error) { return nil, failure }}
	inventory, err := claude.ReadHooks(sys, "requested", "package")
	require.ErrorIs(t, err, failure)
	assert.Empty(t, inventory)
	sys.OpenRootHandler = func(string) (system.Root, error) {
		return system.FakeRoot{Path: "/source/tools", Source: fstest.MapFS{claude.ManifestPath: {Data: []byte(`{"name":"tools","hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo stop"}]}]}}`)}}, CloseHandler: func() error { return failure }}, nil
	}

	inventory, err = claude.ReadHooks(sys, "requested", "package")
	require.ErrorIs(t, err, failure)
	assert.Empty(t, inventory)
}

type hookReadFailureFS struct {
	fstest.MapFS
	failure error
}

func (f hookReadFailureFS) ReadFile(filename string) ([]byte, error) {
	if filename == "hooks/hooks.json" {
		return nil, f.failure
	}

	return fs.ReadFile(f.MapFS, filename)
}
