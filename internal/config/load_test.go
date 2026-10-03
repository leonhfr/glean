package config_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/leonhfr/glean/internal/config"
)

func TestParseDefaults(t *testing.T) {
	t.Parallel()
	doc, err := config.Parse([]byte("# Intent\n{}\n"), filepath.Join(t.TempDir(), "config.yaml"))
	require.NoError(t, err)
	assert.Equal(t, config.AutomationNone, doc.Config.Automation)
	assert.Equal(t, 30*time.Second, doc.Config.GitTimeout)
	assert.Equal(t, 168*time.Hour, doc.Config.RefreshPeriod)
	assert.Empty(t, doc.Config.Selections)
	assert.Empty(t, doc.Config.MCPServers)
	assert.Equal(t, "# Intent", doc.Root.Content[0].HeadComment)
}

func TestParseSelection(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	doc, err := config.Parse([]byte(`selections:
  Reviews:
    repo: example/marketplace
    path: plugins/reviews
    plugin: review-tools
    format: claude-marketplace
    select:
      agents: [reviewer]
      hooks: [PreToolUse]
  reviews:
    path: ./skills
    format: skill-collection
    select: all
  empty:
    path: ./empty
    select: {}
automation: refresh
git_timeout: 0s
refresh_period: 0s
`), filepath.Join(directory, "config.yaml"))
	require.NoError(t, err)
	assert.Equal(t, config.AutomationRefresh, doc.Config.Automation)
	assert.Equal(t, config.FormatClaudeMarketplace, doc.Config.Selections["Reviews"].Format)
	assert.Equal(t, "plugins/reviews", doc.Config.Selections["Reviews"].Path)
	assert.Equal(t, []string{"reviewer"}, doc.Config.Selections["Reviews"].Select.Agents)
	assert.True(t, doc.Config.Selections["reviews"].Select.All)
	assert.Equal(t, filepath.Join(directory, "skills"), doc.Config.Selections["reviews"].Path)
	assert.False(t, doc.Config.Selections["empty"].Select.All)
	assert.Zero(t, doc.Config.GitTimeout)
	assert.Zero(t, doc.Config.RefreshPeriod)
}

func TestParseMCP(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	doc, err := config.Parse([]byte(`mcp_servers:
  docs.api:
    transport: http
    url: https://example.com/mcp
    headers:
      X-Mode: ""
    auth:
      bearer_token: {from_env: GLEAN_TEST_TOKEN_NOT_RESOLVED}
  docs:
    transport: stdio
    command: node
    args: ["", "--stdio"]
    cwd: ./server
    enabled: false
    env:
      MODE: development
      TOKEN: {from_env: GLEAN_TEST_TOKEN_NOT_RESOLVED}
    native: {claude: {}}
`), filepath.Join(directory, "config.yaml"))
	require.NoError(t, err)
	http := doc.Config.MCPServers["docs.api"]
	assert.Equal(t, config.TransportHTTP, http.Transport)
	assert.True(t, http.Enabled)
	assert.Equal(t, "GLEAN_TEST_TOKEN_NOT_RESOLVED", http.Auth.BearerToken.FromEnv)
	assert.Empty(t, http.Headers["X-Mode"].Literal)
	stdio := doc.Config.MCPServers["docs"]
	assert.Equal(t, config.TransportStdio, stdio.Transport)
	assert.False(t, stdio.Enabled)
	assert.Equal(t, filepath.Join(directory, "server"), stdio.Cwd)
	assert.Equal(t, []string{"", "--stdio"}, stdio.Args)
	assert.Equal(t, "development", stdio.Env["MODE"].Literal)
	assert.Equal(t, "GLEAN_TEST_TOKEN_NOT_RESOLVED", stdio.Env["TOKEN"].FromEnv)
}

