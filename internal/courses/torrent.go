package courses

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const maxTorrentSize = 16 << 20

func buildTorrentLinks(torrentDir string, messages []sourceMessage) (map[string]CatalogLink, error) {
	if strings.TrimSpace(torrentDir) == "" {
		return nil, nil
	}

	messageIDsByName := make(map[string][]string)
	for _, message := range messages {
		if !isTorrentDocument(message) {
			continue
		}
		name := normalizeTorrentFileName(message.Media.Document.FileName)
		if name == "" {
			continue
		}
		messageIDsByName[name] = append(messageIDsByName[name], message.MessageID)
	}
	filesByName, err := torrentFilesBySourceName(torrentDir, messageIDsByName)
	if err != nil {
		return nil, err
	}

	links := make(map[string]CatalogLink)
	for name, messageIDs := range messageIDsByName {
		slices.Sort(messageIDs)
		messageIDs = slices.Compact(messageIDs)
		paths := filesByName[name]
		if len(paths) == 0 || (len(messageIDs) > 1 && len(paths) < len(messageIDs)) {
			continue
		}
		infoHash, ok, err := commonTorrentInfoHash(paths)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		for _, messageID := range messageIDs {
			links[messageID] = CatalogLink{
				URL:      "magnet:?xt=urn:btih:" + infoHash,
				Provider: "magnet",
				Kind:     "torrent",
				Role:     "mirror",
			}
		}
	}
	return links, nil
}

func torrentFilesBySourceName(torrentDir string, sourceNames map[string][]string) (map[string][]string, error) {
	entries, err := os.ReadDir(torrentDir)
	if err != nil {
		return nil, fmt.Errorf("read torrent directory: %w", err)
	}
	filesByName := make(map[string][]string)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := normalizeTorrentFileName(entry.Name())
		if name == "" {
			continue
		}
		sourceName := torrentCandidateSourceName(name, sourceNames)
		if sourceName == "" {
			continue
		}
		filesByName[sourceName] = append(filesByName[sourceName], filepath.Join(torrentDir, entry.Name()))
	}
	for _, paths := range filesByName {
		slices.Sort(paths)
	}
	return filesByName, nil
}

func normalizeTorrentFileName(name string) string {
	name = strings.TrimSpace(filepath.Base(name))
	if !strings.HasSuffix(strings.ToLower(name), ".torrent") {
		return ""
	}
	return strings.ToLower(name)
}

func torrentCandidateSourceName(name string, sourceNames map[string][]string) string {
	if _, ok := sourceNames[name]; ok {
		return name
	}
	stripped := stripBrowserCollisionSuffix(name)
	if _, ok := sourceNames[stripped]; ok {
		return stripped
	}
	return ""
}

func stripBrowserCollisionSuffix(name string) string {
	base := strings.TrimSuffix(name, ".torrent")
	if !strings.HasSuffix(base, ")") {
		return ""
	}
	start := strings.LastIndex(base, " (")
	if start < 0 {
		return ""
	}
	suffix := base[start+2 : len(base)-1]
	if suffix == "" {
		return ""
	}
	for _, digit := range suffix {
		if digit < '0' || digit > '9' {
			return ""
		}
	}
	return base[:start] + ".torrent"
}

func isTorrentDocument(message sourceMessage) bool {
	return message.Media.Type == "messageMediaDocument" &&
		strings.EqualFold(message.Media.Document.MIMEType, "application/x-bittorrent")
}

func commonTorrentInfoHash(paths []string) (string, bool, error) {
	var common string
	for _, path := range paths {
		infoHash, err := torrentInfoHash(path)
		if err != nil {
			return "", false, err
		}
		if common == "" {
			common = infoHash
			continue
		}
		if infoHash != common {
			return "", false, nil
		}
	}
	return common, common != "", nil
}

func torrentInfoHash(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open torrent %q: %w", path, err)
	}
	defer file.Close()

	payload, err := io.ReadAll(io.LimitReader(file, maxTorrentSize+1))
	if err != nil {
		return "", fmt.Errorf("read torrent %q: %w", path, err)
	}
	if len(payload) > maxTorrentSize {
		return "", fmt.Errorf("torrent %q exceeds %d byte limit", path, maxTorrentSize)
	}
	info, err := torrentInfoBytes(payload)
	if err != nil {
		return "", fmt.Errorf("parse torrent %q: %w", path, err)
	}
	digest := sha1.Sum(info)
	return hex.EncodeToString(digest[:]), nil
}

