package claude

import (
	"encoding/json"

	"github.com/leonhfr/glean/internal/model"
)

// HookHandlerType identifies native handler semantics, including unknown forms.
type HookHandlerType string

// Documented native handler types; availability requires runtime assessment.
const (
	HookCommand HookHandlerType = "command"
	HookPrompt  HookHandlerType = "prompt"
	HookAgent   HookHandlerType = "agent"
	HookHTTP    HookHandlerType = "http"
	HookMCPTool HookHandlerType = "mcp_tool"
)

// Hook contains every matcher group contributing to one selectable native event.
// Groups retain source order. Inventory does not prove runtime availability.
type Hook struct {
	Event  string
	Groups []HookGroup
}

// HookGroup preserves one authored matcher group and its contributing locations.
// Document remains authoritative; decoded handlers support later assessment.
type HookGroup struct {
	Document     json.RawMessage
	Handlers     []HookHandler
	Declarations []model.Location
}

// HookHandler retains complete native fields without executing or expanding them.
type HookHandler struct {
	Type   HookHandlerType
	Fields map[string]json.RawMessage
}

// Kind implements model.Definition.
func (Hook) Kind() model.CapabilityKind { return model.KindClaudeHook }
