package claude_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/leonhfr/glean/internal/model"
	claudemodel "github.com/leonhfr/glean/internal/model/claude"
	"github.com/leonhfr/glean/internal/source/claude"
)

func TestReadSkillsDefaultAndDeclaredDirectories(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeManifest(t, root, `{"name":"review-tools","skills":["./extras/","./extras/review","./skills/"]}`)
	document := "---\nname: review-tools:fancy\ndescription: Careful review\nhooks:\n  PreToolUse:\n    - hooks:\n        - type: command\n          command: ./scripts/check.sh\n---\n\n# Review\n\n!`printf never-executed`\n"
	writeSkillFile(t, root, "skills/review/SKILL.md", document)
	writeSkillFile(t, root, "skills/review/scripts/check.sh", "#!/bin/sh\nexit 0\n")
	writeSkillFile(t, root, "skills/review/references/checklist.txt", "checklist\n")
	writeSkillFile(t, root, "extras/review/SKILL.md", "---\nname: extra\n---\n\n# Extra review\n")
	writeSkillFile(t, root, "skills/empty/asset.txt", "not a skill")

	inventory, err := claude.ReadSkills(root, "resolved-package")
	require.NoError(t, err)
	assert.Equal(t, model.PackageID("resolved-package"), inventory.Package.ID)
	assert.Equal(t, "review-tools", inventory.Package.Name)
	require.Len(t, inventory.Capabilities, 2)
	extra, review := inventory.Capabilities[0], inventory.Capabilities[1]
	assert.Equal(t, "skill:extra", extra.ID.String())
	assert.Equal(t, "skill:fancy", review.ID.String())
	assert.Equal(t, inventory.Package.ID, review.PackageID)
	assert.Equal(t, inventory.Package.Root, review.Provenance.Root)
	assert.Equal(t, "Careful review", review.Description)
	assert.Equal(t, []string{"skills/review/SKILL.md"}, review.Payload.Entrypoints)
	assert.Equal(t, []string{"skills/review"}, review.Payload.Assets)
	assert.Contains(t, review.Provenance.Declarations, model.Location{Path: claude.ManifestPath, Pointer: "/skills/2"})
	assert.Contains(t, extra.Provenance.Declarations, model.Location{Path: claude.ManifestPath, Pointer: "/skills/0"})
	assert.Contains(t, extra.Provenance.Declarations, model.Location{Path: claude.ManifestPath, Pointer: "/skills/1"})
	definition, ok := review.Definition.(claudemodel.Skill)
	require.True(t, ok)
	assert.Equal(t, model.KindSkill, definition.Kind())
	assert.Equal(t, document, definition.Document)
	require.NotNil(t, definition.Name)
	assert.Equal(t, "review-tools:fancy", *definition.Name)
	after, err := os.ReadFile(filepath.Join(root, "skills/review/SKILL.md"))
	require.NoError(t, err)
	assert.Equal(t, document, string(after))
}

func TestReadSkillsRootFallbackAndExplicitRoot(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		declaration string
		defaultDir  bool
		count       int
	}{
		{"implicit root", "", false, 1},
		{"default suppresses root", "", true, 0},
		{"explicit empty suppresses root", `,"skills":[]`, false, 0},
		{"explicit root", `,"skills":"."`, true, 1},
		{"explicit slash root", `,"skills":"./"`, false, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := filepath.Join(t.TempDir(), "payload-folder")
			writeManifest(t, root, `{"name":"review-tools"`+tc.declaration+`}`)
			writeSkillFile(t, root, "SKILL.md", "# Root skill\n")
			if tc.defaultDir {
				require.NoError(t, os.Mkdir(filepath.Join(root, "skills"), 0o700))
			}

			inventory, err := claude.ReadSkills(root, "package")
			require.NoError(t, err)
			require.Len(t, inventory.Capabilities, tc.count)
			if tc.count != 0 {
				assert.Equal(t, "payload-folder", inventory.Capabilities[0].ID.Name)
				assert.Equal(t, []string{"."}, inventory.Capabilities[0].Payload.Assets)
			}
		})
	}
}

