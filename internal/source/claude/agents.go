package claude

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/leonhfr/glean/internal/model"
	claudemodel "github.com/leonhfr/glean/internal/model/claude"
	"github.com/leonhfr/glean/internal/system"
)

// ErrInvalidAgents identifies invalid declarations or ambiguous agent names.
var ErrInvalidAgents = errors.New("invalid Claude agent source")

// ReadAgents inventories a local plugin's agents under the supplied package ID.
func ReadAgents(sys system.RootOpener, directory string, packageID model.PackageID) (model.Inventory, error) {
	plugin, err := readPluginInventory(sys, directory, packageID, ErrInvalidAgents, readAgents, nil)
	return plugin.Inventory, err
}

func readAgents(source fs.FS, manifest Manifest, packageID model.PackageID) ([]model.Capability, error) {
	files, err := agentFiles(source, manifest)
	if err != nil {
		return nil, err
	}

	var capabilities []model.Capability
	for _, file := range files {
		capability, err := readAgent(source, manifest, packageID, file)
		if err != nil {
			return nil, err
		}

		capabilities, err = includeMarkdownCapability(capabilities, capability, ErrInvalidAgents)
		if err != nil {
			return nil, err
		}
	}

	slices.SortFunc(capabilities, func(a, b model.Capability) int {
		return cmp.Compare(a.ID.String(), b.ID.String())
	})
	return capabilities, nil
}

func agentFiles(source fs.FS, manifest Manifest) ([]componentLocation, error) {
	if raw, declared := manifest.Fields["agents"]; declared {
		return declaredComponentPaths(raw, "agents", false, ErrInvalidAgents)
	}

	info, err := fs.Stat(source, "agents")
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("inspect agents directory: %w", err)
	}

	if !info.IsDir() {
		return nil, fmt.Errorf("%w: agents must be a directory", ErrInvalidAgents)
	}

	var files []componentLocation
	err = fs.WalkDir(source, "agents", func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("discover agent %s: %w", filename, walkErr)
		}

		if !entry.IsDir() && strings.HasSuffix(filename, ".md") {
			files = append(files, componentLocation{path: filename})
		}

		return nil
	})
	return files, err
}

func readAgent(source fs.FS, manifest Manifest, packageID model.PackageID, file componentLocation) (model.Capability, error) {
	if err := validateAgentFile(source, file.path); err != nil {
		return model.Capability{}, err
	}

	data, err := fs.ReadFile(source, file.path)
	if err != nil {
		return model.Capability{}, fmt.Errorf("read agent %s: %w", file.path, err)
	}

	metadata, _ := documentMetadata(data)
	name, err := agentName(file, metadata.name)
	if err != nil {
		return model.Capability{}, err
	}

	description := fmt.Sprintf("Agent from %s plugin", manifest.Name)
	if metadata.description != nil {
		description = *metadata.description
	}

	locations := []model.Location{{Path: file.path}}
	if file.location.Path != "" {
		locations = append(locations, file.location)
	}

	return model.Capability{
		ID: model.CapabilityID{Kind: model.KindClaudeAgent, Name: name}, PackageID: packageID,
		Name: name, Description: description,
		Definition: claudemodel.Agent{Document: string(data), Name: metadata.name, Description: metadata.description},
		Provenance: model.Provenance{Root: manifest.Root, Declarations: locations},
		Payload:    model.Payload{Entrypoints: []string{file.path}},
	}, nil
}

func validateAgentFile(source fs.FS, filename string) error {
	if !strings.HasSuffix(filename, ".md") {
		return fmt.Errorf("%w: declared agents must be Markdown files", ErrInvalidAgents)
	}

	info, err := fs.Stat(source, filename)
	if err != nil {
		return fmt.Errorf("%w: inspect agent %s: %w", ErrInvalidAgents, filename, err)
	}

	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %s must be a regular Markdown file", ErrInvalidAgents, filename)
	}

	return nil
}

func agentName(file componentLocation, authored *string) (string, error) {
	name := strings.TrimSuffix(path.Base(file.path), ".md")
	if authored != nil {
		name = *authored
	}

	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("%w: %s has a blank native name", ErrInvalidAgents, file.path)
	}

	if file.location.Path == "" {
		directory := path.Dir(strings.TrimPrefix(file.path, "agents/"))
		if directory != "." {
			name = strings.ReplaceAll(directory, "/", ":") + ":" + name
		}
	}

	return name, nil
}
