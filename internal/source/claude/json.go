package claude

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// manifestFields rejects duplicate top-level keys rather than allowing the JSON
// decoder to silently choose one. Native nested definitions are parsed separately.
func manifestFields(data []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("%w: expected a JSON object", ErrInvalidManifest)
	}

	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		if err = readManifestField(decoder, fields); err != nil {
			return nil, err
		}
	}

	if _, err = decoder.Token(); err != nil {
		return nil, fmt.Errorf("%w: incomplete JSON object", ErrInvalidManifest)
	}

	var extra json.RawMessage
	if err = decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: expected exactly one JSON object", ErrInvalidManifest)
	}

	return fields, nil
}

func readManifestField(decoder *json.Decoder, fields map[string]json.RawMessage) error {
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("%w: malformed JSON field", ErrInvalidManifest)
	}

	key, ok := token.(string)
	if !ok {
		return fmt.Errorf("%w: expected a field name", ErrInvalidManifest)
	}

	if _, exists := fields[key]; exists {
		return fmt.Errorf("%w: duplicate field %q", ErrInvalidManifest, key)
	}

	var value json.RawMessage
	if err = decoder.Decode(&value); err != nil {
		return fmt.Errorf("%w: malformed value for field %q", ErrInvalidManifest, key)
	}

	fields[key] = value
	return nil
}
