package courses

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildGzipWithTorrentDirAddsMagnetForMatchingAttachment(t *testing.T) {
	payload, info := validTorrentPayload()
	source := sourceWithTorrentAttachment(t, "Course.torrent")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Course.torrent"), payload, 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	var output bytes.Buffer
	stats, err := BuildGzipWithTorrentDir(sourceReader(t, source), &output, dir)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats.SourceLinks != 1 || stats.Links != 2 {
		t.Fatalf("stats = %+v, want one source link and two catalog links", stats)
	}

	catalog := decodeBuiltCatalog(t, &output)
	wantMagnet := "magnet:?xt=urn:btih:" + sha1Hex(info)
	if len(catalog.Entries[0].Links) != 2 || catalog.Entries[0].Links[1].URL != wantMagnet {
		t.Fatalf("links = %+v, want appended magnet %q", catalog.Entries[0].Links, wantMagnet)
	}
	if catalog.Entries[0].Links[1].Provider != "magnet" || catalog.Entries[0].Links[1].Kind != "torrent" ||
		catalog.Entries[0].Links[1].Role != "mirror" {
		t.Fatalf("magnet metadata = %+v", catalog.Entries[0].Links[1])
	}
}

func TestBuildGzipWithTorrentDirSkipsAmbiguousSourceFilenames(t *testing.T) {
	payload, _ := validTorrentPayload()
	source := sourceWithTwoTorrentAttachments(t, "Course.torrent", "Course.torrent")

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Course.torrent"), payload, 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}

	var output bytes.Buffer
	stats, err := BuildGzipWithTorrentDir(sourceReader(t, source), &output, dir)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats.Links != 2 {
		t.Fatalf("links = %d, want only original links when source filename is ambiguous", stats.Links)
	}
	catalog := decodeBuiltCatalog(t, &output)
	for _, entry := range catalog.Entries {
		for _, link := range entry.Links {
			if link.Provider == "magnet" {
				t.Fatalf("ambiguous source filename received magnet: %+v", catalog.Entries)
			}
		}
	}
}

func TestBuildGzipWithTorrentDirAddsMagnetForSameHashDuplicateSourceFilenames(t *testing.T) {
	payload, _ := validTorrentPayload()
	source := sourceWithTwoTorrentAttachments(t, "Course.torrent", "Course.torrent")
	dir := t.TempDir()
	for _, name := range []string{"Course.torrent", "Course (1).torrent"} {
		if err := os.WriteFile(filepath.Join(dir, name), payload, 0o600); err != nil {
			t.Fatalf("write torrent %q: %v", name, err)
		}
	}

	var output bytes.Buffer
	stats, err := BuildGzipWithTorrentDir(sourceReader(t, source), &output, dir)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats.Links != 4 {
		t.Fatalf("links = %d, want original links plus one common magnet per duplicate source", stats.Links)
	}
	catalog := decodeBuiltCatalog(t, &output)
	wantMagnet := "magnet:?xt=urn:btih:" + sha1Hex(validTorrentInfo())
	for _, entry := range catalog.Entries {
		if len(entry.Links) != 2 || entry.Links[1].URL != wantMagnet {
			t.Fatalf("entry links = %+v, want appended common magnet %q", entry.Links, wantMagnet)
		}
	}
}

func TestBuildGzipWithTorrentDirSkipsConflictingDuplicateTorrentHashes(t *testing.T) {
	payload, _ := validTorrentPayload()
	otherPayload, _ := otherTorrentPayload()
	source := sourceWithTwoTorrentAttachments(t, "Course.torrent", "Course.torrent")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Course.torrent"), payload, 0o600); err != nil {
		t.Fatalf("write torrent: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Course (1).torrent"), otherPayload, 0o600); err != nil {
		t.Fatalf("write collision torrent: %v", err)
	}

	var output bytes.Buffer
	stats, err := BuildGzipWithTorrentDir(sourceReader(t, source), &output, dir)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats.Links != 2 {
		t.Fatalf("links = %d, want only original links when duplicate candidates conflict", stats.Links)
	}
	catalog := decodeBuiltCatalog(t, &output)
	for _, entry := range catalog.Entries {
		for _, link := range entry.Links {
			if link.Provider == "magnet" {
				t.Fatalf("conflicting duplicate source filename received magnet: %+v", catalog.Entries)
			}
		}
	}
}

