package config

// Automation controls automatic source refresh.
type Automation string

// Supported automation policies.
const (
	AutomationNone    Automation = "none"
	AutomationRefresh Automation = "refresh"
)

// Format identifies the reader for a selection's source.
// The empty value requests detection rather than a particular format.
type Format string

// Supported source formats.
const (
	FormatClaudeMarketplace Format = "claude-marketplace"
	FormatClaudePlugin      Format = "claude-plugin"
	FormatSkill             Format = "skill"
	FormatSkillCollection   Format = "skill-collection"
)

// Transport identifies an MCP connection type. There is no implicit transport.
type Transport string

// Supported MCP transports.
const (
	TransportStdio Transport = "stdio"
	TransportHTTP  Transport = "http"
)
