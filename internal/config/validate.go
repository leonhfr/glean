package config

import (
	"fmt"
	"maps"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
)

func validateIntent(cfg *Config, fields fieldSet, directory string) error {
	for _, entry := range []struct {
		name  string
		value time.Duration
	}{
		{"git_timeout", cfg.GitTimeout}, {"refresh_period", cfg.RefreshPeriod},
	} {
		if entry.value < 0 {
			return finding(entry.name, "expected a nonnegative Go duration string")
		}
	}

	if cfg.Automation != AutomationNone && cfg.Automation != AutomationRefresh {
		return finding("automation", "expected none or refresh")
	}

	if err := resolveSelections(cfg, fields, directory); err != nil {
		return err
	}

	return resolveServers(cfg, fields, directory)
}

func resolveSelections(cfg *Config, fields fieldSet, directory string) error {
	for _, name := range slices.Sorted(maps.Keys(cfg.Selections)) {
		selection := cfg.Selections[name]
		field := fieldPath("selections", name)
		if !validAlias(name) {
			return finding(field, "alias must start with an ASCII letter/digit and contain only letters, digits, - or _")
		}

		if err := validateSelection(selection, fields, field); err != nil {
			return err
		}

		if selection.Repo == "" {
			resolved, err := resolveLocal(selection.Path, directory)
			if err != nil {
				return err
			}

			selection.Path = resolved
			cfg.Selections[name] = selection
		}
	}

	return nil
}

func resolveServers(cfg *Config, fields fieldSet, directory string) error {
	for _, name := range slices.Sorted(maps.Keys(cfg.MCPServers)) {
		server := cfg.MCPServers[name]
		field := fieldPath("mcp_servers", name)
		if strings.TrimSpace(name) == "" || strings.ContainsFunc(name, unicode.IsControl) {
			return finding(field, "server name must be nonblank and contain no control characters")
		}

		if err := validateServer(server, fields, field); err != nil {
			return err
		}

		if server.Cwd != "" {
			resolved, err := resolveLocal(server.Cwd, directory)
			if err != nil {
				return err
			}

			server.Cwd = resolved
			cfg.MCPServers[name] = server
		}
	}

	return nil
}

func validateSelection(selection Selection, fields fieldSet, field string) error {
	for _, entry := range []struct{ key, value string }{
		{"repo", selection.Repo}, {"path", selection.Path}, {"plugin", selection.Plugin}, {"ref", selection.Ref}, {"format", string(selection.Format)},
	} {
		if fields[field+"."+entry.key] && strings.TrimSpace(entry.value) == "" {
			return finding(field+"."+entry.key, "must not be blank")
		}
	}

	if selection.Repo == "" && selection.Path == "" {
		return finding(field, "require repo or local path")
	}

	if selection.Repo == "" && selection.Ref != "" {
		return finding(field+".ref", "ref is only valid with a Git repo")
	}

	if selection.Repo != "" && selection.Path != "" && !safeSubpath(selection.Path) {
		return finding(field+".path", "repository path must be relative and stay inside the repo")
	}

	if err := validateFormat(selection, field); err != nil {
		return err
	}

	return validateCapabilities(selection.Select, field+".select")
}

func validateCapabilities(set CapabilitySet, field string) error {
	for _, kind := range []struct {
		name   string
		values []string
	}{
		{"skills", set.Skills},
		{"agents", set.Agents},
		{"commands", set.Commands},
		{"hooks", set.Hooks},
		{"mcp", set.MCP},
		{"lsp", set.LSP},
	} {
		seen := make(map[string]bool)
		for i, name := range kind.values {
			if strings.TrimSpace(name) == "" || seen[name] {
				return finding(fmt.Sprintf("%s.%s[%d]", field, kind.name, i), "capability names must be nonblank and unique within their kind")
			}

			seen[name] = true
		}
	}

	return nil
}

func validateServer(server MCPServer, fields fieldSet, field string) error {
	if err := validateConnection(server, fields, field); err != nil {
		return err
	}

	if fields[field+".cwd"] && strings.TrimSpace(server.Cwd) == "" {
		return finding(field+".cwd", "must not be blank")
	}

	if err := validateValues(server.Env, fields, field+".env", false); err != nil {
		return err
	}

	if err := validateValues(server.Headers, fields, field+".headers", true); err != nil {
		return err
	}

	return validateAuth(server, fields, field)
}

