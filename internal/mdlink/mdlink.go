// Package mdlink discovers local CommonMark link destinations without rewriting documents.
// Inline links, images and reference definitions are covered; code, HTML attributes,
// frontmatter and arbitrary prose paths are not interpreted as links.
package mdlink

import (
	"bytes"
	"net/url"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

var scheme = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*:`)

// Link preserves authored spelling and the decoded local path separately.
// Line is one-based; Invalid marks a malformed path encoding.
type Link struct {
	Line    int
	Raw     string
	Path    string
	Invalid bool
}

// Local returns destinations in source order, once per reference definition.
// It never changes source bytes or evaluates native expressions.
func Local(document []byte) []Link {
	source := withoutFrontmatter(document)
	root := goldmark.New().Parser().Parse(text.NewReader(source))
	var links []Link
	_ = ast.Walk(root, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}

		var destination []byte
		switch typed := node.(type) {
		case *ast.Link:
			if typed.Reference == nil {
				destination = typed.Destination
			}

		case *ast.Image:
			if typed.Reference == nil {
				destination = typed.Destination
			}

		case *ast.LinkReferenceDefinition:
			destination = typed.Destination
		}

		if link, local := localDestination(destination); local {
			link.Line = bytes.Count(source[:node.Pos()], []byte("\n")) + 1
			links = append(links, link)
		}

		return ast.WalkContinue, nil
	})
	return links
}

func localDestination(raw []byte) (Link, bool) {
	value := string(util.ResolveEntityNames(util.ResolveNumericReferences(util.UnescapePunctuations(raw))))
	if value == "" || strings.HasPrefix(value, "#") || strings.HasPrefix(value, "/") || strings.HasPrefix(value, `\`) || scheme.MatchString(value) {
		return Link{}, false
	}

	parsed, err := url.Parse(value)
	if err == nil && (parsed.IsAbs() || parsed.Host != "") {
		return Link{}, false
	}

	pathname, _, _ := strings.Cut(value, "?")
	pathname, _, _ = strings.Cut(pathname, "#")
	if pathname == "" {
		return Link{}, false
	}

	decoded, err := url.PathUnescape(pathname)
	return Link{Raw: string(raw), Path: decoded, Invalid: err != nil}, true
}

func withoutFrontmatter(document []byte) []byte {
	source := bytes.Clone(document)
	content := bytes.TrimPrefix(source, []byte("\xef\xbb\xbf"))
	lines := bytes.Split(content, []byte("\n"))
	if string(bytes.TrimSuffix(lines[0], []byte("\r"))) != "---" {
		return source
	}

	end := len(source) - len(content) + len(lines[0]) + 1
	for _, line := range lines[1:] {
		end += len(line) + 1
		if string(bytes.TrimSuffix(line, []byte("\r"))) != "---" {
			continue
		}

		for i := range min(end, len(source)) {
			if source[i] != '\n' && source[i] != '\r' {
				source[i] = ' '
			}
		}

		return source
	}

	return source
}