func TestParseInvalid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, input, field string
	}{
		{"empty", "", ""},
		{"comments only", "# accidentally truncated\n", ""},
		{"null root", "null", ""},
		{"sequence root", "[]", ""},
		{"multiple documents", "{}\n---\n{}", ""},
		{"explicit version", "version: 1", "version"},
		{"future version", "version: 2", "version"},
		{"overflow version", "version: 18446744073709551615", "version"},
		{"blank Git subdir", "selections: {a: {repo: owner/repo, path: ' '}}", "selections.a.path"},
		{"truncated YAML", "selections: [", ""},
		{"string version", "version: '1'", "version"},
		{"unknown root", "profiles: {}", "profiles"},
		{"null optional", "selections: null", "selections"},
		{"duplicate root", "version: 1\nversion: 1", "version"},
		{"duplicate nested", "selections: {a: {path: one, path: two}}", "selections.a.path"},
		{"invalid alias", "selections: {'a.b': {path: one}}", `selections["a.b"]`},
		{"missing origin", "selections: {a: {select: all}}", "selections.a"},
		{"blank origin", "selections: {a: {repo: ' '}}", "selections.a.repo"},
		{"local ref", "selections: {a: {path: one, ref: main}}", "selections.a.ref"},
		{"traversal", "selections: {a: {repo: owner/repo, path: ../other}}", "selections.a.path"},
		{"absolute subdir", "selections: {a: {repo: owner/repo, path: /other}}", "selections.a.path"},
		{"bad format", "selections: {a: {path: one, format: codex}}", "selections.a.format"},
		{"plugin on skill", "selections: {a: {path: one, format: skill, plugin: a}}", "selections.a.plugin"},
		{"numeric locator", "selections: {a: {repo: 123}}", "selections.a.repo"},
		{"boolean select", "selections: {a: {path: one, select: true}}", "selections.a.select"},
		{"invalid select scalar", "selections: {a: {path: one, select: '*'}}", "selections.a.select"},
		{"unknown kind", "selections: {a: {path: one, select: {bin: []}}}", "selections.a.select.bin"},
		{"duplicate capability", "selections: {a: {path: one, select: {skills: [a, a]}}}", "selections.a.select.skills[1]"},
		{"blank capability", "selections: {a: {path: one, select: {skills: ['']}}}", "selections.a.select.skills[0]"},
		{"boolean capability", "selections: {a: {path: one, select: {skills: [true]}}}", "selections.a.select.skills[0]"},
		{"invalid automation", "automation: apply", "automation"},
		{"numeric duration", "git_timeout: 0", "git_timeout"},
		{"bad duration", "refresh_period: yesterday", "refresh_period"},
		{"negative duration", "git_timeout: -1s", "git_timeout"},
		{"missing transport", "mcp_servers: {a: {command: node}}", "mcp_servers.a.transport"},
		{"missing command", "mcp_servers: {a: {transport: stdio}}", "mcp_servers.a.command"},
		{"mixed transport", "mcp_servers: {a: {transport: stdio, command: node, headers: {}}}", "mcp_servers.a.headers"},
		{"wrong enabled type", "mcp_servers: {a: {transport: stdio, command: node, enabled: 'false'}}", "mcp_servers.a.enabled"},
		{"unsafe URL", "mcp_servers: {a: {transport: http, url: 'https://user:pass@example.com'}}", "mcp_servers.a.url"},
		{"relative URL", "mcp_servers: {a: {transport: http, url: /mcp}}", "mcp_servers.a.url"},
		{"unsupported URL", "mcp_servers: {a: {transport: http, url: 'ftp://example.com'}}", "mcp_servers.a.url"},
		{"bad reference", "mcp_servers: {a: {transport: stdio, command: node, env: {KEY: {from_env: 'BAD-NAME'}}}}", "mcp_servers.a.env.KEY"},
		{"empty reference", "mcp_servers: {a: {transport: stdio, command: node, env: {KEY: {}}}}", "mcp_servers.a.env.KEY"},
		{"reference extra", "mcp_servers: {a: {transport: stdio, command: node, env: {KEY: {from_env: KEY, default: value}}}}", "mcp_servers.a.env.KEY.default"},
		{"bad env key", "mcp_servers: {a: {transport: stdio, command: node, env: {'BAD-NAME': ''}}}", "mcp_servers.a.env"},
		{"header case clash", "mcp_servers: {a: {transport: http, url: 'https://example.com', headers: {X-Key: a, x-key: b}}}", "mcp_servers.a.headers"},
		{"bearer clash", "mcp_servers: {a: {transport: http, url: 'https://example.com', headers: {authorization: ''}, auth: {bearer_token: {from_env: TOKEN}}}}", "mcp_servers.a.headers"},
		{"literal bearer", "mcp_servers: {a: {transport: http, url: 'https://example.com', auth: {bearer_token: secret}}}", "mcp_servers.a.auth.bearer_token"},
		{"unknown native option", "mcp_servers: {a: {transport: stdio, command: node, native: {claude: {alwaysLoad: true}}}}", "mcp_servers.a.native.claude.alwaysLoad"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := config.Parse([]byte(tt.input), filepath.Join(t.TempDir(), "config.yaml"))
			require.ErrorIs(t, err, config.ErrInvalid)
			var diagnostic *config.Error
			if tt.name != "truncated YAML" {
				require.ErrorAs(t, err, &diagnostic)
				assert.Equal(t, tt.field, diagnostic.Field)
			}
		})
	}
}

