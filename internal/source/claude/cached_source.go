package claude

import (
	"bytes"
	"io/fs"
)

// cachedSource keeps inventory and assessment on the same successfully read bytes.
// Stat/directory observations remain live; later source drift checks are required.
type cachedSource struct {
	source fs.FS
	files  map[string][]byte
}

func (s *cachedSource) Open(name string) (fs.File, error) { return s.source.Open(name) }

func (s *cachedSource) ReadFile(name string) ([]byte, error) {
	if data, exists := s.files[name]; exists {
		return bytes.Clone(data), nil
	}

	data, err := fs.ReadFile(s.source, name)
	if err != nil {
		return nil, err
	}

	s.files[name] = bytes.Clone(data)
	return data, nil
}

func (s *cachedSource) Stat(name string) (fs.FileInfo, error) { return fs.Stat(s.source, name) }

func (s *cachedSource) ReadDir(name string) ([]fs.DirEntry, error) { return fs.ReadDir(s.source, name) }
