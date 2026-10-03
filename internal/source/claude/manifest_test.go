package claude_test

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/leonhfr/glean/internal/source/claude"
	"github.com/leonhfr/glean/internal/system"
)

func TestReadManifestPreservesAuthoredFields(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.CopyFS(root, os.DirFS("testdata/full")))
	path := filepath.Join(root, filepath.FromSlash(claude.ManifestPath))
	before, err := os.ReadFile(path)
	require.NoError(t, err)

	manifest, err := claude.ReadManifest(system.Real{}, root)
	require.NoError(t, err)
	assert.True(t, manifest.Present)
	assert.Equal(t, "review-tools", manifest.Name)
	physicalRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	assert.Equal(t, physicalRoot, manifest.Root)
	var authored map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(before, &authored))
	assert.Equal(t, authored, manifest.Fields)
	after, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, before, after)
}

func TestReadManifestWithoutFile(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "default-plugin")
	require.NoError(t, os.Mkdir(root, 0o700))
	manifest, err := claude.ReadManifest(system.Real{}, root)
	require.NoError(t, err)
	assert.False(t, manifest.Present)
	assert.Equal(t, "default-plugin", manifest.Name)
	assert.Empty(t, manifest.Fields)

	_, err = claude.ReadManifest(system.Real{}, filepath.Join(root, "missing"))
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestReadManifestRejectsMalformedMetadata(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		data string
	}{
		{"empty", ""},
		{"array", "[]"},
		{"null", "null"},
		{"missing name", "{}"},
		{"null name", `{"name":null}`},
		{"numeric name", `{"name":123}`},
		{"empty name", `{"name":""}`},
		{"whitespace name", `{"name":"review tools"}`},
		{"qualified name", `{"name":"review@marketplace"}`},
		{"namespace name", `{"name":"review:tools"}`},
		{"path name", `{"name":"review/tools"}`},
		{"backslash name", `{"name":"review\\tools"}`},
		{"control name", `{"name":"review\u0000tools"}`},
		{"bidi name", `{"name":"review\u202etools"}`},
		{"duplicate name", `{"name":"review","name":"other"}`},
		{"duplicate declaration", `{"name":"review","hooks":{},"hooks":{}}`},
		{"trailing object", `{"name":"review"} {}`},
		{"trailing garbage", `{"name":"review"} broken`},
		{"trailing comma", `{"name":"review",}`},
		{"incomplete object", `{"name":"review"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeManifest(t, root, tc.data)
			_, err := claude.ReadManifest(system.Real{}, root)
			require.ErrorIs(t, err, claude.ErrInvalidManifest)
		})
	}
}

func TestReadManifestPreservesNativeName(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeManifest(t, root, `{"name":"Review_Tools"}`)
	manifest, err := claude.ReadManifest(system.Real{}, root)
	require.NoError(t, err)
	assert.Equal(t, "Review_Tools", manifest.Name)
}

func TestReadManifestRejectsPayloadSymlinks(t *testing.T) {
	t.Parallel()
	for _, target := range []string{"asset.txt", "assets", "missing", "outside"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeManifest(t, root, `{"name":"review"}`)
			require.NoError(t, os.WriteFile(filepath.Join(root, "asset.txt"), []byte("asset"), 0o600))
			require.NoError(t, os.Mkdir(filepath.Join(root, "assets"), 0o700))
			linkTarget := target
			if target == "outside" {
				linkTarget = t.TempDir()
			}

			require.NoError(t, os.Symlink(linkTarget, filepath.Join(root, "linked")))
			_, err := claude.ReadManifest(system.Real{}, root)
			require.ErrorIs(t, err, claude.ErrUnsupportedPayload)
			assert.ErrorContains(t, err, "linked")
		})
	}
}

func TestReadManifestRejectsLinkedManifestDirectory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := t.TempDir()
	writeManifest(t, outside, `{"name":"review"}`)
	require.NoError(t, os.Symlink(filepath.Join(outside, ".claude-plugin"), filepath.Join(root, ".claude-plugin")))
	_, err := claude.ReadManifest(system.Real{}, root)
	require.ErrorIs(t, err, claude.ErrUnsupportedPayload)
}

func TestReadManifestIgnoresOnlyGitMetadata(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeManifest(t, root, `{"name":"review"}`)
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o700))
	require.NoError(t, os.Symlink("missing", filepath.Join(root, ".git", "metadata-link")))
	_, err := claude.ReadManifest(system.Real{}, root)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitignore"), []byte("ignored-link\n"), 0o600))
	require.NoError(t, os.Symlink("missing", filepath.Join(root, "ignored-link")))
	_, err = claude.ReadManifest(system.Real{}, root)
	require.ErrorIs(t, err, claude.ErrUnsupportedPayload)
}

func TestReadManifestClosesRootOnSuccess(t *testing.T) {
	t.Parallel()
	source := fstest.MapFS{
		claude.ManifestPath:      {Data: []byte(`{"name":"review-tools"}`)},
		"skills/review/SKILL.md": {Data: []byte("# Review\n")},
	}
	closed := 0
	sys := &system.Fake{OpenRootHandler: func(string) (system.Root, error) {
		return system.FakeRoot{
			Path: "/source/review-tools", Source: source,
			CloseHandler: func() error { closed++; return nil },
		}, nil
	}}
	_, err := claude.ReadManifest(sys, "requested-path")
	require.NoError(t, err)
	assert.Equal(t, 1, closed)
}

func TestReadManifestClosesRejectedPayloadAndPreservesBothErrors(t *testing.T) {
	t.Parallel()
	closeErr := errors.New("scripted close failure")
	closed := false
	sys := &system.Fake{OpenRootHandler: func(string) (system.Root, error) {
		return system.FakeRoot{
			Path:         "/source/review-tools",
			Source:       fstest.MapFS{"link": {Mode: fs.ModeSymlink}},
			CloseHandler: func() error { closed = true; return closeErr },
		}, nil
	}}
	_, err := claude.ReadManifest(sys, "requested-path")
	require.ErrorIs(t, err, claude.ErrUnsupportedPayload)
	require.ErrorIs(t, err, closeErr)
	assert.True(t, closed)
}

func TestReadManifestCloseFailureRejectsResult(t *testing.T) {
	t.Parallel()
	closeErr := errors.New("scripted close failure")
	sys := &system.Fake{OpenRootHandler: func(string) (system.Root, error) {
		return system.FakeRoot{
			Path: "/source/review-tools", Source: fstest.MapFS{},
			CloseHandler: func() error { return closeErr },
		}, nil
	}}
	manifest, err := claude.ReadManifest(sys, "requested-path")
	require.ErrorIs(t, err, closeErr)
	assert.Zero(t, manifest)
}

func TestReadManifestPropagatesAcquisitionFailure(t *testing.T) {
	t.Parallel()
	openErr := errors.New("scripted acquisition failure")
	sys := &system.Fake{OpenRootHandler: func(string) (system.Root, error) { return nil, openErr }}
	_, err := claude.ReadManifest(sys, "requested-path")
	require.ErrorIs(t, err, openErr)
}

func writeManifest(t *testing.T, root, data string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".claude-plugin"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, filepath.FromSlash(claude.ManifestPath)), []byte(data), 0o600))
}
