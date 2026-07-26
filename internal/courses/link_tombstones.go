package courses

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"time"
	"unicode/utf8"
)

const linkTombstonesSchema = "link-tombstones/v1"

var linkTombstoneHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type LinkTombstones struct {
	hashes  map[string]struct{}
	records map[string]linkTombstoneRecord
}

type linkTombstonesFile struct {
	SchemaVersion           string            `json:"schema_version"`
	CanonicalizationVersion int               `json:"canonicalization_version"`
	Links                   []json.RawMessage `json:"links"`
}

type linkTombstoneRecord struct {
	SHA256      string `json:"sha256"`
	Reason      string `json:"reason"`
	ConfirmedAt string `json:"confirmed_at"`
}

func LoadLinkTombstones(r io.Reader) (*LinkTombstones, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read link tombstones: %w", err)
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("read link tombstones: invalid utf-8")
	}
	if err := rejectDuplicateTopLevelKeys(data); err != nil {
		return nil, fmt.Errorf("decode link tombstones: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var file linkTombstonesFile
	if err := decoder.Decode(&file); err != nil {
		return nil, fmt.Errorf("decode link tombstones: %w", err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("decode link tombstones: multiple json values")
		}
		return nil, fmt.Errorf("decode link tombstones: %w", err)
	}
	if file.SchemaVersion != linkTombstonesSchema {
		return nil, fmt.Errorf("decode link tombstones: unsupported schema_version %q", file.SchemaVersion)
	}
	if file.CanonicalizationVersion != 1 {
		return nil, fmt.Errorf("decode link tombstones: unsupported canonicalization_version %d", file.CanonicalizationVersion)
	}
	if file.Links == nil {
		return nil, fmt.Errorf("decode link tombstones: links is required")
	}

	tombstones := NewLinkTombstones()
	for index, raw := range file.Links {
		if err := rejectDuplicateTopLevelKeys(raw); err != nil {
			return nil, fmt.Errorf("decode link tombstones: links[%d]: %w", index, err)
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		var record linkTombstoneRecord
		if err := decoder.Decode(&record); err != nil {
			return nil, fmt.Errorf("decode link tombstones: links[%d]: %w", index, err)
		}
		if !linkTombstoneHashPattern.MatchString(record.SHA256) {
			return nil, fmt.Errorf("decode link tombstones: links[%d] has invalid sha256", index)
		}
		if !validLinkTombstoneReason(record.Reason) {
			return nil, fmt.Errorf("decode link tombstones: links[%d] has invalid reason %q", index, record.Reason)
		}
		if _, err := time.Parse(time.RFC3339, record.ConfirmedAt); err != nil {
			return nil, fmt.Errorf("decode link tombstones: links[%d] has invalid confirmed_at %q: %w", index, record.ConfirmedAt, err)
		}
		if _, exists := tombstones.hashes[record.SHA256]; exists {
			return nil, fmt.Errorf("decode link tombstones: duplicate sha256 %q", record.SHA256)
		}
		tombstones.hashes[record.SHA256] = struct{}{}
		tombstones.records[record.SHA256] = record
	}
	return tombstones, nil
}

func NewLinkTombstones() *LinkTombstones {
	return &LinkTombstones{
		hashes:  make(map[string]struct{}),
		records: make(map[string]linkTombstoneRecord),
	}
}

func (t *LinkTombstones) Len() int {
	if t == nil {
		return 0
	}
	return len(t.hashes)
}

func (t *LinkTombstones) MergeEligibleAuditResults(report *LinkAuditReport, required int, confirmedAt time.Time) int {
	if t == nil || report == nil {
		return 0
	}
	if t.hashes == nil {
		t.hashes = make(map[string]struct{})
	}
	if t.records == nil {
		t.records = make(map[string]linkTombstoneRecord)
	}

	timestamp := confirmedAt.UTC().Format(time.RFC3339)
	added := 0
	for _, result := range report.Results {
		if !result.EligibleForTombstone(required) || !linkTombstoneHashPattern.MatchString(result.SHA256) {
			continue
		}
		if _, exists := t.hashes[result.SHA256]; exists {
			continue
		}
		reason := ""
		switch result.State {
		case LinkAuditStateExpired:
			reason = "expired"
		case LinkAuditStateContentMismatch:
			reason = "content_mismatch"
		default:
			continue
		}
		record := linkTombstoneRecord{
			SHA256:      result.SHA256,
			Reason:      reason,
			ConfirmedAt: timestamp,
		}
		t.hashes[result.SHA256] = struct{}{}
		t.records[result.SHA256] = record
		added++
	}
	return added
}

func (t LinkTombstones) MarshalJSON() ([]byte, error) {
	records := make([]linkTombstoneRecord, 0, len(t.records))
	for _, record := range t.records {
		records = append(records, record)
	}
	sort.Slice(records, func(i, j int) bool {
		return records[i].SHA256 < records[j].SHA256
	})
	return json.Marshal(struct {
		SchemaVersion           string                `json:"schema_version"`
		CanonicalizationVersion int                   `json:"canonicalization_version"`
		Links                   []linkTombstoneRecord `json:"links"`
	}{
		SchemaVersion:           linkTombstonesSchema,
		CanonicalizationVersion: 1,
		Links:                   records,
	})
}

func (t *LinkTombstones) ContainsURL(rawURL string) bool {
	if t == nil || len(t.hashes) == 0 {
		return false
	}
	hash, err := linkHash(rawURL)
	if err != nil {
		return false
	}
	_, exists := t.hashes[hash]
	return exists
}

func validLinkTombstoneReason(reason string) bool {
	switch reason {
	case "expired", "content_mismatch", "manual":
		return true
	default:
		return false
	}
}
