package main

import (
	"bytes"
	"compress/gzip"
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
	if err := buildFiles(config.InputPaths, config.OutputPath, config.TorrentDir); err != nil {
		t.Fatalf("build files: %v", err)
	}

	payload := readGzipFile(t, outputPath)
	if !strings.Contains(payload, "Legacy Course") || !strings.Contains(payload, "Current Course") {
		t.Fatalf("catalog does not preserve both inputs: %s", payload)
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
