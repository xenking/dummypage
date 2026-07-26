package courses

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestLoadLinkTombstonesAcceptsStrictHashOnlyFile(t *testing.T) {
	hash := hashForTest(t, "https://example.test/course")

	tombstones, err := LoadLinkTombstones(strings.NewReader(`{
		"schema_version":"link-tombstones/v1",
		"canonicalization_version":1,
		"links":[{"sha256":"` + hash + `","reason":"expired","confirmed_at":"2026-07-26T12:34:56Z"}]
	}`))
	if err != nil {
		t.Fatalf("LoadLinkTombstones() error = %v", err)
	}
	if !tombstones.ContainsURL("HTTPS://Example.Test/course#fragment") {
		t.Fatal("ContainsURL() = false, want true for canonical URL variant")
	}
}

func TestLoadLinkTombstonesAcceptsEmptyLinks(t *testing.T) {
	tombstones, err := LoadLinkTombstones(strings.NewReader(`{
		"schema_version":"link-tombstones/v1",
		"canonicalization_version":1,
		"links":[]
	}`))
	if err != nil {
		t.Fatalf("LoadLinkTombstones() error = %v", err)
	}
	if tombstones.ContainsURL("https://example.test/course") {
		t.Fatal("empty tombstones unexpectedly matched URL")
	}
}

func TestLoadLinkTombstonesRejectsInvalidFiles(t *testing.T) {
	validHash := hashForTest(t, "https://example.test/course")
	tests := []struct {
		name string
		json string
	}{
		{"invalid utf8", "{\"schema_version\":\"link-tombstones/v1\",\"canonicalization_version\":1,\"links\":[\"\xff\"]}"},
		{"unknown top-level field", `{"schema_version":"link-tombstones/v1","canonicalization_version":1,"links":[],"extra":true}`},
		{"multiple json values", `{"schema_version":"link-tombstones/v1","canonicalization_version":1,"links":[]} {}`},
		{"duplicate top-level key", `{"schema_version":"link-tombstones/v1","schema_version":"link-tombstones/v1","canonicalization_version":1,"links":[]}`},
		{"wrong schema", `{"schema_version":"bad","canonicalization_version":1,"links":[]}`},
		{"wrong canonicalization", `{"schema_version":"link-tombstones/v1","canonicalization_version":2,"links":[]}`},
		{"missing links", `{"schema_version":"link-tombstones/v1","canonicalization_version":1}`},
		{"empty hash", `{"schema_version":"link-tombstones/v1","canonicalization_version":1,"links":[{"sha256":"","reason":"manual","confirmed_at":"2026-07-26T00:00:00Z"}]}`},
		{"uppercase hash", `{"schema_version":"link-tombstones/v1","canonicalization_version":1,"links":[{"sha256":"` + strings.ToUpper(validHash) + `","reason":"manual","confirmed_at":"2026-07-26T00:00:00Z"}]}`},
		{"short hash", `{"schema_version":"link-tombstones/v1","canonicalization_version":1,"links":[{"sha256":"abc","reason":"manual","confirmed_at":"2026-07-26T00:00:00Z"}]}`},
		{"unknown reason", `{"schema_version":"link-tombstones/v1","canonicalization_version":1,"links":[{"sha256":"` + validHash + `","reason":"bad","confirmed_at":"2026-07-26T00:00:00Z"}]}`},
		{"invalid timestamp", `{"schema_version":"link-tombstones/v1","canonicalization_version":1,"links":[{"sha256":"` + validHash + `","reason":"manual","confirmed_at":"not-time"}]}`},
		{"duplicate link key", `{"schema_version":"link-tombstones/v1","canonicalization_version":1,"links":[{"sha256":"` + validHash + `","sha256":"` + validHash + `","reason":"manual","confirmed_at":"2026-07-26T00:00:00Z"}]}`},
		{"duplicate hash", `{"schema_version":"link-tombstones/v1","canonicalization_version":1,"links":[{"sha256":"` + validHash + `","reason":"manual","confirmed_at":"2026-07-26T00:00:00Z"},{"sha256":"` + validHash + `","reason":"manual","confirmed_at":"2026-07-26T00:00:00Z"}]}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := LoadLinkTombstones(strings.NewReader(test.json)); err == nil {
				t.Fatal("LoadLinkTombstones() error = nil, want error")
			}
		})
	}
}

func TestLoadLinkTombstonesDuplicateKeyErrorsIdentifyLinkTombstones(t *testing.T) {
	validHash := hashForTest(t, "https://example.test/course")
	tests := []struct {
		name string
		json string
	}{
		{
			name: "top level",
			json: `{"schema_version":"link-tombstones/v1","schema_version":"link-tombstones/v1","canonicalization_version":1,"links":[]}`,
		},
		{
			name: "link record",
			json: `{"schema_version":"link-tombstones/v1","canonicalization_version":1,"links":[{"sha256":"` + validHash + `","sha256":"` + validHash + `","reason":"manual","confirmed_at":"2026-07-26T00:00:00Z"}]}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := LoadLinkTombstones(strings.NewReader(test.json))
			if err == nil {
				t.Fatal("LoadLinkTombstones() error = nil, want duplicate key error")
			}
			if got := err.Error(); !strings.Contains(got, "decode link tombstones") || strings.Contains(got, "title rules") {
				t.Fatalf("LoadLinkTombstones() error = %q, want link tombstones context only", got)
			}
		})
	}
}

