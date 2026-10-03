package system_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/leonhfr/glean/internal/system"
)

func TestRealRootReadAndContainment(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(directory, "asset.txt"), []byte("asset"), 0o600))
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "outside.txt"), []byte("outside"), 0o600))
	require.NoError(t, os.Symlink(filepath.Join(outside, "outside.txt"), filepath.Join(directory, "escape")))
	root, err := (system.Real{}).OpenRoot(directory)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })
	physical, err := filepath.EvalSymlinks(directory)
	require.NoError(t, err)
	assert.Equal(t, physical, root.Name())
	data, err := fs.ReadFile(root.FS(), "asset.txt")
	require.NoError(t, err)
	assert.Equal(t, "asset", string(data))
	for _, path := range []string{"../outside.txt", "escape"} {
		_, err = fs.ReadFile(root.FS(), path)
		require.Error(t, err)
	}
}

func TestRealRootPreservesMissingFileCause(t *testing.T) {
	t.Parallel()
	_, err := (system.Real{}).OpenRoot(filepath.Join(t.TempDir(), "missing"))
	require.ErrorIs(t, err, fs.ErrNotExist)
}
