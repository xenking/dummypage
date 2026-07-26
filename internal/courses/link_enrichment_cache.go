package courses

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	linkEnrichmentCacheSchema     = "link-enrichment/v1"
	linkEnrichmentExtractorV1     = 1
	linkEnrichmentStateExtracted  = "extracted"
	linkEnrichmentStateNotFound   = "not_found"
	maxLinkEnrichmentCacheBytes   = 256 << 20
	maxLinkEnrichmentCacheEntries = 250_000
)

type LinkEnrichmentCache struct {
	SchemaVersion    string                `json:"schema_version"`
	ExtractorVersion int                   `json:"extractor_version"`
	GeneratedAt      string                `json:"generated_at"`
	Entries          []LinkEnrichmentEntry `json:"entries"`

	byHash map[string]int
}

type LinkEnrichmentEntry struct {
	SHA256    string       `json:"sha256"`
	State     string       `json:"state"`
	CheckedAt string       `json:"checked_at"`
	Content   *LinkContent `json:"content,omitempty"`
}

type linkEnrichmentCacheFile struct {
	SchemaVersion    string            `json:"schema_version"`
	ExtractorVersion int               `json:"extractor_version"`
	GeneratedAt      string            `json:"generated_at"`
	Entries          []json.RawMessage `json:"entries"`
}

type linkEnrichmentEntryFile struct {
	SHA256    string          `json:"sha256"`
	State     string          `json:"state"`
	CheckedAt string          `json:"checked_at"`
	Content   json.RawMessage `json:"content"`
}

type linkContentFile struct {
	Name          string            `json:"name"`
	Kind          string            `json:"kind"`
	SizeBytes     int64             `json:"size_bytes"`
	FileCount     int               `json:"file_count"`
	FolderCount   int               `json:"folder_count"`
	Items         []json.RawMessage `json:"items"`
	MaterialTypes []string          `json:"material_types"`
}

func NewLinkEnrichmentCache(now time.Time) *LinkEnrichmentCache {
	return &LinkEnrichmentCache{
		SchemaVersion:    linkEnrichmentCacheSchema,
		ExtractorVersion: linkEnrichmentExtractorV1,
		GeneratedAt:      now.UTC().Format(time.RFC3339),
		Entries:          make([]LinkEnrichmentEntry, 0),
		byHash:           make(map[string]int),
	}
}

func LoadLinkEnrichmentCache(r io.Reader) (*LinkEnrichmentCache, error) {
	return loadLinkEnrichmentCache(r, maxLinkEnrichmentCacheBytes, maxLinkEnrichmentCacheEntries)
}

func loadLinkEnrichmentCache(r io.Reader, maxBytes int64, maxEntries int) (*LinkEnrichmentCache, error) {
	if maxBytes < 1 || maxEntries < 1 {
		return nil, errors.New("read link enrichment cache: invalid limits")
	}
	data, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read link enrichment cache: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return nil, errors.New("read link enrichment cache: file exceeds size limit")
	}
	if !utf8.Valid(data) {
		return nil, errors.New("read link enrichment cache: invalid utf-8")
	}
	if err := rejectDuplicateTopLevelKeys(data); err != nil {
		return nil, fmt.Errorf("decode link enrichment cache: %w", err)
	}
	var file linkEnrichmentCacheFile
	if err := decodeSingleJSONValue(data, &file); err != nil {
		return nil, fmt.Errorf("decode link enrichment cache: %w", err)
	}
	if file.SchemaVersion != linkEnrichmentCacheSchema {
		return nil, fmt.Errorf("decode link enrichment cache: unsupported schema_version %q", file.SchemaVersion)
	}
	if file.ExtractorVersion != linkEnrichmentExtractorV1 {
		return nil, fmt.Errorf("decode link enrichment cache: unsupported extractor_version %d", file.ExtractorVersion)
	}
	if _, err := time.Parse(time.RFC3339, file.GeneratedAt); err != nil {
		return nil, fmt.Errorf("decode link enrichment cache: invalid generated_at %q: %w", file.GeneratedAt, err)
	}
	if file.Entries == nil {
		return nil, errors.New("decode link enrichment cache: entries is required")
	}
	if len(file.Entries) > maxEntries {
		return nil, errors.New("decode link enrichment cache: entries exceeds limit")
	}

	cache := &LinkEnrichmentCache{
		SchemaVersion:    file.SchemaVersion,
		ExtractorVersion: file.ExtractorVersion,
		GeneratedAt:      file.GeneratedAt,
		Entries:          make([]LinkEnrichmentEntry, 0, len(file.Entries)),
		byHash:           make(map[string]int, len(file.Entries)),
	}
	for index, raw := range file.Entries {
		entry, err := decodeLinkEnrichmentEntry(index, raw)
		if err != nil {
			return nil, err
		}
		if _, exists := cache.byHash[entry.SHA256]; exists {
			return nil, fmt.Errorf("decode link enrichment cache: duplicate sha256 %q", entry.SHA256)
		}
		cache.byHash[entry.SHA256] = len(cache.Entries)
		cache.Entries = append(cache.Entries, entry)
	}
	sortLinkEnrichmentEntries(cache.Entries)
	cache.reindex()
	return cache, nil
}

