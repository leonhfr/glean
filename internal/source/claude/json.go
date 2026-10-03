package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

func manifestFields(data []byte) (map[string]json.RawMessage, error) {
	return objectFields(data, ErrInvalidManifest)
}

// objectFields rejects duplicate keys and preserves authored values.
func objectFields(data []byte, invalidErr error) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("%w: expected a JSON object", invalidErr)
	}

	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		if err = readObjectField(decoder, fields, invalidErr); err != nil {
			return nil, err
		}
	}

	if _, err = decoder.Token(); err != nil {
		return nil, fmt.Errorf("%w: incomplete JSON object", invalidErr)
	}

	var extra json.RawMessage
	if err = decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: expected exactly one JSON object", invalidErr)
	}

	return fields, nil
}

func readObjectField(decoder *json.Decoder, fields map[string]json.RawMessage, invalidErr error) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("%w: malformed JSON field", invalidErr)
	}

	key, ok := token.(string)
	if !ok {
		return fmt.Errorf("%w: expected a field name", invalidErr)
	}

	if _, exists := fields[key]; exists {
		return fmt.Errorf("%w: duplicate field %q", invalidErr, key)
	}

	var value json.RawMessage
	if err = decoder.Decode(&value); err != nil {
		return fmt.Errorf("%w: malformed value for field %q", invalidErr, key)
	}

	fields[key] = value
	return nil
}

func equivalentJSONFields(first, second map[string]json.RawMessage, invalidErr error) (bool, error) {
	// Compare JSON values without expanding references, inferring defaults or merging.
	a, err := canonicalJSONFields(first, invalidErr)
	if err != nil {
		return false, err
	}

	b, err := canonicalJSONFields(second, invalidErr)
	if err != nil {
		return false, err
	}

	return string(a) == string(b), nil
}

func canonicalJSONFields(fields map[string]json.RawMessage, invalidErr error) ([]byte, error) {
	raw, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("%w: encode JSON definition: %w", invalidErr, err)
	}

	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("%w: decode JSON definition: %w", invalidErr, err)
	}

	normalized, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("%w: normalize JSON definition: %w", invalidErr, err)
	}

	return normalized, nil
}

func validateJSONObjects(raw json.RawMessage, invalidErr error) error {
	var values []json.RawMessage
	if len(raw) > 0 && raw[0] == '{' {
		fields, err := objectFields(raw, invalidErr)
		if err != nil {
			return err
		}

		for _, value := range fields {
			values = append(values, value)
		}
	} else if len(raw) > 0 && raw[0] == '[' {
		if err := json.Unmarshal(raw, &values); err != nil {
			return fmt.Errorf("%w: invalid nested array", invalidErr)
		}
	}

	for _, value := range values {
		if err := validateJSONObjects(value, invalidErr); err != nil {
			return err
		}
	}

	return nil
}
