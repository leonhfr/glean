package claude

import (
	"encoding/json"
	"maps"
	"math/big"
	"slices"
	"strings"

	"github.com/leonhfr/glean/internal/model"
)

type nativeFieldRule struct {
	valid    func(json.RawMessage) bool
	required bool
}

func assessNativeFields(capability model.Capability, fields map[string]json.RawMessage, rules map[string]nativeFieldRule, inactive []string) []model.Finding {
	var findings []model.Finding
	for _, field := range slices.Sorted(maps.Keys(rules)) {
		rule := rules[field]
		raw, exists := fields[field]
		if !exists && !rule.required {
			continue
		}

		if !exists || !rule.valid(raw) {
			findings = append(findings, nativeFieldFinding(capability, "INVALID_NATIVE_FIELD", model.FindingMissing, field, "Required native field is absent or its authored value has an invalid shape."))
		}
	}

	for _, field := range slices.Sorted(maps.Keys(fields)) {
		if _, known := rules[field]; known {
			continue
		}

		code, status, message := "UNKNOWN_NATIVE_FIELD", model.FindingUnknown, "Native field semantics and version availability require explicit assessment."
		if slices.Contains(inactive, field) {
			code, status, message = "INAPPLICABLE_NATIVE_FIELD", model.FindingUnsupported, "This field cannot retain its semantics with the declared transport."
		}

		findings = append(findings, nativeFieldFinding(capability, code, status, field, message))
	}

	return findings
}

func nativeFieldFinding(capability model.Capability, code string, status model.FindingStatus, field, message string) model.Finding {
	finding := capabilityFinding(capability, code, status, field, message)
	if len(finding.Locations) > 0 {
		// First location is the definition; subsequent locations retain related declarations.
		finding.Locations[0].Pointer += "/" + pointerSegment(field)
	}

	return finding
}

func jsonString(raw json.RawMessage) bool {
	var value string
	return len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, &value) == nil
}

func nonblankJSONString(raw json.RawMessage) bool {
	var value string
	return jsonString(raw) && json.Unmarshal(raw, &value) == nil && strings.TrimSpace(value) != ""
}

func jsonStringArray(raw json.RawMessage) bool {
	var values []json.RawMessage
	if len(raw) == 0 || raw[0] != '[' || json.Unmarshal(raw, &values) != nil {
		return false
	}

	for _, value := range values {
		if !jsonString(value) {
			return false
		}
	}

	return true
}

func jsonStringMap(raw json.RawMessage) bool {
	var values map[string]json.RawMessage
	if !jsonObject(raw) || json.Unmarshal(raw, &values) != nil {
		return false
	}

	for _, value := range values {
		if !jsonString(value) {
			return false
		}
	}

	return true
}

func jsonObject(raw json.RawMessage) bool {
	return len(raw) > 0 && raw[0] == '{' && json.Valid(raw)
}

func jsonBoolean(raw json.RawMessage) bool {
	var value bool
	return string(raw) != "null" && json.Unmarshal(raw, &value) == nil
}

func positiveJSONInteger(raw json.RawMessage) bool {
	value, ok := new(big.Rat).SetString(string(raw))
	return ok && json.Valid(raw) && value.IsInt() && value.Sign() > 0
}

func nonnegativeJSONInteger(raw json.RawMessage) bool {
	value, ok := new(big.Rat).SetString(string(raw))
	return ok && json.Valid(raw) && value.IsInt() && value.Sign() >= 0
}

func jsonValue(raw json.RawMessage) bool {
	return json.Valid(raw)
}
