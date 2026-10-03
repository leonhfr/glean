package claude

import (
	"encoding/json"
	"maps"
	"math/big"
	"net/url"
	"slices"
	"strings"

	"github.com/leonhfr/glean/internal/model"
	native "github.com/leonhfr/glean/internal/model/claude"
)

type serverFieldRule struct {
	valid    func(json.RawMessage) bool
	required bool
}

// Rules check authored shapes, not acceptance by a particular Claude release.
func assessServerDefinition(capability model.Capability) []model.Finding {
	switch definition := capability.Definition.(type) {
	case native.MCP:
		return assessMCPDefinition(capability, definition)

	case native.LSP:
		return assessLSPDefinition(capability, definition)

	default:
		return nil
	}
}

func assessMCPDefinition(capability model.Capability, definition native.MCP) []model.Finding {
	rules := map[string]serverFieldRule{"type": {valid: nonblankJSONString}}
	var inactive []string
	switch definition.Transport {
	case native.MCPStdio:
		rules["command"] = serverFieldRule{valid: nonblankJSONString, required: true}
		rules["args"] = serverFieldRule{valid: jsonStringArray}
		rules["env"] = serverFieldRule{valid: jsonStringMap}
		inactive = []string{"url", "headers", "headersHelper", "oauth"}

	case native.MCPHTTP, native.MCPStreamableHTTP:
		rules["url"] = serverFieldRule{valid: nonblankJSONString, required: true}
		rules["headers"] = serverFieldRule{valid: jsonStringMap}
		rules["headersHelper"] = serverFieldRule{valid: nonblankJSONString}
		inactive = []string{"command", "args", "env"}

	case native.MCPSSE:
		return []model.Finding{serverFieldFinding(capability, "UNSUPPORTED_TRANSPORT", model.FindingUnsupported, "type", "SSE is outside the MVP stdio/HTTP delivery contract.")}

	default:
		return []model.Finding{serverFieldFinding(capability, "UNSUPPORTED_TRANSPORT", model.FindingUnsupported, "type", "This transport is outside the MVP stdio/HTTP delivery contract.")}
	}

	findings := assessServerFields(capability, definition.Fields, rules, inactive)
	if definition.Transport != native.MCPStdio {
		findings = append(findings, assessServerURL(capability, definition.Fields["url"])...)
	}

	return findings
}

func assessLSPDefinition(capability model.Capability, definition native.LSP) []model.Finding {
	rules := map[string]serverFieldRule{
		"command": {valid: nonblankJSONString, required: true}, "extensionToLanguage": {valid: jsonStringMap, required: true},
		"args": {valid: jsonStringArray}, "env": {valid: jsonStringMap}, "workspaceFolder": {valid: jsonString},
		"transport": {valid: nonblankJSONString}, "initializationOptions": {valid: jsonValue}, "settings": {valid: jsonValue},
		"startupTimeout": {valid: positiveJSONInteger}, "shutdownTimeout": {valid: positiveJSONInteger},
		"restartOnCrash": {valid: jsonBoolean}, "maxRestarts": {valid: nonnegativeJSONInteger}, "diagnostics": {valid: jsonBoolean},
	}
	findings := assessServerFields(capability, definition.Fields, rules, nil)
	if definition.Transport != native.LSPStdio && definition.Transport != native.LSPSocket {
		findings = append(findings, serverFieldFinding(capability, "UNSUPPORTED_TRANSPORT", model.FindingUnsupported, "transport", "This LSP transport has no documented native mapping."))
	}

	if strings.Contains(definition.Command, " ") && !strings.HasPrefix(definition.Command, "/") {
		findings = append(findings, serverFieldFinding(capability, "INVALID_NATIVE_FIELD", model.FindingMissing, "command", "Native LSP command must name a binary; put arguments in args."))
	}

	return findings
}

func assessServerFields(capability model.Capability, fields map[string]json.RawMessage, rules map[string]serverFieldRule, inactive []string) []model.Finding {
	var findings []model.Finding
	for _, field := range slices.Sorted(maps.Keys(rules)) {
		rule := rules[field]
		raw, exists := fields[field]
		if !exists && !rule.required {
			continue
		}

		if !exists || !rule.valid(raw) {
			findings = append(findings, serverFieldFinding(capability, "INVALID_NATIVE_FIELD", model.FindingMissing, field, "Required native field is absent or its authored value has an invalid shape."))
		}
	}

	for _, field := range slices.Sorted(maps.Keys(fields)) {
		if _, known := rules[field]; known {
			continue
		}

		code, status, message := "UNKNOWN_NATIVE_FIELD", model.FindingUnknown, "Native field semantics and version availability require explicit assessment."
		if slices.Contains(inactive, field) {
			code, status, message = "INAPPLICABLE_NATIVE_FIELD", model.FindingUnsupported, "This field cannot retain its semantics with the declared transport."
		}

		findings = append(findings, serverFieldFinding(capability, code, status, field, message))
	}

	return findings
}

func assessServerURL(capability model.Capability, raw json.RawMessage) []model.Finding {
	if !nonblankJSONString(raw) {
		// The shape finding already identifies this field.
		return nil
	}

	var value string
	_ = json.Unmarshal(raw, &value)
	if strings.Contains(value, "${") {
		return []model.Finding{serverFieldFinding(capability, "NATIVE_URL", model.FindingUnknown, "url", "Resolve native substitutions before validating the server URL.")}
	}

	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return []model.Finding{serverFieldFinding(capability, "INVALID_NATIVE_FIELD", model.FindingMissing, "url", "HTTP server requires an absolute HTTP or HTTPS URL.")}
	}

	return nil
}

func serverFieldFinding(capability model.Capability, code string, status model.FindingStatus, field, message string) model.Finding {
	finding := capabilityFinding(capability, code, status, field, message)
	if len(finding.Locations) > 0 {
		// First location is the definition; subsequent locations retain related declarations.
		finding.Locations[0].Pointer += "/" + pointerSegment(field)
	}

	return finding
}

func jsonString(raw json.RawMessage) bool {
	var value string
	return len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, &value) == nil
}

func nonblankJSONString(raw json.RawMessage) bool {
	var value string
	return jsonString(raw) && json.Unmarshal(raw, &value) == nil && strings.TrimSpace(value) != ""
}

func jsonStringArray(raw json.RawMessage) bool {
	var values []json.RawMessage
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &values) != nil {
		return false
	}

	for _, value := range values {
		if !jsonString(value) {
			return false
		}
	}

	return true
}

func jsonStringMap(raw json.RawMessage) bool {
	var values map[string]json.RawMessage
	if !jsonObject(raw) || json.Unmarshal(raw, &values) != nil {
		return false
	}

	for _, value := range values {
		if !jsonString(value) {
			return false
		}
	}

	return true
}

func jsonObject(raw json.RawMessage) bool {
	return len(raw) > 0 && raw[0] == '{' && json.Valid(raw)
}

func jsonBoolean(raw json.RawMessage) bool {
	var value bool
	return string(raw) != "null" && json.Unmarshal(raw, &value) == nil
}

func positiveJSONInteger(raw json.RawMessage) bool {
	value, ok := new(big.Rat).SetString(string(raw))
	return ok && json.Valid(raw) && value.IsInt() && value.Sign() > 0
}

func nonnegativeJSONInteger(raw json.RawMessage) bool {
	value, ok := new(big.Rat).SetString(string(raw))
	return ok && json.Valid(raw) && value.IsInt() && value.Sign() >= 0
}

func jsonValue(raw json.RawMessage) bool {
	return json.Valid(raw)
}
