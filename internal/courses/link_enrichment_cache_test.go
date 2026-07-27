package courses

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

func TestLoadLinkEnrichmentCacheLookupAndFreshness(t *testing.T) {
	hash := mustLinkHash(t, "https://assets.example.test/bundle")
	cache, err := LoadLinkEnrichmentCache(strings.NewReader(`{
		"schema_version":"link-enrichment/v1",
		"extractor_version":1,
		"generated_at":"2026-07-26T00:00:00Z",
		"entries":[{
			"sha256":"` + hash + `",
			"state":"extracted",
			"checked_at":"2026-07-26T00:00:00Z",
			"content":{
				"name":"Example bundle",
				"kind":"folder",
				"size_bytes":42,
				"file_count":1,
				"folder_count":0,
				"items":[{"name":"lesson.mp4","kind":"file","size_bytes":42}],
				"material_types":["video"]
			}
		}]
	}`))
	if err != nil {
		t.Fatalf("LoadLinkEnrichmentCache() error = %v", err)
	}

	content, ok := cache.ContentForURL("https://ASSETS.example.test:443/bundle#fragment")
	if !ok || content.Name != "Example bundle" || len(content.Items) != 1 {
		t.Fatalf("ContentForURL() = (%+v, %v)", content, ok)
	}
	if !cache.IsFreshURL("https://assets.example.test/bundle", time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC), 24*time.Hour) {
		t.Fatal("IsFreshURL() = false, want true")
	}
	if cache.IsFreshURL("https://assets.example.test/bundle", time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC), 24*time.Hour) {
		t.Fatal("stale IsFreshURL() = true")
	}
}

func TestLinkEnrichmentCacheTracksNotFoundWithoutCatalogContent(t *testing.T) {
	hash := mustLinkHash(t, "https://assets.example.test/no-metadata")
	cache, err := LoadLinkEnrichmentCache(strings.NewReader(`{
		"schema_version":"link-enrichment/v1",
		"extractor_version":1,
		"generated_at":"2026-07-26T00:00:00Z",
		"entries":[{"sha256":"` + hash + `","state":"not_found","checked_at":"2026-07-26T00:00:00Z"}]
	}`))
	if err != nil {
		t.Fatalf("LoadLinkEnrichmentCache() error = %v", err)
	}
	if _, ok := cache.ContentForURL("https://assets.example.test/no-metadata"); ok {
		t.Fatal("not_found record returned content")
	}
	if !cache.IsFreshURL("https://assets.example.test/no-metadata", time.Date(2026, 7, 26, 1, 0, 0, 0, time.UTC), 24*time.Hour) {
		t.Fatal("not_found record was not cached")
	}
}

func TestLinkEnrichmentCacheMarshalIsDeterministicAndHashOnly(t *testing.T) {
	cache := NewLinkEnrichmentCache(time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC))
	second := strings.Repeat("b", 64)
	first := strings.Repeat("a", 64)
	if err := cache.PutNotFound(second, time.Date(2026, 7, 26, 0, 0, 2, 0, time.UTC)); err != nil {
		t.Fatalf("PutNotFound() error = %v", err)
	}
	if err := cache.PutExtracted(first, LinkContent{Name: "Bundle"}, time.Date(2026, 7, 26, 0, 0, 1, 0, time.UTC)); err != nil {
		t.Fatalf("PutExtracted() error = %v", err)
	}

	var one bytes.Buffer
	var two bytes.Buffer
	if err := WriteLinkEnrichmentCache(&one, cache); err != nil {
		t.Fatalf("WriteLinkEnrichmentCache(one) error = %v", err)
	}
	if err := WriteLinkEnrichmentCache(&two, cache); err != nil {
		t.Fatalf("WriteLinkEnrichmentCache(two) error = %v", err)
	}
	if one.String() != two.String() {
		t.Fatal("cache serialization is not deterministic")
	}
	if strings.Index(one.String(), first) > strings.Index(one.String(), second) {
		t.Fatalf("entries are not sorted: %s", one.String())
	}
	for _, forbidden := range []string{"https://", "assets.example.test", "raw_html", `"url"`, `"host"`} {
		if strings.Contains(one.String(), forbidden) {
			t.Fatalf("serialized cache contains forbidden value %q", forbidden)
		}
	}
}

