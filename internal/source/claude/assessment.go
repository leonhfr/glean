package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/leonhfr/glean/internal/model"
	native "github.com/leonhfr/glean/internal/model/claude"
)

var userConfigReference = regexp.MustCompile(`\$\{user_config\.([A-Za-z_][A-Za-z0-9_]*)\}`)

func assessPlugin(source fs.FS, plugin PluginInventory) ([]model.Finding, error) {
	findings, err := assessComponents(source, plugin.Manifest)
	if err != nil {
		return nil, err
	}

	hookFindings, err := assessHookFileComponents(source, plugin.Manifest)
	if err != nil {
		return nil, err
	}

	findings = append(findings, hookFindings...)
	config, err := pluginUserConfig(plugin.Manifest)
	if err != nil {
		return nil, err
	}

	findings = append(findings, assessDependencies(plugin.Manifest)...)
	if _, exists := plugin.Manifest.Fields["userConfig"]; exists {
		findings = append(findings, model.Finding{Code: "NATIVE_USER_CONFIG", Status: model.FindingUnknown, Message: "Native schema, configured values and derived credential identity require verification.", Locations: []model.Location{{Path: ManifestPath, Pointer: "/userConfig"}}})
	}

	for _, capability := range plugin.Inventory.Capabilities {
		findings = append(findings, assessCapability(capability, plugin, config)...)
		local, err := assessLocalExecutable(source, capability)
		if err != nil {
			return nil, err
		}

		findings = append(findings, local...)
	}

	return findings, nil
}

func assessComponents(source fs.FS, manifest Manifest) ([]model.Finding, error) {
	metadata := map[string]bool{"$schema": true, "name": true, "displayName": true, "version": true, "description": true, "author": true, "homepage": true, "repository": true, "license": true, "keywords": true, "metadata": true, "icon": true, "documentationUrl": true, "supportUrl": true, "privacyPolicyUrl": true, "termsOfServiceUrl": true, "defaultEnabled": true, "dependencies": true, "userConfig": true, "skills": true, "agents": true, "commands": true, "hooks": true, "mcpServers": true, "lspServers": true}
	deferred := map[string]bool{"outputStyles": true, "workflows": true, "settings": true, "channels": true, "types": true, "monitors": true}
	var findings []model.Finding
	for _, field := range slices.Sorted(maps.Keys(manifest.Fields)) {
		if metadata[field] {
			continue
		}

		status, code := model.FindingUnknown, "UNKNOWN_NATIVE_FIELD"
		if deferred[field] {
			status, code = model.FindingUnsupported, "UNSUPPORTED_COMPONENT"
		}

		if field == "experimental" {
			fields, err := objectFields(manifest.Fields[field], ErrInvalidPlugin)
			if err != nil {
				return nil, err
			}

			for _, key := range slices.Sorted(maps.Keys(fields)) {
				status, code := model.FindingUnknown, "UNKNOWN_NATIVE_FIELD"
				if slices.Contains([]string{"themes", "monitors", "evals"}, key) {
					status, code = model.FindingUnsupported, "UNSUPPORTED_COMPONENT"
				}

				findings = append(findings, model.Finding{Code: code, Status: status, Reference: "experimental." + key, Message: "This native component is outside the MVP delivery contract.", Locations: []model.Location{{Path: ManifestPath, Pointer: "/experimental/" + pointerSegment(key)}}})
			}

			continue
		}

		findings = append(findings, model.Finding{Code: code, Status: status, Reference: field, Message: "Native field needs explicit assessment before derived delivery.", Locations: []model.Location{{Path: ManifestPath, Pointer: "/" + pointerSegment(field)}}})
	}

	defaults, err := assessDefaultComponents(source, manifest)
	if err != nil {
		return nil, err
	}

	return append(findings, defaults...), nil
}

func assessDefaultComponents(source fs.FS, manifest Manifest) ([]model.Finding, error) {
	var findings []model.Finding

	for _, component := range []struct {
		path, field string
		directory   bool
	}{
		{"output-styles", "outputStyles", true}, {"workflows", "workflows", true}, {"themes", "experimental.themes", true}, {"monitors/monitors.json", "experimental.monitors", false}, {"bin", "", true}, {"settings.json", "settings", false},
	} {
		if componentDeclared(manifest, component.field) {
			continue
		}

		info, err := fs.Stat(source, component.path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}

		if err != nil {
			return nil, fmt.Errorf("inspect native component %s: %w", component.path, err)
		}

		if component.directory && info.IsDir() {
			entries, err := fs.ReadDir(source, component.path)
			if err != nil {
				return nil, err
			}

			if len(entries) == 0 {
				continue
			}
		}

		findings = append(findings, model.Finding{Code: "UNSUPPORTED_COMPONENT", Status: model.FindingUnsupported, Reference: component.path, Message: "Native activation at this path is outside the MVP delivery contract.", Locations: []model.Location{{Path: component.path}}})
	}

	return findings, nil
}

