package claude

import (
	"encoding/json"
	"fmt"
	"math/big"
	"slices"

	"github.com/leonhfr/glean/internal/model"
	native "github.com/leonhfr/glean/internal/model/claude"
)

func assessHookDefinition(capability model.Capability, definition native.Hook, plugin PluginInventory) []model.Finding {
	var findings []model.Finding
	for _, group := range definition.Groups {
		groupCapability := capability
		groupCapability.Provenance.Declarations = slices.Clone(group.Declarations)
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(group.Document, &fields)
		findings = append(findings, assessNativeFields(groupCapability, fields, map[string]nativeFieldRule{
			"matcher": {valid: jsonString}, "hooks": {valid: jsonValue, required: true},
		}, nil)...)
		for i, handler := range group.Handlers {
			handlerCapability := groupCapability
			handlerCapability.Provenance.Declarations = slices.Clone(group.Declarations)
			if len(handlerCapability.Provenance.Declarations) > 0 {
				handlerCapability.Provenance.Declarations[0].Pointer += fmt.Sprintf("/hooks/%d", i)
			}

			findings = append(findings, assessHookHandler(handlerCapability, handler)...)
			status := model.FindingUnknown
			if !slices.Contains([]native.HookHandlerType{native.HookCommand, native.HookPrompt, native.HookAgent, native.HookHTTP, native.HookMCPTool}, handler.Type) {
				status = model.FindingUnsupported
			}

			support := nativeFieldFinding(handlerCapability, "NATIVE_HOOK_SUPPORT", status, "type", "Verify event-to-handler validity and tested-version availability.")
			support.Reference = hookTypeReference(handler.Type)
			findings = append(findings, support)
			if handler.Type == native.HookMCPTool {
				companion := assessHookServer(handlerCapability, handler, plugin)
				if len(companion.Locations) > 0 {
					companion.Locations[0].Pointer += "/server"
				}

				findings = append(findings, companion)
			}
		}
	}

	return findings
}

func assessHookHandler(capability model.Capability, handler native.HookHandler) []model.Finding {
	rules := map[string]nativeFieldRule{
		"type": {valid: nonblankJSONString, required: true}, "timeout": {valid: positiveJSONNumber},
		"statusMessage": {valid: jsonString}, "once": {valid: jsonBoolean}, "if": {valid: jsonString},
	}
	var inactive []string
	switch handler.Type {
	case native.HookCommand:
		rules["command"] = nativeFieldRule{valid: nonblankJSONString, required: true}
		rules["args"] = nativeFieldRule{valid: jsonStringArray}
		rules["async"] = nativeFieldRule{valid: jsonBoolean}
		rules["asyncRewake"] = nativeFieldRule{valid: jsonBoolean}
		rules["shell"] = nativeFieldRule{valid: hookShell}
		inactive = []string{"prompt", "model", "url", "headers", "allowedEnvVars", "server", "tool", "input"}

	case native.HookPrompt, native.HookAgent:
		rules["prompt"] = nativeFieldRule{valid: nonblankJSONString, required: true}
		rules["model"] = nativeFieldRule{valid: nonblankJSONString}
		inactive = []string{"command", "args", "async", "asyncRewake", "shell", "url", "headers", "allowedEnvVars", "server", "tool", "input"}

	case native.HookHTTP:
		rules["url"] = nativeFieldRule{valid: nonblankJSONString, required: true}
		rules["headers"] = nativeFieldRule{valid: jsonStringMap}
		rules["allowedEnvVars"] = nativeFieldRule{valid: jsonStringArray}
		inactive = []string{"command", "args", "async", "asyncRewake", "shell", "prompt", "model", "server", "tool", "input"}

	case native.HookMCPTool:
		rules["server"] = nativeFieldRule{valid: nonblankJSONString, required: true}
		rules["tool"] = nativeFieldRule{valid: nonblankJSONString, required: true}
		rules["input"] = nativeFieldRule{valid: jsonObject}
		inactive = []string{"command", "args", "async", "asyncRewake", "shell", "prompt", "model", "url", "headers", "allowedEnvVars"}

	default:
		// The existing support finding rejects unknown forms without guessing their fields.
		return nil
	}

	findings := assessNativeFields(capability, handler.Fields, rules, inactive)
	if string(handler.Fields["once"]) == "true" {
		findings = append(findings, nativeFieldFinding(capability, "INAPPLICABLE_NATIVE_FIELD", model.FindingUnsupported, "once", "Native once behavior is honored in skill frontmatter, not plugin hook declarations."))
	}

	if handler.Type == native.HookCommand {
		_, execForm := handler.Fields["args"]
		var command string
		_ = json.Unmarshal(handler.Fields["command"], &command)
		if !execForm && userConfigReference.MatchString(command) {
			findings = append(findings, nativeFieldFinding(capability, "UNSUPPORTED_NATIVE_REFERENCE", model.FindingUnsupported, "command", "Native user-config substitution is rejected in shell-form hook commands; use exec-form args or native option environment variables."))
		}
	}

	return findings
}

func hookShell(raw json.RawMessage) bool {
	var value string
	return json.Unmarshal(raw, &value) == nil && (value == "bash" || value == "powershell")
}

func positiveJSONNumber(raw json.RawMessage) bool {
	value, ok := new(big.Rat).SetString(string(raw))
	return ok && json.Valid(raw) && value.Sign() > 0
}
