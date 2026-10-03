package claude

import (
	"encoding/json"

	"github.com/leonhfr/glean/internal/model"
)

// LSPTransport preserves native transport declarations for later assessment.
type LSPTransport string

// Documented native transports. Omitted transport means stdio.
const (
	LSPStdio  LSPTransport = "stdio"
	LSPSocket LSPTransport = "socket"
)

// LSP retains a server's core fields and complete authored native configuration.
// Fields remain authoritative, including omitted defaults and native references.
// Inventory does not prove binary availability or runtime option support.
type LSP struct {
	Command             string
	ExtensionToLanguage map[string]string
	Transport           LSPTransport
	Fields              map[string]json.RawMessage
}

// Kind implements model.Definition.
func (LSP) Kind() model.CapabilityKind { return model.KindClaudeLSP }
