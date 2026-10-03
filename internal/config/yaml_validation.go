package config

import (
	"errors"
	"fmt"
	"time"

	"go.yaml.in/yaml/v3"
)

// yamlFields indexes authored fields and list items, including opaque map keys.
func yamlFields(node *yaml.Node, field string, nodes map[string]*yaml.Node) {
	node = dereference(node)
	nodes[field] = node
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i < len(node.Content); i += 2 {
			yamlFields(node.Content[i+1], fieldPath(field, node.Content[i].Value), nodes)
		}

	case yaml.SequenceNode:
		for i, child := range node.Content {
			yamlFields(child, fmt.Sprintf("%s[%d]", field, i), nodes)
		}

	case yaml.ScalarNode, yaml.AliasNode, yaml.DocumentNode:
	}
}

func validateScalars(nodes map[string]*yaml.Node) error {
	for _, name := range []string{"git_timeout", "refresh_period"} {
		if node := nodes[name]; node != nil {
			_, err := time.ParseDuration(node.Value)
			if err != nil {
				return invalid(node, name, "expected a nonnegative Go duration string")
			}
		}
	}

	return nil
}

func locateFinding(err error, nodes map[string]*yaml.Node) error {
	if detail, ok := errors.AsType[*Error](err); ok {
		node := nodes[detail.Field]
		if node == nil {
			node = nodes[""]
		}

		return &Error{Field: detail.Field, Message: detail.Message, Line: node.Line, Column: node.Column}
	}

	return err
}
