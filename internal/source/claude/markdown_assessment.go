package claude

import (
	"encoding/json"
	"maps"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/leonhfr/glean/internal/model"
	native "github.com/leonhfr/glean/internal/model/claude"
)

var markdownArgument = regexp.MustCompile(`\$ARGUMENTS(?:\[[0-9]+\])?|\$[0-9]+`)

func assessMarkdownDefinition(capability model.Capability, plugin PluginInventory) []model.Finding {
	var document string
	switch definition := capability.Definition.(type) {
	case native.Skill:
		document = definition.Document

	case native.Agent:
		document = definition.Document

	case native.Command:
		document = definition.Document

	default:
		return nil
	}

	frontmatter, body := splitFrontmatter([]byte(document))
	findings := assessMarkdownBody(capability, string(body))
	if frontmatter != nil {
		findings = append(findings, assessFrontmatter(capability, frontmatter, plugin)...)
	}

	if definition, ok := capability.Definition.(native.Command); ok && definition.Declaration != nil {
		findings = append(findings, assessCommandDeclaration(capability, definition.Declaration)...)
	}

	return findings
}

func assessFrontmatter(capability model.Capability, data []byte, plugin PluginInventory) []model.Finding {
	var fields map[string]any
	if err := yaml.Unmarshal(data, &fields); err != nil {
		return []model.Finding{capabilityFinding(capability, "NATIVE_FRONTMATTER_FALLBACK", model.FindingUnknown, "frontmatter", "Native metadata fallback may discard authored behavior; verify before delivery.")}
	}

	raw := make(map[string]json.RawMessage)
	var findings []model.Finding
	for _, field := range slices.Sorted(maps.Keys(fields)) {
		value, err := json.Marshal(fields[field])
		if err != nil {
			findings = append(findings, capabilityFinding(capability, "INVALID_NATIVE_FIELD", model.FindingMissing, field, "Frontmatter value cannot be represented as a native structured declaration."))
			continue
		}

		raw[field] = value
	}

	rules, inactive := markdownFieldRules(capability.ID.Kind)
	checked := assessNativeFields(capability, raw, rules, inactive)
	for i := range checked {
		// Markdown is not JSON; retain document locations rather than invent JSON pointers.
		checked[i].Locations = slices.Clone(capability.Provenance.Declarations)
		if checked[i].Code == "INAPPLICABLE_NATIVE_FIELD" {
			checked[i].Message = "Native plugin loading ignores this declaration; its authored behavior cannot be assumed enforced."
		}
	}

	findings = append(findings, checked...)
	if len(fields) > 0 {
		findings = append(findings, capabilityFinding(capability, "NATIVE_MARKDOWN_SUPPORT", model.FindingUnknown, "frontmatter", "Verify native invocation, restrictions, configuration and tested-version behavior."))
	}

	if hooks, exists := raw["hooks"]; exists && capability.ID.Kind != model.KindClaudeAgent {
		findings = append(findings, assessEmbeddedHooks(capability, hooks, plugin)...)
	}

	return findings
}

func markdownFieldRules(kind model.CapabilityKind) (map[string]nativeFieldRule, []string) {
	rules := map[string]nativeFieldRule{
		"name": {valid: jsonString}, "description": {valid: jsonString}, "model": {valid: jsonString},
	}
	if kind == model.KindClaudeAgent {
		for _, field := range []string{"tools", "disallowedTools", "skills"} {
			rules[field] = nativeFieldRule{valid: stringOrStringArray}
		}

		for _, field := range []string{"memory", "isolation", "color", "effort"} {
			rules[field] = nativeFieldRule{valid: jsonString}
		}

		for _, field := range []string{"background", "omitClaudeMd"} {
			rules[field] = nativeFieldRule{valid: jsonBoolean}
		}

		rules["maxTurns"] = nativeFieldRule{valid: positiveJSONInteger}
		return rules, []string{"hooks", "mcpServers", "permissionMode", "initialPrompt"}
	}

	for _, field := range []string{"argument-hint", "when_to_use", "agent", "context", "effort", "license", "compatibility"} {
		rules[field] = nativeFieldRule{valid: jsonString}
	}

	for _, field := range []string{"allowed-tools", "disallowed-tools", "arguments"} {
		rules[field] = nativeFieldRule{valid: stringOrStringArray}
	}

	for _, field := range []string{"disable-model-invocation", "user-invocable", "background"} {
		rules[field] = nativeFieldRule{valid: nativeMarkdownBoolean}
	}

	rules["hooks"] = nativeFieldRule{valid: jsonObject}
	rules["metadata"] = nativeFieldRule{valid: jsonObject}
	if kind == model.KindClaudeCommand {
		delete(rules, "name")
		return rules, []string{"name", "paths"}
	}

	return rules, nil
}

