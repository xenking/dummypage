package courses

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildGzipFromSourcesPreservesOldOnlyEntry(t *testing.T) {
	oldEntry := validSourceEntry()
	oldEntry.Title = "Legacy Course"
	newEntry := validSourceEntry()
	newEntry.EntryID = "1:2:0"
	newEntry.MessageID = "1:2"
	newEntry.SourceMessageIDs = []string{"1:2"}
	newEntry.Title = "New Course"
	newSource := validSource(t, newEntry)
	newSource.Messages[0].MessageID = "1:2"
	newSource.Messages[0].TelegramMessageID = 2
	newSource.Messages[0].URL = "https://example.test/messages/2"

	var output bytes.Buffer
	stats, err := BuildGzipFromSources([]SourceInput{
		{Reader: sourceReader(t, validSource(t, oldEntry)), Name: "old.json"},
		{Reader: sourceReader(t, newSource), Name: "new.json"},
	}, &output, "")
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats.SourceEntries != 2 || stats.Entries != 2 {
		t.Fatalf("stats = %+v, want two preserved source entries", stats)
	}
	catalog := decodeBuiltCatalog(t, &output)
	titles := catalogTitles(catalog)
	if !titles["Legacy Course"] || !titles["New Course"] {
		t.Fatalf("catalog titles = %v, want old-only and new entries", titles)
	}
}

func TestBuildGzipFromSourcesMergesLaterEnrichmentAndMissingFields(t *testing.T) {
	oldEntry := validSourceEntry()
	oldEntry.Title = "Original Course"
	oldEntry.Credit.Author = stringPointer("Original Author")
	oldEntry.Passwords = []string{"old-password"}
	oldEntry.Notes = stringPointer("old note")

	newEntry := validSourceEntry()
	newEntry.Title = "Updated Course"
	newEntry.Credit.Author = nil
	newEntry.Passwords = []string{"new-password"}
	newEntry.Notes = nil
	newEntry.SourceMessageIDs = []string{"1:1", "1:1:document"}
	newEntry.Links = append([]sourceLink(nil), oldEntry.Links...)
	newEntry.Links[0].URL = "https://example.test/updated"
	newEntry.Links[0].Host = "example.test"
	newSource := validSource(t, newEntry)
	newSource.Messages[0].URL = ""

	var output bytes.Buffer
	_, err := BuildGzipFromSources([]SourceInput{
		{Reader: sourceReader(t, validSource(t, oldEntry)), Name: "old.json"},
		{Reader: sourceReader(t, newSource), Name: "new.json"},
	}, &output, "")
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	entry := decodeBuiltCatalog(t, &output).Entries[0]
	if entry.Title != "Updated Course" {
		t.Fatalf("title = %q, want newer non-empty title", entry.Title)
	}
	if entry.Author == nil || *entry.Author != "Original Author" {
		t.Fatalf("author = %v, want preserved old author", entry.Author)
	}
	if entry.Sources[0].MessageURL != "https://messages.example.test/source/1" {
		t.Fatalf("message url = %q, want old non-empty URL preserved", entry.Sources[0].MessageURL)
	}
	if len(entry.Passwords) != 2 || entry.Passwords[0] != "old-password" || entry.Passwords[1] != "new-password" {
		t.Fatalf("passwords = %v, want old and new", entry.Passwords)
	}
	if len(entry.Notes) != 1 || entry.Notes[0] != "old note" {
		t.Fatalf("notes = %v, want old note preserved", entry.Notes)
	}
	if len(entry.Sources[0].SourceMessageIDs) != 2 {
		t.Fatalf("source message ids = %v, want union", entry.Sources[0].SourceMessageIDs)
	}
	if len(entry.Links) != 2 {
		t.Fatalf("links = %v, want old and new", entry.Links)
	}
}

