package claude

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strings"

	"github.com/leonhfr/glean/internal/model"
	claudemodel "github.com/leonhfr/glean/internal/model/claude"
	"github.com/leonhfr/glean/internal/system"
)

// ErrInvalidCommands identifies invalid declarations or ambiguous command names.
var ErrInvalidCommands = errors.New("invalid Claude command source")

type commandFile struct {
	path     string
	location model.Location
	name     string
}

// ReadCommands inventories a local plugin's commands under the supplied package ID.
func ReadCommands(sys system.RootOpener, directory string, packageID model.PackageID) (model.Inventory, error) {
	plugin, err := readPluginInventory(sys, directory, packageID, ErrInvalidCommands, readCommands, nil)
	return plugin.Inventory, err
}

func readCommands(source fs.FS, manifest Manifest, packageID model.PackageID) ([]model.Capability, error) {
	var capabilities []model.Capability
	raw, declared := manifest.Fields["commands"]
	if declared && len(raw) > 0 && raw[0] == '{' {
		return readMappedCommands(source, manifest, packageID, raw)
	}

	locations, err := commandLocations(source, manifest)
	if err != nil {
		return nil, err
	}

	for _, location := range locations {
		files, err := commandFiles(source, location)
		if err != nil {
			return nil, err
		}

		for _, file := range files {
			capability, err := readCommand(source, manifest, packageID, file)
			if err != nil {
				return nil, err
			}

			capabilities, err = includeMarkdownCapability(capabilities, capability, ErrInvalidCommands)
			if err != nil {
				return nil, err
			}
		}
	}

	slices.SortFunc(capabilities, func(a, b model.Capability) int { return cmp.Compare(a.ID.String(), b.ID.String()) })
	return capabilities, nil
}

func readMappedCommands(source fs.FS, manifest Manifest, packageID model.PackageID, raw json.RawMessage) ([]model.Capability, error) {
	entries, err := manifestFields(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: commands map: %w", ErrInvalidCommands, err)
	}

	var capabilities []model.Capability
	for _, name := range slices.Sorted(maps.Keys(entries)) {
		capability, err := mappedCommand(source, manifest, packageID, name, entries[name])
		if err != nil {
			return nil, err
		}

		capabilities = append(capabilities, capability)
	}

	return capabilities, nil
}

func commandLocations(source fs.FS, manifest Manifest) ([]componentLocation, error) {
	if raw, declared := manifest.Fields["commands"]; declared {
		return declaredComponentPaths(raw, "commands", false, ErrInvalidCommands)
	}

	if _, err := fs.Stat(source, "commands"); errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	} else if err != nil {
		return nil, fmt.Errorf("inspect commands directory: %w", err)
	}

	return []componentLocation{{path: "commands"}}, nil
}

func commandFiles(source fs.FS, location componentLocation) ([]commandFile, error) {
	info, err := fs.Stat(source, location.path)
	if err != nil {
		return nil, fmt.Errorf("%w: inspect command %s: %w", ErrInvalidCommands, location.path, err)
	}

	if !info.IsDir() {
		if location.location.Path == "" {
			return nil, fmt.Errorf("%w: commands must be a directory", ErrInvalidCommands)
		}

		return []commandFile{{path: location.path, location: location.location, name: strings.TrimSuffix(path.Base(location.path), ".md")}}, nil
	}

	var files []commandFile
	err = fs.WalkDir(source, location.path, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("discover command %s: %w", filename, walkErr)
		}

		if filename == ".git" {
			if entry.IsDir() {
				return fs.SkipDir
			}

			return nil
		}

		if !entry.IsDir() && strings.HasSuffix(filename, ".md") {
			relative := strings.TrimPrefix(filename, location.path+"/")
			name := strings.ReplaceAll(strings.TrimSuffix(relative, ".md"), "/", ":")
			files = append(files, commandFile{path: filename, location: location.location, name: name})
		}

		return nil
	})
	return files, err
}

