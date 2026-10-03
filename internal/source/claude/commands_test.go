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

func TestReadCommandsDefaultInventory(t *testing.T) {
	t.Parallel()
	document := "---\nname: ignored-command-name\ndescription: Migrate database\nallowed-tools: Bash\nargument-hint: [env]\nmodel: sonnet\n---\n\nRun $ARGUMENTS using ${CLAUDE_PLUGIN_ROOT}.\n"
	source := fstest.MapFS{
		claude.ManifestPath:      {Data: []byte(`{"name":"tools"}`)},
		"commands/db/migrate.md": {Data: []byte(document)},
		"commands/about.md":      {Data: []byte("# About\n")},
		"commands/db/script.sh":  {Data: []byte("echo ignored")},
	}
	inventory, err := claude.ReadCommands(agentSystem(source), "requested", "package")
	require.NoError(t, err)
	require.Len(t, inventory.Capabilities, 2)
	assert.Equal(t, "about", inventory.Capabilities[0].ID.Name)
	command := inventory.Capabilities[1]
	assert.Equal(t, "claude:command:db:migrate", command.ID.String())
	assert.Equal(t, model.PackageID("package"), command.PackageID)
	assert.Equal(t, "Migrate database", command.Description)
	assert.Equal(t, []string{"commands/db/migrate.md"}, command.Payload.Entrypoints)
	assert.Empty(t, command.Payload.Assets)
	assert.Equal(t, []model.Location{{Path: "commands/db/migrate.md"}}, command.Provenance.Declarations)
	definition, ok := command.Definition.(claudemodel.Command)
	require.True(t, ok)
	assert.Equal(t, model.KindClaudeCommand, definition.Kind())
	assert.Equal(t, document, definition.Document)
	assert.Nil(t, definition.Declaration)
}

func TestReadCommandsPathsReplaceDefaults(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{
		claude.ManifestPath:    {Data: []byte(`{"name":"tools","commands":["./custom","./custom/db/migrate.md"]}`)},
		"commands/ignored.md":  {Data: []byte("ignored")},
		"custom/db/migrate.md": {Data: []byte("Migrate")},
	}
	inventory, err := claude.ReadCommands(agentSystem(source), "requested", "package")
	require.NoError(t, err)
	require.Len(t, inventory.Capabilities, 2)
	assert.Equal(t, "db:migrate", inventory.Capabilities[0].ID.Name)
	assert.Equal(t, "migrate", inventory.Capabilities[1].ID.Name)
	assert.Equal(t, []model.Location{{Path: "custom/db/migrate.md"}, {Path: claude.ManifestPath, Pointer: "/commands/0"}}, inventory.Capabilities[0].Provenance.Declarations)
}

func TestReadCommandsRepeatedFilesDeduplicate(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{
		claude.ManifestPath: {Data: []byte(`{"name":"tools","commands":["./custom/run.md","./custom/./run.md"]}`)},
		"custom/run.md":     {Data: []byte("Run")},
	}
	inventory, err := claude.ReadCommands(agentSystem(source), "requested", "package")
	require.NoError(t, err)
	require.Len(t, inventory.Capabilities, 1)
	assert.Len(t, inventory.Capabilities[0].Provenance.Declarations, 3)
}