func TestLoadLinkEnrichmentCacheRejectsInvalidFiles(t *testing.T) {
	hash := strings.Repeat("a", 64)
	valid := `{
		"schema_version":"link-enrichment/v1",
		"extractor_version":1,
		"generated_at":"2026-07-26T00:00:00Z",
		"entries":[{"sha256":"` + hash + `","state":"not_found","checked_at":"2026-07-26T00:00:00Z"}]
	}`
	tests := []struct {
		name string
		json string
	}{
		{"wrong schema", strings.Replace(valid, "link-enrichment/v1", "bad", 1)},
		{"wrong extractor version", strings.Replace(valid, `"extractor_version":1`, `"extractor_version":2`, 1)},
		{"unknown top-level", strings.Replace(valid, `"entries"`, `"url":"https://assets.example.test/private","entries"`, 1)},
		{"duplicate top-level", strings.Replace(valid, `"entries"`, `"generated_at":"2026-07-26T00:00:00Z","entries"`, 1)},
		{"invalid generated at", strings.Replace(valid, `"2026-07-26T00:00:00Z"`, `"bad"`, 1)},
		{"invalid hash", strings.Replace(valid, hash, "abc", 1)},
		{"invalid state", strings.Replace(valid, `"not_found"`, `"transient"`, 1)},
		{"invalid checked at", strings.Replace(valid, `"checked_at":"2026-07-26T00:00:00Z"`, `"checked_at":"bad"`, 1)},
		{"content on not found", strings.Replace(valid, `"checked_at":"2026-07-26T00:00:00Z"`, `"checked_at":"2026-07-26T00:00:00Z","content":{"name":"x"}`, 1)},
		{"missing extracted content", strings.Replace(valid, `"state":"not_found"`, `"state":"extracted"`, 1)},
		{"unknown entry field", strings.Replace(valid, `"state"`, `"raw_html":"secret","state"`, 1)},
		{"duplicate hash", `{"schema_version":"link-enrichment/v1","extractor_version":1,"generated_at":"2026-07-26T00:00:00Z","entries":[{"sha256":"` + hash + `","state":"not_found","checked_at":"2026-07-26T00:00:00Z"},{"sha256":"` + hash + `","state":"not_found","checked_at":"2026-07-26T00:00:00Z"}]}`},
		{"url content string", strings.Replace(valid, `"state":"not_found","checked_at"`, `"state":"extracted","content":{"name":"https://private.example/path"},"checked_at"`, 1)},
		{"html content string", strings.Replace(valid, `"state":"not_found","checked_at"`, `"state":"extracted","content":{"name":"<script>secret</script>"},"checked_at"`, 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := LoadLinkEnrichmentCache(strings.NewReader(test.json)); err == nil {
				t.Fatal("LoadLinkEnrichmentCache() error = nil")
			}
		})
	}
}

func TestLinkEnrichmentCachePutAndWriteRevalidatePrivateSafeInvariants(t *testing.T) {
	cache := NewLinkEnrichmentCache(time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC))
	hash := strings.Repeat("a", 64)
	for _, name := range []string{
		"https://private.example/path",
		"http:private",
		"mailto:user@example.com",
		"file:/private/path",
		"urn:secret",
		"<div>raw html</div>",
		"bad\u0001value",
	} {
		if err := cache.PutExtracted(hash, LinkContent{Name: name}, time.Now()); err == nil {
			t.Fatalf("PutExtracted(%q) error = nil", name)
		}
	}
	if err := cache.PutExtracted(hash, LinkContent{Name: "Example bundle"}, time.Now()); err != nil {
		t.Fatalf("PutExtracted() error = %v", err)
	}
	cache.Entries = append(cache.Entries, cache.Entries[0])
	if err := WriteLinkEnrichmentCache(io.Discard, cache); err == nil {
		t.Fatal("WriteLinkEnrichmentCache() accepted duplicate mutated entry")
	}
}

func TestLoadLinkEnrichmentCacheEnforcesConfiguredCaps(t *testing.T) {
	hash := strings.Repeat("a", 64)
	payload := `{"schema_version":"link-enrichment/v1","extractor_version":1,"generated_at":"2026-07-26T00:00:00Z","entries":[{"sha256":"` + hash + `","state":"not_found","checked_at":"2026-07-26T00:00:00Z"}]}`
	if _, err := loadLinkEnrichmentCache(strings.NewReader(payload), int64(len(payload)-1), 10); err == nil {
		t.Fatal("loadLinkEnrichmentCache() accepted oversized file")
	}
	if _, err := loadLinkEnrichmentCache(strings.NewReader(payload), int64(len(payload)+1), 1); err != nil {
		t.Fatalf("loadLinkEnrichmentCache() rejected entry at limit: %v", err)
	}
	twoEntries := strings.Replace(payload, `]}`, `,{"sha256":"`+strings.Repeat("b", 64)+`","state":"not_found","checked_at":"2026-07-26T00:00:00Z"}]}`, 1)
	if _, err := loadLinkEnrichmentCache(strings.NewReader(twoEntries), int64(len(twoEntries)+1), 1); err == nil {
		t.Fatal("loadLinkEnrichmentCache() accepted too many entries")
	}
}