func readCommand(source fs.FS, manifest Manifest, packageID model.PackageID, file commandFile) (model.Capability, error) {
	if !strings.HasSuffix(file.path, ".md") || path.Base(file.path) == ".md" {
		return model.Capability{}, fmt.Errorf("%w: commands require Markdown files", ErrInvalidCommands)
	}

	info, err := fs.Stat(source, file.path)
	if err != nil {
		return model.Capability{}, fmt.Errorf("%w: inspect command %s: %w", ErrInvalidCommands, file.path, err)
	}

	if !info.Mode().IsRegular() {
		return model.Capability{}, fmt.Errorf("%w: %s must be a regular Markdown file", ErrInvalidCommands, file.path)
	}

	data, err := fs.ReadFile(source, file.path)
	if err != nil {
		return model.Capability{}, fmt.Errorf("read command %s: %w", file.path, err)
	}

	capability, err := commandCapability(manifest, packageID, file.name, string(data), file.location)
	if err != nil {
		return model.Capability{}, err
	}

	capability.Payload.Entrypoints = []string{file.path}
	capability.Provenance.Declarations = append([]model.Location{{Path: file.path}}, capability.Provenance.Declarations...)
	return capability, nil
}

func mappedCommand(source fs.FS, manifest Manifest, packageID model.PackageID, name string, raw json.RawMessage) (model.Capability, error) {
	fields, err := manifestFields(raw)
	if err != nil {
		return model.Capability{}, fmt.Errorf("%w: command %q: %w", ErrInvalidCommands, name, err)
	}

	if err := validateCommandOptions(fields); err != nil {
		return model.Capability{}, err
	}

	pointer := "/commands/" + strings.ReplaceAll(strings.ReplaceAll(name, "~", "~0"), "/", "~1")
	location := model.Location{Path: ManifestPath, Pointer: pointer}
	capability, err := commandFromDeclaration(source, manifest, packageID, name, fields, location)
	if err != nil {
		return model.Capability{}, err
	}

	definition, ok := capability.Definition.(claudemodel.Command)
	if !ok {
		return model.Capability{}, fmt.Errorf("%w: missing native command definition", ErrInvalidCommands)
	}

	definition.Declaration = fields
	capability.Definition = definition
	if rawDescription, exists := fields["description"]; exists {
		description, err := commandString(rawDescription, "description")
		if err != nil {
			return model.Capability{}, err
		}

		capability.Description = description
	}

	return capability, nil
}

func commandFromDeclaration(source fs.FS, manifest Manifest, packageID model.PackageID, name string, fields map[string]json.RawMessage, location model.Location) (model.Capability, error) {
	sourceValue, hasSource := fields["source"]
	contentValue, hasContent := fields["content"]
	if hasSource == hasContent {
		return model.Capability{}, fmt.Errorf("%w: command %q requires exactly one of source or content", ErrInvalidCommands, name)
	}

	if hasSource {
		value, err := commandString(sourceValue, "source")
		if err != nil {
			return model.Capability{}, err
		}

		filename, err := componentPath(value, "commands", false, ErrInvalidCommands)
		if err != nil {
			return model.Capability{}, err
		}

		return readCommand(source, manifest, packageID, commandFile{path: filename, location: location, name: name})
	}

	content, err := commandString(contentValue, "content")
	if err != nil {
		return model.Capability{}, err
	}

	return commandCapability(manifest, packageID, name, content, location)
}

func commandCapability(manifest Manifest, packageID model.PackageID, name, document string, location model.Location) (model.Capability, error) {
	if strings.TrimSpace(name) == "" {
		return model.Capability{}, fmt.Errorf("%w: blank native command name", ErrInvalidCommands)
	}

	metadata, body := documentMetadata([]byte(document))
	description := firstContentLine(body)
	if metadata.description != nil {
		description = *metadata.description
	}

	var locations []model.Location
	if location.Path != "" {
		locations = append(locations, location)
	}

	return model.Capability{
		ID: model.CapabilityID{Kind: model.KindClaudeCommand, Name: name}, PackageID: packageID,
		Name: name, Description: description,
		Definition: claudemodel.Command{Document: document, Description: metadata.description},
		Provenance: model.Provenance{Root: manifest.Root, Declarations: locations},
	}, nil
}

func validateCommandOptions(fields map[string]json.RawMessage) error {
	for _, field := range []string{"description", "argumentHint", "model"} {
		if raw, exists := fields[field]; exists {
			if _, err := commandString(raw, field); err != nil {
				return err
			}
		}
	}

	if raw, exists := fields["allowedTools"]; exists {
		var tools []json.RawMessage
		if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &tools) != nil {
			return fmt.Errorf("%w: allowedTools requires an array of strings", ErrInvalidCommands)
		}

		for _, tool := range tools {
			if _, err := commandString(tool, "allowedTools"); err != nil {
				return err
			}
		}
	}

	return nil
}

func commandString(raw json.RawMessage, field string) (string, error) {
	var value string
	if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &value) != nil {
		return "", fmt.Errorf("%w: %s requires a string", ErrInvalidCommands, field)
	}

	return value, nil
}