func TestReadCommandsNamedFileAndInline(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{
		claude.ManifestPath:   {Data: []byte(`{"name":"tools","commands":{"status":{"source":"./custom/actual.md","description":"Override","argumentHint":"[env]","model":"sonnet","allowedTools":["Read"],"futureOption":true},"a~/b":{"content":"---\ndescription: Inline description\n---\nBody $ARGUMENTS","allowedTools":[]}}}`)},
		"custom/actual.md":    {Data: []byte("---\ndescription: Authored\n---\nBody")},
		"commands/ignored.md": {Data: []byte("ignored")},
	}
	inventory, err := claude.ReadCommands(agentSystem(source), "requested", "package")
	require.NoError(t, err)
	require.Len(t, inventory.Capabilities, 2)
	inline := inventory.Capabilities[0]
	assert.Equal(t, "a~/b", inline.ID.Name)
	assert.Equal(t, "Inline description", inline.Description)
	assert.Empty(t, inline.Payload.Entrypoints)
	assert.Equal(t, []model.Location{{Path: claude.ManifestPath, Pointer: "/commands/a~0~1b"}}, inline.Provenance.Declarations)
	file := inventory.Capabilities[1]
	assert.Equal(t, "status", file.ID.Name)
	assert.Equal(t, "Override", file.Description)
	assert.Equal(t, []string{"custom/actual.md"}, file.Payload.Entrypoints)
	definition, ok := file.Definition.(claudemodel.Command)
	require.True(t, ok)
	assert.Equal(t, "Authored", *definition.Description)
	assert.JSONEq(t, `true`, string(definition.Declaration["futureOption"]))
	assert.JSONEq(t, `["Read"]`, string(definition.Declaration["allowedTools"]))
}

func TestReadCommandsEmptyAndMissing(t *testing.T) {
	t.Parallel()
	for _, manifest := range []string{`{"name":"tools","commands":[]}`, `{"name":"tools","commands":{}}`, `{"name":"tools"}`} {
		t.Run(manifest, func(t *testing.T) {
			t.Parallel()
			inventory, err := claude.ReadCommands(agentSystem(fstest.MapFS{claude.ManifestPath: {Data: []byte(manifest)}}), "requested", "package")
			require.NoError(t, err)
			assert.Empty(t, inventory.Capabilities)
		})
	}
}

func TestReadCommandsInvalidDeclarations(t *testing.T) {
	t.Parallel()
	declarations := []string{
		`null`, `true`, `3`, `[null]`, `"commands/run.md"`, `"./../run.md"`, `"./.git/run.md"`, `"./missing.md"`, `"./run.txt"`,
		`{"run":{}}`, `{"run":{"source":"./run.md","content":"inline"}}`, `{"run":{"content":null}}`, `{"run":{"content":true}}`,
		`{"run":{"source":3}}`, `{"run":{"source":"../run.md"}}`, `{"run":{"content":"x","description":null}}`,
		`{"run":{"content":"x","argumentHint":[]}}`, `{"run":{"content":"x","model":false}}`,
		`{"run":{"content":"x","allowedTools":null}}`, `{"run":{"content":"x","allowedTools":[null]}}`,
		`{"run":{"content":"x","allowedTools":[4]}}`, `{"run":"./run.md"}`, `{" ":{"content":"x"}}`,
		`{"run":{"content":"x","content":"y"}}`, `{"run":{"content":"x"},"run":{"content":"y"}}`,
	}
	for _, declaration := range declarations {
		t.Run(declaration, func(t *testing.T) {
			t.Parallel()
			source := fstest.MapFS{claude.ManifestPath: {Data: []byte(`{"name":"tools","commands":` + declaration + `}`)}, "run.md": {Data: []byte("Run")}, "run.txt": {Data: []byte("Text")}}
			inventory, err := claude.ReadCommands(agentSystem(source), "requested", "package")
			require.ErrorIs(t, err, claude.ErrInvalidCommands)
			assert.Empty(t, inventory)
		})
	}
}

func TestReadCommandsNameCollision(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{
		claude.ManifestPath: {Data: []byte(`{"name":"tools","commands":["./first/run.md","./second/run.md"]}`)},
		"first/run.md":      {Data: []byte("First")}, "second/run.md": {Data: []byte("Second")},
	}
	inventory, err := claude.ReadCommands(agentSystem(source), "requested", "package")
	require.ErrorIs(t, err, claude.ErrInvalidCommands)
	require.ErrorContains(t, err, "first/run.md")
	require.ErrorContains(t, err, "second/run.md")
	assert.Empty(t, inventory)
}