func torrentInfoBytes(payload []byte) ([]byte, error) {
	parser := bencodeParser{data: payload}
	start, end, err := parser.parseTopLevelInfo()
	if err != nil {
		return nil, err
	}
	if parser.offset != len(payload) {
		return nil, errors.New("trailing data")
	}
	return payload[start:end], nil
}

type bencodeParser struct {
	data   []byte
	offset int
}

func (parser *bencodeParser) parseTopLevelInfo() (int, int, error) {
	if !parser.consume('d') {
		return 0, 0, errors.New("top-level value is not a dictionary")
	}
	var infoStart, infoEnd int
	for {
		if parser.consume('e') {
			if infoStart == 0 && infoEnd == 0 {
				return 0, 0, errors.New("missing info dictionary")
			}
			return infoStart, infoEnd, nil
		}
		key, err := parser.parseString()
		if err != nil {
			return 0, 0, fmt.Errorf("parse dictionary key: %w", err)
		}
		valueStart := parser.offset
		if err := parser.skipValue(); err != nil {
			return 0, 0, err
		}
		if bytes.Equal(key, []byte("info")) {
			if infoStart != 0 || infoEnd != 0 {
				return 0, 0, errors.New("duplicate info dictionary")
			}
			if valueStart >= len(parser.data) || parser.data[valueStart] != 'd' {
				return 0, 0, errors.New("info value is not a dictionary")
			}
			infoStart = valueStart
			infoEnd = parser.offset
		}
	}
}

func (parser *bencodeParser) skipValue() error {
	if parser.offset >= len(parser.data) {
		return errors.New("unexpected end of input")
	}
	switch value := parser.data[parser.offset]; {
	case value == 'd':
		parser.offset++
		for {
			if parser.consume('e') {
				return nil
			}
			if _, err := parser.parseString(); err != nil {
				return fmt.Errorf("parse dictionary key: %w", err)
			}
			if err := parser.skipValue(); err != nil {
				return err
			}
		}
	case value == 'l':
		parser.offset++
		for {
			if parser.consume('e') {
				return nil
			}
			if err := parser.skipValue(); err != nil {
				return err
			}
		}
	case value == 'i':
		return parser.skipInt()
	case value >= '0' && value <= '9':
		_, err := parser.parseString()
		return err
	default:
		return fmt.Errorf("invalid bencode value byte %q", value)
	}
}

func (parser *bencodeParser) skipInt() error {
	parser.offset++
	start := parser.offset
	if parser.consume('-') && (parser.offset >= len(parser.data) || parser.data[parser.offset] == '0') {
		return errors.New("invalid integer")
	}
	for parser.offset < len(parser.data) && parser.data[parser.offset] >= '0' && parser.data[parser.offset] <= '9' {
		parser.offset++
	}
	if parser.offset == start || (parser.data[start] == '-' && parser.offset == start+1) {
		return errors.New("invalid integer")
	}
	if !parser.consume('e') {
		return errors.New("unterminated integer")
	}
	return nil
}

func (parser *bencodeParser) parseString() ([]byte, error) {
	start := parser.offset
	if start >= len(parser.data) || parser.data[start] < '0' || parser.data[start] > '9' {
		return nil, errors.New("string length is missing")
	}
	if parser.data[start] == '0' && start+1 < len(parser.data) && parser.data[start+1] >= '0' && parser.data[start+1] <= '9' {
		return nil, errors.New("string length has leading zero")
	}
	length := 0
	for parser.offset < len(parser.data) && parser.data[parser.offset] >= '0' && parser.data[parser.offset] <= '9' {
		length = length*10 + int(parser.data[parser.offset]-'0')
		if length > len(parser.data) {
			return nil, errors.New("string length exceeds input size")
		}
		parser.offset++
	}
	if !parser.consume(':') {
		return nil, errors.New("string length is not terminated")
	}
	end := parser.offset + length
	if end > len(parser.data) {
		return nil, errors.New("string exceeds input size")
	}
	value := parser.data[parser.offset:end]
	parser.offset = end
	return value, nil
}

func (parser *bencodeParser) consume(value byte) bool {
	if parser.offset >= len(parser.data) || parser.data[parser.offset] != value {
		return false
	}
	parser.offset++
	return true
}
