package courses

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const linkSuppressionsSchema = "link-suppressions/v1"

var linkSuppressionHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type LinkSuppressions struct {
	records map[linkSuppressionKey]linkSuppressionRecord
}

type linkSuppressionsFile struct {
	SchemaVersion           string            `json:"schema_version"`
	CanonicalizationVersion int               `json:"canonicalization_version"`
	Suppressions            []json.RawMessage `json:"suppressions"`
}

type linkSuppressionKey struct {
	SourceEntryID string
	SHA256        string
}

type linkSuppressionRecord struct {
	SourceEntryID string `json:"source_entry_id"`
	SHA256        string `json:"sha256"`
	Reason        string `json:"reason"`
	ConfirmedAt   string `json:"confirmed_at"`
}

func LoadLinkSuppressions(r io.Reader) (*LinkSuppressions, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read link suppressions: %w", err)
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("read link suppressions: invalid utf-8")
	}
	if err := rejectDuplicateTopLevelKeys(data); err != nil {
		return nil, fmt.Errorf("decode link suppressions: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var file linkSuppressionsFile
	if err := decoder.Decode(&file); err != nil {
		return nil, fmt.Errorf("decode link suppressions: %w", err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("decode link suppressions: multiple json values")
		}
		return nil, fmt.Errorf("decode link suppressions: %w", err)
	}
	if file.SchemaVersion != linkSuppressionsSchema {
		return nil, fmt.Errorf("decode link suppressions: unsupported schema_version %q", file.SchemaVersion)
	}
	if file.CanonicalizationVersion != 1 {
		return nil, fmt.Errorf("decode link suppressions: unsupported canonicalization_version %d", file.CanonicalizationVersion)
	}
	if file.Suppressions == nil {
		return nil, fmt.Errorf("decode link suppressions: suppressions is required")
	}

	suppressions := &LinkSuppressions{records: make(map[linkSuppressionKey]linkSuppressionRecord)}
	for index, raw := range file.Suppressions {
		if err := rejectDuplicateTopLevelKeys(raw); err != nil {
			return nil, fmt.Errorf("decode link suppressions: suppressions[%d]: %w", index, err)
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		var record linkSuppressionRecord
		if err := decoder.Decode(&record); err != nil {
			return nil, fmt.Errorf("decode link suppressions: suppressions[%d]: %w", index, err)
		}
		if record.SourceEntryID == "" || strings.TrimSpace(record.SourceEntryID) != record.SourceEntryID {
			return nil, fmt.Errorf("decode link suppressions: suppressions[%d] has invalid source_entry_id", index)
		}
		if !linkSuppressionHashPattern.MatchString(record.SHA256) {
			return nil, fmt.Errorf("decode link suppressions: suppressions[%d] has invalid sha256", index)
		}
		if !validLinkSuppressionReason(record.Reason) {
			return nil, fmt.Errorf("decode link suppressions: suppressions[%d] has invalid reason %q", index, record.Reason)
		}
		if _, err := time.Parse(time.RFC3339, record.ConfirmedAt); err != nil {
			return nil, fmt.Errorf("decode link suppressions: suppressions[%d] has invalid confirmed_at %q: %w", index, record.ConfirmedAt, err)
		}
		key := linkSuppressionKey{SourceEntryID: record.SourceEntryID, SHA256: record.SHA256}
		if _, exists := suppressions.records[key]; exists {
			return nil, fmt.Errorf(
				"decode link suppressions: duplicate source_entry_id %q and sha256 %q",
				record.SourceEntryID,
				record.SHA256,
			)
		}
		suppressions.records[key] = record
	}
	return suppressions, nil
}

func (s *LinkSuppressions) ContainsURL(sourceEntryID, rawURL string) bool {
	if s == nil || len(s.records) == 0 {
		return false
	}
	hash, err := linkHash(rawURL)
	if err != nil {
		return false
	}
	_, exists := s.records[linkSuppressionKey{SourceEntryID: sourceEntryID, SHA256: hash}]
	return exists
}

func (s LinkSuppressions) MarshalJSON() ([]byte, error) {
	records := make([]linkSuppressionRecord, 0, len(s.records))
	for _, record := range s.records {
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		if records[i].SourceEntryID != records[j].SourceEntryID {
			return records[i].SourceEntryID < records[j].SourceEntryID
		}
		return records[i].SHA256 < records[j].SHA256
	})
	return json.Marshal(struct {
		SchemaVersion           string                  `json:"schema_version"`
		CanonicalizationVersion int                     `json:"canonicalization_version"`
		Suppressions            []linkSuppressionRecord `json:"suppressions"`
	}{
		SchemaVersion:           linkSuppressionsSchema,
		CanonicalizationVersion: 1,
		Suppressions:            records,
	})
}

func validLinkSuppressionReason(reason string) bool {
	switch reason {
	case "content_mismatch", "manual":
		return true
	default:
		return false
	}
}
