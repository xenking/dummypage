package courses

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestBuildGzipPreservesCatalogAndClassifies(t *testing.T) {
	source := `{
		"schema_version":"telegram-webk-channel-export/v3",
		"exported_at":"2026-07-26T00:00:00Z",
		"source":{"channel_id":1,"title":"Course Export","web_url":"https://example.test"},
		"stats":{
			"retrieval":{"exported_message_count":1},
			"parsing":{"catalog_entry_count":1,"parsed_link_count":1,"password_value_count":1}
		},
		"messages":[{"message_id":"1:10","telegram_message_id":10,"url":"https://messages.example.test/source/10"}],
		"catalog_entries":[{
			"entry_id":"1:10:0",
			"message_id":"1:10",
			"source_message_ids":["1:10"],
			"added_at":"2024-01-01T00:00:00Z",
			"origin":"text_block",
			"title":"Практическая астрология",
			"credit":{"author":"Автор Пример"},
			"year":2024,
			"year_range":null,
			"availability":"download_link",
			"links":[{
				"url":"https://files.example.test/course-a",
				"host":"files.example.test",
				"provider":"example_files",
				"kind":"file_host",
				"role":"primary",
				"primary":true,
				"label":null
			}],
			"passwords":["fixture-password-a"],
			"notes":null,
			"raw_block":"[Автор Пример] Практическая астрология (2024)"
		}]
	}`

	var compressed bytes.Buffer
	stats, err := BuildGzip(bytes.NewBufferString(source), &compressed)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats != (CatalogStats{
		Messages:        1,
		SourceEntries:   1,
		SourceLinks:     1,
		SourcePasswords: 1,
		Entries:         1,
		Links:           1,
		Passwords:       1,
	}) {
		t.Fatalf("unexpected stats: %+v", stats)
	}

	reader, err := gzip.NewReader(&compressed)
	if err != nil {
		t.Fatalf("open gzip catalog: %v", err)
	}
	payload, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read catalog: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close catalog: %v", err)
	}

	var catalog Catalog
	if err := json.Unmarshal(payload, &catalog); err != nil {
		t.Fatalf("decode catalog: %v", err)
	}
	entry := catalog.Entries[0]
	if entry.PrimaryCategory != "esoteric_astrology" {
		t.Fatalf("primary category = %q, want esoteric_astrology", entry.PrimaryCategory)
	}
	if catalog.SchemaVersion != "courses-catalog/v2" {
		t.Fatalf("schema = %q, want courses-catalog/v2", catalog.SchemaVersion)
	}
	if entry.Sources[0].MessageURL != "https://messages.example.test/source/10" || entry.Links[0].URL == "" || entry.Passwords[0] != "fixture-password-a" {
		t.Fatalf("catalog entry lost source data: %+v", entry)
	}
}

func TestClassifyDoesNotUseURLFragments(t *testing.T) {
	title := "Практический курс"
	raw := "https://example.test/path/gpt/python/design"
	entry := sourceEntry{Title: title, RawBlock: raw}

	categories := classify(entry)
	if len(categories) != 1 || categories[0] != "other" {
		t.Fatalf("categories = %v, want [other]", categories)
	}
}

func TestBuildGzipFiltersStructurallyUnsafeLinks(t *testing.T) {
	source := `{
		"schema_version":"telegram-webk-channel-export/v3",
		"stats":{
			"retrieval":{"exported_message_count":1},
			"parsing":{"catalog_entry_count":1,"parsed_link_count":2,"password_value_count":0}
		},
		"messages":[{"message_id":"1:1","telegram_message_id":1,"url":"https://messages.example.test/source/1"}],
		"catalog_entries":[{
			"entry_id":"1:1:0",
			"message_id":"1:1",
			"added_at":"2024-01-01T00:00:00Z",
			"title":"Unsafe",
			"availability":"download_link",
			"links":[
				{"url":"javascript:alert(1)"},
				{"url":" https://files.example.test/course ","host":"files.example.test","kind":"file_host","role":"primary","primary":true}
			]
		}]
	}`

	var output bytes.Buffer
	stats, err := BuildGzip(bytes.NewBufferString(source), &output)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats.SourceLinks != 2 || stats.StructuralLinksRemoved != 1 || stats.EntriesWithoutLinksRemoved != 0 || stats.Links != 1 {
		t.Fatalf("stats = %+v, want source=2 structural=1 no-link=0 links=1", stats)
	}
	catalog := decodeBuiltCatalog(t, &output)
	if len(catalog.Entries) != 1 || len(catalog.Entries[0].Links) != 1 {
		t.Fatalf("catalog links = %+v, want one accepted link", catalog.Entries)
	}
	if got := catalog.Entries[0].Links[0].URL; got != "https://files.example.test/course" {
		t.Fatalf("stored URL = %q, want trimmed raw URL", got)
	}
}

func TestBuildGzipRejectsUnsupportedSourceSchema(t *testing.T) {
	source := validSource(t, validSourceEntry())
	source.SchemaVersion = "telegram-webk-channel-export/v2"

	var output bytes.Buffer
	if _, err := BuildGzip(sourceReader(t, source), &output); err == nil {
		t.Fatal("unsupported source schema was accepted")
	}
}

func TestBuildGzipRejectsSourceCountMismatch(t *testing.T) {
	source := validSource(t, validSourceEntry())
	source.Stats.Parsing.ParsedLinkCount = 2

	var output bytes.Buffer
	if _, err := BuildGzip(sourceReader(t, source), &output); err == nil {
		t.Fatal("source count mismatch was accepted")
	}
}

func TestBuildGzipRejectsEntryWithoutStableID(t *testing.T) {
	entry := validSourceEntry()
	entry.EntryID = ""
	source := validSource(t, entry)

	var output bytes.Buffer
	if _, err := BuildGzip(sourceReader(t, source), &output); err == nil {
		t.Fatal("entry without stable ID was accepted")
	}
}

func TestBuildGzipRejectsHTTPURLWithoutHost(t *testing.T) {
	entry := validSourceEntry()
	entry.Links[0].URL = "https:///missing-host"
	source := validSource(t, entry)

	var output bytes.Buffer
	stats, err := BuildGzip(sourceReader(t, source), &output)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats.SourceLinks != 1 || stats.StructuralLinksRemoved != 1 || stats.EntriesWithoutLinksRemoved != 1 || stats.Entries != 0 || stats.Links != 0 {
		t.Fatalf("stats = %+v, want rejected link and dropped entry", stats)
	}
}

