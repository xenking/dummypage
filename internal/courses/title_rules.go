package courses

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const titleRulesSchema = "title-normalization-rules/v2"

type TitleRules struct {
	protectedTokensCI       map[string]struct{}
	protectedTokensExact    map[string]struct{}
	protectedSubstringsCI   []string
	forceNormalizeTokensCI  map[string]struct{}
	forceNormalizePhrasesCI []string
	structuralCleanup       titleStructuralCleanup
}

type titleRulesFile struct {
	SchemaVersion           string          `json:"schema_version"`
	ProtectedTokensCI       []string        `json:"protected_tokens_ci"`
	ProtectedTokensExact    []string        `json:"protected_tokens_exact"`
	ProtectedSubstringsCI   []string        `json:"protected_substrings_ci"`
	ForceNormalizeTokensCI  []string        `json:"force_normalize_tokens_ci"`
	ForceNormalizePhrasesCI []string        `json:"force_normalize_phrases_ci"`
	StructuralCleanup       json.RawMessage `json:"structural_cleanup"`
}

type titleStructuralCleanup struct {
	decodeHTMLEntities        bool
	dropZeroWidthFormatChars  bool
	underscoresAsSpaces       bool
	stripLeadingProviderNoise bool
}

type titleStructuralCleanupFile struct {
	DecodeHTMLEntities        *bool `json:"decode_html_entities"`
	DropZeroWidthFormatChars  *bool `json:"drop_zero_width_format_chars"`
	UnderscoresAsSpaces       *bool `json:"underscores_as_spaces"`
	StripLeadingProviderNoise *bool `json:"strip_leading_provider_noise"`
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
		file.ProtectedSubstringsCI == nil ||
		file.ForceNormalizeTokensCI == nil ||
		file.ForceNormalizePhrasesCI == nil {
		return nil, fmt.Errorf("decode title rules: all rule arrays are required")
	}
	if len(file.StructuralCleanup) == 0 {
		return nil, fmt.Errorf("decode title rules: structural_cleanup is required")
	}
	if err := rejectDuplicateTopLevelKeys(file.StructuralCleanup); err != nil {
		return nil, fmt.Errorf("decode title rules: structural_cleanup: %w", err)
	}
	var cleanupFile titleStructuralCleanupFile
	if err := decodeSingleJSONValue(file.StructuralCleanup, &cleanupFile); err != nil {
		return nil, fmt.Errorf("decode title rules: structural_cleanup: %w", err)
	}
	if cleanupFile.DecodeHTMLEntities == nil ||
		cleanupFile.DropZeroWidthFormatChars == nil ||
		cleanupFile.UnderscoresAsSpaces == nil ||
		cleanupFile.StripLeadingProviderNoise == nil {
		return nil, fmt.Errorf("decode title rules: all structural_cleanup booleans are required")
	}

	rules := &TitleRules{
		protectedTokensCI:       make(map[string]struct{}, len(file.ProtectedTokensCI)),
		protectedTokensExact:    make(map[string]struct{}, len(file.ProtectedTokensExact)),
		protectedSubstringsCI:   make([]string, 0, len(file.ProtectedSubstringsCI)),
		forceNormalizeTokensCI:  make(map[string]struct{}, len(file.ForceNormalizeTokensCI)),
		forceNormalizePhrasesCI: make([]string, 0, len(file.ForceNormalizePhrasesCI)),
		structuralCleanup: titleStructuralCleanup{
			decodeHTMLEntities:        *cleanupFile.DecodeHTMLEntities,
			dropZeroWidthFormatChars:  *cleanupFile.DropZeroWidthFormatChars,
			underscoresAsSpaces:       *cleanupFile.UnderscoresAsSpaces,
			stripLeadingProviderNoise: *cleanupFile.StripLeadingProviderNoise,
		},
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
	if err := addCIRules("force_normalize_tokens_ci", file.ForceNormalizeTokensCI, rules.forceNormalizeTokensCI); err != nil {
		return nil, err
	}
	for _, value := range file.ForceNormalizeTokensCI {
		if !isSingleCourseTitleWord(value) {
			return nil, fmt.Errorf("decode title rules: force_normalize_tokens_ci value %q must be exactly one title word", value)
		}
	}
	phrases := make(map[string]struct{}, len(file.ForceNormalizePhrasesCI))
	for _, value := range file.ForceNormalizePhrasesCI {
		normalized, err := normalizeTitleRuleValue("force_normalize_phrases_ci", value)
		if err != nil {
			return nil, err
		}
		words := strings.Fields(normalized)
		if len(words) < 2 {
			return nil, fmt.Errorf("decode title rules: force_normalize_phrases_ci value %q must contain at least two words", value)
		}
		lower := strings.ToLower(strings.Join(words, " "))
		if _, exists := phrases[lower]; exists {
			return nil, fmt.Errorf("decode title rules: duplicate force_normalize_phrases_ci value %q", value)
		}
		phrases[lower] = struct{}{}
		rules.forceNormalizePhrasesCI = append(rules.forceNormalizePhrasesCI, lower)
	}
	for token := range rules.forceNormalizeTokensCI {
		if _, exists := rules.protectedTokensCI[token]; exists {
			return nil, fmt.Errorf("decode title rules: token %q appears in both protected and forced sets", token)
		}
		for exact := range rules.protectedTokensExact {
			if strings.EqualFold(token, exact) {
				return nil, fmt.Errorf("decode title rules: token %q appears in both protected and forced sets", token)
			}
		}
	}

	return rules, nil
}

func isSingleCourseTitleWord(value string) bool {
	for offset := 0; offset < len(value); {
		if !isCourseTitleWordRuneAt(value, offset) {
			return false
		}
		_, size := utf8.DecodeRuneInString(value[offset:])
		offset += size
	}
	return value != ""
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
		protectedTokensCI:       make(map[string]struct{}, len(rules.protectedTokensCI)),
		protectedTokensExact:    make(map[string]struct{}, len(rules.protectedTokensExact)),
		protectedSubstringsCI:   append([]string(nil), rules.protectedSubstringsCI...),
		forceNormalizeTokensCI:  make(map[string]struct{}, len(rules.forceNormalizeTokensCI)),
		forceNormalizePhrasesCI: append([]string(nil), rules.forceNormalizePhrasesCI...),
		structuralCleanup:       rules.structuralCleanup,
	}
	for token := range rules.protectedTokensCI {
		clone.protectedTokensCI[token] = struct{}{}
	}
	for token := range rules.protectedTokensExact {
		clone.protectedTokensExact[token] = struct{}{}
	}
	for token := range rules.forceNormalizeTokensCI {
		clone.forceNormalizeTokensCI[token] = struct{}{}
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
