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

// ErrInvalidLSP identifies invalid LSP declarations and source structure.
var ErrInvalidLSP = errors.New("invalid Claude LSP source")

type lspContribution struct {
	servers     map[string]json.RawMessage
	location    model.Location
	declaration model.Location
	entrypoint  string
}

// ReadLSP inventories native LSP structure under the supplied package ID.
// Runtime availability and server requirements need separate assessment.
func ReadLSP(sys system.RootOpener, directory string, packageID model.PackageID) (model.Inventory, error) {
	plugin, err := readPluginInventory(sys, directory, packageID, ErrInvalidLSP, readLSP, nil)
	return plugin.Inventory, err
}

func readLSP(source fs.FS, manifest Manifest, packageID model.PackageID) ([]model.Capability, error) {
	contributions, err := lspContributions(source, manifest)
	if err != nil {
		return nil, err
	}

	servers := make(map[string]model.Capability)
	for _, contribution := range contributions {
		for _, name := range slices.Sorted(maps.Keys(contribution.servers)) {
			capability, err := lspCapability(name, contribution.servers[name], contribution, manifest, packageID)
			if err != nil {
				return nil, err
			}

			if existing, exists := servers[name]; exists {
				capability, err = combineLSP(existing, capability)
				if err != nil {
					return nil, err
				}
			}

			servers[name] = capability
		}
	}

	var capabilities []model.Capability
	for _, name := range slices.Sorted(maps.Keys(servers)) {
		capabilities = append(capabilities, servers[name])
	}

	return capabilities, nil
}

func lspContributions(source fs.FS, manifest Manifest) ([]lspContribution, error) {
	var contributions []lspContribution
	if _, err := fs.Stat(source, ".lsp.json"); err == nil {
		contribution, err := readLSPFile(source, ".lsp.json", model.Location{})
		if err != nil {
			return nil, err
		}

		contributions = append(contributions, contribution)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("inspect default LSP: %w", err)
	}

	raw, declared := manifest.Fields["lspServers"]
	if !declared {
		return contributions, nil
	}

	values := []json.RawMessage{raw}
	array := len(raw) > 0 && raw[0] == '['
	if array {
		if err := json.Unmarshal(raw, &values); err != nil {
			return nil, fmt.Errorf("%w: invalid lspServers array", ErrInvalidLSP)
		}
	}

	for i, value := range values {
		pointer := "/lspServers"
		if array {
			pointer += fmt.Sprintf("/%d", i)
		}

		contribution, err := declaredLSPContribution(source, value, model.Location{Path: ManifestPath, Pointer: pointer})
		if err != nil {
			return nil, err
		}

		contributions = append(contributions, contribution)
	}

	return contributions, nil
}

func declaredLSPContribution(source fs.FS, raw json.RawMessage, location model.Location) (lspContribution, error) {
	if len(raw) > 0 && raw[0] == '{' {
		servers, err := objectFields(raw, ErrInvalidLSP)
		if err != nil {
			return lspContribution{}, err
		}

		return lspContribution{servers: servers, location: location}, nil
	}

	var value string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &value) != nil {
		return lspContribution{}, fmt.Errorf("%w: %s requires a JSON path or inline server map", ErrInvalidLSP, location.Pointer)
	}

	filename, err := componentPath(value, "lspServers", false, ErrInvalidLSP)
	if err != nil {
		return lspContribution{}, err
	}

	return readLSPFile(source, filename, location)
}

func readLSPFile(source fs.FS, filename string, declaration model.Location) (lspContribution, error) {
	if !strings.HasSuffix(filename, ".json") {
		return lspContribution{}, fmt.Errorf("%w: LSP requires JSON files", ErrInvalidLSP)
	}

	info, err := fs.Stat(source, filename)
	if err != nil {
		return lspContribution{}, fmt.Errorf("%w: inspect LSP %s: %w", ErrInvalidLSP, filename, err)
	}

	if !info.Mode().IsRegular() {
		return lspContribution{}, fmt.Errorf("%w: %s must be a regular LSP file", ErrInvalidLSP, filename)
	}

	data, err := fs.ReadFile(source, filename)
	if err != nil {
		return lspContribution{}, fmt.Errorf("read LSP %s: %w", filename, err)
	}

	servers, err := objectFields(data, ErrInvalidLSP)
	if err != nil {
		return lspContribution{}, fmt.Errorf("%s: %w", filename, err)
	}

	location := model.Location{Path: filename}

	return lspContribution{servers: servers, location: location, declaration: declaration, entrypoint: filename}, nil
}

