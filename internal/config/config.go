// Package config loads and validates glean's handwritten configuration.
package config

import "time"

// Config is the version-1 Claude-only desired configuration.
// All root fields are optional. An absent schema version means version 1.
type Config struct {
	// Selections and MCPServers default to empty maps.
	Selections map[string]Selection `koanf:"selections"`
	MCPServers map[string]MCPServer `koanf:"mcp_servers"`
	// Automation defaults to none.
	Automation Automation `koanf:"automation"`
	// GitTimeout defaults to 30s; RefreshPeriod defaults to 168h.
	GitTimeout    time.Duration `koanf:"git_timeout"`
	RefreshPeriod time.Duration `koanf:"refresh_period"`
}

// Selection locates a package and chooses its capabilities.
// Repo or Path is required; with Repo, Path is an optional repository subdirectory.
type Selection struct {
	Repo string `koanf:"repo"`
	Path string `koanf:"path"`
	// Plugin optionally identifies a marketplace plugin.
	Plugin string `koanf:"plugin"`
	// Format is optional; omission requests format detection.
	Format Format `koanf:"format"`
	// Ref is Git-only and defaults to the repository default branch.
	Ref string `koanf:"ref"`
	// Select is optional; omission selects nothing.
	Select CapabilitySet `koanf:"select"`
}

// CapabilitySet is either an explicit subset or all supported capabilities.
// An omitted or empty subset selects nothing.
type CapabilitySet struct {
	All      bool     `koanf:"-"`
	Skills   []string `koanf:"skills"`
	Agents   []string `koanf:"agents"`
	Commands []string `koanf:"commands"`
	Hooks    []string `koanf:"hooks"`
	MCP      []string `koanf:"mcp"`
	LSP      []string `koanf:"lsp"`
}

// MCPServer declares a native server without resolving credentials.
type MCPServer struct {
	// Transport is required.
	Transport Transport `koanf:"transport"`
	// Command is required for stdio; Args, Env and Cwd are optional stdio fields.
	Command string           `koanf:"command"`
	Args    []string         `koanf:"args"`
	Env     map[string]Value `koanf:"env"`
	Cwd     string           `koanf:"cwd"`
	// URL is required for http; Headers and Auth are optional HTTP fields.
	URL     string           `koanf:"url"`
	Headers map[string]Value `koanf:"headers"`
	Auth    Auth             `koanf:"auth"`
	// Description is optional metadata.
	Description string `koanf:"description"`
	// Enabled defaults to true when omitted; explicit false is preserved.
	Enabled bool `koanf:"enabled"`
	// Native is optional; currently only empty Claude options are accepted.
	Native map[string]map[string]any `koanf:"native"`
}

// Value preserves a literal string or an environment-variable reference.
// FromEnv is empty for literals, including an explicitly empty literal.
type Value struct {
	Literal string
	FromEnv string
}

// Auth contains the supported HTTP authentication reference.
type Auth struct {
	// BearerToken is required when auth is present.
	BearerToken Reference `koanf:"bearer_token"`
}

// Reference names an environment variable; loading never reads its value.
type Reference struct {
	// FromEnv is required and names a variable without reading it.
	FromEnv string `koanf:"from_env"`
}

func defaults() Config {
	return Config{
		Selections:    make(map[string]Selection),
		MCPServers:    make(map[string]MCPServer),
		Automation:    AutomationNone,
		GitTimeout:    30 * time.Second,
		RefreshPeriod: 168 * time.Hour,
	}
}
