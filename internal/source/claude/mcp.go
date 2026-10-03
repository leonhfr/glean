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

// ErrInvalidMCP identifies invalid MCP declarations and source structure.
var ErrInvalidMCP = errors.New("invalid Claude MCP source")

// ErrUnsupportedMCPBundle identifies deferred packaged-server acquisition.
var ErrUnsupportedMCPBundle = errors.New("MCP bundles are unsupported")

type mcpContribution struct {
	servers     map[string]json.RawMessage
	location    model.Location
	declaration model.Location
	entrypoint  string
}

// ReadMCP inventories native MCP structure under the supplied package ID.
// Runtime availability and server requirements need separate assessment.
func ReadMCP(sys system.RootOpener, directory string, packageID model.PackageID) (model.Inventory, error) {
	plugin, err := readPluginInventory(sys, directory, packageID, ErrInvalidMCP, readMCP)
	return plugin.Inventory, err
}

func readMCP(source fs.FS, manifest Manifest, packageID model.PackageID) ([]model.Capability, error) {
	contributions, err := mcpContributions(source, manifest)
	if err != nil {
		return nil, err
	}

	servers := make(map[string]model.Capability)
	for _, contribution := range contributions {
		for _, name := range slices.Sorted(maps.Keys(contribution.servers)) {
			capability, err := mcpCapability(name, contribution.servers[name], contribution, manifest, packageID)
			if err != nil {
				return nil, err
			}

			if existing, exists := servers[name]; exists {
				capability, err = combineMCP(existing, capability)
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

func mcpContributions(source fs.FS, manifest Manifest) ([]mcpContribution, error) {
	var contributions []mcpContribution
	if _, err := fs.Stat(source, ".mcp.json"); err == nil {
		contribution, err := readMCPFile(source, ".mcp.json", model.Location{})
		if err != nil {
			return nil, err
		}

		contributions = append(contributions, contribution)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("inspect default MCP: %w", err)
	}

	raw, declared := manifest.Fields["mcpServers"]
	if !declared {
		return contributions, nil
	}

	values := []json.RawMessage{raw}
	array := len(raw) > 0 && raw[0] == '['
	if array {
		if err := json.Unmarshal(raw, &values); err != nil {
			return nil, fmt.Errorf("%w: invalid mcpServers array", ErrInvalidMCP)
		}
	}

	for i, value := range values {
		pointer := "/mcpServers"
		if array {
			pointer += fmt.Sprintf("/%d", i)
		}

		contribution, err := declaredMCPContribution(source, value, model.Location{Path: ManifestPath, Pointer: pointer})
		if err != nil {
			return nil, err
		}

		contributions = append(contributions, contribution)
	}

	return contributions, nil
}

func declaredMCPContribution(source fs.FS, raw json.RawMessage, location model.Location) (mcpContribution, error) {
	if len(raw) > 0 && raw[0] == '{' {
		servers, err := objectFields(raw, ErrInvalidMCP)
		if err != nil {
			return mcpContribution{}, err
		}

		return mcpContribution{servers: servers, location: location}, nil
	}

	var value string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &value) != nil {
		return mcpContribution{}, fmt.Errorf("%w: %s requires a JSON path or inline server map", ErrInvalidMCP, location.Pointer)
	}

	suffix, _, _ := strings.Cut(value, "?")
	suffix, _, _ = strings.Cut(suffix, "#")
	if strings.HasSuffix(strings.ToLower(suffix), ".mcpb") || strings.HasSuffix(strings.ToLower(suffix), ".dxt") {
		return mcpContribution{}, fmt.Errorf("%w: %w", ErrInvalidMCP, ErrUnsupportedMCPBundle)
	}

	filename, err := componentPath(value, "mcpServers", false, ErrInvalidMCP)
	if err != nil {
		return mcpContribution{}, err
	}

	return readMCPFile(source, filename, location)
}

func readMCPFile(source fs.FS, filename string, declaration model.Location) (mcpContribution, error) {
	if !strings.HasSuffix(filename, ".json") {
		return mcpContribution{}, fmt.Errorf("%w: MCP requires JSON files", ErrInvalidMCP)
	}

	info, err := fs.Stat(source, filename)
	if err != nil {
		return mcpContribution{}, fmt.Errorf("%w: inspect MCP %s: %w", ErrInvalidMCP, filename, err)
	}

	if !info.Mode().IsRegular() {
		return mcpContribution{}, fmt.Errorf("%w: %s must be a regular MCP file", ErrInvalidMCP, filename)
	}

	data, err := fs.ReadFile(source, filename)
	if err != nil {
		return mcpContribution{}, fmt.Errorf("read MCP %s: %w", filename, err)
	}

	servers, err := objectFields(data, ErrInvalidMCP)
	if err != nil {
		return mcpContribution{}, fmt.Errorf("%s: %w", filename, err)
	}

	location := model.Location{Path: filename}
	if wrapped, exists := servers["mcpServers"]; exists {
		servers, err = objectFields(wrapped, ErrInvalidMCP)
		if err != nil {
			return mcpContribution{}, fmt.Errorf("%s/mcpServers: %w", filename, err)
		}

		location.Pointer = "/mcpServers"
	}

	return mcpContribution{servers: servers, location: location, declaration: declaration, entrypoint: filename}, nil
}

func mcpCapability(name string, raw json.RawMessage, contribution mcpContribution, manifest Manifest, packageID model.PackageID) (model.Capability, error) {
	if strings.TrimSpace(name) == "" {
		return model.Capability{}, fmt.Errorf("%w: blank MCP server name", ErrInvalidMCP)
	}

	if err := validateJSONObjects(raw, ErrInvalidMCP); err != nil {
		return model.Capability{}, fmt.Errorf("server %q: %w", name, err)
	}

	fields, err := objectFields(raw, ErrInvalidMCP)
	if err != nil {
		return model.Capability{}, fmt.Errorf("server %q in %s: %w", name, contribution.location.Path, err)
	}

	transport := claudemodel.MCPStdio
	if rawType, exists := fields["type"]; exists {
		var value string
		if len(rawType) == 0 || rawType[0] != '"' || json.Unmarshal(rawType, &value) != nil || strings.TrimSpace(value) == "" {
			return model.Capability{}, fmt.Errorf("%w: MCP type requires a nonblank string", ErrInvalidMCP)
		}

		transport = claudemodel.MCPTransport(value)
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
		ID: model.CapabilityID{Kind: model.KindClaudeMCP, Name: name}, PackageID: packageID, Name: name,
		Definition: claudemodel.MCP{Transport: transport, Fields: fields},
		Provenance: model.Provenance{Root: manifest.Root, Declarations: declarations}, Payload: model.Payload{Entrypoints: entrypoints},
	}, nil
}

func combineMCP(existing, candidate model.Capability) (model.Capability, error) {
	previous, previousOK := existing.Definition.(claudemodel.MCP)
	next, nextOK := candidate.Definition.(claudemodel.MCP)
	if !previousOK || !nextOK {
		return model.Capability{}, fmt.Errorf("%w: missing MCP definition", ErrInvalidMCP)
	}

	equivalent, err := equivalentJSONFields(previous.Fields, next.Fields, ErrInvalidMCP)
	if err != nil {
		return model.Capability{}, err
	}

	if !equivalent {
		return model.Capability{}, fmt.Errorf("%w: conflicting server %q in %s and %s", ErrInvalidMCP, existing.ID.Name, existing.Provenance.Declarations[0].Path, candidate.Provenance.Declarations[0].Path)
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