func TestBuildGzipMarksServiceCategoryHidden(t *testing.T) {
	entry := validSourceEntry()
	entry.Title = "Инструкция как скачать по magnet"
	source := validSource(t, entry)

	var output bytes.Buffer
	if _, err := BuildGzip(sourceReader(t, source), &output); err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	catalog := decodeBuiltCatalog(t, &output)

	if catalog.Entries[0].PrimaryCategory != "service" {
		t.Fatalf("primary category = %q, want service", catalog.Entries[0].PrimaryCategory)
	}
	for _, category := range catalog.Categories {
		if category.ID == "service" {
			if !category.Hidden || category.Count != 1 {
				t.Fatalf("service category = %+v, want hidden count 1", category)
			}
			return
		}
	}
	t.Fatal("service category is missing")
}

func TestBuildGzipPreservesMultipleLinksAndPasswords(t *testing.T) {
	entry := validSourceEntry()
	entry.Links = []sourceLink{
		{
			URL:      "https://files.example.test/course-a",
			Host:     "files.example.test",
			Provider: "example_files",
			Kind:     "file_host",
			Role:     "primary",
			Primary:  true,
		},
		{
			URL:      "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
			Provider: "magnet",
			Kind:     "torrent",
			Role:     "mirror",
		},
	}
	entry.Passwords = []string{"first", "second"}
	source := validSource(t, entry)

	var output bytes.Buffer
	stats, err := BuildGzip(sourceReader(t, source), &output)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats.Links != 2 || stats.Passwords != 2 {
		t.Fatalf("stats = %+v, want 2 links and 2 passwords", stats)
	}

	catalog := decodeBuiltCatalog(t, &output)
	if len(catalog.Entries[0].Links) != 2 || catalog.Entries[0].Links[1].URL != "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567" {
		t.Fatalf("links were not preserved: %+v", catalog.Entries[0].Links)
	}
	if len(catalog.Entries[0].Passwords) != 2 || catalog.Entries[0].Passwords[0] != "first" || catalog.Entries[0].Passwords[1] != "second" {
		t.Fatalf("passwords were not preserved: %+v", catalog.Entries[0].Passwords)
	}
}

func TestBuildGzipWithoutTitleRulesPreservesTransliteratedTitle(t *testing.T) {
	entry := validSourceEntry()
	entry.Title = "Analiz dannyih na Python"
	entry.Credit.Author = stringPointer("Example Academy")
	entry.RawBlock = "[Example Academy] Analiz dannyih na Python"
	source := validSource(t, entry)
	expectedID := courseID(courseIdentityKey(source.Source.ChannelID, entry))

	var output bytes.Buffer
	stats, err := BuildGzip(sourceReader(t, source), &output)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats.NormalizedTitles != 0 {
		t.Fatalf("normalized title count = %d, want 0", stats.NormalizedTitles)
	}

	catalog := decodeBuiltCatalog(t, &output)
	if catalog.Stats.NormalizedTitles != 0 {
		t.Fatalf("catalog normalized title count = %d, want 0", catalog.Stats.NormalizedTitles)
	}
	got := catalog.Entries[0]
	if got.ID != expectedID {
		t.Fatalf("course ID = %q, want raw-title identity %q", got.ID, expectedID)
	}
	if got.Title != "Analiz dannyih na Python" {
		t.Fatalf("title = %q, want raw source title", got.Title)
	}
	if got.TitleOriginal != nil {
		t.Fatalf("title_original = %v, want nil", got.TitleOriginal)
	}
}

func TestBuildGzipWithTitleRulesNormalizesTransliteratedTitleWithoutChangingIdentity(t *testing.T) {
	entry := validSourceEntry()
	entry.Title = "Analiz dannyih na Python"
	entry.Credit.Author = stringPointer("Example Academy")
	entry.RawBlock = "[Example Academy] Analiz dannyih na Python"
	source := validSource(t, entry)
	expectedID := courseID(courseIdentityKey(source.Source.ChannelID, entry))
	rules := titleRulesForTest(t, `{
		"schema_version":"title-normalization-rules/v1",
		"protected_tokens_ci":[],
		"protected_tokens_exact":[],
		"protected_substrings_ci":[]
	}`)

	var output bytes.Buffer
	stats, err := BuildGzipFromSourcesWithOptions(
		[]SourceInput{{Reader: sourceReader(t, source), Name: "synthetic.json"}},
		&output,
		BuildOptions{TitleRules: rules},
	)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats.NormalizedTitles != 1 {
		t.Fatalf("normalized title count = %d, want 1", stats.NormalizedTitles)
	}

	catalog := decodeBuiltCatalog(t, &output)
	got := catalog.Entries[0]
	if got.ID != expectedID {
		t.Fatalf("course ID = %q, want raw-title identity %q", got.ID, expectedID)
	}
	if got.Title != "Анализ данных на Python" {
		t.Fatalf("title = %q, want normalized Russian title", got.Title)
	}
	if got.TitleOriginal == nil || *got.TitleOriginal != "Analiz dannyih na Python" {
		t.Fatalf("title_original = %v, want raw title", got.TitleOriginal)
	}
	if !slices.Contains(got.Categories, "data_ai") {
		t.Fatalf("categories = %v, want normalized title to classify as data_ai", got.Categories)
	}
}

func TestBuildGzipRepairsLegacyDateRangeHeadingWithoutChangingIdentity(t *testing.T) {
	entry := validSourceEntry()
	entry.Title = "30.04"
	entry.Credit.Author = stringPointer("Practical Layout. 01.03")
	entry.RawBlock = "[Example Academy] Practical Layout. 01.03 ― 30.04 (2024)\n" +
		"https://example.test/course"
	year := 2024
	entry.Year = &year
	source := validSource(t, entry)
	expectedID := courseID(courseIdentityKey(source.Source.ChannelID, entry))

	var output bytes.Buffer
	stats, err := BuildGzip(sourceReader(t, source), &output)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats.NormalizedTitles != 1 {
		t.Fatalf("normalized title count = %d, want 1", stats.NormalizedTitles)
	}

	got := decodeBuiltCatalog(t, &output).Entries[0]
	if got.ID != expectedID {
		t.Fatalf("course ID = %q, want raw-title identity %q", got.ID, expectedID)
	}
	if got.Title != "Practical Layout. 01.03 ― 30.04" {
		t.Fatalf("title = %q, want repaired date-range heading", got.Title)
	}
	if got.Author == nil || *got.Author != "Example Academy" {
		t.Fatalf("author = %v, want provider label", got.Author)
	}
	if got.TitleOriginal == nil || *got.TitleOriginal != "30.04" {
		t.Fatalf("title_original = %v, want legacy parsed title", got.TitleOriginal)
	}
}

