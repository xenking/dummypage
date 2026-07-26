package main

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildFileAcceptsOptionalTorrentDir(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "telegram.json")
	outputPath := filepath.Join(dir, "catalog.json.gz")
	torrentDir := filepath.Join(dir, "torrents")
	if err := os.Mkdir(torrentDir, 0o700); err != nil {
		t.Fatalf("create torrent dir: %v", err)
	}
	if err := os.WriteFile(inputPath, []byte(sourceWithTorrentAttachmentJSON()), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	if err := os.WriteFile(filepath.Join(torrentDir, "Course.torrent"), validTorrentPayload(), 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	if err := buildFile(inputPath, outputPath, torrentDir); err != nil {
		t.Fatalf("build file: %v", err)
	}

	compressed, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read catalog: %v", err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("open catalog gzip: %v", err)
	}
	var payload bytes.Buffer
	if _, err := payload.ReadFrom(reader); err != nil {
		t.Fatalf("read catalog gzip: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close catalog gzip: %v", err)
	}
	if !strings.Contains(payload.String(), `"magnet:?xt=urn:btih:`) {
		t.Fatalf("catalog does not contain enriched magnet: %s", payload.String())
	}
}

func sourceWithTorrentAttachmentJSON() string {
	return `{
		"schema_version":"telegram-webk-channel-export/v3",
		"exported_at":"2026-07-26T00:00:00Z",
			"source":{"channel_id":1,"title":"Course Export","web_url":"https://example.test"},
		"stats":{
			"retrieval":{"exported_message_count":1},
			"parsing":{"catalog_entry_count":1,"parsed_link_count":1,"password_value_count":0}
		},
		"messages":[{
			"message_id":"1:1",
			"telegram_message_id":1,
			"url":"https://messages.example.test/source/1",
			"media":{
				"type":"messageMediaDocument",
				"document":{"file_name":"Course.torrent","mime_type":"application/x-bittorrent"}
			}
		}],
		"catalog_entries":[{
			"entry_id":"1:1:0",
			"message_id":"1:1",
			"source_message_ids":["1:1"],
			"added_at":"2024-01-01T00:00:00Z",
			"origin":"document",
			"title":"Practical Go",
			"year":null,
			"year_range":null,
			"availability":"document_attachment",
			"links":[{
				"url":"https://example.test/course",
				"host":"example.test",
				"provider":"example",
				"kind":"file_host",
				"role":"primary",
				"primary":true,
				"label":null
			}],
			"passwords":[],
			"notes":null,
			"raw_block":"Practical Go"
		}]
	}`
}

func validTorrentPayload() []byte {
	info := []byte("d4:name6:course6:lengthi12345e12:piece lengthi16384e6:pieces20:aaaaaaaaaaaaaaaaaaaae")
	return append(append([]byte("d4:info"), info...), 'e')
}

func TestBuildFilesMergesMultipleInputsFromFlagStyleConfig(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.json")
	newPath := filepath.Join(dir, "new.json")
	outputPath := filepath.Join(dir, "catalog.json.gz")
	if err := os.WriteFile(oldPath, []byte(sourceWithTitleJSON("1:1:0", "1:1", 1, "Legacy Course")), 0o600); err != nil {
		t.Fatalf("write old source: %v", err)
	}
	if err := os.WriteFile(newPath, []byte(sourceWithTitleJSON("1:2:0", "1:2", 2, "Current Course")), 0o600); err != nil {
		t.Fatalf("write new source: %v", err)
	}

	config, err := parseArgs([]string{"--input", oldPath, "--input", newPath, "--output", outputPath})
	if err != nil {
		t.Fatalf("parse args: %v", err)
	}
	if err := buildFiles(config.InputPaths, config.OutputPath, config.TorrentDir, config.TitleRulesPath, config.LinkTombstonesPath); err != nil {
		t.Fatalf("build files: %v", err)
	}

	payload := readGzipFile(t, outputPath)
	if !strings.Contains(payload, "Legacy Course") || !strings.Contains(payload, "Current Course") {
		t.Fatalf("catalog does not preserve both inputs: %s", payload)
	}
}

func TestParseArgsAcceptsTitleRules(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "source.json")
	outputPath := filepath.Join(dir, "catalog.json.gz")
	rulesPath := filepath.Join(dir, "rules.json")

	config, err := parseArgs([]string{
		"--input", inputPath,
		"--output", outputPath,
		"--torrent-dir", filepath.Join(dir, "torrents"),
		"--title-rules", rulesPath,
	})
	if err != nil {
		t.Fatalf("parse args: %v", err)
	}
	if config.TitleRulesPath != rulesPath {
		t.Fatalf("title rules path = %q, want %q", config.TitleRulesPath, rulesPath)
	}
	if config.InputPaths[0] != inputPath || config.OutputPath != outputPath {
		t.Fatalf("config lost existing args: %+v", config)
	}
}

