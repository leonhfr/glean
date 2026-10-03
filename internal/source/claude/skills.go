package claude

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/leonhfr/glean/internal/model"
	"github.com/leonhfr/glean/internal/system"
)

// ErrInvalidSkills identifies invalid paths or ambiguous skill invocation names.
var ErrInvalidSkills = errors.New("invalid Claude skill source")

// ReadSkills inventories a local plugin's skills under the supplied package ID.
func ReadSkills(sys system.RootOpener, directory string, packageID model.PackageID) (model.Inventory, error) {
	plugin, err := readPluginInventory(sys, directory, packageID, ErrInvalidSkills, readSkills)
	return plugin.Inventory, err
}

func readSkills(source fs.FS, manifest Manifest, packageID model.PackageID) ([]model.Capability, error) {
	directories, err := skillDirectories(source, manifest)
	if err != nil {
		return nil, err
	}

	var capabilities []model.Capability
	for _, directory := range directories {
		entries, err := skillsInDirectory(source, directory.path, directory.location.Path != "")
		if err != nil {
			return nil, err
		}

		for _, entry := range entries {
			capability, err := readSkill(source, manifest, packageID, entry, directory.location)
			if err != nil {
				return nil, err
			}

			capabilities, err = includeMarkdownCapability(capabilities, capability, ErrInvalidSkills)
			if err != nil {
				return nil, err
			}
		}
	}

	slices.SortFunc(capabilities, func(a, b model.Capability) int {
		return cmp.Compare(a.ID.String(), b.ID.String())
	})
	return capabilities, nil
}

func skillDirectories(source fs.FS, manifest Manifest) ([]componentLocation, error) {
	var directories []componentLocation
	_, err := fs.Stat(source, "skills")
	if err == nil {
		directories = append(directories, componentLocation{path: "skills"})
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("inspect skills directory: %w", err)
	}

	if raw, declared := manifest.Fields["skills"]; declared {
		custom, err := declaredComponentPaths(raw, "skills", true, ErrInvalidSkills)
		if err != nil {
			return nil, err
		}

		directories = append(directories, custom...)
	} else if len(directories) == 0 {
		if exists, err := skillEntrypoint(source, "."); err != nil {
			return nil, err
		} else if exists {
			directories = append(directories, componentLocation{path: "."})
		}
	}

	return directories, nil
}

func skillsInDirectory(source fs.FS, directory string, declared bool) ([]string, error) {
	if declared || directory == "." {
		if exists, err := skillEntrypoint(source, directory); err != nil {
			return nil, err
		} else if exists {
			return []string{directory}, nil
		}
	}

	entries, err := fs.ReadDir(source, directory)
	if err != nil {
		return nil, fmt.Errorf("%w: read skills directory %s: %w", ErrInvalidSkills, directory, err)
	}

	var skills []string
	for _, entry := range entries {
		if !entry.IsDir() || directory == "." && entry.Name() == ".git" {
			continue
		}

		candidate := path.Join(directory, entry.Name())
		exists, err := skillEntrypoint(source, candidate)
		if err != nil {
			return nil, err
		}

		if exists {
			skills = append(skills, candidate)
		}
	}

	return skills, nil
}

func skillEntrypoint(source fs.FS, directory string) (bool, error) {
	info, err := fs.Stat(source, path.Join(directory, "SKILL.md"))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("%w: inspect skill %s: %w", ErrInvalidSkills, directory, err)
	}

	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("%w: %s/SKILL.md must be a regular file", ErrInvalidSkills, directory)
	}

	return true, nil
}

func readSkill(source fs.FS, manifest Manifest, packageID model.PackageID, directory string, declaration model.Location) (model.Capability, error) {
	entrypoint := path.Join(directory, "SKILL.md")
	data, err := fs.ReadFile(source, entrypoint)
	if err != nil {
		return model.Capability{}, fmt.Errorf("read skill %s: %w", entrypoint, err)
	}

	definition, description := skillDocument(data)
	name := path.Base(directory)
	if directory == "." {
		name = filepath.Base(manifest.Root)
	}

	if definition.Name != nil {
		name = strings.TrimPrefix(*definition.Name, manifest.Name+":")
	}

	if strings.TrimSpace(name) == "" {
		return model.Capability{}, fmt.Errorf("%w: %s has a blank invocation name", ErrInvalidSkills, entrypoint)
	}

	if definition.Description != nil {
		description = *definition.Description
	}

	locations := []model.Location{{Path: entrypoint}}
	if declaration.Path != "" {
		locations = append(locations, declaration)
	}

	return model.Capability{
		ID: model.CapabilityID{Kind: model.KindSkill, Name: name}, PackageID: packageID,
		Name: name, Description: description, Definition: definition,
		Provenance: model.Provenance{Root: manifest.Root, Declarations: locations},
		Payload:    model.Payload{Entrypoints: []string{entrypoint}, Assets: []string{directory}},
	}, nil
}