func TestBuildGzipFromSourcesRepeatedIdenticalSnapshotIsIdempotent(t *testing.T) {
	source := validSource(t, validSourceEntry())

	var output bytes.Buffer
	stats, err := BuildGzipFromSources([]SourceInput{
		{Reader: sourceReader(t, source), Name: "one.json"},
		{Reader: sourceReader(t, source), Name: "two.json"},
	}, &output, "")
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats.Messages != 1 || stats.SourceEntries != 1 || stats.SourceLinks != 1 || stats.Entries != 1 || stats.Links != 1 {
		t.Fatalf("stats = %+v, want idempotent single snapshot result", stats)
	}
	catalog := decodeBuiltCatalog(t, &output)
	if len(catalog.Entries[0].Sources) != 1 {
		t.Fatalf("sources = %+v, want single source record", catalog.Entries[0].Sources)
	}
}

func TestBuildGzipFromSourcesWithLinkTombstonesPreventsSnapshotResurrectionAndIsIdempotent(t *testing.T) {
	oldEntry := validSourceEntry()
	oldEntry.Title = "Persistent Course"
	oldEntry.Passwords = []string{"old-password"}
	oldEntry.Notes = stringPointer("old note")
	oldEntry.Links = append(oldEntry.Links, sourceLink{
		URL:      "https://example.test/old-keep",
		Host:     "example.test",
		Provider: "example",
		Kind:     "file_host",
		Role:     "primary",
		Primary:  true,
	})

	newEntry := validSourceEntry()
	newEntry.EntryID = "1:2:0"
	newEntry.MessageID = "1:2"
	newEntry.SourceMessageIDs = []string{"1:2"}
	newEntry.Title = "Persistent Course"
	newEntry.Passwords = []string{"new-password"}
	newEntry.Notes = stringPointer("new note")
	newEntry.Links = []sourceLink{
		oldEntry.Links[0],
		{
			URL:      "https://example.test/new-keep",
			Host:     "example.test",
			Provider: "example",
			Kind:     "file_host",
			Role:     "primary",
			Primary:  true,
		},
	}
	tombstones := &LinkTombstones{hashes: map[string]struct{}{hashForTest(t, oldEntry.Links[0].URL): {}}}

	build := func(t *testing.T) (CatalogStats, Catalog) {
		t.Helper()

		var output bytes.Buffer
		newSource := validSource(t, newEntry)
		newSource.Messages[0].MessageID = "1:2"
		newSource.Messages[0].TelegramMessageID = 2
		newSource.Messages[0].URL = "https://messages.example.test/source/2"
		stats, err := BuildGzipFromSourcesWithOptions([]SourceInput{
			{Reader: sourceReader(t, validSource(t, oldEntry)), Name: "old.json"},
			{Reader: sourceReader(t, newSource), Name: "new.json"},
			{Reader: sourceReader(t, newSource), Name: "new-again.json"},
		}, &output, BuildOptions{LinkTombstones: tombstones})
		if err != nil {
			t.Fatalf("build catalog: %v", err)
		}
		return stats, decodeBuiltCatalog(t, &output)
	}

	firstStats, firstCatalog := build(t)
	secondStats, secondCatalog := build(t)
	if firstStats != secondStats {
		t.Fatalf("repeated stats differ: first=%+v second=%+v", firstStats, secondStats)
	}
	entry := firstCatalog.Entries[0]
	if len(entry.Links) != 2 || entry.Links[0].URL != "https://example.test/old-keep" || entry.Links[1].URL != "https://example.test/new-keep" {
		t.Fatalf("links = %+v, want tombstone removed and old/new keep links preserved", entry.Links)
	}
	if strings.Contains(mustMarshalCatalog(t, firstCatalog), "https://example.test/course") {
		t.Fatalf("catalog contains tombstoned URL: %+v", firstCatalog.Entries[0].Links)
	}
	if len(entry.Passwords) != 2 || len(entry.Notes) != 2 {
		t.Fatalf("metadata was not preserved: passwords=%v notes=%v", entry.Passwords, entry.Notes)
	}
	if mustMarshalCatalog(t, firstCatalog) != mustMarshalCatalog(t, secondCatalog) {
		t.Fatal("repeated identical snapshots with tombstones changed catalog output")
	}
}

func mustMarshalCatalog(t *testing.T, catalog Catalog) string {
	t.Helper()

	payload, err := json.Marshal(catalog)
	if err != nil {
		t.Fatalf("marshal catalog: %v", err)
	}
	return string(payload)
}