func lspCapability(name string, raw json.RawMessage, contribution lspContribution, manifest Manifest, packageID model.PackageID) (model.Capability, error) {
	if strings.TrimSpace(name) == "" {
		return model.Capability{}, fmt.Errorf("%w: blank LSP server name", ErrInvalidLSP)
	}

	if err := validateJSONObjects(raw, ErrInvalidLSP); err != nil {
		return model.Capability{}, fmt.Errorf("server %q: %w", name, err)
	}

	fields, err := objectFields(raw, ErrInvalidLSP)
	if err != nil {
		return model.Capability{}, fmt.Errorf("server %q in %s: %w", name, contribution.location.Path, err)
	}

	definition, err := lspDefinition(fields)
	if err != nil {
		return model.Capability{}, fmt.Errorf("server %q in %s: %w", name, contribution.location.Path, err)
	}

	location := contribution.location
	location.Pointer += "/" + strings.ReplaceAll(strings.ReplaceAll(name, "~", "~0"), "/", "~1")
	declarations := []model.Location{location}
	if contribution.declaration.Path != "" {
		declarations = append(declarations, contribution.declaration)
	}

	var entrypoints []string
	if contribution.entrypoint != "" {
		entrypoints = append(entrypoints, contribution.entrypoint)
	}

	return model.Capability{
		ID: model.CapabilityID{Kind: model.KindClaudeLSP, Name: name}, PackageID: packageID, Name: name,
		Definition: definition,
		Provenance: model.Provenance{Root: manifest.Root, Declarations: declarations}, Payload: model.Payload{Entrypoints: entrypoints},
	}, nil
}

func combineLSP(existing, candidate model.Capability) (model.Capability, error) {
	previous, previousOK := existing.Definition.(claudemodel.LSP)
	next, nextOK := candidate.Definition.(claudemodel.LSP)
	if !previousOK || !nextOK {
		return model.Capability{}, fmt.Errorf("%w: missing LSP definition", ErrInvalidLSP)
	}

	equivalent, err := equivalentJSONFields(previous.Fields, next.Fields, ErrInvalidLSP)
	if err != nil {
		return model.Capability{}, err
	}

	if !equivalent {
		return model.Capability{}, fmt.Errorf("%w: conflicting server %q in %s and %s", ErrInvalidLSP, existing.ID.Name, existing.Provenance.Declarations[0].Path, candidate.Provenance.Declarations[0].Path)
	}

	for _, location := range candidate.Provenance.Declarations {
		if !slices.Contains(existing.Provenance.Declarations, location) {
			existing.Provenance.Declarations = append(existing.Provenance.Declarations, location)
		}
	}

	for _, entrypoint := range candidate.Payload.Entrypoints {
		if !slices.Contains(existing.Payload.Entrypoints, entrypoint) {
			existing.Payload.Entrypoints = append(existing.Payload.Entrypoints, entrypoint)
		}
	}

	return existing, nil
}

func lspDefinition(fields map[string]json.RawMessage) (claudemodel.LSP, error) {
	command, err := lspString(fields["command"], "command")
	if err != nil {
		return claudemodel.LSP{}, err
	}

	mappings, err := objectFields(fields["extensionToLanguage"], ErrInvalidLSP)
	if err != nil {
		return claudemodel.LSP{}, fmt.Errorf("extensionToLanguage: %w", err)
	}

	if len(mappings) == 0 {
		return claudemodel.LSP{}, fmt.Errorf("%w: require at least one language mapping", ErrInvalidLSP)
	}

	languages := make(map[string]string)
	for _, extension := range slices.Sorted(maps.Keys(mappings)) {
		if !strings.HasPrefix(extension, ".") {
			return claudemodel.LSP{}, fmt.Errorf("%w: language extension %q must start with a dot", ErrInvalidLSP, extension)
		}

		language, err := lspString(mappings[extension], "language ID")
		if err != nil {
			return claudemodel.LSP{}, err
		}

		languages[extension] = language
	}

	transport := claudemodel.LSPStdio
	if raw, exists := fields["transport"]; exists {
		value, err := lspString(raw, "transport")
		if err != nil {
			return claudemodel.LSP{}, err
		}

		transport = claudemodel.LSPTransport(value)
	}

	return claudemodel.LSP{Command: command, ExtensionToLanguage: languages, Transport: transport, Fields: fields}, nil
}

func lspString(raw json.RawMessage, field string) (string, error) {
	var value string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &value) != nil || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%w: %s requires a nonblank string", ErrInvalidLSP, field)
	}

	return value, nil
}