func TestRepairLegacyDateRangeHeadingRequiresExactRawEvidence(t *testing.T) {
	entry := validSourceEntry()
	entry.Title = "30.04"
	entry.Credit.Author = stringPointer("Practical Layout. 01.03")
	year := 2024
	entry.Year = &year

	for _, rawBlock := range []string{
		"Practical Layout. 01.03 ― 30.04 (2024)",
		"[Example Academy] Other Course. 01.03 ― 30.04 (2024)",
		"[Example Academy] Practical Layout. 01.03 → 30.04 (2024)",
	} {
		entry.RawBlock = rawBlock
		got, repaired := repairLegacyDateRangeHeading(entry)
		if repaired || got.Title != entry.Title || *got.Credit.Author != *entry.Credit.Author {
			t.Fatalf("repairLegacyDateRangeHeading(%q) = (%+v, %t), want unchanged", rawBlock, got, repaired)
		}
	}
}

func TestRepairLegacyDateRangeHeadingMatchesUserscriptWhitespaceSemantics(t *testing.T) {
	entry := validSourceEntry()
	entry.Title = "30.04"
	entry.Credit.Author = stringPointer("Practical Layout. 01.03")
	year := 2024
	entry.Year = &year

	for _, rawBlock := range []string{
		"[[Example Academy] Practical Layout. 01.03 ― 30.04 (2024)",
		"[Example Academy] Practical Layout. 01.03―30.04 (2024)",
		"[Example   Academy]  Practical   Layout. 01.03  ―  30.04 (2024)",
	} {
		entry.RawBlock = rawBlock
		got, repaired := repairLegacyDateRangeHeading(entry)
		if !repaired {
			t.Fatalf("repairLegacyDateRangeHeading(%q) repaired = false, want true", rawBlock)
		}
		if got.Title != "Practical Layout. 01.03 ― 30.04" {
			t.Fatalf("repairLegacyDateRangeHeading(%q) title = %q", rawBlock, got.Title)
		}
		if got.Credit.Author == nil || *got.Credit.Author != "Example Academy" {
			t.Fatalf("repairLegacyDateRangeHeading(%q) author = %v", rawBlock, got.Credit.Author)
		}
	}
}

func TestBuildGzipCountsMergedNormalizedTitleOnce(t *testing.T) {
	first := validSourceEntry()
	first.Title = "Analiz dannyih na Python"
	first.Credit.Author = stringPointer("Example Academy")
	first.RawBlock = "[Example Academy] Analiz dannyih na Python"

	second := first
	second.EntryID = "1:2:0"
	second.MessageID = "1:2"
	second.SourceMessageIDs = []string{"1:2"}
	second.AddedAt = "2024-02-01T00:00:00Z"

	source := validSource(t, first)
	source.Messages = append(source.Messages, sourceMessage{
		MessageID:         "1:2",
		TelegramMessageID: 2,
		URL:               "https://messages.example.test/source/2",
	})
	source.CatalogEntries = []sourceEntry{first, second}
	setSourceCounts(&source)

	var output bytes.Buffer
	rules := titleRulesForTest(t, `{
		"schema_version":"title-normalization-rules/v1",
		"protected_tokens_ci":[],
		"protected_tokens_exact":[],
		"protected_substrings_ci":[]
	}`)
	stats, err := BuildGzipFromSourcesWithOptions(
		[]SourceInput{{Reader: sourceReader(t, source), Name: "synthetic.json"}},
		&output,
		BuildOptions{TitleRules: rules},
	)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats.Entries != 1 || stats.NormalizedTitles != 1 {
		t.Fatalf("stats = %+v, want one merged normalized course", stats)
	}
}

func TestBuildGzipWithLinkEnrichmentAddsCanonicalContentWithoutChangingIdentityOrClassification(t *testing.T) {
	entry := validSourceEntry()
	entry.Links = append(entry.Links, sourceLink{
		URL:      "HTTPS://EXAMPLE.TEST:443/course#fragment",
		Host:     "example.test",
		Provider: "mirror",
		Kind:     "file_host",
		Role:     "alternate",
	})
	source := validSource(t, entry)
	expectedID := courseID(courseIdentityKey(source.Source.ChannelID, entry))
	cache := NewLinkEnrichmentCache(time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC))
	if err := cache.PutExtracted(
		hashForTest(t, "https://example.test/course"),
		LinkContent{
			Name:          "Course bundle",
			Kind:          "folder",
			SizeBytes:     42,
			FileCount:     1,
			Items:         []LinkContentItem{{Name: "lesson.mp4", Kind: "file", SizeBytes: 42}},
			MaterialTypes: []string{"video"},
		},
		time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC),
	); err != nil {
		t.Fatalf("PutExtracted() error = %v", err)
	}
	probe := cachedLinkContent(cache, "HTTPS://EXAMPLE.TEST:443/course#probe")
	if probe == nil {
		t.Fatal("canonical enrichment lookup missed")
	}
	probe.Items[0].Name = "mutated"
	fresh, ok := cache.ContentForURL("https://example.test/course")
	if !ok || fresh.Items[0].Name != "lesson.mp4" {
		t.Fatalf("cached content was not cloned: %+v", fresh)
	}

	var baselineOutput bytes.Buffer
	baselineStats, err := BuildGzipFromSourcesWithOptions(
		[]SourceInput{{Reader: sourceReader(t, source), Name: "source.json"}},
		&baselineOutput,
		BuildOptions{},
	)
	if err != nil {
		t.Fatalf("baseline build: %v", err)
	}
	baseline := decodeBuiltCatalog(t, &baselineOutput)

	var output bytes.Buffer
	stats, err := BuildGzipFromSourcesWithOptions(
		[]SourceInput{{Reader: sourceReader(t, source), Name: "source.json"}},
		&output,
		BuildOptions{LinkEnrichment: cache},
	)
	if err != nil {
		t.Fatalf("enriched build: %v", err)
	}
	catalog := decodeBuiltCatalog(t, &output)
	if stats.EnrichedLinks != 1 || catalog.Stats.EnrichedLinks != 1 {
		t.Fatalf("enriched stats = %+v / %+v, want 1 final enriched link", stats, catalog.Stats)
	}
	if len(catalog.Entries) != 1 || len(catalog.Entries[0].Links) != 1 {
		t.Fatalf("catalog shape = %+v, want one course and one canonical link", catalog.Entries)
	}
	got := catalog.Entries[0]
	if got.ID != expectedID || baseline.Entries[0].ID != expectedID {
		t.Fatalf("course identity changed: baseline=%q enriched=%q want=%q", baseline.Entries[0].ID, got.ID, expectedID)
	}
	if !slices.Equal(got.Categories, baseline.Entries[0].Categories) ||
		!slices.Equal(got.Formats, baseline.Entries[0].Formats) {
		t.Fatalf("content changed taxonomy: baseline=%+v enriched=%+v", baseline.Entries[0], got)
	}
	content := got.Links[0].Content
	if content == nil ||
		content.Name != "Course bundle" ||
		len(content.Items) != 1 ||
		content.Items[0].Name != "lesson.mp4" {
		t.Fatalf("content = %+v, want cached metadata", content)
	}
	if baselineStats.EnrichedLinks != 0 || baseline.Entries[0].Links[0].Content != nil {
		t.Fatalf("nil enrichment changed baseline: stats=%+v link=%+v", baselineStats, baseline.Entries[0].Links[0])
	}
}

