package courses

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	linkEnrichmentPolicySchema   = "link-enrichment-policy/v1"
	maxLinkEnrichmentBodyBytes   = 8 << 20
	maxLinkEnrichmentItems       = 1000
	maxLinkContentStringBytes    = 4096
	maxLinkEnrichmentPolicyBytes = 1 << 20
	maxLinkEnrichmentRules       = 64
	maxLinkEnrichmentMarkers     = 32
)

type LinkEnrichmentPolicy struct {
	MaxBodyBytes int64
	MaxItems     int
	StaleAfter   time.Duration
	Rules        []LinkEnrichmentRule
}

type LinkEnrichmentRule struct {
	HostSuffixes      []string
	JSONObjectMarkers []string
	Fields            LinkEnrichmentFields
}

type LinkEnrichmentFields struct {
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	SizeBytes     string `json:"size_bytes"`
	FileCount     string `json:"file_count"`
	FolderCount   string `json:"folder_count"`
	Items         string `json:"items"`
	ItemName      string `json:"item_name"`
	ItemKind      string `json:"item_kind"`
	ItemSizeBytes string `json:"item_size_bytes"`
}

type LinkContent struct {
	Name          string            `json:"name,omitempty"`
	Kind          string            `json:"kind,omitempty"`
	SizeBytes     int64             `json:"size_bytes,omitempty"`
	FileCount     int               `json:"file_count,omitempty"`
	FolderCount   int               `json:"folder_count,omitempty"`
	Items         []LinkContentItem `json:"items,omitempty"`
	MaterialTypes []string          `json:"material_types,omitempty"`
}