func TestBuildGzipWithTorrentDirKeepsExactSourceNameEndingInCollisionSuffix(t *testing.T) {
	payload, _ := validTorrentPayload()
	source := sourceWithTwoTorrentAttachments(t, "Course.torrent", "Course (1).torrent")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Course (1).torrent"), payload, 0o600); err != nil {
		t.Fatalf("write exact suffix torrent: %v", err)
	}

	var output bytes.Buffer
	stats, err := BuildGzipWithTorrentDir(sourceReader(t, source), &output, dir)
	if err != nil {
		t.Fatalf("build catalog: %v", err)
	}
	if stats.Links != 3 {
		t.Fatalf("links = %d, want only exact '(1)' source to receive a magnet", stats.Links)
	}
	catalog := decodeBuiltCatalog(t, &output)
	if len(catalog.Entries[0].Links) != 1 {
		t.Fatalf("base source links = %+v, want no collision-suffix match", catalog.Entries[0].Links)
	}
	if len(catalog.Entries[1].Links) != 2 || catalog.Entries[1].Links[1].Provider != "magnet" {
		t.Fatalf("exact '(1)' source links = %+v, want appended magnet", catalog.Entries[1].Links)
	}
}

func TestTorrentInfoBytesRejectsMalformedTorrent(t *testing.T) {
	if _, err := torrentInfoBytes([]byte("d4:infod4:name6:courseeejunk")); err == nil {
		t.Fatal("malformed torrent with trailing data was accepted")
	}
	if _, err := torrentInfoBytes([]byte("d4:name6:coursee")); err == nil {
		t.Fatal("torrent without info dictionary was accepted")
	}
	if _, err := torrentInfoBytes([]byte("d4:info4:spam")); err == nil {
		t.Fatal("torrent with non-dictionary info was accepted")
	}
}

func TestTorrentInfoHashRejectsOversizedTorrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "large.torrent")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create large torrent: %v", err)
	}
	if _, err := file.Write(make([]byte, maxTorrentSize+1)); err != nil {
		t.Fatalf("write large torrent: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close large torrent: %v", err)
	}
	if _, err := torrentInfoHash(path); err == nil {
		t.Fatal("oversized torrent was accepted")
	}
}

func sourceWithTorrentAttachment(t *testing.T, fileName string) sourceExport {
	t.Helper()

	entry := validSourceEntry()
	entry.Origin = "document"
	entry.Availability = "document_attachment"
	source := validSource(t, entry)
	source.Messages[0].Media.Type = "messageMediaDocument"
	source.Messages[0].Media.Document.FileName = fileName
	source.Messages[0].Media.Document.MIMEType = "application/x-bittorrent"
	return source
}

func sourceWithTwoTorrentAttachments(t *testing.T, firstFileName, secondFileName string) sourceExport {
	t.Helper()

	first := validSourceEntry()
	first.Origin = "document"
	first.Availability = "document_attachment"
	second := first
	second.EntryID = "1:2:0"
	second.MessageID = "1:2"
	second.SourceMessageIDs = []string{"1:2"}
	second.AddedAt = "2024-01-02T00:00:00Z"
	second.Title = "Fixture Course Two"
	second.Links = append([]sourceLink(nil), first.Links...)
	second.Links[0].URL = "https://example.test/course-two"
	source := validSource(t, first)
	source.Messages[0].Media.Type = "messageMediaDocument"
	source.Messages[0].Media.Document.FileName = firstFileName
	source.Messages[0].Media.Document.MIMEType = "application/x-bittorrent"
	source.Messages = append(source.Messages, sourceMessage{
		MessageID:         "1:2",
		TelegramMessageID: 2,
		URL:               "https://messages.example.test/source/2",
	})
	source.Messages[1].Media.Type = "messageMediaDocument"
	source.Messages[1].Media.Document.FileName = secondFileName
	source.Messages[1].Media.Document.MIMEType = "application/x-bittorrent"
	source.CatalogEntries = []sourceEntry{first, second}
	setSourceCounts(&source)
	return source
}

func validTorrentPayload() ([]byte, []byte) {
	info := validTorrentInfo()
	return append(append([]byte("d4:info"), info...), 'e'), info
}

func validTorrentInfo() []byte {
	return []byte("d4:name6:course6:lengthi12345e12:piece lengthi16384e6:pieces20:aaaaaaaaaaaaaaaaaaaae")
}

func otherTorrentPayload() ([]byte, []byte) {
	info := []byte("d4:name11:othercourse6:lengthi12345e12:piece lengthi16384e6:pieces20:bbbbbbbbbbbbbbbbbbbbe")
	return append(append([]byte("d4:info"), info...), 'e'), info
}

func sha1Hex(payload []byte) string {
	digest := sha1.Sum(payload)
	return hex.EncodeToString(digest[:])
}
