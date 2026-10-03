package claude

import (
	"errors"

	"github.com/leonhfr/glean/internal/system"
)

func openPluginRoot(sys system.RootOpener, directory string) (system.Root, error) {
	root, err := sys.OpenRoot(directory)
	if err != nil {
		return nil, err
	}

	if err = validatePayload(root.FS()); err != nil {
		return nil, errors.Join(err, root.Close())
	}

	return root, nil
}