func validateValues(values map[string]Value, fields fieldSet, field string, headers bool) error {
	seen := make(map[string]bool)
	for _, name := range slices.Sorted(maps.Keys(values)) {
		value := values[name]
		if headers {
			canonical := strings.ToLower(name)
			if !validHeader(name) || seen[canonical] {
				return finding(field, "HTTP header names must be valid and unique ignoring case")
			}

			seen[canonical] = true
		} else if !validEnv(name) {
			return finding(field, "invalid environment variable name")
		}

		if fields[fieldPath(field, name)+".from_env"] && !validEnv(value.FromEnv) {
			return finding(fieldPath(field, name), "invalid from_env variable name")
		}

		if headers && strings.ContainsAny(value.Literal, "\r\n") {
			return finding(fieldPath(field, name), "HTTP header value cannot contain line breaks")
		}
	}

	return nil
}

// fieldSet records authored field presence independently of its decoded value.
type fieldSet map[string]bool

func finding(field, message string) error {
	return &Error{Field: field, Message: message}
}

func forbid(fields fieldSet, field string, names ...string) error {
	for _, name := range names {
		if fields[fieldPath(field, name)] {
			return finding(field+"."+name, "field is not valid for this transport")
		}
	}

	return nil
}

func safeSubpath(value string) bool {
	if strings.HasPrefix(value, "/") || strings.Contains(value, "\\") || strings.HasPrefix(value, "~") {
		return false
	}

	for part := range strings.SplitSeq(value, "/") {
		if part == ".." {
			return false
		}
	}

	return filepath.IsLocal(value)
}

func validAlias(value string) bool {
	if value == "" {
		return false
	}

	for i, char := range value {
		letter := char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z'
		digit := char >= '0' && char <= '9'
		if !letter && !digit && (i == 0 || char != '-' && char != '_') {
			return false
		}
	}

	return true
}

func validEnv(value string) bool {
	if value == "" {
		return false
	}

	for i, char := range value {
		letter := char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char == '_'
		if !letter && (i == 0 || char < '0' || char > '9') {
			return false
		}
	}

	return true
}

func validHeader(value string) bool {
	if value == "" {
		return false
	}

	for _, char := range value {
		if char > 127 || !strings.ContainsRune("!#$%&'*+-.^_`|~0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ", char) {
			return false
		}
	}

	return true
}

func validateFormat(selection Selection, field string) error {
	if selection.Format != "" {
		switch selection.Format {
		case FormatClaudeMarketplace, FormatClaudePlugin, FormatSkill, FormatSkillCollection:
		default:
			return finding(field+".format", "unsupported reader format")
		}
	}

	if selection.Plugin != "" && selection.Format != "" && selection.Format != FormatClaudeMarketplace {
		return finding(field+".plugin", "plugin selector requires marketplace format")
	}

	return nil
}

func validateConnection(server MCPServer, fields fieldSet, field string) error {
	switch server.Transport {
	case TransportStdio:
		if strings.TrimSpace(server.Command) == "" {
			return finding(field+".command", "stdio requires an executable command")
		}

		if err := forbid(fields, field, "url", "headers", "auth"); err != nil {
			return err
		}

	case TransportHTTP:
		parsed, err := url.Parse(server.URL)
		if err != nil || parsed.Hostname() == "" || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.User != nil {
			return finding(field+".url", "http requires an absolute HTTP(S) URL without embedded credentials")
		}

		if err := forbid(fields, field, "command", "args", "env", "cwd"); err != nil {
			return err
		}

	default:
		return finding(field+".transport", "require explicit stdio or http transport")
	}

	return nil
}

func validateAuth(server MCPServer, fields fieldSet, field string) error {
	if fields[field+".auth"] {
		name := server.Auth.BearerToken.FromEnv
		if !validEnv(name) {
			return finding(field+".auth.bearer_token", "require {from_env: NAME} with a valid environment variable name")
		}

		for header := range server.Headers {
			if strings.EqualFold(header, "Authorization") {
				return finding(field+".headers", "Authorization and bearer_token cannot be combined")
			}
		}
	}

	return nil
}
