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

func TestReadAgentsRecursiveNamesAndPayloadBoundary(t *testing.T) {
	t.Parallel()
	document := "---\nname: audit\ndescription: Security review\ntools: Read, Grep\nmodel: sonnet\nhooks:\n  PreToolUse: []\n---\n\n# Review\n\nSee [checklist](checklist.txt).\n"
	source := fstest.MapFS{
		claude.ManifestPath:           {Data: []byte(`{"name":"review-tools"}`)},
		"agents/review/security.md":   {Data: []byte(document)},
		"agents/review/checklist.txt": {Data: []byte("supporting asset")},
		"agents/general.md":           {Data: []byte("# General\n")},
		"agents/docs/readme.md":       {Data: []byte("# Documentation\n")},
	}
	inventory, err := claude.ReadAgents(agentSystem(source), "requested", "resolved-package")
	require.NoError(t, err)
	assert.Equal(t, model.PackageID("resolved-package"), inventory.Package.ID)
	assert.Equal(t, "review-tools", inventory.Package.Name)
	assert.Equal(t, "/source/review-tools", inventory.Package.Root)
	require.Len(t, inventory.Capabilities, 3)
	assert.Equal(t, "docs:readme", inventory.Capabilities[0].ID.Name)
	assert.Equal(t, "general", inventory.Capabilities[1].ID.Name)
	assert.Equal(t, "review:audit", inventory.Capabilities[2].ID.Name)
	agent := inventory.Capabilities[2]
	assert.Equal(t, "claude:agent:review:audit", agent.ID.String())
	assert.Equal(t, inventory.Package.ID, agent.PackageID)
	assert.Equal(t, "Security review", agent.Description)
	assert.Equal(t, []string{"agents/review/security.md"}, agent.Payload.Entrypoints)
	assert.Empty(t, agent.Payload.Assets)
	assert.Equal(t, []model.Location{{Path: "agents/review/security.md"}}, agent.Provenance.Declarations)
	definition, ok := agent.Definition.(claudemodel.Agent)
	require.True(t, ok)
	assert.Equal(t, model.KindClaudeAgent, definition.Kind())
	assert.Equal(t, document, definition.Document)
	require.NotNil(t, definition.Name)
	assert.Equal(t, "audit", *definition.Name)
	assert.Equal(t, document, string(source["agents/review/security.md"].Data))
}

func TestReadAgentsExplicitFilesReplaceDefaultsAndDeduplicate(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{
		claude.ManifestPath:         {Data: []byte(`{"name":"review-tools","agents":["./custom/review/security.md","./custom/./review/security.md"]}`)},
		"agents/default.md":         {Data: []byte("# Default\n")},
		"custom/review/security.md": {Data: []byte("---\nname: audit\n---\n\n# Review\n")},
	}
	inventory, err := claude.ReadAgents(agentSystem(source), "requested", "package")
	require.NoError(t, err)
	require.Len(t, inventory.Capabilities, 1)
	agent := inventory.Capabilities[0]
	assert.Equal(t, "audit", agent.ID.Name)
	assert.Equal(t, []model.Location{
		{Path: "custom/review/security.md"},
		{Path: claude.ManifestPath, Pointer: "/agents/0"},
		{Path: claude.ManifestPath, Pointer: "/agents/1"},
	}, agent.Provenance.Declarations)
}

func TestReadAgentsSingleExplicitFileUsesBasename(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{
		claude.ManifestPath:         {Data: []byte(`{"name":"review-tools","agents":"./custom/nested/reviewer.md"}`)},
		"custom/nested/reviewer.md": {Data: []byte("# Review\n")},
	}
	inventory, err := claude.ReadAgents(agentSystem(source), "requested", "package")
	require.NoError(t, err)
	require.Len(t, inventory.Capabilities, 1)
	assert.Equal(t, "reviewer", inventory.Capabilities[0].ID.Name)
	assert.Contains(t, inventory.Capabilities[0].Provenance.Declarations, model.Location{
		Path: claude.ManifestPath, Pointer: "/agents",
	})
}

func TestReadAgentsExplicitEmptyListSuppressesDefaults(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{
		claude.ManifestPath:  {Data: []byte(`{"name":"review-tools","agents":[]}`)},
		"agents/reviewer.md": {Data: []byte("# Review\n")},
	}
	inventory, err := claude.ReadAgents(agentSystem(source), "requested", "package")
	require.NoError(t, err)
	assert.Empty(t, inventory.Capabilities)
}

func TestReadAgentsMetadataFallbacksPreserveDocument(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, document, invocation, description string }{
		{"plain", "# Review\n", "reviewer", "Agent from review-tools plugin"},
		{"malformed YAML", "---\nname: [broken\n---\n\n# Review\n", "reviewer", "Agent from review-tools plugin"},
		{"not first line", "# Review\n---\nname: audit\n---\n", "reviewer", "Agent from review-tools plugin"},
		{"empty description", "---\ndescription: ''\n---\n\n# Review\n", "reviewer", ""},
		{"BOM and CRLF", "\ufeff---\r\nname: audit\r\ndescription: Review\r\n---\r\n# Review\r\n", "audit", "Review"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			source := fstest.MapFS{
				claude.ManifestPath:  {Data: []byte(`{"name":"review-tools"}`)},
				"agents/reviewer.md": {Data: []byte(tc.document)},
			}
			inventory, err := claude.ReadAgents(agentSystem(source), "requested", "package")
			require.NoError(t, err)
			require.Len(t, inventory.Capabilities, 1)
			agent := inventory.Capabilities[0]
			assert.Equal(t, tc.invocation, agent.ID.Name)
			assert.Equal(t, tc.description, agent.Description)
			definition, ok := agent.Definition.(claudemodel.Agent)
			require.True(t, ok)
			assert.Equal(t, tc.document, definition.Document)
		})
	}
}

