package system

import (
	"fmt"
	"os"
	"path/filepath"
)

// Real accesses the operating system. Its zero value is ready for use.
type Real struct{}

// OpenRoot resolves a directory to its physical path and opens bounded access.
// os.Root prevents reads through paths or links that escape the directory.
func (Real) OpenRoot(directory string) (Root, error) {
	rootPath, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("resolve source root: %w", err)
	}

	rootPath, err = filepath.EvalSymlinks(rootPath)
	if err != nil {
		return nil, fmt.Errorf("resolve source root: %w", err)
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, fmt.Errorf("open source root: %w", err)
	}

	return root, nil
}