func TestMergeLinksChoosesContentDeterministicallyByInformationThenJSON(t *testing.T) {
	rawURL := "https://example.test/course"
	weak := &LinkContent{Name: "Bundle"}
	rich := &LinkContent{
		Name:      "Bundle",
		Kind:      "folder",
		FileCount: 1,
		Items:     []LinkContentItem{{Name: "lesson.mp4", Kind: "file"}},
	}
	for _, test := range []struct {
		name   string
		first  *LinkContent
		second *LinkContent
		want   *LinkContent
	}{
		{"rich second", weak, rich, rich},
		{"rich first", rich, weak, rich},
		{"lexical tie forward", &LinkContent{Name: "Zulu"}, &LinkContent{Name: "Alpha"}, &LinkContent{Name: "Alpha"}},
		{"lexical tie reverse", &LinkContent{Name: "Alpha"}, &LinkContent{Name: "Zulu"}, &LinkContent{Name: "Alpha"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			links := mergeLinks(
				[]CatalogLink{{URL: rawURL, Content: test.first}},
				[]CatalogLink{{URL: rawURL + "#fragment", Content: test.second}},
			)
			if len(links) != 1 {
				t.Fatalf("links = %d, want 1", len(links))
			}
			got, err := json.Marshal(links[0].Content)
			if err != nil {
				t.Fatalf("marshal got content: %v", err)
			}
			want, err := json.Marshal(test.want)
			if err != nil {
				t.Fatalf("marshal want content: %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("content = %s, want %s", got, want)
			}
		})
	}

	t.Run("clones appended content", func(t *testing.T) {
		content := &LinkContent{Items: []LinkContentItem{{Name: "lesson.mp4"}}}
		links := mergeLinks(nil, []CatalogLink{{URL: rawURL, Content: content}})
		content.Items[0].Name = "mutated"
		if got := links[0].Content.Items[0].Name; got != "lesson.mp4" {
			t.Fatalf("merged content alias = %q, want cloned value", got)
		}
	})
}

func TestBuildGzipFiltersBareDomainTitleEntityLinksWithRealProvenanceShape(t *testing.T) {
	title := "Example Blog: Practical Writing"
	entry := validSourceEntry()
	entry.Title = title
	entry.RawBlock = title
	entry.Links = []sourceLink{
		{
			URL:            "https://example.blog",
			Host:           "example.blog",
			Provider:       "website",
			Kind:           "external",
			Role:           "reference",
			Label:          &title,
			NormalizedFrom: "example.blog",
			Sources:        []string{"entity_url"},
		},
		{
			URL:      "https://files.example.test/course",
			Host:     "files.example.test",
			Provider: "example_files",
			Kind:     "file_host",
			Role:     "primary",
			Primary:  true,
		},
	}
	source := validSource(t, entry)

	var output bytes.Buffer
	stats, err := BuildGzip(sourceReader(t, source), &output)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats.SourceLinks != 2 || stats.Links != 1 {
		t.Fatalf("stats = %+v, want raw source links counted and only explicit file link actionable", stats)
	}

	catalog := decodeBuiltCatalog(t, &output)
	if len(catalog.Entries[0].Links) != 1 || catalog.Entries[0].Links[0].URL != "https://files.example.test/course" {
		t.Fatalf("links = %+v, want title bare-domain entity filtered and explicit file link kept", catalog.Entries[0].Links)
	}
}

func TestBuildGzipKeepsExplicitHTTPLinkInTitle(t *testing.T) {
	title := "Example Blog: https://example.blog"
	entry := validSourceEntry()
	entry.Title = title
	entry.RawBlock = title
	entry.Links = []sourceLink{
		{
			URL:            "https://example.blog",
			Host:           "example.blog",
			Provider:       "website",
			Kind:           "external",
			Role:           "reference",
			Label:          &title,
			NormalizedFrom: "explicit_url",
			Sources:        []string{"title", "entity_url"},
		},
	}
	source := validSource(t, entry)

	var output bytes.Buffer
	stats, err := BuildGzip(sourceReader(t, source), &output)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats.SourceLinks != 1 || stats.Links != 1 {
		t.Fatalf("stats = %+v, want explicit source link actionable", stats)
	}

	catalog := decodeBuiltCatalog(t, &output)
	if len(catalog.Entries[0].Links) != 1 || catalog.Entries[0].Links[0].URL != "https://example.blog" {
		t.Fatalf("links = %+v, want explicit title HTTP link preserved", catalog.Entries[0].Links)
	}
}

func TestBuildGzipKeepsStandaloneExplicitBareDomainLink(t *testing.T) {
	title := "Practical Writing"
	entry := validSourceEntry()
	entry.Title = title
	entry.RawBlock = title + "\nexample.blog"
	entry.Links = []sourceLink{
		{
			URL:            "https://example.blog",
			Host:           "example.blog",
			Provider:       "website",
			Kind:           "external",
			Role:           "reference",
			Label:          stringPointer("example.blog"),
			NormalizedFrom: "example.blog",
			Sources:        []string{"entity_url"},
		},
	}
	source := validSource(t, entry)

	var output bytes.Buffer
	stats, err := BuildGzip(sourceReader(t, source), &output)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats.SourceLinks != 1 || stats.Links != 1 {
		t.Fatalf("stats = %+v, want standalone explicit link actionable", stats)
	}

	catalog := decodeBuiltCatalog(t, &output)
	if len(catalog.Entries[0].Links) != 1 || catalog.Entries[0].Links[0].URL != "https://example.blog" {
		t.Fatalf("links = %+v, want standalone bare domain preserved", catalog.Entries[0].Links)
	}
}

func TestBuildGzipKeepsPrimaryFileHostAndMagnetLinks(t *testing.T) {
	entry := validSourceEntry()
	entry.Title = "Practical Writing"
	entry.RawBlock = entry.Title
	entry.Links = []sourceLink{
		{
			URL:            "https://files.example.test/course",
			Host:           "files.example.test",
			Provider:       "example_files",
			Kind:           "file_host",
			Role:           "reference",
			NormalizedFrom: "files.example.test",
			Sources:        []string{"title", "entity_url"},
		},
		{
			URL:            "https://primary.example.test/course",
			Host:           "primary.example.test",
			Provider:       "example_primary",
			Kind:           "external",
			Role:           "primary",
			Primary:        true,
			NormalizedFrom: "primary.example.test",
			Sources:        []string{"title", "entity_url"},
		},
		{
			URL:            "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
			Provider:       "magnet",
			Kind:           "torrent",
			Role:           "mirror",
			NormalizedFrom: "magnet.example.test",
			Sources:        []string{"title", "entity_url"},
		},
	}
	source := validSource(t, entry)

	var output bytes.Buffer
	stats, err := BuildGzip(sourceReader(t, source), &output)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats.SourceLinks != 3 || stats.Links != 3 {
		t.Fatalf("stats = %+v, want all primary/file-host/magnet links actionable", stats)
	}

	catalog := decodeBuiltCatalog(t, &output)
	if len(catalog.Entries[0].Links) != 3 {
		t.Fatalf("links = %+v, want primary/file-host/magnet links preserved", catalog.Entries[0].Links)
	}
}

func TestBuildGzipMergesRepostsWithoutLosingData(t *testing.T) {
	author := "Студия Пример"
	year := 2024
	first := validSourceEntry()
	first.EntryID = "1:1:0"
	first.MessageID = "1:1"
	first.SourceMessageIDs = []string{"1:1"}
	first.AddedAt = "2024-02-18T19:36:15Z"
	first.Title = "- Анимация в Adobe After Effects"
	first.Credit.Author = &author
	first.Year = &year
	first.Passwords = []string{"first"}
	first.Notes = stringPointer("Первый источник")
	first.Links[0].URL = "https://files.example.test/course-a"

	second := first
	second.EntryID = "1:2:0"
	second.MessageID = "1:2"
	second.SourceMessageIDs = []string{"1:2"}
	second.AddedAt = "2024-02-22T21:48:27Z"
	second.Title = "Анимация в Adobe After Effects"
	second.Passwords = []string{"second"}
	second.Notes = stringPointer("Новый источник")
	second.Links = []sourceLink{
		first.Links[0],
		{
			URL:      "https://files.example.test/course-b",
			Host:     "files.example.test",
			Provider: "example_files",
			Kind:     "file_host",
			Role:     "mirror",
		},
	}

	source := validSource(t, first)
	source.Messages = append(source.Messages, sourceMessage{
		MessageID:         "1:2",
		TelegramMessageID: 2,
		URL:               "https://messages.example.test/source/2",
	})
	source.CatalogEntries = []sourceEntry{first, second}
	setSourceCounts(&source)

	var output bytes.Buffer
	stats, err := BuildGzip(sourceReader(t, source), &output)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats.SourceEntries != 2 || stats.Entries != 1 || stats.SourceLinks != 3 || stats.Links != 2 ||
		stats.SourcePasswords != 2 || stats.Passwords != 2 {
		t.Fatalf("unexpected merged stats: %+v", stats)
	}

	catalog := decodeBuiltCatalog(t, &output)
	if len(catalog.Entries) != 1 {
		t.Fatalf("entry count = %d, want 1", len(catalog.Entries))
	}
	entry := catalog.Entries[0]
	if entry.Title != "Анимация в Adobe After Effects" {
		t.Fatalf("clean title = %q", entry.Title)
	}
	if entry.FirstAddedAt != first.AddedAt || entry.LastAddedAt != second.AddedAt {
		t.Fatalf("added range = %q..%q", entry.FirstAddedAt, entry.LastAddedAt)
	}
	if len(entry.Sources) != 2 || entry.Sources[0].EntryID != first.EntryID || entry.Sources[1].MessageURL != "https://messages.example.test/source/2" {
		t.Fatalf("sources were not preserved: %+v", entry.Sources)
	}
	if len(entry.Links) != 2 || len(entry.Passwords) != 2 || len(entry.Notes) != 2 {
		t.Fatalf("merged data was lost: links=%v passwords=%v notes=%v", entry.Links, entry.Passwords, entry.Notes)
	}

	var firstOnly bytes.Buffer
	if _, err := BuildGzip(sourceReader(t, validSource(t, first)), &firstOnly); err != nil {
		t.Fatalf("build first-only catalog: %v", err)
	}
	firstOnlyCatalog := decodeBuiltCatalog(t, &firstOnly)
	if firstOnlyCatalog.Entries[0].ID != entry.ID {
		t.Fatalf("stable course ID changed after a repost: %q != %q", firstOnlyCatalog.Entries[0].ID, entry.ID)
	}

	for _, category := range catalog.Categories {
		if category.ID == "three_d_motion_vfx" && category.Count != 1 {
			t.Fatalf("design category count = %d, want one merged course", category.Count)
		}
	}
}

func TestBuildGzipMergesTitleOnlyRepostsWithDifferentMirrors(t *testing.T) {
	first := validSourceEntry()
	first.Title = "Программирование на C"
	first.Links[0].URL = "https://example.test/first"

	second := first
	second.EntryID = "1:2:0"
	second.MessageID = "1:2"
	second.SourceMessageIDs = []string{"1:2"}
	second.AddedAt = "2024-02-01T00:00:00Z"
	second.Links = append([]sourceLink(nil), first.Links...)
	second.Links[0].URL = "https://example.test/second"

	source := validSource(t, first)
	source.Messages = append(source.Messages, sourceMessage{
		MessageID:         "1:2",
		TelegramMessageID: 2,
		URL:               "https://messages.example.test/source/2",
	})
	source.CatalogEntries = []sourceEntry{first, second}
	setSourceCounts(&source)

	var output bytes.Buffer
	if _, err := BuildGzip(sourceReader(t, source), &output); err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	catalog := decodeBuiltCatalog(t, &output)
	if len(catalog.Entries) != 1 || len(catalog.Entries[0].Links) != 2 {
		t.Fatalf("title-only reposts were not merged: %+v", catalog.Entries)
	}
}

func TestBuildGzipMergesSharedLinkAcrossMetadataVariants(t *testing.T) {
	author := "Example Author"
	year := 2023
	first := validSourceEntry()
	first.Title = "'[Example Author] Доход с рекламной платформы"
	first.Credit.Author = &author
	first.Year = &year
	first.Links[0].URL = "https://example.test/shared"

	second := first
	second.EntryID = "1:2:0"
	second.MessageID = "1:2"
	second.SourceMessageIDs = []string{"1:2"}
	second.AddedAt = "2024-02-01T00:00:00Z"
	second.Title = "[Example Author] Доход с рекламной платформы"
	second.Credit.Author = nil
	second.Year = nil
	second.Links = append([]sourceLink(nil), first.Links...)

	source := validSource(t, first)
	source.Messages = append(source.Messages, sourceMessage{
		MessageID:         "1:2",
		TelegramMessageID: 2,
		URL:               "https://messages.example.test/source/2",
	})
	source.CatalogEntries = []sourceEntry{first, second}
	setSourceCounts(&source)

	var output bytes.Buffer
	if _, err := BuildGzip(sourceReader(t, source), &output); err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	catalog := decodeBuiltCatalog(t, &output)
	if len(catalog.Entries) != 1 {
		t.Fatalf("shared-link variants produced %d courses, want 1", len(catalog.Entries))
	}
	entry := catalog.Entries[0]
	if entry.Title != "[Example Author] Доход с рекламной платформы" {
		t.Fatalf("clean title = %q", entry.Title)
	}
	if len(entry.Sources) != 2 || len(entry.Links) != 1 {
		t.Fatalf("shared-link merge lost data: sources=%v links=%v", entry.Sources, entry.Links)
	}
}

func TestBuildGzipMergesIntraEntryCanonicalDuplicateLinks(t *testing.T) {
	firstLabel := "Primary"
	secondLabel := "Mirror"
	entry := validSourceEntry()
	entry.Links = []sourceLink{
		{
			URL:      " https://EXAMPLE.test:443/course?sig=b&sig=a#first ",
			Host:     "EXAMPLE.test",
			Provider: "example",
			Kind:     "file_host",
			Role:     "mirror",
			Label:    &firstLabel,
		},
		{
			URL:      "https://example.test/course?sig=b&sig=a#second",
			Host:     "example.test",
			Provider: "example",
			Kind:     "file_host",
			Role:     "primary",
			Primary:  true,
			Label:    &secondLabel,
		},
	}
	source := validSource(t, entry)

	var output bytes.Buffer
	stats, err := BuildGzip(sourceReader(t, source), &output)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats.SourceLinks != 2 || stats.Links != 1 {
		t.Fatalf("stats = %+v, want two source links and one canonical catalog link", stats)
	}
	catalog := decodeBuiltCatalog(t, &output)
	link := catalog.Entries[0].Links[0]
	if link.URL != "https://EXAMPLE.test:443/course?sig=b&sig=a#first" {
		t.Fatalf("URL = %q, want first trimmed URL retained", link.URL)
	}
	if !link.Primary || link.Label == nil || *link.Label != firstLabel {
		t.Fatalf("merged link metadata = %+v, want primary merged and first label retained", link)
	}
}

func TestBuildGzipKeepsSameCanonicalURLOnUnrelatedDistinctCourses(t *testing.T) {
	first := validSourceEntry()
	first.Title = "Go Basics"
	first.Links[0].URL = "https://EXAMPLE.test:443/shared#first"

	second := validSourceEntry()
	second.EntryID = "1:2:0"
	second.MessageID = "1:2"
	second.SourceMessageIDs = []string{"1:2"}
	second.AddedAt = "2024-02-01T00:00:00Z"
	second.Title = "Watercolor Basics"
	second.Links[0].URL = "https://example.test/shared#second"

	source := validSource(t, first)
	source.Messages = append(source.Messages, sourceMessage{
		MessageID:         "1:2",
		TelegramMessageID: 2,
		URL:               "https://messages.example.test/source/2",
	})
	source.CatalogEntries = []sourceEntry{first, second}
	setSourceCounts(&source)

	var output bytes.Buffer
	stats, err := BuildGzip(sourceReader(t, source), &output)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats.SourceEntries != 2 || stats.Entries != 2 || stats.SourceLinks != 2 || stats.Links != 2 {
		t.Fatalf("stats = %+v, want unrelated courses to keep their own links", stats)
	}
	catalog := decodeBuiltCatalog(t, &output)
	if len(catalog.Entries) != 2 || len(catalog.Entries[0].Links) != 1 || len(catalog.Entries[1].Links) != 1 {
		t.Fatalf("entries = %+v, want two linked entries", catalog.Entries)
	}
}

func TestBuildGzipDropsFinalEntryWithoutAcceptedLinks(t *testing.T) {
	first := validSourceEntry()
	first.Title = "Unsafe Only"
	first.Links = []sourceLink{{URL: "ftp://example.test/course"}}

	second := validSourceEntry()
	second.EntryID = "1:2:0"
	second.MessageID = "1:2"
	second.SourceMessageIDs = []string{"1:2"}
	second.AddedAt = "2024-02-01T00:00:00Z"
	second.Title = "Safe Course"
	second.Links[0].URL = "https://files.example.test/safe"

	source := validSource(t, first)
	source.Messages = append(source.Messages, sourceMessage{
		MessageID:         "1:2",
		TelegramMessageID: 2,
		URL:               "https://messages.example.test/source/2",
	})
	source.CatalogEntries = []sourceEntry{first, second}
	setSourceCounts(&source)

	var output bytes.Buffer
	stats, err := BuildGzip(sourceReader(t, source), &output)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats.SourceEntries != 2 || stats.Entries != 1 || stats.StructuralLinksRemoved != 1 || stats.EntriesWithoutLinksRemoved != 1 || stats.Links != 1 {
		t.Fatalf("stats = %+v, want one dropped no-link entry and one safe entry", stats)
	}
	catalog := decodeBuiltCatalog(t, &output)
	if len(catalog.Entries) != 1 || catalog.Entries[0].Title != "Safe Course" {
		t.Fatalf("entries = %+v, want only safe course", catalog.Entries)
	}
}

func TestBuildGzipClassifiesFormatsConservatively(t *testing.T) {
	tests := []struct {
		name           string
		title          string
		author         string
		rawBlock       string
		formats        []string
		primaryFormat  string
		formatSource   string
		metadataCounts map[string]int
	}{
		{
			name:           "course remains primary with workshop",
			title:          "Курс-практикум по AI",
			formats:        []string{"course", "workshop"},
			primaryFormat:  "course",
			formatSource:   "title",
			metadataCounts: map[string]int{"workshop": 1, "course": 1},
		},
		{
			name:          "provider is not a format",
			title:         "Яндекс.Практикум - Python",
			formats:       []string{"unspecified"},
			primaryFormat: "unspecified",
			formatSource:  "fallback",
		},
		{
			name:          "author and raw block are ignored",
			title:         "Практическая работа",
			author:        "MasterClass",
			rawBlock:      "Вебинар и полный курс",
			formats:       []string{"unspecified"},
			primaryFormat: "unspecified",
			formatSource:  "fallback",
		},
		{
			name:          "manual therapy is not a manual",
			title:         "Мануальная терапия позвоночника",
			formats:       []string{"unspecified"},
			primaryFormat: "unspecified",
			formatSource:  "fallback",
		},
		{
			name:          "professional adjective is not a profession",
			title:         "AI для профессионального создания задников",
			formats:       []string{"unspecified"},
			primaryFormat: "unspecified",
			formatSource:  "fallback",
		},
		{
			name:          "book and templates are both retained",
			title:         "Книга стиля для женщин. Готовый шаблон для работы",
			formats:       []string{"book_guide", "templates_assets"},
			primaryFormat: "book_guide",
			formatSource:  "title",
		},
		{
			name:          "live recording",
			title:         "Запись вебинара по коммуникации",
			formats:       []string{"live_recording"},
			primaryFormat: "live_recording",
			formatSource:  "title",
		},
		{
			name:          "marathon",
			title:         "Марафон привычек на месяц",
			formats:       []string{"marathon"},
			primaryFormat: "marathon",
			formatSource:  "title",
		},
		{
			name:          "bundle library",
			title:         "Библиотека шаблонов для проекта",
			formats:       []string{"templates_assets", "bundle_library"},
			primaryFormat: "templates_assets",
			formatSource:  "title",
		},
		{
			name:          "club membership",
			title:         "Клуб с ежемесячной подпиской",
			formats:       []string{"club_membership"},
			primaryFormat: "club_membership",
			formatSource:  "title",
		},
		{
			name:          "audio book is audio",
			title:         "Развитие памяти (Аудиокнига)",
			formats:       []string{"audio"},
			primaryFormat: "audio",
			formatSource:  "title",
		},
		{
			name:          "course remains primary over supplemental templates",
			title:         "Вводное обучение SERM + полезные материалы",
			formats:       []string{"course", "templates_assets"},
			primaryFormat: "course",
			formatSource:  "title",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entry := validSourceEntry()
			entry.Title = test.title
			entry.RawBlock = test.rawBlock
			if test.author != "" {
				entry.Credit.Author = &test.author
			}

			var output bytes.Buffer
			if _, err := BuildGzip(sourceReader(t, validSource(t, entry)), &output); err != nil {
				t.Fatalf("build catalog: %v", err)
			}
			catalog := decodeBuiltCatalogFormatView(t, &output)
			if len(catalog.Entries) != 1 {
				t.Fatalf("entry count = %d, want 1", len(catalog.Entries))
			}
			got := catalog.Entries[0]
			if !slices.Equal(got.Formats, test.formats) {
				t.Fatalf("formats = %v, want %v", got.Formats, test.formats)
			}
			if got.PrimaryFormat != test.primaryFormat {
				t.Fatalf("primary format = %q, want %q", got.PrimaryFormat, test.primaryFormat)
			}
			if !slices.Equal(got.FormatSources, []string{test.formatSource}) {
				t.Fatalf("format sources = %v, want [%s]", got.FormatSources, test.formatSource)
			}
			if test.metadataCounts != nil {
				counts := make(map[string]int, len(catalog.Formats))
				for _, format := range catalog.Formats {
					counts[format.ID] = format.Count
				}
				for id, want := range test.metadataCounts {
					if got := counts[id]; got != want {
						t.Fatalf("format %q count = %d, want %d", id, got, want)
					}
				}
			}
		})
	}
}

