package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateIntentWithoutDocument(t *testing.T) {
	t.Parallel()
	cfg := defaults()
	cfg.MCPServers["docs.api"] = MCPServer{Transport: TransportHTTP, URL: "https://example.com/mcp"}
	fields := fieldSet{}
	require.NoError(t, validateIntent(&cfg, fields, t.TempDir()))

	// Even an empty command is forbidden when explicitly authored on HTTP.
	fields[`mcp_servers["docs.api"].command`] = true
	err := validateIntent(&cfg, fields, t.TempDir())
	require.ErrorIs(t, err, ErrInvalid)
	var detail *Error
	require.ErrorAs(t, err, &detail)
	assert.Equal(t, `mcp_servers["docs.api"].command`, detail.Field)
	assert.Zero(t, detail.Line)
	assert.Zero(t, detail.Column)
}

func TestValidateReferenceWithoutDocument(t *testing.T) {
	t.Parallel()
	server := MCPServer{
		Transport: TransportStdio, Command: "node",
		Env: map[string]Value{"TOKEN": {}},
	}
	require.NoError(t, validateServer(server, fieldSet{}, "mcp_servers.docs"))
	// Empty literals are valid; an authored empty environment reference is not.
	fields := fieldSet{"mcp_servers.docs.env.TOKEN.from_env": true}
	err := validateServer(server, fields, "mcp_servers.docs")
	require.ErrorIs(t, err, ErrInvalid)
	var detail *Error
	require.ErrorAs(t, err, &detail)
	assert.Equal(t, "mcp_servers.docs.env.TOKEN", detail.Field)
}