func TestReadSkillsRejectsDistinctInvocationCollision(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeManifest(t, root, `{"name":"review-tools","skills":"./extras/"}`)
	writeSkillFile(t, root, "skills/review/SKILL.md", "# Default review\n")
	writeSkillFile(t, root, "extras/review/SKILL.md", "# Custom review\n")
	_, err := claude.ReadSkills(root, "package")
	require.ErrorIs(t, err, claude.ErrInvalidSkills)
	require.ErrorContains(t, err, "skills/review/SKILL.md")
	require.ErrorContains(t, err, "extras/review/SKILL.md")
}

func TestReadSkillsRejectsInvalidDeclarations(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{
		`null`, `{}`, `true`, `42`, `["./skills/",null]`, `["./skills/",42]`,
		`"skills"`, `""`, `"/tmp/skills"`, `"./../outside"`, `"./a/../skills"`,
		`"./skills\\review"`, `"./skills\u0000"`, `"./.git"`, `"./missing"`, `"./file.txt"`,
	} {
		t.Run(raw, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeManifest(t, root, `{"name":"review-tools","skills":`+raw+`}`)
			writeSkillFile(t, root, "file.txt", "not a directory")
			_, err := claude.ReadSkills(root, "package")
			require.ErrorIs(t, err, claude.ErrInvalidSkills)
		})
	}
}

func TestReadSkillsMissingDeclaredDirectoryPreservesCause(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeManifest(t, root, `{"name":"review-tools","skills":"./missing"}`)
	_, err := claude.ReadSkills(root, "package")
	require.ErrorIs(t, err, fs.ErrNotExist)
	require.ErrorIs(t, err, claude.ErrInvalidSkills)
}

func TestReadSkillsWithoutManifestOrCapabilities(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	inventory, err := claude.ReadSkills(root, "package")
	require.NoError(t, err)
	assert.Empty(t, inventory.Capabilities)
	writeSkillFile(t, root, "skills/review/SKILL.md", "# Review\n")
	inventory, err = claude.ReadSkills(root, "package")
	require.NoError(t, err)
	require.Len(t, inventory.Capabilities, 1)
	assert.Equal(t, "review", inventory.Capabilities[0].ID.Name)
	_, err = claude.ReadSkills(root, "")
	require.ErrorIs(t, err, claude.ErrInvalidSkills)
}

func TestReadSkillsPreservesDocumentAndMetadataFallbacks(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, document, invocation, description string
	}{
		{"plain", "# Review\n", "review", "# Review"},
		{"named", "---\nname: fancy\ndescription: Detailed review\n---\n\n# Review\n", "fancy", "Detailed review"},
		{"empty description", "---\ndescription: ''\n---\n\n# Review\n", "review", ""},
		{"malformed YAML", "---\nname: [broken\n---\n\n# Review\n", "review", "# Review"},
		{"duplicate metadata", "---\nname: first\nname: second\n---\n\n# Review\n", "review", "# Review"},
		{"not first line", "# Review\n---\nname: fancy\n---\n", "review", "# Review"},
		{"BOM and CRLF", "\ufeff---\r\nname: fancy\r\n---\r\n\r\n# Review\r\n", "fancy", "# Review"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeManifest(t, root, `{"name":"review-tools"}`)
			writeSkillFile(t, root, "skills/review/SKILL.md", tc.document)
			inventory, err := claude.ReadSkills(root, "package")
			require.NoError(t, err)
			require.Len(t, inventory.Capabilities, 1)
			capability := inventory.Capabilities[0]
			assert.Equal(t, tc.invocation, capability.ID.Name)
			assert.Equal(t, tc.description, capability.Description)
			definition, ok := capability.Definition.(claudemodel.Skill)
			require.True(t, ok)
			assert.Equal(t, tc.document, definition.Document)
		})
	}
}

func TestReadSkillsRejectsInvalidEntrypoints(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeManifest(t, root, `{"name":"review-tools"}`)
	require.NoError(t, os.MkdirAll(filepath.Join(root, "skills/review/SKILL.md"), 0o700))
	_, err := claude.ReadSkills(root, "package")
	require.ErrorIs(t, err, claude.ErrInvalidSkills)
}

func writeSkillFile(t *testing.T, root, relative, content string) {
	t.Helper()
	filename := filepath.Join(root, filepath.FromSlash(relative))
	require.NoError(t, os.MkdirAll(filepath.Dir(filename), 0o700))
	require.NoError(t, os.WriteFile(filename, []byte(content), 0o600))
}