func pluginUserConfig(manifest Manifest) (map[string]json.RawMessage, error) {
	raw, exists := manifest.Fields["userConfig"]
	if !exists {
		return map[string]json.RawMessage{}, nil
	}

	fields, err := objectFields(raw, ErrInvalidPlugin)
	if err != nil {
		return nil, fmt.Errorf("userConfig: %w", err)
	}

	return fields, nil
}

func assessDependencies(manifest Manifest) []model.Finding {
	raw, exists := manifest.Fields["dependencies"]
	if !exists {
		return nil
	}

	var entries []json.RawMessage
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &entries) != nil {
		return []model.Finding{{Code: "INVALID_DEPENDENCY", Status: model.FindingMissing, Message: "Native dependencies require an array of declarations.", Locations: []model.Location{{Path: ManifestPath, Pointer: "/dependencies"}}}}
	}

	var findings []model.Finding
	for i, entry := range entries {
		name, status := "", model.FindingUnknown
		if len(entry) > 0 && entry[0] == '"' {
			_ = json.Unmarshal(entry, &name)
		} else {
			fields, err := objectFields(entry, ErrInvalidPlugin)
			if err == nil {
				_ = json.Unmarshal(fields["name"], &name)
			}
		}

		dependency, marketplace, qualified := strings.Cut(name, "@")
		if !validPluginName(dependency) || qualified && !validPluginName(marketplace) {
			status = model.FindingMissing
			name = ""
		}

		findings = append(findings, model.Finding{Code: "PLUGIN_DEPENDENCY", Status: status, Reference: name, Message: "Resolve the declared companion explicitly; inventory does not prove installation or compatibility.", Locations: []model.Location{{Path: ManifestPath, Pointer: fmt.Sprintf("/dependencies/%d", i)}}})
	}

	return findings
}

func assessCapability(capability model.Capability, plugin PluginInventory, config map[string]json.RawMessage) []model.Finding {
	texts := capabilityTexts(capability)
	var findings []model.Finding
	seen := make(map[string]bool)
	for _, text := range texts {
		for _, match := range userConfigReference.FindAllStringSubmatch(text, -1) {
			key := match[1]
			if seen[key] {
				continue
			}

			seen[key] = true
			status := model.FindingUnknown
			if _, exists := config[key]; !exists {
				status = model.FindingMissing
			}

			findings = append(findings, capabilityFinding(capability, "USER_CONFIG_REFERENCE", status, key, "Verify the declared native option and its derived-plugin value resolution."))
		}

		if strings.Contains(text, "CLAUDE_PLUGIN_DATA") && !seen["$data"] {
			seen["$data"] = true
			findings = append(findings, capabilityFinding(capability, "PERSISTENT_DATA", model.FindingUnknown, "CLAUDE_PLUGIN_DATA", "Verify native persistent-data identity; do not copy or relocate runtime data."))
		}
	}

	return append(findings, assessNativeCapability(capability, plugin)...)
}

func assessNativeCapability(capability model.Capability, plugin PluginInventory) []model.Finding {
	findings := assessServerDefinition(capability)
	findings = append(findings, assessMarkdownDefinition(capability, plugin)...)
	switch definition := capability.Definition.(type) {
	case native.MCP:
		if definition.Transport == native.MCPStdio {
			findings = append(findings, capabilityFinding(capability, "EXECUTABLE", model.FindingUnknown, "command", "Executable availability is unverified; do not execute it during discovery."))
		}

		findings = append(findings, capabilityFinding(capability, "NATIVE_SERVER_SUPPORT", model.FindingUnknown, "transport", "Native field validity, authentication, runtime requirements and version support require assessment."))

	case native.LSP:
		findings = append(findings, capabilityFinding(capability, "EXECUTABLE", model.FindingUnknown, "command", "Executable availability is unverified; do not execute it during discovery."))
		findings = append(findings, capabilityFinding(capability, "NATIVE_SERVER_SUPPORT", model.FindingUnknown, "transport", "Native option validity, language conflicts and version support require assessment."))

	case native.Hook:
		findings = append(findings, assessHookDefinition(capability, definition, plugin)...)
	}

	return findings
}

func assessHookServer(capability model.Capability, handler native.HookHandler, plugin PluginInventory) model.Finding {
	var server string
	_ = json.Unmarshal(handler.Fields["server"], &server)
	status := model.FindingUnknown
	prefix := "plugin:" + plugin.Manifest.Name + ":"
	reference := "external server"
	if strings.TrimSpace(server) == "" {
		status = model.FindingMissing
		reference = "server"
	}

	if strings.HasPrefix(server, prefix) {
		status = model.FindingMissing
		name := strings.TrimPrefix(server, prefix)
		reference = name
		for _, candidate := range plugin.Inventory.Capabilities {
			if candidate.ID.Kind == model.KindClaudeMCP && candidate.ID.Name == name {
				status = model.FindingVerified
				break
			}
		}
	}

	return capabilityFinding(capability, "MCP_COMPANION", status, reference, "A local server declaration does not prove selection, tool availability or live connectivity.")
}

