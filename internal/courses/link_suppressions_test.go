package courses

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLoadLinkSuppressionsMatchesOnlyTheConfiguredEntryOccurrence(t *testing.T) {
	hash := hashForTest(t, "https://example.test/course")
	suppressions, err := LoadLinkSuppressions(strings.NewReader(`{
		"schema_version":"link-suppressions/v1",
		"canonicalization_version":1,
		"suppressions":[{
			"source_entry_id":"1:1:0",
			"sha256":"` + hash + `",
			"reason":"content_mismatch",
			"confirmed_at":"2026-07-27T00:00:00Z"
		}]
	}`))
	if err != nil {
		t.Fatalf("LoadLinkSuppressions() error = %v", err)
	}
	if !suppressions.ContainsURL("1:1:0", "HTTPS://Example.Test:443/course#fragment") {
		t.Fatal("ContainsURL() = false, want canonical URL occurrence match")
	}
	if suppressions.ContainsURL("1:2:0", "https://example.test/course") {
		t.Fatal("ContainsURL() = true for a different source_entry_id")
	}
	if suppressions.ContainsURL("1:1:0", "https://example.test/other") {
		t.Fatal("ContainsURL() = true for a different URL")
	}
	var nilSuppressions *LinkSuppressions
	if nilSuppressions.ContainsURL("1:1:0", "https://example.test/course") {
		t.Fatal("nil ContainsURL() = true")
	}
}

func TestLoadLinkSuppressionsAllowsTheSameHashForDifferentEntries(t *testing.T) {
	hash := hashForTest(t, "https://example.test/shared")
	suppressions, err := LoadLinkSuppressions(strings.NewReader(`{
		"schema_version":"link-suppressions/v1",
		"canonicalization_version":1,
		"suppressions":[
			{"source_entry_id":"1:1:0","sha256":"` + hash + `","reason":"manual","confirmed_at":"2026-07-27T00:00:00Z"},
			{"source_entry_id":"1:2:0","sha256":"` + hash + `","reason":"content_mismatch","confirmed_at":"2026-07-27T01:00:00+01:00"}
		]
	}`))
	if err != nil {
		t.Fatalf("LoadLinkSuppressions() error = %v", err)
	}
	if !suppressions.ContainsURL("1:1:0", "https://example.test/shared") ||
		!suppressions.ContainsURL("1:2:0", "https://example.test/shared") {
		t.Fatal("same canonical hash did not match both configured entry occurrences")
	}
}

func TestLoadLinkSuppressionsAcceptsEmptySuppressions(t *testing.T) {
	suppressions, err := LoadLinkSuppressions(strings.NewReader(`{
		"schema_version":"link-suppressions/v1",
		"canonicalization_version":1,
		"suppressions":[]
	}`))
	if err != nil {
		t.Fatalf("LoadLinkSuppressions() error = %v", err)
	}
	if suppressions.ContainsURL("1:1:0", "https://example.test/course") {
		t.Fatal("empty suppressions unexpectedly matched")
	}
}

