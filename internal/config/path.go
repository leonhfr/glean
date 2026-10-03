package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// DefaultPath discovers the user config under XDG_CONFIG_HOME or ~/.config.
// It returns the intended .yaml path if neither file exists, without creating it.
func DefaultPath() (string, error) {
	directory := os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(directory) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("config home: %w", err)
		}

		directory = filepath.Join(home, ".config")
	}

	return Discover(filepath.Join(directory, "glean"))
}

// Discover selects config.yaml or config.yml in directory. Both existing is an
// ambiguity error; explicit Load bypasses discovery. It does not create files.
func Discover(directory string) (string, error) {
	long := filepath.Join(directory, "config.yaml")
	short := filepath.Join(directory, "config.yml")
	longExists, err := configExists(long)
	if err != nil {
		return "", err
	}

	shortExists, err := configExists(short)
	if err != nil {
		return "", err
	}

	if longExists && shortExists {
		return "", fmt.Errorf("%w: both config.yaml and config.yml exist; select one explicitly", ErrInvalid)
	}

	if shortExists {
		return short, nil
	}

	return long, nil
}

func configExists(path string) (bool, error) {
	// #nosec G703 -- Inspecting an explicitly chosen config location is intentional.
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("inspect config: %w", err)
	}

	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("%w: config path is not a regular file", ErrInvalid)
	}

	return true, nil
}