func TestLinkTombstonesContainsURLUsesCanonicalHashVariants(t *testing.T) {
	hash := hashForTest(t, "https://example.test:443/course?b=2&a=1#old")
	tombstones := &LinkTombstones{hashes: map[string]struct{}{hash: {}}}

	if !tombstones.ContainsURL("https://EXAMPLE.TEST/course?b=2&a=1#new") {
		t.Fatal("ContainsURL() = false, want true for canonical HTTPS variant")
	}
	if tombstones.ContainsURL("https://example.test/course?a=1&b=2") {
		t.Fatal("ContainsURL() = true, want false for distinct canonical query order")
	}
	var nilTombstones *LinkTombstones
	if nilTombstones.ContainsURL("https://example.test/course") {
		t.Fatal("nil ContainsURL() = true, want false")
	}
	if tombstones.ContainsURL("javascript:alert(1)") {
		t.Fatal("invalid URL matched tombstone")
	}
}

func TestLinkTombstonesMergeEligibleAuditResultsPreservesAndSortsRecords(t *testing.T) {
	existingHash := hashForTest(t, "https://example.test/existing")
	expiredHash := hashForTest(t, "https://example.test/expired")
	mismatchHash := hashForTest(t, "https://example.test/mismatch")
	ineligibleHash := hashForTest(t, "https://example.test/ineligible")
	tombstones, err := LoadLinkTombstones(strings.NewReader(`{
		"schema_version":"link-tombstones/v1",
		"canonicalization_version":1,
		"links":[{"sha256":"` + existingHash + `","reason":"manual","confirmed_at":"2026-07-25T00:00:00Z"}]
	}`))
	if err != nil {
		t.Fatalf("LoadLinkTombstones() error = %v", err)
	}

	added := tombstones.MergeEligibleAuditResults(&LinkAuditReport{
		Results: []LinkAuditResult{
			{SHA256: expiredHash, State: LinkAuditStateExpired, Confirmations: 2},
			{SHA256: mismatchHash, State: LinkAuditStateContentMismatch, Confirmations: 3},
			{SHA256: ineligibleHash, State: LinkAuditStateExpired, Confirmations: 1},
			{SHA256: existingHash, State: LinkAuditStateExpired, Confirmations: 9},
		},
	}, 2, time.Date(2026, 7, 27, 1, 2, 3, 0, time.FixedZone("UTC+10", 10*60*60)))
	if added != 2 {
		t.Fatalf("MergeEligibleAuditResults() = %d, want 2", added)
	}
	if tombstones.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", tombstones.Len())
	}

	payload, err := json.Marshal(tombstones)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var file struct {
		Links []struct {
			SHA256      string `json:"sha256"`
			Reason      string `json:"reason"`
			ConfirmedAt string `json:"confirmed_at"`
		} `json:"links"`
	}
	if err := json.Unmarshal(payload, &file); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(file.Links) != 3 {
		t.Fatalf("links = %d, want 3", len(file.Links))
	}
	for index := 1; index < len(file.Links); index++ {
		if file.Links[index-1].SHA256 >= file.Links[index].SHA256 {
			t.Fatalf("links are not sorted: %s", payload)
		}
	}
	byHash := make(map[string]struct {
		Reason      string
		ConfirmedAt string
	})
	for _, record := range file.Links {
		byHash[record.SHA256] = struct {
			Reason      string
			ConfirmedAt string
		}{record.Reason, record.ConfirmedAt}
	}
	if got := byHash[existingHash]; got.Reason != "manual" || got.ConfirmedAt != "2026-07-25T00:00:00Z" {
		t.Fatalf("existing record changed: %+v", got)
	}
	if got := byHash[expiredHash]; got.Reason != "expired" || got.ConfirmedAt != "2026-07-26T15:02:03Z" {
		t.Fatalf("expired record = %+v", got)
	}
	if got := byHash[mismatchHash]; got.Reason != "content_mismatch" {
		t.Fatalf("mismatch record = %+v", got)
	}
}

func hashForTest(t *testing.T, rawURL string) string {
	t.Helper()

	hash, err := linkHash(rawURL)
	if err != nil {
		t.Fatalf("hash %q: %v", rawURL, err)
	}
	return hash
}
