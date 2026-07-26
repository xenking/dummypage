package courses

import (
	"strings"
	"testing"
)

func TestLoadTitleRulesAcceptsStrictRulesFile(t *testing.T) {
	rules, err := LoadTitleRules(strings.NewReader(`{
		"schema_version": "title-normalization-rules/v1",
		"protected_tokens_ci": ["nimbus"],
		"protected_tokens_exact": ["Vector"],
		"protected_substrings_ci": ["academy"]
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
}

func TestLoadTitleRulesRejectsInvalidFiles(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{
			name: "wrong schema",
			json: `{
				"schema_version": "title-normalization-rules/v2",
				"protected_tokens_ci": [],
				"protected_tokens_exact": [],
				"protected_substrings_ci": []
			}`,
		},
		{
			name: "unknown field",
			json: `{
				"schema_version": "title-normalization-rules/v1",
				"protected_tokens_ci": [],
				"protected_tokens_exact": [],
				"protected_substrings_ci": [],
				"extra": []
			}`,
		},
		{
			name: "trailing json value",
			json: `{
				"schema_version": "title-normalization-rules/v1",
				"protected_tokens_ci": [],
				"protected_tokens_exact": [],
				"protected_substrings_ci": []
			}{}`,
		},
		{
			name: "empty value",
			json: `{
				"schema_version": "title-normalization-rules/v1",
				"protected_tokens_ci": [""],
				"protected_tokens_exact": [],
				"protected_substrings_ci": []
			}`,
		},
		{
			name: "untrimmed value",
			json: `{
				"schema_version": "title-normalization-rules/v1",
				"protected_tokens_ci": [" nimbus"],
				"protected_tokens_exact": [],
				"protected_substrings_ci": []
			}`,
		},
		{
			name: "case insensitive duplicate",
			json: `{
				"schema_version": "title-normalization-rules/v1",
				"protected_tokens_ci": ["nimbus", "Nimbus"],
				"protected_tokens_exact": [],
				"protected_substrings_ci": []
			}`,
		},
		{
			name: "exact duplicate",
			json: `{
				"schema_version": "title-normalization-rules/v1",
				"protected_tokens_ci": [],
				"protected_tokens_exact": ["Vector", "Vector"],
				"protected_substrings_ci": []
			}`,
		},
		{
			name: "substring duplicate",
			json: `{
				"schema_version": "title-normalization-rules/v1",
				"protected_tokens_ci": [],
				"protected_tokens_exact": [],
				"protected_substrings_ci": ["academy", "Academy"]
			}`,
		},
		{
			name: "contradictory ci exact token overlap",
			json: `{
				"schema_version": "title-normalization-rules/v1",
				"protected_tokens_ci": ["nimbus"],
				"protected_tokens_exact": ["Nimbus"],
				"protected_substrings_ci": []
			}`,
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
	tests := []struct {
		name string
		json string
	}{
		{
			name: "schema version",
			json: `{
				"schema_version": "title-normalization-rules/v1",
				"schema_version": "title-normalization-rules/v1",
				"protected_tokens_ci": [],
				"protected_tokens_exact": [],
				"protected_substrings_ci": []
			}`,
		},
		{
			name: "case insensitive tokens",
			json: `{
				"schema_version": "title-normalization-rules/v1",
				"protected_tokens_ci": ["nimbus"],
				"protected_tokens_ci": ["vector"],
				"protected_tokens_exact": [],
				"protected_substrings_ci": []
			}`,
		},
		{
			name: "exact tokens",
			json: `{
				"schema_version": "title-normalization-rules/v1",
				"protected_tokens_ci": [],
				"protected_tokens_exact": ["Vector"],
				"protected_tokens_exact": ["Nimbus"],
				"protected_substrings_ci": []
			}`,
		},
		{
			name: "case insensitive substrings",
			json: `{
				"schema_version": "title-normalization-rules/v1",
				"protected_tokens_ci": [],
				"protected_tokens_exact": [],
				"protected_substrings_ci": ["academy"],
				"protected_substrings_ci": ["studio"]
			}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadTitleRules(strings.NewReader(test.json))
			if err == nil {
				t.Fatal("LoadTitleRules() error = nil, want duplicate key error")
			}
			if got := err.Error(); !strings.Contains(got, "decode title rules") || strings.Contains(got, "link tombstones") {
				t.Fatalf("LoadTitleRules() error = %q, want title rules context only", got)
			}
		})
	}
}

func TestLoadTitleRulesRejectsInvalidUTF8(t *testing.T) {
	if _, err := LoadTitleRules(strings.NewReader("{\"schema_version\":\"title-normalization-rules/v1\",\"protected_tokens_ci\":[\"\xff\"],\"protected_tokens_exact\":[],\"protected_substrings_ci\":[]}")); err == nil {
		t.Fatal("LoadTitleRules() error = nil, want error")
	}
}