func decodeLinkEnrichmentEntry(index int, raw json.RawMessage) (LinkEnrichmentEntry, error) {
	if err := rejectDuplicateTopLevelKeys(raw); err != nil {
		return LinkEnrichmentEntry{}, fmt.Errorf("decode link enrichment cache: entries[%d]: %w", index, err)
	}
	var file linkEnrichmentEntryFile
	if err := decodeSingleJSONValue(raw, &file); err != nil {
		return LinkEnrichmentEntry{}, fmt.Errorf("decode link enrichment cache: entries[%d]: %w", index, err)
	}
	if !linkAuditHashPattern.MatchString(file.SHA256) {
		return LinkEnrichmentEntry{}, fmt.Errorf("decode link enrichment cache: entries[%d] has invalid sha256", index)
	}
	if file.State != linkEnrichmentStateExtracted && file.State != linkEnrichmentStateNotFound {
		return LinkEnrichmentEntry{}, fmt.Errorf("decode link enrichment cache: entries[%d] has invalid state %q", index, file.State)
	}
	if _, err := time.Parse(time.RFC3339, file.CheckedAt); err != nil {
		return LinkEnrichmentEntry{}, fmt.Errorf("decode link enrichment cache: entries[%d] has invalid checked_at %q: %w", index, file.CheckedAt, err)
	}
	entry := LinkEnrichmentEntry{
		SHA256:    file.SHA256,
		State:     file.State,
		CheckedAt: file.CheckedAt,
	}
	if file.State == linkEnrichmentStateNotFound {
		if len(file.Content) != 0 {
			return LinkEnrichmentEntry{}, fmt.Errorf("decode link enrichment cache: entries[%d] not_found content must be omitted", index)
		}
		return entry, nil
	}
	if len(file.Content) == 0 || bytes.Equal(bytes.TrimSpace(file.Content), []byte("null")) {
		return LinkEnrichmentEntry{}, fmt.Errorf("decode link enrichment cache: entries[%d] extracted content is required", index)
	}
	content, err := decodeCachedLinkContent(index, file.Content)
	if err != nil {
		return LinkEnrichmentEntry{}, err
	}
	entry.Content = content
	return entry, nil
}

func decodeCachedLinkContent(entryIndex int, raw json.RawMessage) (*LinkContent, error) {
	if err := rejectDuplicateTopLevelKeys(raw); err != nil {
		return nil, fmt.Errorf("decode link enrichment cache: entries[%d].content: %w", entryIndex, err)
	}
	var file linkContentFile
	if err := decodeSingleJSONValue(raw, &file); err != nil {
		return nil, fmt.Errorf("decode link enrichment cache: entries[%d].content: %w", entryIndex, err)
	}
	content := LinkContent{
		Name:          file.Name,
		Kind:          file.Kind,
		SizeBytes:     file.SizeBytes,
		FileCount:     file.FileCount,
		FolderCount:   file.FolderCount,
		Items:         make([]LinkContentItem, 0, len(file.Items)),
		MaterialTypes: append([]string(nil), file.MaterialTypes...),
	}
	for itemIndex, rawItem := range file.Items {
		if err := rejectDuplicateTopLevelKeys(rawItem); err != nil {
			return nil, fmt.Errorf("decode link enrichment cache: entries[%d].content.items[%d]: %w", entryIndex, itemIndex, err)
		}
		var item LinkContentItem
		if err := decodeSingleJSONValue(rawItem, &item); err != nil {
			return nil, fmt.Errorf("decode link enrichment cache: entries[%d].content.items[%d]: %w", entryIndex, itemIndex, err)
		}
		content.Items = append(content.Items, item)
	}
	if err := validateCachedLinkContent(content); err != nil {
		return nil, fmt.Errorf("decode link enrichment cache: entries[%d].content: %w", entryIndex, err)
	}
	return &content, nil
}

