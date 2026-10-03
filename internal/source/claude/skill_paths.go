package claude

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strings"

	"github.com/leonhfr/glean/internal/model"
)

type skillDirectory struct {
	path     string
	location model.Location
}

func declaredSkillDirectories(raw json.RawMessage) ([]skillDirectory, error) {
	var values []string
	array := len(raw) != 0 && raw[0] == '['
	if array {
		if err := json.Unmarshal(raw, &values); err != nil {
			return nil, fmt.Errorf("%w: skills requires a path or array of paths", ErrInvalidSkills)
		}
	} else {
		var value string
		if len(raw) == 0 || raw[0] != '"' || json.Unmarshal(raw, &value) != nil {
			return nil, fmt.Errorf("%w: skills requires a path or array of paths", ErrInvalidSkills)
		}

		values = []string{value}
	}

	var directories []skillDirectory
	for i, value := range values {
		pointer := "/skills"
		if array {
			pointer += fmt.Sprintf("/%d", i)
		}

		relative, err := skillPath(value)
		if err != nil {
			return nil, err
		}

		directories = append(directories, skillDirectory{
			path: relative, location: model.Location{Path: ManifestPath, Pointer: pointer},
		})
	}

	return directories, nil
}

func skillPath(value string) (string, error) {
	if value != "." && !strings.HasPrefix(value, "./") || strings.ContainsAny(value, "\\\x00") {
		return "", fmt.Errorf("%w: skills paths must start with './' or use '.' for the root", ErrInvalidSkills)
	}

	for segment := range strings.SplitSeq(value, "/") {
		if segment == ".." {
			return "", fmt.Errorf("%w: skills path contains traversal", ErrInvalidSkills)
		}
	}

	relative := path.Clean(value)
	if !fs.ValidPath(relative) || relative == ".git" || strings.HasPrefix(relative, ".git/") {
		return "", fmt.Errorf("%w: skills path must stay within the plugin payload", ErrInvalidSkills)
	}

	return relative, nil
}
