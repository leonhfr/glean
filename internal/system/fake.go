package system

import (
	"fmt"
	"io/fs"
)

// Fake scripts root acquisition for tests.
type Fake struct {
	OpenRootHandler func(directory string) (Root, error)
}

// FakeRoot supplies a test filesystem and optional close-failure injection.
type FakeRoot struct {
	Path         string
	Source       fs.FS
	CloseHandler func() error
}

// OpenRoot calls the handler; an unset handler panics.
func (f *Fake) OpenRoot(directory string) (Root, error) {
	if f.OpenRootHandler == nil {
		panic(fmt.Sprintf("system.Fake: OpenRoot(%q) called without a handler", directory))
	}

	return f.OpenRootHandler(directory)
}

// Name returns the scripted physical root path.
func (r FakeRoot) Name() string {
	return r.Path
}

// FS returns the scripted filesystem.
func (r FakeRoot) FS() fs.FS {
	return r.Source
}

// Close calls the handler when set, otherwise it succeeds.
func (r FakeRoot) Close() error {
	if r.CloseHandler != nil {
		return r.CloseHandler()
	}

	return nil
}
