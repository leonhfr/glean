// Package model defines shared capability inventory types independently of
// configuration syntax, source acquisition and native harness implementations.
package model

// CapabilityKind identifies shared or harness-specific capability semantics.
type CapabilityKind string

// Supported MVP capability kinds. Skills use the shared format; other kinds
// retain Claude semantics rather than implying cross-harness equivalence.
const (
	KindSkill         CapabilityKind = "skill"
	KindClaudeAgent   CapabilityKind = "claude:agent"
	KindClaudeCommand CapabilityKind = "claude:command"
	KindClaudeHook    CapabilityKind = "claude:hook"
	KindClaudeMCP     CapabilityKind = "claude:mcp"
	KindClaudeLSP     CapabilityKind = "claude:lsp"
)

// Definition preserves a capability's typed native semantics. Implementations
// live in definition packages; the common model does not import them.
type Definition interface {
	Kind() CapabilityKind
}

// Capability describes one selectable item within a package inventory.
// ID is package-relative; PackageID distinguishes identical IDs across packages.
// Name and Description are display metadata, not identity or selection aliases.
type Capability struct {
	ID          CapabilityID
	PackageID   PackageID
	Name        string
	Description string
	Provenance  Provenance
	Payload     Payload
	Definition  Definition
}

// Provenance records the package root and contributing native declarations.
// Root is absolute. Paths in Declarations are relative to Root. Multiple locations
// allow one capability, such as a hook event, to combine native declarations.
type Provenance struct {
	Root         string
	Declarations []Location
}

// Location identifies a source file and, optionally, a declaration within it.
// Pointer is a JSON Pointer for structured declarations; an empty pointer refers
// to the whole file. It does not contain credentials or resolved values.
type Location struct {
	Path    string
	Pointer string
}

// Payload identifies active entrypoints and their supporting files/directories.
// All paths are package-root-relative. Directory paths include their trees.
// Readers establish safe boundaries and reject symlinks before producing output.
// Inline native definitions may have no filesystem entrypoints. Assets are
// supporting content, not permission to activate unselected entrypoints.
type Payload struct {
	Entrypoints []string
	Assets      []string
}
