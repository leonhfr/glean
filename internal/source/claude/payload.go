package claude

import (
	"errors"
	"fmt"
	"io/fs"
)

// ErrUnsupportedPayload identifies a symlink or special file in source content.
var ErrUnsupportedPayload = errors.New("unsupported plugin payload")

func validatePayload(source fs.FS) error {
	return fs.WalkDir(source, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("inspect plugin payload %s: %w", path, walkErr)
		}

		// Git metadata is outside the payload, including worktree .git files.
		if path == ".git" {
			if entry.IsDir() {
				return fs.SkipDir
			}

			return nil
		}

		if entry.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%w: %s is a symlink; replace it with regular files or directories", ErrUnsupportedPayload, path)
		}

		if !entry.IsDir() && !entry.Type().IsRegular() {
			return fmt.Errorf("%w: %s is a special file; use regular files or directories", ErrUnsupportedPayload, path)
		}

		return nil
	})
}
