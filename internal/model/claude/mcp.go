package claude

import (
	"encoding/json"

	"github.com/leonhfr/glean/internal/model"
)

// MCPTransport identifies native transports, including forms awaiting assessment.
type MCPTransport string

// Documented native transports. Omitted type means stdio, not URL inference.
const (
	MCPStdio          MCPTransport = "stdio"
	MCPHTTP           MCPTransport = "http"
	MCPSSE            MCPTransport = "sse"
	MCPStreamableHTTP MCPTransport = "streamable-http"
)

// MCP preserves a native server definition without expanding references.
// Fields remain authoritative, including omission of the implicit stdio type.
// Inventory does not validate runtime requirements or approve credentials.
type MCP struct {
	Transport MCPTransport
	Fields    map[string]json.RawMessage
}

// Kind implements model.Definition.
func (MCP) Kind() model.CapabilityKind { return model.KindClaudeMCP }