func TestReadCommandsMalformedMetadataPreserved(t *testing.T) {
	t.Parallel()
	document := "---\ndescription: [invalid\n---\nBody\n"
	inventory, err := claude.ReadCommands(agentSystem(fstest.MapFS{"commands/run.md": {Data: []byte(document)}}), "requested", "package")
	require.NoError(t, err)
	require.Len(t, inventory.Capabilities, 1)
	definition, ok := inventory.Capabilities[0].Definition.(claudemodel.Command)
	require.True(t, ok)
	assert.Nil(t, definition.Description)
	assert.Equal(t, document, definition.Document)
}

func TestReadCommandsFailureBoundaries(t *testing.T) {
	t.Parallel()
	readErr := errors.New("read failed")
	closeErr := errors.New("close failed")
	source := commandReadFailureFS{MapFS: fstest.MapFS{"commands/run.md": {Data: []byte("Run")}}, failure: readErr}
	closed := 0
	sys := &system.Fake{OpenRootHandler: func(string) (system.Root, error) {
		return system.FakeRoot{Path: "/source/tools", Source: source, CloseHandler: func() error { closed++; return closeErr }}, nil
	}}
	inventory, err := claude.ReadCommands(sys, "requested", "package")
	require.ErrorIs(t, err, readErr)
	require.ErrorIs(t, err, closeErr)
	assert.Equal(t, 1, closed)
	assert.Empty(t, inventory)
	_, err = claude.ReadCommands(&system.Fake{}, "requested", " ")
	require.ErrorIs(t, err, claude.ErrInvalidCommands)
	_, err = claude.ReadCommands(agentSystem(fstest.MapFS{claude.ManifestPath: {Data: []byte(`{"name":"tools","commands":"./missing.md"}`)}}), "requested", "package")
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestReadCommandsRootDirectoryIgnoresGitMetadata(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{
		claude.ManifestPath: {Data: []byte(`{"name":"tools","commands":"./"}`)},
		"run.md":            {Data: []byte("Run")},
		".git/ignored.md":   {Data: []byte("Ignored")},
	}
	inventory, err := claude.ReadCommands(agentSystem(source), "requested", "package")
	require.NoError(t, err)
	require.Len(t, inventory.Capabilities, 1)
	assert.Equal(t, "run", inventory.Capabilities[0].ID.Name)
}

func TestReadCommandsInvalidDefaultAndBlankFilename(t *testing.T) {
	t.Parallel()
	for _, source := range []fstest.MapFS{
		{"commands": {Data: []byte("not a directory")}},
		{"commands/nested/.md": {Data: []byte("Blank filename")}},
	} {
		t.Run("invalid payload", func(t *testing.T) {
			t.Parallel()
			inventory, err := claude.ReadCommands(agentSystem(source), "requested", "package")
			require.ErrorIs(t, err, claude.ErrInvalidCommands)
			assert.Empty(t, inventory)
		})
	}
}

func TestReadCommandsAcquisitionAndSuccessfulCloseFailure(t *testing.T) {
	t.Parallel()
	failure := errors.New("injected failure")
	sys := &system.Fake{OpenRootHandler: func(string) (system.Root, error) { return nil, failure }}
	inventory, err := claude.ReadCommands(sys, "requested", "package")
	require.ErrorIs(t, err, failure)
	assert.Empty(t, inventory)
	closed := 0
	sys.OpenRootHandler = func(string) (system.Root, error) {
		return system.FakeRoot{Path: "/source/tools", Source: fstest.MapFS{"commands/run.md": {Data: []byte("Run")}}, CloseHandler: func() error { closed++; return failure }}, nil
	}

	inventory, err = claude.ReadCommands(sys, "requested", "package")
	require.ErrorIs(t, err, failure)
	assert.Empty(t, inventory)
	assert.Equal(t, 1, closed)
}

type commandReadFailureFS struct {
	fstest.MapFS
	failure error
}

func (f commandReadFailureFS) ReadFile(filename string) ([]byte, error) {
	if filename == "commands/run.md" {
		return nil, f.failure
	}

	return fs.ReadFile(f.MapFS, filename)
}
