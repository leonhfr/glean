package model

// PackageID is the resolved source/package identity, independent of config aliases.
// Source resolution assigns it; a native display name alone is not sufficient.
type PackageID string

// Package describes a resolved package available to a reader.
// Root is its absolute local payload directory. Name is the native package name.
type Package struct {
	ID   PackageID
	Name string
	Root string
}

// Inventory contains all discovered capabilities before selection filtering.
// Readers reject ambiguous IDs within a package and preserve native ordering
// inside definitions. Delivery consumes this inventory rather than rescanning.
type Inventory struct {
	Package      Package
	Capabilities []Capability
}
