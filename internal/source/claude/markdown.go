package claude

import (
	"bytes"
	"strings"

	"go.yaml.in/yaml/v3"

	claudemodel "github.com/leonhfr/glean/internal/model/claude"
)

type markdownMetadata struct {
	name        *string
	description *string
}

func skillDocument(data []byte) (claudemodel.Skill, string) {
	metadata, body := documentMetadata(data)
	return claudemodel.Skill{Document: string(data), Name: metadata.name, Description: metadata.description}, firstContentLine(body)
}

func documentMetadata(data []byte) (markdownMetadata, []byte) {
	var metadata markdownMetadata
	frontmatter, body := splitFrontmatter(data)
	var fields map[string]any
	// Native plugin Markdown keeps loading when YAML metadata cannot be parsed.
	if yaml.Unmarshal(frontmatter, &fields) == nil {
		if name, ok := fields["name"].(string); ok {
			metadata.name = &name
		}

		if description, ok := fields["description"].(string); ok {
			metadata.description = &description
		}
	}

	return metadata, body
}

func splitFrontmatter(data []byte) ([]byte, []byte) {
	content := bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))
	lines := bytes.Split(content, []byte("\n"))
	if string(bytes.TrimSuffix(lines[0], []byte("\r"))) != "---" {
		return nil, content
	}

	for i := 1; i < len(lines); i++ {
		if string(bytes.TrimSuffix(lines[i], []byte("\r"))) == "---" {
			return bytes.Join(lines[1:i], []byte("\n")), bytes.Join(lines[i+1:], []byte("\n"))
		}
	}

	return nil, content
}

func firstContentLine(body []byte) string {
	for line := range strings.SplitSeq(string(body), "\n") {
		if text := strings.TrimSpace(line); text != "" {
			return text
		}
	}

	return ""
}