func TestReadAgentsRejectsInvalidDeclarations(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		`null`, `{}`, `true`, `42`, `[null]`, `[42]`, `""`, `"."`, `"agents/reviewer.md"`,
		`"/outside.md"`, `"./../outside.md"`, `"./a/../reviewer.md"`,
		`"./agents\\reviewer.md"`, `"./agents\u0000.md"`, `"./.git/agent.md"`,
		`"./agents"`, `"./folder.md"`, `"./asset.txt"`, `"./missing.md"`,
	} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			source := fstest.MapFS{
				claude.ManifestPath:  {Data: []byte(`{"name":"review-tools","agents":` + raw + `}`)},
				"agents/reviewer.md": {Data: []byte("# Review\n")},
				"folder.md":          {Mode: fs.ModeDir},
				"asset.txt":          {Data: []byte("asset")},
			}
			_, err := claude.ReadAgents(agentSystem(source), "requested", "package")
			require.ErrorIs(t, err, claude.ErrInvalidAgents)
		})
	}
}

func TestReadAgentsRejectsAmbiguousNativeNames(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{
		claude.ManifestPath: {Data: []byte(`{"name":"review-tools"}`)},
		"agents/first.md":   {Data: []byte("---\nname: reviewer\n---\n# First\n")},
		"agents/second.md":  {Data: []byte("---\nname: reviewer\n---\n# Second\n")},
	}
	_, err := claude.ReadAgents(agentSystem(source), "requested", "package")
	require.ErrorIs(t, err, claude.ErrInvalidAgents)
	require.ErrorContains(t, err, "agents/first.md")
	require.ErrorContains(t, err, "agents/second.md")
}

func TestReadAgentsAbsentDefaultDirectoryAndManifest(t *testing.T) {
	t.Parallel()
	inventory, err := claude.ReadAgents(agentSystem(fstest.MapFS{}), "requested", "package")
	require.NoError(t, err)
	assert.Empty(t, inventory.Capabilities)
	assert.Equal(t, "review-tools", inventory.Package.Name)
	_, err = claude.ReadAgents(&system.Fake{}, "requested", "")
	require.ErrorIs(t, err, claude.ErrInvalidAgents)
}

func TestReadAgentsPreservesReadAndCloseFailures(t *testing.T) {
	t.Parallel()
	readErr := errors.New("scripted read failure")
	closeErr := errors.New("scripted close failure")
	source := agentReadFailureFS{
		FS:      fstest.MapFS{"agents/reviewer.md": {Data: []byte("# Review\n")}},
		failure: readErr,
	}
	closed := 0
	sys := &system.Fake{OpenRootHandler: func(directory string) (system.Root, error) {
		assert.Equal(t, "requested", directory)
		return system.FakeRoot{
			Path: "/source/review-tools", Source: source,
			CloseHandler: func() error { closed++; return closeErr },
		}, nil
	}}
	inventory, err := claude.ReadAgents(sys, "requested", "package")
	require.ErrorIs(t, err, readErr)
	require.ErrorIs(t, err, closeErr)
	assert.Zero(t, inventory)
	assert.Equal(t, 1, closed)
}

func TestReadAgentsPreservesAcquisitionFailure(t *testing.T) {
	t.Parallel()
	openErr := errors.New("scripted acquisition failure")
	sys := &system.Fake{OpenRootHandler: func(string) (system.Root, error) { return nil, openErr }}
	_, err := claude.ReadAgents(sys, "requested", "package")
	require.ErrorIs(t, err, openErr)
}

func TestReadAgentsMissingDeclaredFilePreservesCause(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{
		claude.ManifestPath: {Data: []byte(`{"name":"review-tools","agents":"./missing.md"}`)},
	}
	_, err := claude.ReadAgents(agentSystem(source), "requested", "package")
	require.ErrorIs(t, err, claude.ErrInvalidAgents)
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestReadAgentsCloseFailureDiscardsSuccessfulInventory(t *testing.T) {
	t.Parallel()
	closeErr := errors.New("scripted close failure")
	closed := 0
	sys := &system.Fake{OpenRootHandler: func(string) (system.Root, error) {
		return system.FakeRoot{
			Path:         "/source/review-tools",
			Source:       fstest.MapFS{"agents/reviewer.md": {Data: []byte("# Review\n")}},
			CloseHandler: func() error { closed++; return closeErr },
		}, nil
	}}
	inventory, err := claude.ReadAgents(sys, "requested", "package")
	require.ErrorIs(t, err, closeErr)
	assert.Zero(t, inventory)
	assert.Equal(t, 1, closed)
}

func TestReadAgentsRejectsInvalidDefaultDirectoryAndNames(t *testing.T) {
	t.Parallel()
	for name, source := range map[string]fstest.MapFS{
		"file in place of directory": {"agents": {Data: []byte("not a directory")}},
		"empty filename":             {"agents/.md": {Data: []byte("# Agent\n")}},
		"blank metadata name":        {"agents/reviewer.md": {Data: []byte("---\nname: ''\n---\n# Agent\n")}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := claude.ReadAgents(agentSystem(source), "requested", "package")
			require.ErrorIs(t, err, claude.ErrInvalidAgents)
		})
	}
}

type agentReadFailureFS struct {
	fs.FS
	failure error
}

func (f agentReadFailureFS) ReadFile(name string) ([]byte, error) {
	if name == "agents/reviewer.md" {
		return nil, f.failure
	}

	return fs.ReadFile(f.FS, name)
}

func agentSystem(source fs.FS) *system.Fake {
	return &system.Fake{OpenRootHandler: func(string) (system.Root, error) {
		return system.FakeRoot{Path: "/source/review-tools", Source: source}, nil
	}}
}
