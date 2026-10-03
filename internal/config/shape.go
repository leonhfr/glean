package config

import (
	"fmt"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

type shape struct {
	tag    string
	fields map[string]shape
	item   *shape
	union  string
}

func configShape() shape {
	text := shape{tag: "!!str"}
	list := shape{tag: "!!seq", item: &text}
	reference := shape{tag: "!!map", fields: map[string]shape{"from_env": text}}
	value := reference
	value.union = "value"
	values := shape{tag: "!!map", item: &value}
	set := shape{tag: "!!map", union: "all", fields: map[string]shape{
		"skills": list, "agents": list, "commands": list, "hooks": list, "mcp": list, "lsp": list,
	}}
	selection := shape{tag: "!!map", fields: map[string]shape{
		"repo": text, "path": text, "plugin": text, "ref": text, "format": text, "select": set,
	}}
	server := shape{tag: "!!map", fields: map[string]shape{
		"transport": text, "command": text, "args": list, "env": values, "cwd": text,
		"url": text, "headers": values, "enabled": {tag: "!!bool"}, "description": text,
		"auth":   {tag: "!!map", fields: map[string]shape{"bearer_token": reference}},
		"native": {tag: "!!map", fields: map[string]shape{"claude": {tag: "!!map", fields: map[string]shape{}}}},
	}}
	return shape{tag: "!!map", fields: map[string]shape{
		"automation": text, "git_timeout": text, "refresh_period": text,
		"selections": {tag: "!!map", item: &selection}, "mcp_servers": {tag: "!!map", item: &server},
	}}
}

func validate(node *yaml.Node, path string, expected shape) error {
	node = dereference(node)
	if node.Tag == "!!null" {
		return invalid(node, path, "null is not allowed; omit optional fields")
	}

	if node.Tag == "!!str" && (expected.union == "value" || expected.union == "all" && node.Value == "all") {
		return nil
	}

	if node.Tag != expected.tag {
		return invalid(node, path, "expected "+strings.TrimPrefix(expected.tag, "!!"))
	}

	switch node.Kind {
	case yaml.MappingNode:
		return validateMapping(node, path, expected)

	case yaml.SequenceNode:
		for i, child := range node.Content {
			if err := validate(child, fmt.Sprintf("%s[%d]", path, i), *expected.item); err != nil {
				return err
			}
		}

	case yaml.ScalarNode, yaml.AliasNode, yaml.DocumentNode:
		// Scalar tags were checked above; aliases are dereferenced first.
	}

	return nil
}

func validateMapping(node *yaml.Node, path string, expected shape) error {
	seen := make(map[string]bool)
	for i := 0; i < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		if key.Tag != "!!str" || strings.ContainsRune(key.Value, '\x00') {
			return invalid(key, path, "mapping keys must be strings without NUL characters")
		}

		field := fieldPath(path, key.Value)
		if seen[key.Value] {
			return invalid(key, field, "duplicate mapping key")
		}

		seen[key.Value] = true
		child, ok := expected.fields[key.Value]
		if expected.item != nil {
			child, ok = *expected.item, true
		}

		if !ok {
			return invalid(key, field, "unknown or unsupported field")
		}

		if err := validate(value, field, child); err != nil {
			return err
		}
	}

	if expected.union == "value" && !seen["from_env"] {
		return invalid(node, path, "expected a string or {from_env: NAME}")
	}

	return nil
}

func dereference(node *yaml.Node) *yaml.Node {
	if node.Kind == yaml.AliasNode {
		return node.Alias
	}

	return node
}

func fieldPath(parent, name string) string {
	if parent == "" {
		return name
	}

	if !validAlias(name) {
		return parent + "[" + strconv.Quote(name) + "]"
	}

	return parent + "." + name
}
