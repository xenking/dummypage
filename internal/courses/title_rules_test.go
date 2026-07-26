package courses

import (
	"strings"
	"testing"
)

func TestLoadTitleRulesAcceptsStrictV2RulesFile(t *testing.T) {
	rules, err := LoadTitleRules(strings.NewReader(`{
		"schema_version": "title-normalization-rules/v2",
		"protected_tokens_ci": ["nimbus"],
		"protected_tokens_exact": ["Vector"],
		"protected_substrings_ci": ["academy"],
		"force_normalize_tokens_ci": ["klyuchmarker"],
		"force_normalize_phrases_ci": ["novyiy stil"],
		"structural_cleanup": {
			"decode_html_entities": true,
			"drop_zero_width_format_chars": true,
			"underscores_as_spaces": true,
			"strip_leading_provider_noise": true
		}
	}`))
	if err != nil {
		t.Fatalf("LoadTitleRules() error = %v", err)
	}
	if _, ok := rules.protectedTokensCI["nimbus"]; !ok {
		t.Fatal("protectedTokensCI missing nimbus")
	}
	if _, ok := rules.protectedTokensExact["Vector"]; !ok {
		t.Fatal("protectedTokensExact missing Vector")
	}
	if got := strings.Join(rules.protectedSubstringsCI, ","); got != "academy" {
		t.Fatalf("protectedSubstringsCI = %q, want academy", got)
	}
	if _, ok := rules.forceNormalizeTokensCI["klyuchmarker"]; !ok {
		t.Fatal("forceNormalizeTokensCI missing klyuchmarker")
	}
	if got := strings.Join(rules.forceNormalizePhrasesCI, ","); got != "novyiy stil" {
		t.Fatalf("forceNormalizePhrasesCI = %q, want novyiy stil", got)
	}
	if !rules.structuralCleanup.decodeHTMLEntities ||
		!rules.structuralCleanup.dropZeroWidthFormatChars ||
		!rules.structuralCleanup.underscoresAsSpaces ||
		!rules.structuralCleanup.stripLeadingProviderNoise {
		t.Fatalf("structuralCleanup = %+v, want all enabled", rules.structuralCleanup)
	}
}

