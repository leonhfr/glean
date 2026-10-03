package claude

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"github.com/leonhfr/glean/internal/model"
	claudemodel "github.com/leonhfr/glean/internal/model/claude"
	"github.com/leonhfr/glean/internal/system"
)

// ErrInvalidHooks identifies invalid hook declarations and source structure.
var ErrInvalidHooks = errors.New("invalid Claude hook source")

type hookContribution struct {
	events      json.RawMessage
	fields      map[string]json.RawMessage
	location    model.Location
	declaration model.Location
}

// ReadHooks inventories native hook structure under the supplied package ID.
// Runtime availability and handler requirements need separate assessment.
func ReadHooks(sys system.RootOpener, directory string, packageID model.PackageID) (model.Inventory, error) {
	plugin, err := readPluginInventory(sys, directory, packageID, ErrInvalidHooks, readHooks, nil)
	return plugin.Inventory, err
}

func readHooks(source fs.FS, manifest Manifest, packageID model.PackageID) ([]model.Capability, error) {
	contributions, err := hookContributions(source, manifest)
	if err != nil {
		return nil, err
	}

	events := make(map[string]model.Capability)
	for _, contribution := range contributions {
		fields, err := objectFields(contribution.events, ErrInvalidHooks)
		if err != nil {
			return nil, fmt.Errorf("%s%s: %w", contribution.location.Path, contribution.location.Pointer, err)
		}

		for _, event := range slices.Sorted(maps.Keys(fields)) {
			if err := includeHookEvent(events, event, fields[event], contribution, manifest, packageID); err != nil {
				return nil, err
			}
		}
	}

	var capabilities []model.Capability
	for _, event := range slices.Sorted(maps.Keys(events)) {
		capabilities = append(capabilities, events[event])
	}

	return capabilities, nil
}

func hookContributions(source fs.FS, manifest Manifest) ([]hookContribution, error) {
	var contributions []hookContribution
	const defaultPath = "hooks/hooks.json"
	if _, err := fs.Stat(source, defaultPath); err == nil {
		contribution, err := readHookFile(source, defaultPath, model.Location{})
		if err != nil {
			return nil, err
		}

		contributions = append(contributions, contribution)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("inspect default hooks: %w", err)
	}

	raw, declared := manifest.Fields["hooks"]
	if !declared {
		return contributions, nil
	}

	var values []json.RawMessage
	array := len(raw) > 0 && raw[0] == '['
	if array {
		if err := json.Unmarshal(raw, &values); err != nil {
			return nil, fmt.Errorf("%w: invalid hooks array", ErrInvalidHooks)
		}
	} else {
		values = []json.RawMessage{raw}
	}

	for i, value := range values {
		pointer := "/hooks"
		if array {
			pointer += fmt.Sprintf("/%d", i)
		}

		contribution, err := declaredHookContribution(source, value, model.Location{Path: ManifestPath, Pointer: pointer})
		if err != nil {
			return nil, err
		}

		contributions = append(contributions, contribution)
	}

	return contributions, nil
}

func declaredHookContribution(source fs.FS, raw json.RawMessage, location model.Location) (hookContribution, error) {
	if len(raw) > 0 && raw[0] == '{' {
		return hookContribution{events: raw, location: location}, nil
	}

	var value string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &value) != nil {
		return hookContribution{}, fmt.Errorf("%w: %s requires a JSON path or inline event map", ErrInvalidHooks, location.Pointer)
	}

	filename, err := componentPath(value, "hooks", false, ErrInvalidHooks)
	if err != nil {
		return hookContribution{}, err
	}

	return readHookFile(source, filename, location)
}

func readHookFile(source fs.FS, filename string, declaration model.Location) (hookContribution, error) {
	if !strings.HasSuffix(filename, ".json") {
		return hookContribution{}, fmt.Errorf("%w: hooks require JSON files", ErrInvalidHooks)
	}

	info, err := fs.Stat(source, filename)
	if err != nil {
		return hookContribution{}, fmt.Errorf("%w: inspect hooks %s: %w", ErrInvalidHooks, filename, err)
	}

	if !info.Mode().IsRegular() {
		return hookContribution{}, fmt.Errorf("%w: %s must be a regular hook file", ErrInvalidHooks, filename)
	}

	data, err := fs.ReadFile(source, filename)
	if err != nil {
		return hookContribution{}, fmt.Errorf("read hooks %s: %w", filename, err)
	}

	fields, err := objectFields(data, ErrInvalidHooks)
	if err != nil {
		return hookContribution{}, fmt.Errorf("%s: %w", filename, err)
	}

	events, exists := fields["hooks"]
	if !exists {
		return hookContribution{}, fmt.Errorf("%w: %s requires a hooks wrapper", ErrInvalidHooks, filename)
	}

	// Extra top-level components are assessed by complete plugin inventory later.
	return hookContribution{events: events, fields: fields, location: model.Location{Path: filename, Pointer: "/hooks"}, declaration: declaration}, nil
}