func TestBuildGzipFromSourcesRejectsConflictingSourceIdentity(t *testing.T) {
	first := validSource(t, validSourceEntry())
	second := validSource(t, validSourceEntry())
	second.Source.ChannelID = 2

	var output bytes.Buffer
	if _, err := BuildGzipFromSources([]SourceInput{
		{Reader: sourceReader(t, first), Name: "first.json"},
		{Reader: sourceReader(t, second), Name: "second.json"},
	}, &output, ""); err == nil {
		t.Fatal("conflicting source identity was accepted")
	}
}

func TestBuildGzipMergesMalformedAuthorBracketOnlyWithMatchingEntry(t *testing.T) {
	canonical := validSourceEntry()
	canonical.EntryID = "1:1:0"
	canonical.Title = "Trading Course"
	canonical.Credit.Author = stringPointer("Example Author")
	malformed := canonical
	malformed.EntryID = "1:2:0"
	malformed.MessageID = "1:2"
	malformed.SourceMessageIDs = []string{"1:2"}
	malformed.Title = "Example Author] Trading Course"
	malformed.Credit.Author = nil
	malformed.Links = append([]sourceLink(nil), canonical.Links...)
	malformed.Links[0].URL = "https://example.test/malformed"
	unmatched := malformed
	unmatched.EntryID = "1:3:0"
	unmatched.MessageID = "1:3"
	unmatched.SourceMessageIDs = []string{"1:3"}
	unmatched.Title = "[Other| Trading Course"
	unmatched.Links = append([]sourceLink(nil), canonical.Links...)
	unmatched.Links[0].URL = "https://example.test/unmatched"

	source := validSource(t, canonical)
	source.Messages = append(source.Messages,
		sourceMessage{MessageID: "1:2", TelegramMessageID: 2, URL: "https://example.test/messages/2"},
		sourceMessage{MessageID: "1:3", TelegramMessageID: 3, URL: "https://example.test/messages/3"},
	)
	source.CatalogEntries = []sourceEntry{canonical, malformed, unmatched}
	setSourceCounts(&source)

	var output bytes.Buffer
	stats, err := BuildGzip(sourceReader(t, source), &output)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats.SourceEntries != 3 || stats.Entries != 2 {
		t.Fatalf("stats = %+v, want only matching malformed author repaired", stats)
	}
}

func TestBuildGzipStripsTerminalDanglingYearForIdentity(t *testing.T) {
	year := 2024
	canonical := validSourceEntry()
	canonical.Title = "Analytics Course"
	canonical.Year = &year
	dangling := canonical
	dangling.EntryID = "1:2:0"
	dangling.MessageID = "1:2"
	dangling.SourceMessageIDs = []string{"1:2"}
	dangling.Title = "Analytics Course (2024"
	dangling.Links = append([]sourceLink(nil), canonical.Links...)
	dangling.Links[0].URL = "https://example.test/dangling"

	source := validSource(t, canonical)
	source.Messages = append(source.Messages, sourceMessage{
		MessageID:         "1:2",
		TelegramMessageID: 2,
		URL:               "https://example.test/messages/2",
	})
	source.CatalogEntries = []sourceEntry{canonical, dangling}
	setSourceCounts(&source)

	var output bytes.Buffer
	stats, err := BuildGzip(sourceReader(t, source), &output)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats.SourceEntries != 2 || stats.Entries != 1 {
		t.Fatalf("stats = %+v, want dangling year variant merged", stats)
	}
}