func TestLoadLinkSuppressionsRejectsInvalidFiles(t *testing.T) {
	hash := hashForTest(t, "https://example.test/course")
	record := `{"source_entry_id":"1:1:0","sha256":"` + hash + `","reason":"manual","confirmed_at":"2026-07-27T00:00:00Z"}`
	tests := []struct {
		name string
		json string
	}{
		{"invalid utf8", "{\"schema_version\":\"link-suppressions/v1\",\"canonicalization_version\":1,\"suppressions\":[\"\xff\"]}"},
		{"unknown top-level field", `{"schema_version":"link-suppressions/v1","canonicalization_version":1,"suppressions":[],"extra":true}`},
		{"multiple json values", `{"schema_version":"link-suppressions/v1","canonicalization_version":1,"suppressions":[]} {}`},
		{"duplicate top-level key", `{"schema_version":"link-suppressions/v1","schema_version":"link-suppressions/v1","canonicalization_version":1,"suppressions":[]}`},
		{"wrong schema", `{"schema_version":"bad","canonicalization_version":1,"suppressions":[]}`},
		{"wrong canonicalization", `{"schema_version":"link-suppressions/v1","canonicalization_version":2,"suppressions":[]}`},
		{"missing suppressions", `{"schema_version":"link-suppressions/v1","canonicalization_version":1}`},
		{"null suppressions", `{"schema_version":"link-suppressions/v1","canonicalization_version":1,"suppressions":null}`},
		{"ambiguous entry id", `{"schema_version":"link-suppressions/v1","canonicalization_version":1,"suppressions":[{"entry_id":"1:1:0","sha256":"` + hash + `","reason":"manual","confirmed_at":"2026-07-27T00:00:00Z"}]}`},
		{"empty source entry id", `{"schema_version":"link-suppressions/v1","canonicalization_version":1,"suppressions":[{"source_entry_id":"","sha256":"` + hash + `","reason":"manual","confirmed_at":"2026-07-27T00:00:00Z"}]}`},
		{"whitespace source entry id", `{"schema_version":"link-suppressions/v1","canonicalization_version":1,"suppressions":[{"source_entry_id":" ","sha256":"` + hash + `","reason":"manual","confirmed_at":"2026-07-27T00:00:00Z"}]}`},
		{"empty hash", `{"schema_version":"link-suppressions/v1","canonicalization_version":1,"suppressions":[{"source_entry_id":"1:1:0","sha256":"","reason":"manual","confirmed_at":"2026-07-27T00:00:00Z"}]}`},
		{"uppercase hash", `{"schema_version":"link-suppressions/v1","canonicalization_version":1,"suppressions":[{"source_entry_id":"1:1:0","sha256":"` + strings.ToUpper(hash) + `","reason":"manual","confirmed_at":"2026-07-27T00:00:00Z"}]}`},
		{"short hash", `{"schema_version":"link-suppressions/v1","canonicalization_version":1,"suppressions":[{"source_entry_id":"1:1:0","sha256":"abc","reason":"manual","confirmed_at":"2026-07-27T00:00:00Z"}]}`},
		{"empty reason", `{"schema_version":"link-suppressions/v1","canonicalization_version":1,"suppressions":[{"source_entry_id":"1:1:0","sha256":"` + hash + `","reason":"","confirmed_at":"2026-07-27T00:00:00Z"}]}`},
		{"unsupported reason", `{"schema_version":"link-suppressions/v1","canonicalization_version":1,"suppressions":[{"source_entry_id":"1:1:0","sha256":"` + hash + `","reason":"expired","confirmed_at":"2026-07-27T00:00:00Z"}]}`},
		{"empty timestamp", `{"schema_version":"link-suppressions/v1","canonicalization_version":1,"suppressions":[{"source_entry_id":"1:1:0","sha256":"` + hash + `","reason":"manual","confirmed_at":""}]}`},
		{"invalid timestamp", `{"schema_version":"link-suppressions/v1","canonicalization_version":1,"suppressions":[{"source_entry_id":"1:1:0","sha256":"` + hash + `","reason":"manual","confirmed_at":"not-time"}]}`},
		{"unknown record field", `{"schema_version":"link-suppressions/v1","canonicalization_version":1,"suppressions":[{"source_entry_id":"1:1:0","sha256":"` + hash + `","reason":"manual","confirmed_at":"2026-07-27T00:00:00Z","url":"https://private.invalid"}]}`},
		{"duplicate record key", `{"schema_version":"link-suppressions/v1","canonicalization_version":1,"suppressions":[{"source_entry_id":"1:1:0","source_entry_id":"1:1:0","sha256":"` + hash + `","reason":"manual","confirmed_at":"2026-07-27T00:00:00Z"}]}`},
		{"duplicate record", `{"schema_version":"link-suppressions/v1","canonicalization_version":1,"suppressions":[` + record + `,` + record + `]}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := LoadLinkSuppressions(strings.NewReader(test.json)); err == nil {
				t.Fatal("LoadLinkSuppressions() error = nil, want error")
			}
		})
	}
}

func TestLinkSuppressionsMarshalJSONSortsByEntryAndHashWithoutRawLinkData(t *testing.T) {
	firstHash := hashForTest(t, "https://example.test/first")
	secondHash := hashForTest(t, "https://example.test/second")
	suppressions, err := LoadLinkSuppressions(strings.NewReader(`{
		"schema_version":"link-suppressions/v1",
		"canonicalization_version":1,
		"suppressions":[
			{"source_entry_id":"2:1:0","sha256":"` + firstHash + `","reason":"manual","confirmed_at":"2026-07-27T02:00:00Z"},
			{"source_entry_id":"1:1:0","sha256":"` + secondHash + `","reason":"content_mismatch","confirmed_at":"2026-07-27T01:00:00Z"},
			{"source_entry_id":"1:1:0","sha256":"` + firstHash + `","reason":"manual","confirmed_at":"2026-07-27T00:00:00Z"}
		]
	}`))
	if err != nil {
		t.Fatalf("LoadLinkSuppressions() error = %v", err)
	}

	payload, err := json.Marshal(suppressions)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	want := `{"schema_version":"link-suppressions/v1","canonicalization_version":1,"suppressions":[` +
		`{"source_entry_id":"1:1:0","sha256":"` + firstHash + `","reason":"manual","confirmed_at":"2026-07-27T00:00:00Z"},` +
		`{"source_entry_id":"1:1:0","sha256":"` + secondHash + `","reason":"content_mismatch","confirmed_at":"2026-07-27T01:00:00Z"},` +
		`{"source_entry_id":"2:1:0","sha256":"` + firstHash + `","reason":"manual","confirmed_at":"2026-07-27T02:00:00Z"}]}`
	if string(payload) != want {
		t.Fatalf("Marshal() = %s, want %s", payload, want)
	}
}

func linkSuppressionsForTest(t *testing.T, sourceEntryID, rawURL string) *LinkSuppressions {
	t.Helper()

	suppressions, err := LoadLinkSuppressions(strings.NewReader(`{
		"schema_version":"link-suppressions/v1",
		"canonicalization_version":1,
		"suppressions":[{
			"source_entry_id":"` + sourceEntryID + `",
			"sha256":"` + hashForTest(t, rawURL) + `",
			"reason":"manual",
			"confirmed_at":"2026-07-27T00:00:00Z"
		}]
	}`))
	if err != nil {
		t.Fatalf("LoadLinkSuppressions() error = %v", err)
	}
	return suppressions
}