func validateCachedLinkContent(content LinkContent) error {
	for _, value := range []string{content.Name, content.Kind} {
		if err := validateLinkEnrichmentCacheString(value); err != nil {
			return err
		}
	}
	if content.SizeBytes < 0 || content.FileCount < 0 || content.FolderCount < 0 {
		return errors.New("numeric field is negative")
	}
	if len(content.Items) > maxLinkEnrichmentItems {
		return errors.New("items exceeds limit")
	}
	for _, item := range content.Items {
		for _, value := range []string{item.Name, item.Kind} {
			if err := validateLinkEnrichmentCacheString(value); err != nil {
				return fmt.Errorf("item: %w", err)
			}
		}
		if item.SizeBytes < 0 {
			return errors.New("item size is negative")
		}
	}
	seenTypes := make(map[string]struct{}, len(content.MaterialTypes))
	previous := ""
	for _, value := range content.MaterialTypes {
		if !validLinkContentMaterialType(value) {
			return fmt.Errorf("invalid material type %q", value)
		}
		if _, exists := seenTypes[value]; exists {
			return fmt.Errorf("duplicate material type %q", value)
		}
		if previous != "" && previous > value {
			return errors.New("material types are not sorted")
		}
		seenTypes[value] = struct{}{}
		previous = value
	}
	if content.Name == "" && content.Kind == "" && content.SizeBytes == 0 &&
		content.FileCount == 0 && content.FolderCount == 0 && len(content.Items) == 0 {
		return errors.New("content is empty")
	}
	return nil
}

func validateLinkEnrichmentCacheString(value string) error {
	if len(value) > maxLinkContentStringBytes {
		return errors.New("string field is too large")
	}
	if strings.TrimSpace(value) != value {
		return errors.New("string field is not trimmed")
	}
	lower := strings.ToLower(value)
	if containsControlCharacter(value) ||
		strings.ContainsAny(value, "<>") ||
		strings.Contains(lower, "://") ||
		hasLocatorSchemePrefix(lower) {
		return errors.New("string field contains non-metadata content")
	}
	return nil
}

func hasLocatorSchemePrefix(value string) bool {
	scheme, _, ok := strings.Cut(value, ":")
	if !ok {
		return false
	}
	switch scheme {
	case "blob", "data", "file", "ftp", "ftps", "geo", "git", "gs", "http", "https",
		"ipfs", "ipns", "irc", "ircs", "javascript", "magnet", "mailto", "news", "nntp",
		"s3", "sip", "sips", "sms", "ssh", "tel", "urn", "webcal", "ws", "wss", "xmpp":
		return true
	default:
		return false
	}
}

func validLinkContentMaterialType(value string) bool {
	switch value {
	case "archive", "audio", "document", "image", "torrent", "video":
		return true
	default:
		return false
	}
}

func (cache *LinkEnrichmentCache) ContentForURL(rawURL string) (*LinkContent, bool) {
	hash, err := linkHash(rawURL)
	if err != nil {
		return nil, false
	}
	entry, ok := cache.entry(hash)
	if !ok || entry.State != linkEnrichmentStateExtracted || entry.Content == nil {
		return nil, false
	}
	content := cloneLinkContent(*entry.Content)
	return &content, true
}

func (cache *LinkEnrichmentCache) IsFreshURL(rawURL string, now time.Time, staleAfter time.Duration) bool {
	hash, err := linkHash(rawURL)
	if err != nil {
		return false
	}
	entry, ok := cache.entry(hash)
	if !ok {
		return false
	}
	checkedAt, err := time.Parse(time.RFC3339, entry.CheckedAt)
	if err != nil {
		return false
	}
	return staleAfter > 0 && now.Before(checkedAt.Add(staleAfter))
}

func (cache *LinkEnrichmentCache) PutExtracted(hash string, content LinkContent, checkedAt time.Time) error {
	if err := validateCachedLinkContent(content); err != nil {
		return fmt.Errorf("put link enrichment: %w", err)
	}
	return cache.put(LinkEnrichmentEntry{
		SHA256:    hash,
		State:     linkEnrichmentStateExtracted,
		CheckedAt: checkedAt.UTC().Format(time.RFC3339),
		Content:   pointerToLinkContent(cloneLinkContent(content)),
	})
}

