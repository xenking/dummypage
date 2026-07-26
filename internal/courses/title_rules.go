package courses

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const titleRulesSchema = "title-normalization-rules/v1"

type TitleRules struct {
	protectedTokensCI     map[string]struct{}
	protectedTokensExact  map[string]struct{}
	protectedSubstringsCI []string
}

type titleRulesFile struct {
	SchemaVersion         string   `json:"schema_version"`
	ProtectedTokensCI     []string `json:"protected_tokens_ci"`
	ProtectedTokensExact  []string `json:"protected_tokens_exact"`
	ProtectedSubstringsCI []string `json:"protected_substrings_ci"`
}

func LoadTitleRules(r io.Reader) (*TitleRules, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read title rules: %w", err)
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("read title rules: invalid utf-8")
	}
	if err := rejectDuplicateTopLevelKeys(data); err != nil {
		return nil, fmt.Errorf("decode title rules: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var file titleRulesFile
	if err := decoder.Decode(&file); err != nil {
		return nil, fmt.Errorf("decode title rules: %w", err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("decode title rules: multiple json values")
		}
		return nil, fmt.Errorf("decode title rules: %w", err)
	}
	if file.SchemaVersion != titleRulesSchema {
		return nil, fmt.Errorf("decode title rules: unsupported schema_version %q", file.SchemaVersion)
	}
	if file.ProtectedTokensCI == nil ||
		file.ProtectedTokensExact == nil ||
		file.ProtectedSubstringsCI == nil {
		return nil, fmt.Errorf("decode title rules: all rule arrays are required")
	}

	rules := &TitleRules{
		protectedTokensCI:     make(map[string]struct{}, len(file.ProtectedTokensCI)),
		protectedTokensExact:  make(map[string]struct{}, len(file.ProtectedTokensExact)),
		protectedSubstringsCI: make([]string, 0, len(file.ProtectedSubstringsCI)),
	}
	if err := addCIRules("protected_tokens_ci", file.ProtectedTokensCI, rules.protectedTokensCI); err != nil {
		return nil, err
	}
	if err := addExactRules("protected_tokens_exact", file.ProtectedTokensExact, rules.protectedTokensExact); err != nil {
		return nil, err
	}
	substrings := make(map[string]struct{}, len(file.ProtectedSubstringsCI))
	for _, value := range file.ProtectedSubstringsCI {
		normalized, err := normalizeTitleRuleValue("protected_substrings_ci", value)
		if err != nil {
			return nil, err
		}
		lower := strings.ToLower(normalized)
		if _, exists := substrings[lower]; exists {
			return nil, fmt.Errorf("decode title rules: duplicate protected_substrings_ci value %q", value)
		}
		substrings[lower] = struct{}{}
		rules.protectedSubstringsCI = append(rules.protectedSubstringsCI, lower)
	}
	for _, exact := range file.ProtectedTokensExact {
		if _, exists := rules.protectedTokensCI[strings.ToLower(exact)]; exists {
			return nil, fmt.Errorf("decode title rules: protected token %q appears in both exact and case-insensitive sets", exact)
		}
	}

	return rules, nil
}

func rejectDuplicateTopLevelKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return fmt.Errorf("top-level value must be object")
	}

	seen := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok {
			return fmt.Errorf("object member name must be string")
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate top-level key %q", key)
		}
		seen[key] = struct{}{}

		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
	}
	token, err = decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '}' {
		return fmt.Errorf("top-level object is not closed")
	}
	return nil
}

func cloneTitleRules(rules *TitleRules) *TitleRules {
	if rules == nil {
		return nil
	}
	clone := &TitleRules{
		protectedTokensCI:     make(map[string]struct{}, len(rules.protectedTokensCI)),
		protectedTokensExact:  make(map[string]struct{}, len(rules.protectedTokensExact)),
		protectedSubstringsCI: append([]string(nil), rules.protectedSubstringsCI...),
	}
	for token := range rules.protectedTokensCI {
		clone.protectedTokensCI[token] = struct{}{}
	}
	for token := range rules.protectedTokensExact {
		clone.protectedTokensExact[token] = struct{}{}
	}
	return clone
}

func addCIRules(field string, values []string, dst map[string]struct{}) error {
	for _, value := range values {
		normalized, err := normalizeTitleRuleValue(field, value)
		if err != nil {
			return err
		}
		lower := strings.ToLower(normalized)
		if _, exists := dst[lower]; exists {
			return fmt.Errorf("decode title rules: duplicate %s value %q", field, value)
		}
		dst[lower] = struct{}{}
	}
	return nil
}

func addExactRules(field string, values []string, dst map[string]struct{}) error {
	for _, value := range values {
		normalized, err := normalizeTitleRuleValue(field, value)
		if err != nil {
			return err
		}
		if _, exists := dst[normalized]; exists {
			return fmt.Errorf("decode title rules: duplicate %s value %q", field, value)
		}
		dst[normalized] = struct{}{}
	}
	return nil
}

func normalizeTitleRuleValue(field, value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("decode title rules: %s contains empty value", field)
	}
	if strings.TrimSpace(value) != value {
		return "", fmt.Errorf("decode title rules: %s value %q is not trimmed", field, value)
	}
	return value, nil
}
