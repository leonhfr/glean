package claude

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func openPluginRoot(directory string) (*os.Root, error) {
	rootPath, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve plugin root: %w", err)
	}

	rootPath, err = filepath.EvalSymlinks(rootPath)
	if err != nil {
		return nil, fmt.Errorf("resolve plugin root: %w", err)
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, fmt.Errorf("open plugin root: %w", err)
	}

	if err = validatePayload(root.FS()); err != nil {
		return nil, errors.Join(err, root.Close())
	}

	return root, nil
}