func TestParseArgsAcceptsLinkTombstones(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "source.json")
	outputPath := filepath.Join(dir, "catalog.json.gz")
	tombstonesPath := filepath.Join(dir, "link-tombstones.json")

	config, err := parseArgs([]string{
		"--input", inputPath,
		"--output", outputPath,
		"--link-tombstones", tombstonesPath,
	})
	if err != nil {
		t.Fatalf("parse args: %v", err)
	}
	if config.LinkTombstonesPath != tombstonesPath {
		t.Fatalf("link tombstones path = %q, want %q", config.LinkTombstonesPath, tombstonesPath)
	}
	if config.InputPaths[0] != inputPath || config.OutputPath != outputPath {
		t.Fatalf("config lost existing args: %+v", config)
	}
}

func TestParseArgsAcceptsLinkEnrichment(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "source.json")
	outputPath := filepath.Join(dir, "catalog.json.gz")
	enrichmentPath := filepath.Join(dir, "link-enrichment.json")

	config, err := parseArgs([]string{
		"--input", inputPath,
		"--output", outputPath,
		"--link-enrichment", enrichmentPath,
	})
	if err != nil {
		t.Fatalf("parse args: %v", err)
	}
	if config.LinkEnrichmentPath != enrichmentPath {
		t.Fatalf("link enrichment path = %q, want %q", config.LinkEnrichmentPath, enrichmentPath)
	}
}

func TestBuildFilesWithLinkEnrichmentEmitsCachedContent(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "source.json")
	outputPath := filepath.Join(dir, "catalog.json.gz")
	enrichmentPath := filepath.Join(dir, "link-enrichment.json")
	writeSource := sourceWithTitleJSON("1:1:0", "1:1", 1, "Practical Go")
	if err := os.WriteFile(inputPath, []byte(writeSource), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	hash := sha256.Sum256([]byte("https://example.test/course/1:1"))
	if err := os.WriteFile(enrichmentPath, []byte(fmt.Sprintf(`{
		"schema_version":"link-enrichment/v1",
		"extractor_version":1,
		"generated_at":"2026-07-27T00:00:00Z",
		"entries":[{
			"sha256":"%x",
			"state":"extracted",
			"checked_at":"2026-07-27T00:00:00Z",
			"content":{"name":"Course bundle","kind":"folder"}
		}]
	}`, hash)), 0o600); err != nil {
		t.Fatalf("write enrichment: %v", err)
	}

	if err := buildFilesWithLinkEnrichment(
		[]string{inputPath},
		outputPath,
		"",
		"",
		"",
		enrichmentPath,
	); err != nil {
		t.Fatalf("build files: %v", err)
	}
	payload := readGzipFile(t, outputPath)
	if !strings.Contains(payload, `"enriched_links":1`) ||
		!strings.Contains(payload, `"content":{"name":"Course bundle","kind":"folder"}`) {
		t.Fatalf("catalog missing enrichment: %s", payload)
	}
}

func TestBuildFilesMalformedLinkEnrichmentDoesNotOpenSourcesOrReplaceOutput(t *testing.T) {
	dir := t.TempDir()
	missingInputPath := filepath.Join(dir, "missing-source.json")
	outputPath := filepath.Join(dir, "catalog.json.gz")
	enrichmentPath := filepath.Join(dir, "link-enrichment.json")
	const sentinel = "sentinel output"
	if err := os.WriteFile(outputPath, []byte(sentinel), 0o600); err != nil {
		t.Fatalf("write sentinel output: %v", err)
	}
	if err := os.WriteFile(enrichmentPath, []byte(`{"schema_version":"bad"}`), 0o600); err != nil {
		t.Fatalf("write malformed enrichment: %v", err)
	}

	err := buildFilesWithLinkEnrichment(
		[]string{missingInputPath},
		outputPath,
		"",
		"",
		"",
		enrichmentPath,
	)
	if err == nil {
		t.Fatal("build files succeeded with malformed link enrichment")
	}
	if !strings.Contains(err.Error(), "load link enrichment") ||
		!strings.Contains(err.Error(), enrichmentPath) {
		t.Fatalf("error = %v, want contextual enrichment path", err)
	}
	if got, readErr := os.ReadFile(outputPath); readErr != nil || string(got) != sentinel {
		t.Fatalf("output changed: %q, error=%v", string(got), readErr)
	}
}

