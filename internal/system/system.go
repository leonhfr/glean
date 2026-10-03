// Package system provides substitutable access to operating-system resources.
package system

import "io/fs"

// RootOpener opens directory roots.
type RootOpener interface {
	OpenRoot(directory string) (Root, error)
}

// Root provides read-only filesystem access bounded by a directory.
// Name reports its absolute physical path; Close releases its resources.
type Root interface {
	Name() string
	FS() fs.FS
	Close() error
}