func TestLoadPreservesFileAndErrorPosition(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := []byte("# Header\nselections:\n  a:\n    path: ./source # keep\n    select: {}\n")
	require.NoError(t, os.WriteFile(path, original, 0o600))
	_, err := config.Load(path)
	require.NoError(t, err)
	actual, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, original, actual)
	_, err = config.Parse([]byte("# Header\nselections:\n  a:\n    path: ./source\n    select: null\n"), path)
	var diagnostic *config.Error
	require.ErrorAs(t, err, &diagnostic)
	assert.Equal(t, 5, diagnostic.Line)
	assert.Equal(t, 13, diagnostic.Column)
}

func TestDiscoverAndMissingFile(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	long := filepath.Join(directory, "config.yaml")
	short := filepath.Join(directory, "config.yml")
	actual, err := config.Discover(directory)
	require.NoError(t, err)
	assert.Equal(t, long, actual)
	_, err = config.Load(long)
	require.ErrorIs(t, err, fs.ErrNotExist)
	require.NoError(t, os.WriteFile(short, []byte("{}"), 0o600))
	actual, err = config.Discover(directory)
	require.NoError(t, err)
	assert.Equal(t, short, actual)
	require.NoError(t, os.WriteFile(long, []byte("{}"), 0o600))
	_, err = config.Discover(directory)
	require.ErrorIs(t, err, config.ErrInvalid)
	_, err = config.Load(long)
	require.NoError(t, err)
}

func TestParseYAMLAlias(t *testing.T) {
	t.Parallel()
	doc, err := config.Parse([]byte(`selections:
  first: &selection
    path: ./skill
    select: {skills: [writing]}
  second: *selection
`), filepath.Join(t.TempDir(), "config.yaml"))
	require.NoError(t, err)
	assert.Equal(t, doc.Config.Selections["first"], doc.Config.Selections["second"])
}

func TestDottedNamesAndErrorLocation(t *testing.T) {
	t.Parallel()
	_, err := config.Parse([]byte(`mcp_servers:
  docs.url:
    transport: stdio
    command: node
  docs:
    transport: http
    url: invalid
`), filepath.Join(t.TempDir(), "config.yaml"))
	var diagnostic *config.Error
	require.ErrorAs(t, err, &diagnostic)
	assert.Equal(t, "mcp_servers.docs.url", diagnostic.Field)
	assert.Equal(t, 7, diagnostic.Line)
}

func TestDefaultPath(t *testing.T) { //nolint:paralleltest // Config discovery intentionally reads process-wide environment.
	directory := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", directory)
	actual, err := config.DefaultPath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(directory, "glean", "config.yaml"), actual)
	t.Setenv("XDG_CONFIG_HOME", "relative-is-not-an-XDG-root")
	t.Setenv("HOME", directory)
	actual, err = config.DefaultPath()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(directory, ".config", "glean", "config.yaml"), actual)
}

func TestHomeExpansion(t *testing.T) {
	t.Parallel()
	home, err := os.UserHomeDir()
	require.NoError(t, err)
	doc, err := config.Parse([]byte("selections: {a: {path: '~/skills'}}"), filepath.Join(t.TempDir(), "config.yaml"))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(home, "skills"), doc.Config.Selections["a"].Path)
}