func TestBuildGzipClassifiesTorrentAttachmentFromMessageMedia(t *testing.T) {
	tests := []struct {
		name          string
		title         string
		formats       []string
		primaryFormat string
		formatSource  string
	}{
		{
			name:          "attachment is the only format signal",
			title:         "OutBlock",
			formats:       []string{"torrent_attachment"},
			primaryFormat: "torrent_attachment",
			formatSource:  "availability",
		},
		{
			name:          "attachment supplements explicit course",
			title:         "Полный курс Go",
			formats:       []string{"course", "torrent_attachment"},
			primaryFormat: "course",
			formatSource:  "title",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			entry := validSourceEntry()
			entry.Title = test.title
			entry.Origin = "document"
			entry.Availability = "document_attachment"
			source := validSource(t, entry)

			payload, err := json.Marshal(source)
			if err != nil {
				t.Fatalf("encode source: %v", err)
			}
			var rawSource map[string]any
			if err := json.Unmarshal(payload, &rawSource); err != nil {
				t.Fatalf("decode source map: %v", err)
			}
			messages := rawSource["messages"].([]any)
			message := messages[0].(map[string]any)
			message["media"] = map[string]any{
				"type": "messageMediaDocument",
				"document": map[string]any{
					"file_name": "course.torrent",
					"mime_type": "application/x-bittorrent",
				},
			}
			payload, err = json.Marshal(rawSource)
			if err != nil {
				t.Fatalf("encode source with media: %v", err)
			}

			var output bytes.Buffer
			if _, err := BuildGzip(bytes.NewReader(payload), &output); err != nil {
				t.Fatalf("build catalog: %v", err)
			}
			catalog := decodeBuiltCatalogFormatView(t, &output)
			got := catalog.Entries[0]
			if !slices.Equal(got.Formats, test.formats) {
				t.Fatalf("formats = %v, want %v", got.Formats, test.formats)
			}
			if got.PrimaryFormat != test.primaryFormat || !slices.Equal(got.FormatSources, []string{test.formatSource}) {
				t.Fatalf("primary/sources = %q/%v, want %q/[%s]", got.PrimaryFormat, got.FormatSources, test.primaryFormat, test.formatSource)
			}
		})
	}
}

