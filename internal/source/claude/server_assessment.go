package claude

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/leonhfr/glean/internal/model"
	native "github.com/leonhfr/glean/internal/model/claude"
)

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
	rules := map[string]nativeFieldRule{"type": {valid: nonblankJSONString}}
	var inactive []string
	switch definition.Transport {
	case native.MCPStdio:
		rules["command"] = nativeFieldRule{valid: nonblankJSONString, required: true}
		rules["args"] = nativeFieldRule{valid: jsonStringArray}
		rules["env"] = nativeFieldRule{valid: jsonStringMap}
		inactive = []string{"url", "headers", "headersHelper", "oauth"}

	case native.MCPHTTP, native.MCPStreamableHTTP:
		rules["url"] = nativeFieldRule{valid: nonblankJSONString, required: true}
		rules["headers"] = nativeFieldRule{valid: jsonStringMap}
		rules["headersHelper"] = nativeFieldRule{valid: nonblankJSONString}
		inactive = []string{"command", "args", "env"}

	case native.MCPSSE:
		return []model.Finding{nativeFieldFinding(capability, "UNSUPPORTED_TRANSPORT", model.FindingUnsupported, "type", "SSE is outside the MVP stdio/HTTP delivery contract.")}

	default:
		return []model.Finding{nativeFieldFinding(capability, "UNSUPPORTED_TRANSPORT", model.FindingUnsupported, "type", "This transport is outside the MVP stdio/HTTP delivery contract.")}
	}

	findings := assessNativeFields(capability, definition.Fields, rules, inactive)
	if definition.Transport != native.MCPStdio {
		findings = append(findings, assessServerURL(capability, definition.Fields["url"])...)
	}

	return findings
}

func assessLSPDefinition(capability model.Capability, definition native.LSP) []model.Finding {
	rules := map[string]nativeFieldRule{
		"command": {valid: nonblankJSONString, required: true}, "extensionToLanguage": {valid: jsonStringMap, required: true},
		"args": {valid: jsonStringArray}, "env": {valid: jsonStringMap}, "workspaceFolder": {valid: jsonString},
		"transport": {valid: nonblankJSONString}, "initializationOptions": {valid: jsonValue}, "settings": {valid: jsonValue},
		"startupTimeout": {valid: positiveJSONInteger}, "shutdownTimeout": {valid: positiveJSONInteger},
		"restartOnCrash": {valid: jsonBoolean}, "maxRestarts": {valid: nonnegativeJSONInteger}, "diagnostics": {valid: jsonBoolean},
	}
	findings := assessNativeFields(capability, definition.Fields, rules, nil)
	if definition.Transport != native.LSPStdio && definition.Transport != native.LSPSocket {
		findings = append(findings, nativeFieldFinding(capability, "UNSUPPORTED_TRANSPORT", model.FindingUnsupported, "transport", "This LSP transport has no documented native mapping."))
	}

	if strings.Contains(definition.Command, " ") && !strings.HasPrefix(definition.Command, "/") {
		findings = append(findings, nativeFieldFinding(capability, "INVALID_NATIVE_FIELD", model.FindingMissing, "command", "Native LSP command must name a binary; put arguments in args."))
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
		return []model.Finding{nativeFieldFinding(capability, "NATIVE_URL", model.FindingUnknown, "url", "Resolve native substitutions before validating the server URL.")}
	}

	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" || parsed.Scheme != "http" && parsed.Scheme != "https" {
		return []model.Finding{nativeFieldFinding(capability, "INVALID_NATIVE_FIELD", model.FindingMissing, "url", "HTTP server requires an absolute HTTP or HTTPS URL.")}
	}

	return nil
}
