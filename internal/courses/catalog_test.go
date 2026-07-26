package courses

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"slices"
	"testing"
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

func TestBuildGzipRejectsUnsafeLinks(t *testing.T) {
	source := `{
		"schema_version":"telegram-webk-channel-export/v3",
		"stats":{
			"retrieval":{"exported_message_count":1},
			"parsing":{"catalog_entry_count":1,"parsed_link_count":1,"password_value_count":0}
		},
		"messages":[{"message_id":"1:1","telegram_message_id":1,"url":"https://messages.example.test/source/1"}],
		"catalog_entries":[{
			"entry_id":"1:1:0",
			"message_id":"1:1",
			"title":"Unsafe",
			"links":[{"url":"javascript:alert(1)"}]
		}]
	}`

	var output bytes.Buffer
	if _, err := BuildGzip(bytes.NewBufferString(source), &output); err == nil {
		t.Fatal("unsafe link was accepted")
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
	if _, err := BuildGzip(sourceReader(t, source), &output); err == nil {
		t.Fatal("HTTP URL without host was accepted")
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
			URL:      "magnet:?xt=urn:btih:0123456789abcdef",
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
	if len(catalog.Entries[0].Links) != 2 || catalog.Entries[0].Links[1].URL != "magnet:?xt=urn:btih:0123456789abcdef" {
		t.Fatalf("links were not preserved: %+v", catalog.Entries[0].Links)
	}
	if len(catalog.Entries[0].Passwords) != 2 || catalog.Entries[0].Passwords[0] != "first" || catalog.Entries[0].Passwords[1] != "second" {
		t.Fatalf("passwords were not preserved: %+v", catalog.Entries[0].Passwords)
	}
}

func TestBuildGzipNormalizesTransliteratedTitleWithoutChangingIdentity(t *testing.T) {
	entry := validSourceEntry()
	entry.Title = "Analitik dannyih na Python"
	entry.Credit.Author = stringPointer("Skillbox")
	entry.RawBlock = "[Skillbox] Analitik dannyih na Python"
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

	catalog := decodeBuiltCatalog(t, &output)
	if catalog.Stats.NormalizedTitles != 1 {
		t.Fatalf("catalog normalized title count = %d, want 1", catalog.Stats.NormalizedTitles)
	}
	got := catalog.Entries[0]
	if got.ID != expectedID {
		t.Fatalf("course ID = %q, want raw-title identity %q", got.ID, expectedID)
	}
	if got.Title != "Аналитик данных на Python" {
		t.Fatalf("title = %q, want normalized Russian title", got.Title)
	}
	if got.TitleOriginal == nil || *got.TitleOriginal != "Analitik dannyih na Python" {
		t.Fatalf("title_original = %v, want raw title", got.TitleOriginal)
	}
	if !slices.Contains(got.Categories, "data_ai") {
		t.Fatalf("categories = %v, want normalized title to classify as data_ai", got.Categories)
	}
}

func TestBuildGzipCountsMergedNormalizedTitleOnce(t *testing.T) {
	first := validSourceEntry()
	first.Title = "Analitik dannyih na Python"
	first.Credit.Author = stringPointer("Skillbox")
	first.RawBlock = "[Skillbox] Analitik dannyih na Python"

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
	stats, err := BuildGzip(sourceReader(t, source), &output)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats.Entries != 1 || stats.NormalizedTitles != 1 {
		t.Fatalf("stats = %+v, want one merged normalized course", stats)
	}
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
	}
	source := validSource(t, entry)

	var output bytes.Buffer
	stats, err := BuildGzip(sourceReader(t, source), &output)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats.SourceLinks != 1 || stats.Links != 0 {
		t.Fatalf("stats = %+v, want raw source link counted and no actionable links", stats)
	}

	catalog := decodeBuiltCatalog(t, &output)
	if len(catalog.Entries[0].Links) != 0 {
		t.Fatalf("links = %+v, want title bare-domain entity filtered", catalog.Entries[0].Links)
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
			URL:            "magnet:?xt=urn:btih:0123456789abcdef",
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