func capabilityFinding(capability model.Capability, code string, status model.FindingStatus, reference, message string) model.Finding {
	return model.Finding{Code: code, Status: status, Capability: capability.ID, Reference: reference, Message: message, Locations: slices.Clone(capability.Provenance.Declarations)}
}

func capabilityTexts(capability model.Capability) []string {
	switch definition := capability.Definition.(type) {
	case native.Skill:
		return []string{definition.Document}

	case native.Agent:
		return []string{definition.Document}

	case native.Command:
		return append([]string{definition.Document}, jsonTexts(definition.Declaration)...)

	case native.MCP:
		return jsonTexts(definition.Fields)

	case native.LSP:
		return jsonTexts(definition.Fields)

	case native.Hook:
		var texts []string
		for _, group := range definition.Groups {
			for _, handler := range group.Handlers {
				texts = append(texts, jsonTexts(handler.Fields)...)
			}
		}

		return texts

	default:
		return nil
	}
}

func jsonTexts(fields map[string]json.RawMessage) []string {
	var texts []string
	for _, key := range slices.Sorted(maps.Keys(fields)) {
		var value any
		if json.Unmarshal(fields[key], &value) == nil {
			texts = append(texts, valueTexts(value)...)
		}
	}

	return texts
}

func valueTexts(value any) []string {
	switch typed := value.(type) {
	case string:
		return []string{typed}

	case []any:
		var texts []string
		for _, item := range typed {
			texts = append(texts, valueTexts(item)...)
		}

		return texts

	case map[string]any:
		var texts []string
		for _, key := range slices.Sorted(maps.Keys(typed)) {
			texts = append(texts, valueTexts(typed[key])...)
		}

		return texts

	default:
		return nil
	}
}

func pointerSegment(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}

func assessHookFileComponents(source fs.FS, manifest Manifest) ([]model.Finding, error) {
	contributions, err := hookContributions(source, manifest)
	if err != nil {
		return nil, err
	}

	seen := make(map[string]bool)
	var findings []model.Finding
	for _, contribution := range contributions {
		for _, field := range slices.Sorted(maps.Keys(contribution.fields)) {
			if field == "hooks" || field == "description" || field == "$schema" {
				continue
			}

			location := model.Location{Path: contribution.location.Path, Pointer: "/" + pointerSegment(field)}
			key := location.Path + location.Pointer
			if seen[key] {
				continue
			}

			seen[key] = true
			status, code := model.FindingUnknown, "UNKNOWN_NATIVE_FIELD"
			if field == "modules" {
				status, code = model.FindingUnsupported, "UNSUPPORTED_COMPONENT"
			}

			findings = append(findings, model.Finding{Code: code, Status: status, Reference: field, Message: "Hook file component requires explicit assessment before derived delivery.", Locations: []model.Location{location}})
		}
	}

	return findings, nil
}

func assessLocalExecutable(source fs.FS, capability model.Capability) ([]model.Finding, error) {
	var command string
	switch definition := capability.Definition.(type) {
	case native.LSP:
		command = definition.Command

	case native.MCP:
		if definition.Transport != native.MCPStdio {
			return nil, nil
		}

		_ = json.Unmarshal(definition.Fields["command"], &command)

	default:
		return nil, nil
	}

	const prefix = "${CLAUDE_PLUGIN_ROOT}/"
	if !strings.HasPrefix(command, prefix) {
		return nil, nil
	}

	relative := strings.TrimPrefix(command, prefix)
	if strings.Contains(relative, "$") {
		return nil, nil
	}

	filename, err := componentPath("./"+relative, "local executable", false, ErrInvalidPlugin)
	if err != nil {
		//nolint:nilerr // Invalid native references are findings, not inspection failures.
		return []model.Finding{capabilityFinding(capability, "LOCAL_EXECUTABLE", model.FindingMissing, "command", "Bundled executable path escapes or is outside the source payload.")}, nil
	}

	status := model.FindingVerified
	info, err := fs.Stat(source, filename)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		status = model.FindingMissing

	case err != nil:
		return nil, fmt.Errorf("inspect bundled executable: %w", err)

	case !info.Mode().IsRegular():
		status = model.FindingMissing
	}

	return []model.Finding{capabilityFinding(capability, "LOCAL_EXECUTABLE", status, filename, "Static path presence does not prove executable permissions, dependencies or runtime usability.")}, nil
}

func hookTypeReference(kind native.HookHandlerType) string {
	if slices.Contains([]native.HookHandlerType{native.HookCommand, native.HookPrompt, native.HookAgent, native.HookHTTP, native.HookMCPTool}, kind) {
		return string(kind)
	}

	return "type"
}

func componentDeclared(manifest Manifest, field string) bool {
	if _, exists := manifest.Fields[field]; exists {
		return true
	}

	if field == "experimental.monitors" {
		if _, exists := manifest.Fields["monitors"]; exists {
			return true
		}
	}

	if !strings.HasPrefix(field, "experimental.") {
		return false
	}

	var fields map[string]json.RawMessage
	if json.Unmarshal(manifest.Fields["experimental"], &fields) != nil {
		return false
	}

	_, exists := fields[strings.TrimPrefix(field, "experimental.")]
	return exists
}