func TestBuildGzipMergesSharedURLTitlePrefixVariant(t *testing.T) {
	year := 2024
	canonical := validSourceEntry()
	canonical.Title = "Trading Fundamentals"
	canonical.Credit.Author = stringPointer("Known Author")
	canonical.Year = &year
	variant := canonical
	variant.EntryID = "1:2:0"
	variant.MessageID = "1:2"
	variant.SourceMessageIDs = []string{"1:2"}
	variant.Title = "Known Author Trading Fundamentals"
	variant.Credit.Author = nil
	variant.Year = nil
	variant.Links = append([]sourceLink(nil), canonical.Links...)

	source := validSource(t, canonical)
	source.Messages = append(source.Messages, sourceMessage{
		MessageID:         "1:2",
		TelegramMessageID: 2,
		URL:               "https://example.test/messages/2",
	})
	source.CatalogEntries = []sourceEntry{canonical, variant}
	setSourceCounts(&source)

	var output bytes.Buffer
	stats, err := BuildGzip(sourceReader(t, source), &output)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats.SourceEntries != 2 || stats.Entries != 1 {
		t.Fatalf("stats = %+v, want shared URL prefix variant merged", stats)
	}
	entry := decodeBuiltCatalog(t, &output).Entries[0]
	if entry.Title != "Trading Fundamentals" || entry.Author == nil || *entry.Author != "Known Author" || entry.Year == nil || *entry.Year != year {
		t.Fatalf("canonical rich metadata was not preserved: %+v", entry)
	}
}

func TestBuildGzipDoesNotMergeSharedURLUnrelatedTitles(t *testing.T) {
	first := validSourceEntry()
	first.Title = "Trading Fundamentals"
	second := first
	second.EntryID = "1:2:0"
	second.MessageID = "1:2"
	second.SourceMessageIDs = []string{"1:2"}
	second.Title = "Cooking Fundamentals"
	second.Links = append([]sourceLink(nil), first.Links...)

	source := validSource(t, first)
	source.Messages = append(source.Messages, sourceMessage{
		MessageID:         "1:2",
		TelegramMessageID: 2,
		URL:               "https://example.test/messages/2",
	})
	source.CatalogEntries = []sourceEntry{first, second}
	setSourceCounts(&source)

	var output bytes.Buffer
	stats, err := BuildGzip(sourceReader(t, source), &output)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats.Entries != 2 {
		t.Fatalf("stats = %+v, want shared URL unrelated titles separate", stats)
	}
}

func TestBuildGzipDoesNotMergeSharedURLSubstringWithoutWordBoundary(t *testing.T) {
	first := validSourceEntry()
	first.Title = "Art"
	second := first
	second.EntryID = "1:2:0"
	second.MessageID = "1:2"
	second.SourceMessageIDs = []string{"1:2"}
	second.Title = "Cartography"
	second.Links = append([]sourceLink(nil), first.Links...)

	source := validSource(t, first)
	source.Messages = append(source.Messages, sourceMessage{
		MessageID:         "1:2",
		TelegramMessageID: 2,
		URL:               "https://example.test/messages/2",
	})
	source.CatalogEntries = []sourceEntry{first, second}
	setSourceCounts(&source)

	var output bytes.Buffer
	stats, err := BuildGzip(sourceReader(t, source), &output)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats.Entries != 2 {
		t.Fatalf("stats = %+v, want substring without word boundary separate", stats)
	}
}

func TestBuildGzipDoesNotMergeSharedURLIncompatibleYears(t *testing.T) {
	firstYear := 2023
	secondYear := 2024
	first := validSourceEntry()
	first.Title = "Trading Fundamentals"
	first.Year = &firstYear
	second := first
	second.EntryID = "1:2:0"
	second.MessageID = "1:2"
	second.SourceMessageIDs = []string{"1:2"}
	second.Title = "Author Trading Fundamentals"
	second.Year = &secondYear
	second.Links = append([]sourceLink(nil), first.Links...)

	source := validSource(t, first)
	source.Messages = append(source.Messages, sourceMessage{
		MessageID:         "1:2",
		TelegramMessageID: 2,
		URL:               "https://example.test/messages/2",
	})
	source.CatalogEntries = []sourceEntry{first, second}
	setSourceCounts(&source)

	var output bytes.Buffer
	stats, err := BuildGzip(sourceReader(t, source), &output)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats.Entries != 2 {
		t.Fatalf("stats = %+v, want incompatible years separate", stats)
	}
}

func catalogTitles(catalog Catalog) map[string]bool {
	titles := make(map[string]bool, len(catalog.Entries))
	for _, entry := range catalog.Entries {
		titles[entry.Title] = true
	}
	return titles
}
