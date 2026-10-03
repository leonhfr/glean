package config

import (
	"errors"
	"fmt"

	"go.yaml.in/yaml/v3"
)

// ErrInvalid identifies invalid configuration, independently of human messages.
var ErrInvalid = errors.New("invalid configuration")

// Error reports a field and its document source position without echoing values.
type Error struct {
	Field   string
	Line    int
	Column  int
	Message string
}

// Error implements error.
func (e *Error) Error() string {
	field := e.Field
	if field == "" {
		field = "configuration"
	}

	return fmt.Sprintf("%s at %d:%d: %s", field, e.Line, e.Column, e.Message)
}

// Unwrap supports errors.Is(err, ErrInvalid).
func (e *Error) Unwrap() error {
	return ErrInvalid
}

func invalid(node *yaml.Node, field, message string) error {
	return &Error{Field: field, Line: node.Line, Column: node.Column, Message: message}
}
