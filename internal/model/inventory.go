package model

// PackageID is the resolved source/package identity, independent of config aliases.
type PackageID string

// Package describes a resolved package.
// Root is its absolute local payload directory. Name is the native package name.
type Package struct {
	ID   PackageID
	Name string
	Root string
}

// Inventory contains discovered capabilities for one package.
type Inventory struct {
	Package      Package
	Capabilities []Capability
}