type LinkContentItem struct {
	Name      string `json:"name,omitempty"`
	Kind      string `json:"kind,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
}

type linkEnrichmentPolicyFile struct {
	SchemaVersion string            `json:"schema_version"`
	MaxBodyBytes  int64             `json:"max_body_bytes"`
	MaxItems      int               `json:"max_items"`
	StaleAfter    string            `json:"stale_after"`
	Rules         []json.RawMessage `json:"rules"`
}

type linkEnrichmentRuleFile struct {
	HostSuffixes      []string        `json:"host_suffixes"`
	JSONObjectMarkers []string        `json:"json_object_markers"`
	Fields            json.RawMessage `json:"fields"`
}

func LoadLinkEnrichmentPolicy(r io.Reader) (*LinkEnrichmentPolicy, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxLinkEnrichmentPolicyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read link enrichment policy: %w", err)
	}
	if len(data) > maxLinkEnrichmentPolicyBytes {
		return nil, errors.New("read link enrichment policy: file exceeds size limit")
	}
	if !utf8.Valid(data) {
		return nil, errors.New("read link enrichment policy: invalid utf-8")
	}
	if err := rejectDuplicateTopLevelKeys(data); err != nil {
		return nil, fmt.Errorf("decode link enrichment policy: %w", err)
	}
	var file linkEnrichmentPolicyFile
	if err := decodeSingleJSONValue(data, &file); err != nil {
		return nil, fmt.Errorf("decode link enrichment policy: %w", err)
	}
	if file.SchemaVersion != linkEnrichmentPolicySchema {
		return nil, fmt.Errorf("decode link enrichment policy: unsupported schema_version %q", file.SchemaVersion)
	}
	if file.MaxBodyBytes < 1 || file.MaxBodyBytes > maxLinkEnrichmentBodyBytes {
		return nil, fmt.Errorf("decode link enrichment policy: max_body_bytes must be between 1 and %d", maxLinkEnrichmentBodyBytes)
	}
	if file.MaxItems < 1 || file.MaxItems > maxLinkEnrichmentItems {
		return nil, fmt.Errorf("decode link enrichment policy: max_items must be between 1 and %d", maxLinkEnrichmentItems)
	}
	staleAfter, err := time.ParseDuration(file.StaleAfter)
	if err != nil || staleAfter <= 0 {
		return nil, errors.New("decode link enrichment policy: stale_after must be a positive duration")
	}
	if len(file.Rules) == 0 || len(file.Rules) > maxLinkEnrichmentRules {
		return nil, fmt.Errorf("decode link enrichment policy: rules must contain between 1 and %d entries", maxLinkEnrichmentRules)
	}

	policy := &LinkEnrichmentPolicy{
		MaxBodyBytes: file.MaxBodyBytes,
		MaxItems:     file.MaxItems,
		StaleAfter:   staleAfter,
		Rules:        make([]LinkEnrichmentRule, 0, len(file.Rules)),
	}
	seenSuffixes := make(map[string]struct{})
	for index, raw := range file.Rules {
		rule, err := decodeLinkEnrichmentRule(index, raw)
		if err != nil {
			return nil, err
		}
		for _, suffix := range rule.HostSuffixes {
			if _, exists := seenSuffixes[suffix]; exists {
				return nil, fmt.Errorf("decode link enrichment policy: duplicate host suffix %q across rules", suffix)
			}
			seenSuffixes[suffix] = struct{}{}
		}
		policy.Rules = append(policy.Rules, rule)
	}
	return policy, nil
}

func decodeLinkEnrichmentRule(index int, raw json.RawMessage) (LinkEnrichmentRule, error) {
	if err := rejectDuplicateTopLevelKeys(raw); err != nil {
		return LinkEnrichmentRule{}, fmt.Errorf("decode link enrichment policy: rules[%d]: %w", index, err)
	}
	var file linkEnrichmentRuleFile
	if err := decodeSingleJSONValue(raw, &file); err != nil {
		return LinkEnrichmentRule{}, fmt.Errorf("decode link enrichment policy: rules[%d]: %w", index, err)
	}
	if len(file.HostSuffixes) == 0 {
		return LinkEnrichmentRule{}, fmt.Errorf("decode link enrichment policy: rules[%d]: host_suffixes must not be empty", index)
	}
	suffixes, err := validateLinkAuditSuffixes("host_suffixes", file.HostSuffixes)
	if err != nil {
		return LinkEnrichmentRule{}, fmt.Errorf("decode link enrichment policy: rules[%d]: %w", index, err)
	}
	for _, suffix := range suffixes {
		if isLocalTarget(suffix) {
			return LinkEnrichmentRule{}, fmt.Errorf("decode link enrichment policy: rules[%d]: unsafe host suffix", index)
		}
	}
	markers, err := validateLinkEnrichmentMarkers(file.JSONObjectMarkers)
	if err != nil {
		return LinkEnrichmentRule{}, fmt.Errorf("decode link enrichment policy: rules[%d]: %w", index, err)
	}
	if err := rejectDuplicateTopLevelKeys(file.Fields); err != nil {
		return LinkEnrichmentRule{}, fmt.Errorf("decode link enrichment policy: rules[%d]: fields: %w", index, err)
	}
	var fields LinkEnrichmentFields
	if err := decodeSingleJSONValue(file.Fields, &fields); err != nil {
		return LinkEnrichmentRule{}, fmt.Errorf("decode link enrichment policy: rules[%d]: fields: %w", index, err)
	}
	if err := validateLinkEnrichmentFields(fields); err != nil {
		return LinkEnrichmentRule{}, fmt.Errorf("decode link enrichment policy: rules[%d]: %w", index, err)
	}
	return LinkEnrichmentRule{
		HostSuffixes:      suffixes,
		JSONObjectMarkers: markers,
		Fields:            fields,
	}, nil
}

func validateLinkEnrichmentMarkers(values []string) ([]string, error) {
	if len(values) == 0 || len(values) > maxLinkEnrichmentMarkers {
		return nil, fmt.Errorf("json_object_markers must contain between 1 and %d entries", maxLinkEnrichmentMarkers)
	}
	seen := make(map[string]struct{}, len(values))
	markers := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || len(value) > 256 || strings.TrimSpace(value) != value {
			return nil, errors.New("json_object_markers contains invalid marker")
		}
		if _, exists := seen[value]; exists {
			return nil, errors.New("json_object_markers contains duplicate marker")
		}
		seen[value] = struct{}{}
		markers = append(markers, value)
	}
	return markers, nil
}

func validateLinkEnrichmentFields(fields LinkEnrichmentFields) error {
	values := []string{
		fields.Name,
		fields.Kind,
		fields.SizeBytes,
		fields.FileCount,
		fields.FolderCount,
		fields.Items,
		fields.ItemName,
		fields.ItemKind,
		fields.ItemSizeBytes,
	}
	nonEmpty := 0
	for _, value := range values {
		if value == "" {
			continue
		}
		nonEmpty++
		for _, segment := range strings.Split(value, ".") {
			if !validLinkEnrichmentPathSegment(segment) {
				return fmt.Errorf("fields contains invalid path %q", value)
			}
		}
	}
	if nonEmpty == 0 {
		return errors.New("fields must contain at least one path")
	}
	rootFields := []string{
		fields.Name,
		fields.Kind,
		fields.SizeBytes,
		fields.FileCount,
		fields.FolderCount,
		fields.Items,
	}
	rootCount := 0
	for _, value := range rootFields {
		if value != "" {
			rootCount++
		}
	}
	if rootCount == 0 {
		return errors.New("fields must contain at least one root path")
	}
	itemCount := 0
	for _, value := range []string{fields.ItemName, fields.ItemKind, fields.ItemSizeBytes} {
		if value != "" {
			itemCount++
		}
	}
	if fields.Items == "" && itemCount > 0 {
		return errors.New("item fields require items")
	}
	if fields.Items != "" && itemCount == 0 {
		return errors.New("items requires at least one item field")
	}
	return nil
}

func validLinkEnrichmentPathSegment(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') &&
			(character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') &&
			character != '_' && character != '-' {
			return false
		}
	}
	return true
}

func (policy *LinkEnrichmentPolicy) Extract(host string, body []byte) (*LinkContent, bool, error) {
	if policy == nil {
		return nil, false, errors.New("extract link enrichment: policy is nil")
	}
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	rule := policy.matchingRule(host)
	if rule == nil {
		return nil, false, nil
	}
	if int64(len(body)) > policy.MaxBodyBytes {
		return nil, false, errors.New("extract link enrichment: response body exceeds policy limit")
	}
	var firstErr error
	for _, marker := range rule.JSONObjectMarkers {
		searchStart := 0
		for searchStart < len(body) {
			relativeIndex := bytes.Index(body[searchStart:], []byte(marker))
			if relativeIndex < 0 {
				break
			}
			index := searchStart + relativeIndex
			searchStart = index + len(marker)
			object, plausible, err := decodeJSONObjectPrefix(body[searchStart:])
			if !plausible {
				continue
			}
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			content, err := decodeLinkContent(object, rule.Fields, policy.MaxItems)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			return content, true, nil
		}
	}
	if firstErr != nil {
		return nil, false, fmt.Errorf("extract link enrichment: %w", firstErr)
	}
	return nil, false, nil
}

func (policy *LinkEnrichmentPolicy) matchingRule(host string) *LinkEnrichmentRule {
	for index := range policy.Rules {
		for _, suffix := range policy.Rules[index].HostSuffixes {
			if hostMatchesSuffix(host, suffix) {
				return &policy.Rules[index]
			}
		}
	}
	return nil
}

func decodeJSONObjectPrefix(data []byte) ([]byte, bool, error) {
	data = bytes.TrimLeft(data, " \t\r\n")
	if len(data) == 0 || data[0] != '{' {
		return nil, false, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	var object json.RawMessage
	if err := decoder.Decode(&object); err != nil {
		return nil, true, fmt.Errorf("decode embedded object: %w", err)
	}
	return object, true, nil
}

func decodeLinkContent(data []byte, fields LinkEnrichmentFields, maxItems int) (*LinkContent, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var root map[string]any
	if err := decoder.Decode(&root); err != nil {
		return nil, fmt.Errorf("decode embedded object: %w", err)
	}
	content := &LinkContent{}
	var err error
	if content.Name, err = linkEnrichmentString(root, fields.Name); err != nil {
		return nil, err
	}
	if content.Kind, err = linkEnrichmentString(root, fields.Kind); err != nil {
		return nil, err
	}
	if content.SizeBytes, err = linkEnrichmentInt64(root, fields.SizeBytes); err != nil {
		return nil, err
	}
	fileCount, err := linkEnrichmentInt64(root, fields.FileCount)
	if err != nil {
		return nil, err
	}
	folderCount, err := linkEnrichmentInt64(root, fields.FolderCount)
	if err != nil {
		return nil, err
	}
	if fileCount > int64(^uint(0)>>1) || folderCount > int64(^uint(0)>>1) {
		return nil, errors.New("embedded count is too large")
	}
	content.FileCount = int(fileCount)
	content.FolderCount = int(folderCount)

	rawItems, ok, err := linkEnrichmentValue(root, fields.Items)
	if err != nil {
		return nil, err
	}
	if ok {
		items, ok := rawItems.([]any)
		if !ok {
			return nil, errors.New("embedded items is not an array")
		}
		if len(items) > maxItems {
			items = items[:maxItems]
		}
		content.Items = make([]LinkContentItem, 0, len(items))
		for _, raw := range items {
			itemObject, ok := raw.(map[string]any)
			if !ok {
				return nil, errors.New("embedded item is not an object")
			}
			item := LinkContentItem{}
			if item.Name, err = linkEnrichmentString(itemObject, fields.ItemName); err != nil {
				return nil, err
			}
			if item.Kind, err = linkEnrichmentString(itemObject, fields.ItemKind); err != nil {
				return nil, err
			}
			if item.SizeBytes, err = linkEnrichmentInt64(itemObject, fields.ItemSizeBytes); err != nil {
				return nil, err
			}
			content.Items = append(content.Items, item)
		}
	}
	content.MaterialTypes = inferLinkContentMaterialTypes(*content)
	return content, nil
}

func linkEnrichmentValue(root map[string]any, fieldPath string) (any, bool, error) {
	if fieldPath == "" {
		return nil, false, nil
	}
	var current any = root
	for _, segment := range strings.Split(fieldPath, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false, fmt.Errorf("embedded field %q traverses a non-object", fieldPath)
		}
		current, ok = object[segment]
		if !ok {
			return nil, false, nil
		}
	}
	return current, true, nil
}

func linkEnrichmentString(root map[string]any, fieldPath string) (string, error) {
	value, ok, err := linkEnrichmentValue(root, fieldPath)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("embedded field %q is not a string", fieldPath)
	}
	if containsControlCharacter(text) {
		controlStripped := strings.Map(func(value rune) rune {
			if unicode.IsControl(value) {
				return -1
			}
			return value
		}, text)
		lower := strings.ToLower(strings.TrimSpace(controlStripped))
		if strings.Contains(lower, "://") || hasLocatorSchemePrefix(lower) {
			return "", fmt.Errorf("embedded field %q contains non-metadata content", fieldPath)
		}
		text = strings.Map(func(value rune) rune {
			if unicode.IsControl(value) {
				return ' '
			}
			return value
		}, text)
	}
	text = strings.TrimSpace(text)
	if len(text) > maxLinkContentStringBytes {
		return "", fmt.Errorf("embedded field %q is too large", fieldPath)
	}
	return text, nil
}

func linkEnrichmentInt64(root map[string]any, fieldPath string) (int64, error) {
	value, ok, err := linkEnrichmentValue(root, fieldPath)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, nil
	}
	number, ok := value.(json.Number)
	if !ok {
		return 0, fmt.Errorf("embedded field %q is not an integer", fieldPath)
	}
	parsed, err := strconv.ParseInt(string(number), 10, 64)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("embedded field %q is not a non-negative integer", fieldPath)
	}
	return parsed, nil
}

func inferLinkContentMaterialTypes(content LinkContent) []string {
	types := make(map[string]struct{})
	addLinkContentMaterialType(types, content.Name)
	for _, item := range content.Items {
		addLinkContentMaterialType(types, item.Name)
	}
	values := make([]string, 0, len(types))
	for value := range types {
		values = append(values, value)
	}
	sort.Strings(values)
	return values
}

func addLinkContentMaterialType(values map[string]struct{}, name string) {
	extension := strings.ToLower(filepath.Ext(strings.TrimSpace(name)))
	var materialType string
	switch extension {
	case ".mp4", ".mkv", ".mov", ".webm", ".m4v", ".avi":
		materialType = "video"
	case ".mp3", ".m4a", ".flac", ".wav", ".ogg", ".aac":
		materialType = "audio"
	case ".pdf", ".epub", ".mobi", ".fb2", ".doc", ".docx", ".txt", ".rtf":
		materialType = "document"
	case ".zip", ".rar", ".7z", ".tar", ".gz", ".bz2", ".xz", ".iso":
		materialType = "archive"
	case ".jpg", ".jpeg", ".png", ".gif", ".webp", ".svg":
		materialType = "image"
	case ".torrent":
		materialType = "torrent"
	}
	if materialType != "" {
		values[materialType] = struct{}{}
	}
}
