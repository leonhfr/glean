// Package claude reads Claude plugin sources.
package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/leonhfr/glean/internal/system"
)

// ManifestPath is relative to a native plugin's payload root.
const ManifestPath = ".claude-plugin/plugin.json"

// ErrInvalidManifest identifies malformed metadata or an invalid plugin name.
var ErrInvalidManifest = errors.New("invalid Claude plugin manifest")

// Manifest contains the package root, native name and authored fields.
// Fields preserves all top-level fields as raw JSON.
type Manifest struct {
	Root    string
	Name    string
	Present bool
	Fields  map[string]json.RawMessage
}

// ReadManifest reads a local plugin manifest after checking the payload boundary.
// Root becomes an absolute physical path. Without a manifest, Name defaults to
// the directory name.
func ReadManifest(sys system.RootOpener, directory string) (result Manifest, err error) {
	root, err := openPluginRoot(sys, directory)
	if err != nil {
		return Manifest{}, err
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			result = Manifest{}
			err = errors.Join(err, fmt.Errorf("close plugin root: %w", closeErr))
		}
	}()

	return readManifest(root)
}

func readManifest(root system.Root) (Manifest, error) {
	rootPath := root.Name()
	data, err := fs.ReadFile(root.FS(), ManifestPath)
	if errors.Is(err, fs.ErrNotExist) {
		name := filepath.Base(rootPath)
		if !validPluginName(name) {
			return Manifest{}, fmt.Errorf("%w: directory name is not a valid native plugin name", ErrInvalidManifest)
		}

		return Manifest{Root: rootPath, Name: name, Fields: make(map[string]json.RawMessage)}, nil
	}

	if err != nil {
		return Manifest{}, fmt.Errorf("read plugin manifest: %w", err)
	}

	fields, err := manifestFields(data)
	if err != nil {
		return Manifest{}, err
	}

	var name string
	if err = json.Unmarshal(fields["name"], &name); err != nil || !validPluginName(name) {
		return Manifest{}, fmt.Errorf("%w: name must be a nonempty native plugin identifier", ErrInvalidManifest)
	}

	return Manifest{Root: rootPath, Name: name, Present: true, Fields: fields}, nil
}

func validPluginName(name string) bool {
	if name == "" {
		return false
	}

	for _, char := range name {
		if unicode.IsSpace(char) || unicode.IsControl(char) || unicode.Is(unicode.Bidi_Control, char) ||
			strings.ContainsRune("@:/\\", char) {
			return false
		}
	}

	return true
}