func TestBuildFilesMalformedLinkTombstonesDoesNotOpenSourcesOrReplaceOutput(t *testing.T) {
	dir := t.TempDir()
	missingInputPath := filepath.Join(dir, "missing-source.json")
	outputPath := filepath.Join(dir, "catalog.json.gz")
	tombstonesPath := filepath.Join(dir, "link-tombstones.json")
	const sentinel = "sentinel output"

	if err := os.WriteFile(outputPath, []byte(sentinel), 0o600); err != nil {
		t.Fatalf("write sentinel output: %v", err)
	}
	if err := os.WriteFile(tombstonesPath, []byte(`{"schema_version":"bad"}`), 0o600); err != nil {
		t.Fatalf("write malformed tombstones: %v", err)
	}

	err := buildFiles([]string{missingInputPath}, outputPath, "", "", tombstonesPath)
	if err == nil {
		t.Fatal("build files succeeded with malformed link tombstones")
	}
	if !strings.Contains(err.Error(), "load link tombstones") || !strings.Contains(err.Error(), tombstonesPath) {
		t.Fatalf("error = %v, want contextual tombstones path", err)
	}
	got, readErr := os.ReadFile(outputPath)
	if readErr != nil {
		t.Fatalf("read output: %v", readErr)
	}
	if string(got) != sentinel {
		t.Fatalf("output was replaced: %q", string(got))
	}
}

func TestBuildFilesMalformedTitleRulesDoesNotOpenSourcesOrReplaceOutput(t *testing.T) {
	dir := t.TempDir()
	missingInputPath := filepath.Join(dir, "missing-source.json")
	outputPath := filepath.Join(dir, "catalog.json.gz")
	rulesPath := filepath.Join(dir, "rules.json")
	const sentinel = "sentinel output"

	if err := os.WriteFile(outputPath, []byte(sentinel), 0o600); err != nil {
		t.Fatalf("write sentinel output: %v", err)
	}
	if err := os.WriteFile(rulesPath, []byte(`{"schema_version":"bad"}`), 0o600); err != nil {
		t.Fatalf("write malformed rules: %v", err)
	}

	err := buildFiles([]string{missingInputPath}, outputPath, "", rulesPath, "")
	if err == nil {
		t.Fatal("build files succeeded with malformed title rules")
	}
	if !strings.Contains(err.Error(), "load title rules") || !strings.Contains(err.Error(), rulesPath) {
		t.Fatalf("error = %v, want contextual title rules path", err)
	}
	got, readErr := os.ReadFile(outputPath)
	if readErr != nil {
		t.Fatalf("read output: %v", readErr)
	}
	if string(got) != sentinel {
		t.Fatalf("output was replaced: %q", string(got))
	}
}

func TestParseArgsReadsInputDirectoryInStableOrder(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"b.json", "a.json", "ignore.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	config, err := parseArgs([]string{"--input-dir", dir, "--output", filepath.Join(dir, "catalog.json.gz")})
	if err != nil {
		t.Fatalf("parse args: %v", err)
	}
	want := []string{filepath.Join(dir, "a.json"), filepath.Join(dir, "b.json")}
	if len(config.InputPaths) != len(want) || config.InputPaths[0] != want[0] || config.InputPaths[1] != want[1] {
		t.Fatalf("input paths = %v, want %v", config.InputPaths, want)
	}
}

func readGzipFile(t *testing.T, path string) string {
	t.Helper()

	compressed, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read catalog: %v", err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("open catalog gzip: %v", err)
	}
	var payload bytes.Buffer
	if _, err := payload.ReadFrom(reader); err != nil {
		t.Fatalf("read catalog gzip: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close catalog gzip: %v", err)
	}
	return payload.String()
}

func sourceWithTitleJSON(entryID, messageID string, telegramID int, title string) string {
	return fmt.Sprintf(`{
		"schema_version":"telegram-webk-channel-export/v3",
		"exported_at":"2026-07-26T00:00:00Z",
		"source":{"channel_id":1,"title":"Course Export","web_url":"https://example.test"},
		"stats":{
			"retrieval":{"exported_message_count":1},
			"parsing":{"catalog_entry_count":1,"parsed_link_count":1,"password_value_count":0}
		},
		"messages":[{
			"message_id":%q,
			"telegram_message_id":%d,
			"url":"https://example.test/messages/%d"
		}],
		"catalog_entries":[{
			"entry_id":%q,
			"message_id":%q,
			"source_message_ids":[%q],
			"added_at":"2024-01-01T00:00:00Z",
			"origin":"text_block",
			"title":%q,
			"year":null,
			"year_range":null,
			"availability":"download_link",
			"links":[{
				"url":"https://example.test/course/%s",
				"host":"example.test",
				"provider":"example",
				"kind":"file_host",
				"role":"primary",
				"primary":true,
				"label":null
			}],
			"passwords":[],
			"notes":null,
			"raw_block":%q
		}]
	}`, messageID, telegramID, telegramID, entryID, messageID, messageID, title, messageID, title)
}