func TestBuildGzipWithLinkTombstonesFiltersSourceAndTorrentLinks(t *testing.T) {
	entry := validSourceEntry()
	entry.Links = append(entry.Links, sourceLink{
		URL:      "https://example.test/keep",
		Host:     "example.test",
		Provider: "example",
		Kind:     "file_host",
		Role:     "primary",
		Primary:  true,
	})
	source := validSource(t, entry)
	source.Messages[0].Media.Type = "messageMediaDocument"
	source.Messages[0].Media.Document.FileName = "Course.torrent"
	source.Messages[0].Media.Document.MIMEType = "application/x-bittorrent"
	setSourceCounts(&source)

	torrentDir := t.TempDir()
	torrentPayload, torrentInfo := validTorrentPayload()
	if err := os.WriteFile(filepath.Join(torrentDir, "Course.torrent"), torrentPayload, 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	magnet := "magnet:?xt=urn:btih:" + sha1Hex(torrentInfo)
	tombstones := &LinkTombstones{hashes: map[string]struct{}{
		hashForTest(t, "https://example.test/course"): {},
		hashForTest(t, magnet):                        {},
	}}

	var output bytes.Buffer
	stats, err := BuildGzipFromSourcesWithOptions(
		[]SourceInput{{Reader: sourceReader(t, source), Name: "source.json"}},
		&output,
		BuildOptions{TorrentDir: torrentDir, LinkTombstones: tombstones},
	)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats.TombstonedLinksRemoved != 2 || stats.SourceLinks != 2 || stats.Links != 1 || stats.EntriesWithoutLinksRemoved != 0 {
		t.Fatalf("stats = %+v, want two tombstoned removals and one kept link", stats)
	}
	links := decodeBuiltCatalog(t, &output).Entries[0].Links
	if len(links) != 1 || links[0].URL != "https://example.test/keep" {
		t.Fatalf("links = %+v, want only non-tombstoned source link", links)
	}
}

func TestBuildGzipWithLinkTombstonesDoesNotClusterByTombstonedSharedURL(t *testing.T) {
	first := validSourceEntry()
	first.Title = "Trading Fundamentals"
	second := first
	second.EntryID = "1:2:0"
	second.MessageID = "1:2"
	second.SourceMessageIDs = []string{"1:2"}
	second.Title = "Known Author Trading Fundamentals"
	second.Links = append([]sourceLink(nil), first.Links...)

	source := validSource(t, first)
	source.Messages = append(source.Messages, sourceMessage{
		MessageID:         "1:2",
		TelegramMessageID: 2,
		URL:               "https://example.test/messages/2",
	})
	source.CatalogEntries = []sourceEntry{first, second}
	setSourceCounts(&source)
	tombstones := &LinkTombstones{hashes: map[string]struct{}{hashForTest(t, first.Links[0].URL): {}}}

	var output bytes.Buffer
	stats, err := BuildGzipFromSourcesWithOptions(
		[]SourceInput{{Reader: sourceReader(t, source), Name: "source.json"}},
		&output,
		BuildOptions{LinkTombstones: tombstones},
	)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats.TombstonedLinksRemoved != 2 || stats.Entries != 0 || stats.EntriesWithoutLinksRemoved != 2 {
		t.Fatalf("stats = %+v, want both unmerged zero-link entries removed", stats)
	}
}

func validSource(t *testing.T, entry sourceEntry) sourceExport {
	t.Helper()

	source := sourceExport{
		SchemaVersion: sourceSchema,
		ExportedAt:    "2026-07-26T00:00:00Z",
		Source: catalogSource{
			ChannelID: 1,
			Title:     "Course Export",
			WebURL:    "https://example.test",
		},
		Messages: []sourceMessage{
			{MessageID: "1:1", TelegramMessageID: 1, URL: "https://messages.example.test/source/1"},
		},
		CatalogEntries: []sourceEntry{entry},
	}
	source.Stats.Retrieval.ExportedMessageCount = len(source.Messages)
	source.Stats.Parsing.CatalogEntryCount = len(source.CatalogEntries)
	for _, catalogEntry := range source.CatalogEntries {
		source.Stats.Parsing.ParsedLinkCount += len(catalogEntry.Links)
		source.Stats.Parsing.PasswordValueCount += len(catalogEntry.Passwords)
	}
	return source
}

func validSourceEntry() sourceEntry {
	return sourceEntry{
		EntryID:          "1:1:0",
		MessageID:        "1:1",
		SourceMessageIDs: []string{"1:1"},
		AddedAt:          "2024-01-01T00:00:00Z",
		Origin:           "text_block",
		Title:            "Practical Go",
		Availability:     "download_link",
		Links: []sourceLink{
			{
				URL:      "https://example.test/course",
				Host:     "example.test",
				Provider: "example",
				Kind:     "file_host",
				Role:     "primary",
				Primary:  true,
			},
		},
	}
}

func setSourceCounts(source *sourceExport) {
	source.Stats.Retrieval.ExportedMessageCount = len(source.Messages)
	source.Stats.Parsing.CatalogEntryCount = len(source.CatalogEntries)
	source.Stats.Parsing.ParsedLinkCount = 0
	source.Stats.Parsing.PasswordValueCount = 0
	for _, entry := range source.CatalogEntries {
		source.Stats.Parsing.ParsedLinkCount += len(entry.Links)
		source.Stats.Parsing.PasswordValueCount += len(entry.Passwords)
	}
}

func stringPointer(value string) *string {
	return &value
}

func titleRulesForTest(t *testing.T, payload string) *TitleRules {
	t.Helper()

	rules, err := LoadTitleRules(strings.NewReader(payload))
	if err != nil {
		t.Fatalf("load title rules: %v", err)
	}
	return rules
}

func sourceReader(t *testing.T, source sourceExport) io.Reader {
	t.Helper()

	payload, err := json.Marshal(source)
	if err != nil {
		t.Fatalf("encode source: %v", err)
	}
	return bytes.NewReader(payload)
}

func decodeBuiltCatalog(t *testing.T, compressed *bytes.Buffer) Catalog {
	t.Helper()

	reader, err := gzip.NewReader(bytes.NewReader(compressed.Bytes()))
	if err != nil {
		t.Fatalf("open gzip catalog: %v", err)
	}
	payload, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read catalog: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close catalog: %v", err)
	}

	var catalog Catalog
	if err := json.Unmarshal(payload, &catalog); err != nil {
		t.Fatalf("decode catalog: %v", err)
	}
	return catalog
}

func decodeBuiltCatalogFormatView(t *testing.T, compressed *bytes.Buffer) struct {
	Formats []struct {
		ID    string `json:"id"`
		Label string `json:"label"`
		Count int    `json:"count"`
	} `json:"formats"`
	Entries []struct {
		Formats       []string `json:"formats"`
		PrimaryFormat string   `json:"primary_format"`
		FormatSources []string `json:"format_sources"`
	} `json:"entries"`
} {
	t.Helper()

	reader, err := gzip.NewReader(bytes.NewReader(compressed.Bytes()))
	if err != nil {
		t.Fatalf("open gzip catalog: %v", err)
	}
	payload, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read catalog: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close catalog: %v", err)
	}

	var catalog struct {
		Formats []struct {
			ID    string `json:"id"`
			Label string `json:"label"`
			Count int    `json:"count"`
		} `json:"formats"`
		Entries []struct {
			Formats       []string `json:"formats"`
			PrimaryFormat string   `json:"primary_format"`
			FormatSources []string `json:"format_sources"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(payload, &catalog); err != nil {
		t.Fatalf("decode catalog: %v", err)
	}
	return catalog
}