func TestLoadTitleRulesRejectsInvalidV2Files(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{
			name: "v1 schema",
			json: `{
				"schema_version": "title-normalization-rules/v1",
				"protected_tokens_ci": [],
				"protected_tokens_exact": [],
				"protected_substrings_ci": [],
				"force_normalize_tokens_ci": [],
				"force_normalize_phrases_ci": [],
				"structural_cleanup": {
					"decode_html_entities": false,
					"drop_zero_width_format_chars": false,
					"underscores_as_spaces": false,
					"strip_leading_provider_noise": false
				}
			}`,
		},
		{
			name: "unknown top-level field",
			json: `{
				"schema_version": "title-normalization-rules/v2",
				"protected_tokens_ci": [],
				"protected_tokens_exact": [],
				"protected_substrings_ci": [],
				"force_normalize_tokens_ci": [],
				"force_normalize_phrases_ci": [],
				"structural_cleanup": {
					"decode_html_entities": false,
					"drop_zero_width_format_chars": false,
					"underscores_as_spaces": false,
					"strip_leading_provider_noise": false
				},
				"extra": []
			}`,
		},
		{
			name: "missing force token array",
			json: `{
				"schema_version": "title-normalization-rules/v2",
				"protected_tokens_ci": [],
				"protected_tokens_exact": [],
				"protected_substrings_ci": [],
				"force_normalize_phrases_ci": [],
				"structural_cleanup": {
					"decode_html_entities": false,
					"drop_zero_width_format_chars": false,
					"underscores_as_spaces": false,
					"strip_leading_provider_noise": false
				}
			}`,
		},
		{
			name: "missing protection array",
			json: `{
				"schema_version": "title-normalization-rules/v2",
				"protected_tokens_ci": [],
				"protected_tokens_exact": [],
				"force_normalize_tokens_ci": [],
				"force_normalize_phrases_ci": [],
				"structural_cleanup": {
					"decode_html_entities": false,
					"drop_zero_width_format_chars": false,
					"underscores_as_spaces": false,
					"strip_leading_provider_noise": false
				}
			}`,
		},
		{
			name: "missing structural cleanup",
			json: `{
				"schema_version": "title-normalization-rules/v2",
				"protected_tokens_ci": [],
				"protected_tokens_exact": [],
				"protected_substrings_ci": [],
				"force_normalize_tokens_ci": [],
				"force_normalize_phrases_ci": []
			}`,
		},
		{
			name: "missing structural cleanup boolean",
			json: `{
				"schema_version": "title-normalization-rules/v2",
				"protected_tokens_ci": [],
				"protected_tokens_exact": [],
				"protected_substrings_ci": [],
				"force_normalize_tokens_ci": [],
				"force_normalize_phrases_ci": [],
				"structural_cleanup": {
					"decode_html_entities": false,
					"drop_zero_width_format_chars": false,
					"underscores_as_spaces": false
				}
			}`,
		},
		{
			name: "unknown structural cleanup field",
			json: `{
				"schema_version": "title-normalization-rules/v2",
				"protected_tokens_ci": [],
				"protected_tokens_exact": [],
				"protected_substrings_ci": [],
				"force_normalize_tokens_ci": [],
				"force_normalize_phrases_ci": [],
				"structural_cleanup": {
					"decode_html_entities": false,
					"drop_zero_width_format_chars": false,
					"underscores_as_spaces": false,
					"strip_leading_provider_noise": false,
					"extra": false
				}
			}`,
		},
		{
			name: "duplicate structural cleanup field",
			json: `{
				"schema_version": "title-normalization-rules/v2",
				"protected_tokens_ci": [],
				"protected_tokens_exact": [],
				"protected_substrings_ci": [],
				"force_normalize_tokens_ci": [],
				"force_normalize_phrases_ci": [],
				"structural_cleanup": {
					"decode_html_entities": false,
					"decode_html_entities": true,
					"drop_zero_width_format_chars": false,
					"underscores_as_spaces": false,
					"strip_leading_provider_noise": false
				}
			}`,
		},
		{
			name: "duplicate force token",
			json: `{
				"schema_version": "title-normalization-rules/v2",
				"protected_tokens_ci": [],
				"protected_tokens_exact": [],
				"protected_substrings_ci": [],
				"force_normalize_tokens_ci": ["klyuch", "Klyuch"],
				"force_normalize_phrases_ci": [],
				"structural_cleanup": {
					"decode_html_entities": false,
					"drop_zero_width_format_chars": false,
					"underscores_as_spaces": false,
					"strip_leading_provider_noise": false
				}
			}`,
		},
		{
			name: "duplicate force phrase",
			json: `{
				"schema_version": "title-normalization-rules/v2",
				"protected_tokens_ci": [],
				"protected_tokens_exact": [],
				"protected_substrings_ci": [],
				"force_normalize_tokens_ci": [],
				"force_normalize_phrases_ci": ["novyiy stil", "NOVYIY STIL"],
				"structural_cleanup": {
					"decode_html_entities": false,
					"drop_zero_width_format_chars": false,
					"underscores_as_spaces": false,
					"strip_leading_provider_noise": false
				}
			}`,
		},
		{
			name: "protected and forced token overlap",
			json: `{
				"schema_version": "title-normalization-rules/v2",
				"protected_tokens_ci": ["klyuch"],
				"protected_tokens_exact": [],
				"protected_substrings_ci": [],
				"force_normalize_tokens_ci": ["KLYUCH"],
				"force_normalize_phrases_ci": [],
				"structural_cleanup": {
					"decode_html_entities": false,
					"drop_zero_width_format_chars": false,
					"underscores_as_spaces": false,
					"strip_leading_provider_noise": false
				}
			}`,
		},
		{
			name: "trailing json value",
			json: `{
				"schema_version": "title-normalization-rules/v2",
				"protected_tokens_ci": [],
				"protected_tokens_exact": [],
				"protected_substrings_ci": [],
				"force_normalize_tokens_ci": [],
				"force_normalize_phrases_ci": [],
				"structural_cleanup": {
					"decode_html_entities": false,
					"drop_zero_width_format_chars": false,
					"underscores_as_spaces": false,
					"strip_leading_provider_noise": false
				}
			}{}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := LoadTitleRules(strings.NewReader(test.json)); err == nil {
				t.Fatal("LoadTitleRules() error = nil, want error")
			}
		})
	}
}

func TestLoadTitleRulesRejectsDuplicateTopLevelKeys(t *testing.T) {
	_, err := LoadTitleRules(strings.NewReader(`{
		"schema_version": "title-normalization-rules/v2",
		"schema_version": "title-normalization-rules/v2",
		"protected_tokens_ci": [],
		"protected_tokens_exact": [],
		"protected_substrings_ci": [],
		"force_normalize_tokens_ci": [],
		"force_normalize_phrases_ci": [],
		"structural_cleanup": {
			"decode_html_entities": false,
			"drop_zero_width_format_chars": false,
			"underscores_as_spaces": false,
			"strip_leading_provider_noise": false
		}
	}`))
	if err == nil {
		t.Fatal("LoadTitleRules() error = nil, want duplicate key error")
	}
	if got := err.Error(); !strings.Contains(got, "decode title rules") || strings.Contains(got, "link tombstones") {
		t.Fatalf("LoadTitleRules() error = %q, want title rules context only", got)
	}
}

func TestLoadTitleRulesRejectsInvalidRuleValues(t *testing.T) {
	tests := []struct {
		name  string
		field string
		value string
	}{
		{name: "empty value", field: "protected_tokens_ci", value: `[""]`},
		{name: "untrimmed value", field: "protected_tokens_ci", value: `[" nimbus"]`},
		{name: "case insensitive duplicate", field: "protected_tokens_ci", value: `["nimbus","Nimbus"]`},
		{name: "exact duplicate", field: "protected_tokens_exact", value: `["Vector","Vector"]`},
		{name: "substring duplicate", field: "protected_substrings_ci", value: `["academy","Academy"]`},
		{name: "multiword force token", field: "force_normalize_tokens_ci", value: `["novyiy stil"]`},
		{name: "punctuation-split force token", field: "force_normalize_tokens_ci", value: `["alpha-beta"]`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := `{
				"schema_version": "title-normalization-rules/v2",
				"protected_tokens_ci": [],
				"protected_tokens_exact": [],
				"protected_substrings_ci": [],
				"force_normalize_tokens_ci": [],
				"force_normalize_phrases_ci": [],
				"structural_cleanup": {
					"decode_html_entities": false,
					"drop_zero_width_format_chars": false,
					"underscores_as_spaces": false,
					"strip_leading_provider_noise": false
				}
			}`
			payload = strings.Replace(payload, `"`+test.field+`": []`, `"`+test.field+`": `+test.value, 1)
			if _, err := LoadTitleRules(strings.NewReader(payload)); err == nil {
				t.Fatal("LoadTitleRules() error = nil, want error")
			}
		})
	}
}

func TestLoadTitleRulesRejectsProtectedTokenOverlap(t *testing.T) {
	_, err := LoadTitleRules(strings.NewReader(`{
		"schema_version": "title-normalization-rules/v2",
		"protected_tokens_ci": ["nimbus"],
		"protected_tokens_exact": ["Nimbus"],
		"protected_substrings_ci": [],
		"force_normalize_tokens_ci": [],
		"force_normalize_phrases_ci": [],
		"structural_cleanup": {
			"decode_html_entities": false,
			"drop_zero_width_format_chars": false,
			"underscores_as_spaces": false,
			"strip_leading_provider_noise": false
		}
	}`))
	if err == nil {
		t.Fatal("LoadTitleRules() error = nil, want overlap error")
	}
}

func TestLoadTitleRulesRejectsInvalidUTF8(t *testing.T) {
	if _, err := LoadTitleRules(strings.NewReader("{\"schema_version\":\"title-normalization-rules/v2\",\"protected_tokens_ci\":[\"\xff\"],\"protected_tokens_exact\":[],\"protected_substrings_ci\":[],\"force_normalize_tokens_ci\":[],\"force_normalize_phrases_ci\":[],\"structural_cleanup\":{\"decode_html_entities\":false,\"drop_zero_width_format_chars\":false,\"underscores_as_spaces\":false,\"strip_leading_provider_noise\":false}}")); err == nil {
		t.Fatal("LoadTitleRules() error = nil, want error")
	}
}
