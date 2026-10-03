package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/go-viper/mapstructure/v2"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/rawbytes"
	"github.com/knadh/koanf/v2"
	yamlv3 "go.yaml.in/yaml/v3"
)

// Document retains the original YAML tree alongside decoded intent.
// Defaults and path resolution do not rewrite this tree or the source file.
type Document struct {
	Config Config
	Path   string
	Root   *yamlv3.Node
}

// Load reads one file. A missing file remains an errors.Is-compatible OS error.
func Load(path string) (*Document, error) {
	// #nosec G304 -- Explicit user-selected config file.
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	return Parse(data, path)
}

// Parse validates and decodes one YAML document. Local paths resolve relative to
// path; Git subpaths retain their repository-relative meaning. It performs no
// acquisition, native inspection, credential expansion or persistent writes.
func Parse(data []byte, path string) (*Document, error) {
	root, err := parseDocument(data)
	if err != nil {
		return nil, err
	}

	if err = validate(root.Content[0], "", configShape()); err != nil {
		return nil, err
	}

	nodes := make(map[string]*yamlv3.Node)
	yamlFields(root.Content[0], "", nodes)
	fields := make(fieldSet, len(nodes))
	for field := range nodes {
		fields[field] = true
	}

	if err = validateScalars(nodes); err != nil {
		return nil, err
	}

	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("config path: %w", err)
	}

	// Native names containing dots are opaque map keys.
	k := koanf.New("\x00")
	if err = k.Load(rawbytes.Provider(data), yaml.Parser()); err != nil {
		return nil, fmt.Errorf("%w: decode config: %w", ErrInvalid, err)
	}

	cfg := defaults()
	if err = k.UnmarshalWithConf("", &cfg, koanf.UnmarshalConf{
		DecoderConfig: &mapstructure.DecoderConfig{
			WeaklyTypedInput: false,
			ErrorUnused:      true,
			MatchName:        func(key, field string) bool { return key == field },
			DecodeHook: mapstructure.ComposeDecodeHookFunc(
				mapstructure.StringToTimeDurationHookFunc(), decodeSpecial),
		},
	}); err != nil {
		return nil, fmt.Errorf("%w: decode config: %w", ErrInvalid, err)
	}

	if err = validateIntent(&cfg, fields, filepath.Dir(absolute)); err != nil {
		return nil, locateFinding(err, nodes)
	}

	return &Document{Config: cfg, Path: absolute, Root: root}, nil
}

func decodeSpecial(_, to reflect.Type, value any) (any, error) {
	switch to {
	case reflect.TypeFor[CapabilitySet]():
		if text, ok := value.(string); ok && text == "all" {
			return CapabilitySet{All: true}, nil
		}

	case reflect.TypeFor[Value]():
		if text, ok := value.(string); ok {
			return Value{Literal: text}, nil
		}

		if fields, ok := value.(map[string]any); ok {
			if name, ok := fields["from_env"].(string); ok {
				return Value{FromEnv: name}, nil
			}
		}

	case reflect.TypeFor[MCPServer]():
		if fields, ok := value.(map[string]any); ok {
			if _, exists := fields["enabled"]; !exists {
				fields["enabled"] = true
			}
		}
	}

	return value, nil
}

func parseDocument(data []byte) (*yamlv3.Node, error) {
	decoder := yamlv3.NewDecoder(bytes.NewReader(data))
	var root yamlv3.Node
	if err := decoder.Decode(&root); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, invalid(&root, "", "empty configuration; use {} for empty intent")
		}

		return nil, fmt.Errorf("%w: malformed YAML: %w", ErrInvalid, err)
	}

	var extra yamlv3.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, invalid(&extra, "", "expected exactly one YAML document")
	}

	if len(root.Content) != 1 {
		return nil, invalid(&root, "", "expected a configuration mapping")
	}

	return &root, nil
}

func resolveLocal(path, directory string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}

		path = filepath.Join(home, strings.TrimPrefix(path, "~"))
	}

	if !filepath.IsAbs(path) {
		path = filepath.Join(directory, path)
	}

	return filepath.Clean(path), nil
}