func assessEmbeddedHooks(capability model.Capability, raw json.RawMessage, plugin PluginInventory) []model.Finding {
	events, err := objectFields(raw, ErrInvalidHooks)
	if err != nil {
		// The field-shape finding already diagnoses non-object hooks.
		return nil
	}

	var findings []model.Finding
	for _, event := range slices.Sorted(maps.Keys(events)) {
		groups, err := hookGroups(events[event], hookContribution{}, event)
		if err != nil {
			findings = append(findings, capabilityFinding(capability, "INVALID_EMBEDDED_HOOK", model.FindingMissing, "hooks", "Embedded hook structure is invalid; retain the source and correct its declaration."))
			continue
		}

		// Skill frontmatter honors once; the plugin-hook assessor must not reject it here.
		for i := range groups {
			for j := range groups[i].Handlers {
				fields := maps.Clone(groups[i].Handlers[j].Fields)
				if jsonBoolean(fields["once"]) {
					delete(fields, "once")
				}

				groups[i].Handlers[j].Fields = fields
			}
		}

		checked := assessHookDefinition(capability, native.Hook{Event: event, Groups: groups}, plugin)
		for i := range checked {
			checked[i].Locations = slices.Clone(capability.Provenance.Declarations)
			checked[i].Reference = "hooks." + checked[i].Reference
		}

		findings = append(findings, checked...)
	}

	return findings
}

func assessCommandDeclaration(capability model.Capability, fields map[string]json.RawMessage) []model.Finding {
	rules := map[string]nativeFieldRule{
		"source": {valid: jsonString}, "content": {valid: jsonString}, "description": {valid: jsonString},
		"argumentHint": {valid: jsonString}, "model": {valid: jsonString}, "allowedTools": {valid: jsonStringArray},
	}
	declaration := capability
	declaration.Provenance.Declarations = nil
	for _, location := range capability.Provenance.Declarations {
		if location.Path == ManifestPath {
			declaration.Provenance.Declarations = append(declaration.Provenance.Declarations, location)
		}
	}

	return assessNativeFields(declaration, fields, rules, nil)
}

func assessMarkdownBody(capability model.Capability, body string) []model.Finding {
	var findings []model.Finding
	for _, feature := range []struct {
		reference string
		observed  bool
	}{
		{"dynamic-command", strings.Contains(body, "!`")},
		{"arguments", markdownArgument.MatchString(body)},
		{"skill-directory", strings.Contains(body, "${CLAUDE_SKILL_DIR}")},
		{"session-id", strings.Contains(body, "${CLAUDE_SESSION_ID}")},
	} {
		if feature.observed {
			findings = append(findings, capabilityFinding(capability, "NATIVE_MARKDOWN_FEATURE", model.FindingUnknown, feature.reference, "Observed native body syntax requires harness/version assessment; discovery never evaluates it."))
		}
	}

	return findings
}

func stringOrStringArray(raw json.RawMessage) bool {
	return jsonString(raw) || jsonStringArray(raw)
}

func nativeMarkdownBoolean(raw json.RawMessage) bool {
	if jsonBoolean(raw) {
		return true
	}

	var value string
	if json.Unmarshal(raw, &value) != nil {
		value = string(raw)
	}

	return slices.Contains([]string{"yes", "no", "on", "off", "1", "0", "true", "false"}, strings.ToLower(value))
}
