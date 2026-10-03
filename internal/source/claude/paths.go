package claude

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/leonhfr/glean/internal/model"
)

type componentLocation struct {
	path     string
	location model.Location
}

func declaredComponentPaths(raw json.RawMessage, field string, allowRoot bool, invalidErr error) ([]componentLocation, error) {
	var values []string
	array := len(raw) != 0 && raw[0] == '['
	if array {
		if err := json.Unmarshal(raw, &values); err != nil {
			return nil, fmt.Errorf("%w: %s requires a path or array of paths", invalidErr, field)
		}
	} else {
		var value string
		if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &value) != nil {
			return nil, fmt.Errorf("%w: %s requires a path or array of paths", invalidErr, field)
		}

		values = []string{value}
	}

	var locations []componentLocation
	for i, value := range values {
		pointer := "/" + field
		if array {
			pointer += fmt.Sprintf("/%d", i)
		}

		relative, err := componentPath(value, field, allowRoot, invalidErr)
		if err != nil {
			return nil, err
		}

		locations = append(locations, componentLocation{
			path: relative, location: model.Location{Path: ManifestPath, Pointer: pointer},
		})
	}

	return locations, nil
}

func componentPath(value, field string, allowRoot bool, invalidErr error) (string, error) {
	if (!allowRoot || value != ".") && !strings.HasPrefix(value, "./") || strings.ContainsAny(value, "\\\x00") {
		return "", fmt.Errorf("%w: %s requires a plugin-relative component path", invalidErr, field)
	}

	for segment := range strings.SplitSeq(value, "/") {
		if segment == ".." {
			return "", fmt.Errorf("%w: %s path contains traversal", invalidErr, field)
		}
	}

	relative := path.Clean(value)
	if !fs.ValidPath(relative) || relative == ".git" || strings.HasPrefix(relative, ".git/") {
		return "", fmt.Errorf("%w: %s path must stay within the plugin payload", invalidErr, field)
	}

	return relative, nil
}
