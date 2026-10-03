package claude

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/leonhfr/glean/internal/model"
	"github.com/leonhfr/glean/internal/system"
)

// ErrInvalidPlugin identifies invalid package identity or component inventory.
var ErrInvalidPlugin = errors.New("invalid Claude plugin source")

// PluginInventory retains the native manifest alongside the shared inventory.
// Retained fields support subsequent requirement assessment and derived delivery.
// Inventory alone does not establish that every declaration is supported.
type PluginInventory struct {
	Manifest  Manifest
	Inventory model.Inventory
}

type capabilityReader func(fs.FS, Manifest, model.PackageID) ([]model.Capability, error)

// ReadPlugin inventories all six kinds using one checked root and manifest read.
// Any component or close failure discards the complete result.
func ReadPlugin(sys system.RootOpener, directory string, packageID model.PackageID) (PluginInventory, error) {
	return readPluginInventory(sys, directory, packageID, ErrInvalidPlugin, readPluginCapabilities)
}

func readPluginInventory(sys system.RootOpener, directory string, packageID model.PackageID, invalidErr error, reader capabilityReader) (result PluginInventory, err error) {
	if strings.TrimSpace(string(packageID)) == "" {
		return PluginInventory{}, fmt.Errorf("%w: require resolved package identity", invalidErr)
	}

	root, err := openPluginRoot(sys, directory)
	if err != nil {
		return PluginInventory{}, err
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			result = PluginInventory{}
			err = errors.Join(err, fmt.Errorf("close plugin root: %w", closeErr))
		}
	}()

	manifest, err := readManifest(root)
	if err != nil {
		return PluginInventory{}, err
	}

	capabilities, err := reader(root.FS(), manifest, packageID)
	if err != nil {
		return PluginInventory{}, err
	}

	return PluginInventory{Manifest: manifest, Inventory: model.Inventory{
		Package: model.Package{ID: packageID, Name: manifest.Name, Root: manifest.Root}, Capabilities: capabilities,
	}}, nil
}

func readPluginCapabilities(source fs.FS, manifest Manifest, packageID model.PackageID) ([]model.Capability, error) {
	var capabilities []model.Capability
	for _, reader := range []capabilityReader{readSkills, readAgents, readCommands, readHooks, readMCP, readLSP} {
		entries, err := reader(source, manifest, packageID)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalidPlugin, err)
		}

		capabilities = append(capabilities, entries...)
	}

	slices.SortFunc(capabilities, func(a, b model.Capability) int { return cmp.Compare(a.ID.String(), b.ID.String()) })
	return capabilities, nil
}