func (cache *LinkEnrichmentCache) PutNotFound(hash string, checkedAt time.Time) error {
	return cache.put(LinkEnrichmentEntry{
		SHA256:    hash,
		State:     linkEnrichmentStateNotFound,
		CheckedAt: checkedAt.UTC().Format(time.RFC3339),
	})
}

func (cache *LinkEnrichmentCache) put(entry LinkEnrichmentEntry) error {
	if cache == nil {
		return errors.New("put link enrichment: cache is nil")
	}
	if !linkAuditHashPattern.MatchString(entry.SHA256) {
		return errors.New("put link enrichment: invalid sha256")
	}
	if cache.byHash == nil {
		cache.reindex()
	}
	if index, exists := cache.byHash[entry.SHA256]; exists {
		cache.Entries[index] = entry
		return nil
	}
	cache.byHash[entry.SHA256] = len(cache.Entries)
	cache.Entries = append(cache.Entries, entry)
	return nil
}

func WriteLinkEnrichmentCache(w io.Writer, cache *LinkEnrichmentCache) error {
	if cache == nil {
		return errors.New("write link enrichment cache: cache is nil")
	}
	if _, err := time.Parse(time.RFC3339, cache.GeneratedAt); err != nil {
		return errors.New("write link enrichment cache: invalid generated_at")
	}
	entries := append([]LinkEnrichmentEntry(nil), cache.Entries...)
	seen := make(map[string]struct{}, len(entries))
	for index, entry := range entries {
		if err := validateLinkEnrichmentEntry(entry); err != nil {
			return fmt.Errorf("write link enrichment cache: entries[%d]: %w", index, err)
		}
		if _, exists := seen[entry.SHA256]; exists {
			return fmt.Errorf("write link enrichment cache: duplicate sha256 %q", entry.SHA256)
		}
		seen[entry.SHA256] = struct{}{}
	}
	sortLinkEnrichmentEntries(entries)
	file := LinkEnrichmentCache{
		SchemaVersion:    linkEnrichmentCacheSchema,
		ExtractorVersion: linkEnrichmentExtractorV1,
		GeneratedAt:      cache.GeneratedAt,
		Entries:          entries,
	}
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(file); err != nil {
		return fmt.Errorf("write link enrichment cache: %w", err)
	}
	return nil
}

func validateLinkEnrichmentEntry(entry LinkEnrichmentEntry) error {
	if !linkAuditHashPattern.MatchString(entry.SHA256) {
		return errors.New("invalid sha256")
	}
	if _, err := time.Parse(time.RFC3339, entry.CheckedAt); err != nil {
		return errors.New("invalid checked_at")
	}
	switch entry.State {
	case linkEnrichmentStateExtracted:
		if entry.Content == nil {
			return errors.New("extracted content is required")
		}
		return validateCachedLinkContent(*entry.Content)
	case linkEnrichmentStateNotFound:
		if entry.Content != nil {
			return errors.New("not_found content must be omitted")
		}
		return nil
	default:
		return errors.New("invalid state")
	}
}

func (cache *LinkEnrichmentCache) entry(hash string) (LinkEnrichmentEntry, bool) {
	if cache == nil {
		return LinkEnrichmentEntry{}, false
	}
	if cache.byHash == nil {
		cache.reindex()
	}
	index, ok := cache.byHash[hash]
	if !ok {
		return LinkEnrichmentEntry{}, false
	}
	return cache.Entries[index], true
}

func (cache *LinkEnrichmentCache) reindex() {
	cache.byHash = make(map[string]int, len(cache.Entries))
	for index, entry := range cache.Entries {
		cache.byHash[entry.SHA256] = index
	}
}

func sortLinkEnrichmentEntries(entries []LinkEnrichmentEntry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].SHA256 < entries[j].SHA256
	})
}

func cloneLinkContent(content LinkContent) LinkContent {
	content.Items = append([]LinkContentItem(nil), content.Items...)
	content.MaterialTypes = append([]string(nil), content.MaterialTypes...)
	return content
}

func pointerToLinkContent(content LinkContent) *LinkContent {
	return &content
}