func includeHookEvent(events map[string]model.Capability, event string, raw json.RawMessage, contribution hookContribution, manifest Manifest, packageID model.PackageID) error {
	if strings.TrimSpace(event) == "" {
		return fmt.Errorf("%w: blank hook event", ErrInvalidHooks)
	}

	groups, err := hookGroups(raw, contribution, event)
	if err != nil {
		return fmt.Errorf("event %s in %s: %w", event, contribution.location.Path, err)
	}

	if len(groups) == 0 {
		return nil
	}

	capability, exists := events[event]
	definition := claudemodel.Hook{Event: event}
	if exists {
		var ok bool
		definition, ok = capability.Definition.(claudemodel.Hook)
		if !ok {
			return fmt.Errorf("%w: missing hook definition", ErrInvalidHooks)
		}
	} else {
		capability = model.Capability{
			ID: model.CapabilityID{Kind: model.KindClaudeHook, Name: event}, PackageID: packageID,
			Name: event, Provenance: model.Provenance{Root: manifest.Root},
		}
	}

	for _, group := range groups {
		definition.Groups = includeHookGroup(definition.Groups, group)
	}

	capability.Definition = definition
	for _, group := range groups {
		for _, location := range group.Declarations {
			if !slices.Contains(capability.Provenance.Declarations, location) {
				capability.Provenance.Declarations = append(capability.Provenance.Declarations, location)
			}
		}
	}

	if contribution.location.Path != ManifestPath && !slices.Contains(capability.Payload.Entrypoints, contribution.location.Path) {
		capability.Payload.Entrypoints = append(capability.Payload.Entrypoints, contribution.location.Path)
	}

	events[event] = capability
	return nil
}

func hookGroups(raw json.RawMessage, contribution hookContribution, event string) ([]claudemodel.HookGroup, error) {
	values, err := hookArray(raw, "matcher groups")
	if err != nil {
		return nil, err
	}

	var groups []claudemodel.HookGroup
	for i, value := range values {
		fields, err := objectFields(value, ErrInvalidHooks)
		if err != nil {
			return nil, err
		}

		if matcher, exists := fields["matcher"]; exists {
			var text string
			if len(matcher) == 0 || matcher[0] != '"' || json.Unmarshal(matcher, &text) != nil {
				return nil, fmt.Errorf("%w: matcher requires a string", ErrInvalidHooks)
			}
		}

		handlers, err := hookHandlers(fields["hooks"])
		if err != nil {
			return nil, err
		}

		escaped := strings.ReplaceAll(strings.ReplaceAll(event, "~", "~0"), "/", "~1")
		location := contribution.location
		location.Pointer += "/" + escaped + fmt.Sprintf("/%d", i)
		declarations := []model.Location{location}
		if contribution.declaration.Path != "" {
			declarations = append(declarations, contribution.declaration)
		}

		groups = append(groups, claudemodel.HookGroup{Document: value, Handlers: handlers, Declarations: declarations})
	}

	return groups, nil
}

func hookHandlers(raw json.RawMessage) ([]claudemodel.HookHandler, error) {
	values, err := hookArray(raw, "handlers")
	if err != nil {
		return nil, err
	}

	var handlers []claudemodel.HookHandler
	for _, value := range values {
		fields, err := objectFields(value, ErrInvalidHooks)
		if err != nil {
			return nil, err
		}

		var kind string
		rawType := fields["type"]
		if len(rawType) == 0 || rawType[0] != '"' || json.Unmarshal(rawType, &kind) != nil || strings.TrimSpace(kind) == "" {
			return nil, fmt.Errorf("%w: handler requires a nonblank type", ErrInvalidHooks)
		}

		handlers = append(handlers, claudemodel.HookHandler{Type: claudemodel.HookHandlerType(kind), Fields: fields})
	}

	return handlers, nil
}

func hookArray(raw json.RawMessage, context string) ([]json.RawMessage, error) {
	var values []json.RawMessage
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &values) != nil {
		return nil, fmt.Errorf("%w: %s require an array", ErrInvalidHooks, context)
	}

	return values, nil
}

func includeHookGroup(groups []claudemodel.HookGroup, candidate claudemodel.HookGroup) []claudemodel.HookGroup {
	for i := range groups {
		if groups[i].Declarations[0] != candidate.Declarations[0] {
			continue
		}

		for _, location := range candidate.Declarations {
			if !slices.Contains(groups[i].Declarations, location) {
				groups[i].Declarations = append(groups[i].Declarations, location)
			}
		}

		return groups
	}

	return append(groups, candidate)
}
