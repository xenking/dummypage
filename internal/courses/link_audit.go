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

const (
	linkAuditPolicySchema = "link-audit-policy/v1"
	linkAuditReportSchema = "link-audit-report/v1"
)

const (
	maxLinkAuditTimeout   = 2 * time.Minute
	maxLinkAuditBodyBytes = 1024 * 1024
)

var linkAuditHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

type LinkAuditState string

const (
	LinkAuditStateLive            LinkAuditState = "live"
	LinkAuditStateExpired         LinkAuditState = "expired"
	LinkAuditStateContentMismatch LinkAuditState = "content_mismatch"
	LinkAuditStateBlocked         LinkAuditState = "blocked"
	LinkAuditStateTransient       LinkAuditState = "transient"
	LinkAuditStateUnknown         LinkAuditState = "unknown"
)

type LinkAuditReason string

const (
	LinkAuditReasonOK                  LinkAuditReason = "ok"
	LinkAuditReasonGlobalDeadStatus    LinkAuditReason = "global_dead_status"
	LinkAuditReasonDeadStatus          LinkAuditReason = "dead_status"
	LinkAuditReasonDeadBody            LinkAuditReason = "dead_body"
	LinkAuditReasonMismatchBody        LinkAuditReason = "mismatch_body"
	LinkAuditReasonRequiredBodyMissing LinkAuditReason = "required_body_missing"
	LinkAuditReasonBlockedStatus       LinkAuditReason = "blocked_status"
	LinkAuditReasonServerStatus        LinkAuditReason = "server_status"
	LinkAuditReasonTransportTimeout    LinkAuditReason = "transport_timeout"
	LinkAuditReasonTransportDNS        LinkAuditReason = "transport_dns"
	LinkAuditReasonTransportTLS        LinkAuditReason = "transport_tls"
	LinkAuditReasonTransportConnection LinkAuditReason = "transport_connection"
	LinkAuditReasonTransportError      LinkAuditReason = "transport_error"
	LinkAuditReasonUnhandledHTTPStatus LinkAuditReason = "unhandled_http_status"
	LinkAuditReasonNoMatchingHostRule  LinkAuditReason = "no_matching_host_rule"
)

type LinkAuditTransportError string

const (
	LinkAuditTransportNone       LinkAuditTransportError = ""
	LinkAuditTransportTimeout    LinkAuditTransportError = "timeout"
	LinkAuditTransportDNS        LinkAuditTransportError = "dns"
	LinkAuditTransportTLS        LinkAuditTransportError = "tls"
	LinkAuditTransportConnection LinkAuditTransportError = "connection"
	LinkAuditTransportOther      LinkAuditTransportError = "other"
)

type LinkAuditPolicy struct {
	Timeout               time.Duration
	MaxBodyBytes          int64
	Concurrency           int
	ConfirmationsRequired int
	Rules                 []LinkAuditRule
}

type LinkAuditRule struct {
	HostSuffixes                []string
	DeadStatuses                []int
	DeadBodyPatterns            []string
	MismatchBodyPatterns        []string
	RequiredBodyPatterns        []string
	AllowedRedirectHostSuffixes []string

	deadBodyRegexps     []*regexp.Regexp
	mismatchBodyRegexps []*regexp.Regexp
	requiredBodyRegexps []*regexp.Regexp
}

type LinkAuditObservation struct {
	HTTPStatus     int
	Body           []byte
	TransportError LinkAuditTransportError
}

type LinkAuditReport struct {
	SchemaVersion string            `json:"schema_version"`
	GeneratedAt   string            `json:"generated_at"`
	Results       []LinkAuditResult `json:"results"`
}

type LinkAuditResult struct {
	SHA256        string          `json:"sha256"`
	State         LinkAuditState  `json:"state"`
	Reason        LinkAuditReason `json:"reason"`
	HTTPStatus    int             `json:"http_status,omitempty"`
	CheckedAt     string          `json:"checked_at"`
	Confirmations int             `json:"confirmations"`
}

type linkAuditPolicyFile struct {
	SchemaVersion         string            `json:"schema_version"`
	Timeout               string            `json:"timeout"`
	MaxBodyBytes          int64             `json:"max_body_bytes"`
	Concurrency           int               `json:"concurrency"`
	ConfirmationsRequired int               `json:"confirmations_required"`
	Rules                 []json.RawMessage `json:"rules"`
}

type linkAuditRuleFile struct {
	HostSuffixes                []string `json:"host_suffixes"`
	DeadStatuses                []int    `json:"dead_statuses"`
	DeadBodyPatterns            []string `json:"dead_body_patterns"`
	MismatchBodyPatterns        []string `json:"mismatch_body_patterns"`
	RequiredBodyPatterns        []string `json:"required_body_patterns"`
	AllowedRedirectHostSuffixes []string `json:"allowed_redirect_host_suffixes"`
}

type linkAuditReportFile struct {
	SchemaVersion string            `json:"schema_version"`
	GeneratedAt   string            `json:"generated_at"`
	Results       []json.RawMessage `json:"results"`
}

type linkAuditResultFile struct {
	SHA256        string          `json:"sha256"`
	State         LinkAuditState  `json:"state"`
	Reason        LinkAuditReason `json:"reason"`
	HTTPStatus    *int            `json:"http_status,omitempty"`
	CheckedAt     string          `json:"checked_at"`
	Confirmations int             `json:"confirmations"`
}

func LoadLinkAuditPolicy(r io.Reader) (*LinkAuditPolicy, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read link audit policy: %w", err)
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("read link audit policy: invalid utf-8")
	}
	if err := rejectDuplicateTopLevelKeys(data); err != nil {
		return nil, fmt.Errorf("decode link audit policy: %w", err)
	}

	var file linkAuditPolicyFile
	if err := decodeSingleJSONValue(data, &file); err != nil {
		return nil, fmt.Errorf("decode link audit policy: %w", err)
	}
	if file.SchemaVersion != linkAuditPolicySchema {
		return nil, fmt.Errorf("decode link audit policy: unsupported schema_version %q", file.SchemaVersion)
	}
	timeout, err := time.ParseDuration(file.Timeout)
	if err != nil || timeout <= 0 || timeout > maxLinkAuditTimeout {
		return nil, fmt.Errorf("decode link audit policy: timeout must be >0 and <=2m")
	}
	if file.MaxBodyBytes < 1 || file.MaxBodyBytes > maxLinkAuditBodyBytes {
		return nil, fmt.Errorf("decode link audit policy: max_body_bytes must be between 1 and 1048576")
	}
	if file.Concurrency < 1 || file.Concurrency > 64 {
		return nil, fmt.Errorf("decode link audit policy: concurrency must be between 1 and 64")
	}
	if file.ConfirmationsRequired < 1 || file.ConfirmationsRequired > 10 {
		return nil, fmt.Errorf("decode link audit policy: confirmations_required must be between 1 and 10")
	}
	if file.Rules == nil {
		return nil, fmt.Errorf("decode link audit policy: rules is required")
	}

	policy := &LinkAuditPolicy{
		Timeout:               timeout,
		MaxBodyBytes:          file.MaxBodyBytes,
		Concurrency:           file.Concurrency,
		ConfirmationsRequired: file.ConfirmationsRequired,
		Rules:                 make([]LinkAuditRule, 0, len(file.Rules)),
	}
	for index, raw := range file.Rules {
		rule, err := decodeLinkAuditRule(index, raw)
		if err != nil {
			return nil, err
		}
		policy.Rules = append(policy.Rules, rule)
	}
	return policy, nil
}

func (p *LinkAuditPolicy) MatchRule(host string) bool {
	return p.matchingRule(host) != nil
}

func (p *LinkAuditPolicy) Classify(host string, obs LinkAuditObservation) LinkAuditResult {
	status := obs.HTTPStatus
	if obs.TransportError != LinkAuditTransportNone {
		return LinkAuditResult{State: LinkAuditStateTransient, Reason: transportErrorReason(obs.TransportError), HTTPStatus: status}
	}
	if status == 404 || status == 410 {
		return LinkAuditResult{State: LinkAuditStateExpired, Reason: LinkAuditReasonGlobalDeadStatus, HTTPStatus: status}
	}

	rule := p.matchingRule(host)
	if rule != nil {
		if containsInt(rule.DeadStatuses, status) {
			return LinkAuditResult{State: LinkAuditStateExpired, Reason: LinkAuditReasonDeadStatus, HTTPStatus: status}
		}
		for _, pattern := range rule.deadBodyRegexps {
			if pattern.Match(obs.Body) {
				return LinkAuditResult{State: LinkAuditStateExpired, Reason: LinkAuditReasonDeadBody, HTTPStatus: status}
			}
		}
	}

	switch {
	case status == 401 || status == 403 || status == 429 || status == 451:
		return LinkAuditResult{State: LinkAuditStateBlocked, Reason: LinkAuditReasonBlockedStatus, HTTPStatus: status}
	case status >= 500 && status <= 599:
		return LinkAuditResult{State: LinkAuditStateTransient, Reason: LinkAuditReasonServerStatus, HTTPStatus: status}
	case status >= 200 && status <= 299:
		if rule != nil {
			for _, pattern := range rule.mismatchBodyRegexps {
				if pattern.Match(obs.Body) {
					return LinkAuditResult{State: LinkAuditStateContentMismatch, Reason: LinkAuditReasonMismatchBody, HTTPStatus: status}
				}
			}
			for _, pattern := range rule.requiredBodyRegexps {
				if !pattern.Match(obs.Body) {
					return LinkAuditResult{State: LinkAuditStateContentMismatch, Reason: LinkAuditReasonRequiredBodyMissing, HTTPStatus: status}
				}
			}
		}
		return LinkAuditResult{State: LinkAuditStateLive, Reason: LinkAuditReasonOK, HTTPStatus: status}
	default:
		return LinkAuditResult{State: LinkAuditStateUnknown, Reason: LinkAuditReasonUnhandledHTTPStatus, HTTPStatus: status}
	}
}

func ClassifyLinkAudit(policy *LinkAuditPolicy, host string, obs LinkAuditObservation) LinkAuditResult {
	return policy.Classify(host, obs)
}

func LoadLinkAuditReport(r io.Reader) (*LinkAuditReport, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read link audit report: %w", err)
	}
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("read link audit report: invalid utf-8")
	}
	if err := rejectDuplicateTopLevelKeys(data); err != nil {
		return nil, fmt.Errorf("decode link audit report: %w", err)
	}

	var file linkAuditReportFile
	if err := decodeSingleJSONValue(data, &file); err != nil {
		return nil, fmt.Errorf("decode link audit report: %w", err)
	}
	if file.SchemaVersion != linkAuditReportSchema {
		return nil, fmt.Errorf("decode link audit report: unsupported schema_version %q", file.SchemaVersion)
	}
	if _, err := time.Parse(time.RFC3339, file.GeneratedAt); err != nil {
		return nil, fmt.Errorf("decode link audit report: invalid generated_at %q: %w", file.GeneratedAt, err)
	}
	if file.Results == nil {
		return nil, fmt.Errorf("decode link audit report: results is required")
	}

	report := &LinkAuditReport{
		SchemaVersion: file.SchemaVersion,
		GeneratedAt:   file.GeneratedAt,
		Results:       make([]LinkAuditResult, 0, len(file.Results)),
	}
	seen := make(map[string]struct{}, len(file.Results))
	for index, raw := range file.Results {
		result, err := decodeLinkAuditResult(index, raw)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[result.SHA256]; exists {
			return nil, fmt.Errorf("decode link audit report: duplicate sha256 %q", result.SHA256)
		}
		seen[result.SHA256] = struct{}{}
		report.Results = append(report.Results, result)
	}
	sortLinkAuditResults(report.Results)
	return report, nil
}

func (r LinkAuditReport) MarshalJSON() ([]byte, error) {
	type alias LinkAuditReport
	results := make([]LinkAuditResult, len(r.Results))
	copy(results, r.Results)
	clone := alias{
		SchemaVersion: r.SchemaVersion,
		GeneratedAt:   r.GeneratedAt,
		Results:       results,
	}
	sortLinkAuditResults(clone.Results)
	return json.Marshal(clone)
}

func mergeAuditResult(previous *LinkAuditResult, current LinkAuditResult) LinkAuditResult {
	if !current.deletable() {
		current.Confirmations = 0
		return current
	}
	current.Confirmations = 1
	if previous != nil && previous.State == current.State && previous.deletable() {
		current.Confirmations = previous.Confirmations + 1
	}
	return current
}

func (r LinkAuditResult) EligibleForTombstone(required int) bool {
	return r.deletable() && required > 0 && r.Confirmations >= required
}

func (r LinkAuditResult) deletable() bool {
	return r.State == LinkAuditStateExpired || r.State == LinkAuditStateContentMismatch
}

func decodeLinkAuditRule(index int, raw json.RawMessage) (LinkAuditRule, error) {
	if err := rejectDuplicateTopLevelKeys(raw); err != nil {
		return LinkAuditRule{}, fmt.Errorf("decode link audit policy: rules[%d]: %w", index, err)
	}
	var file linkAuditRuleFile
	if err := decodeSingleJSONValue(raw, &file); err != nil {
		return LinkAuditRule{}, fmt.Errorf("decode link audit policy: rules[%d]: %w", index, err)
	}
	if file.HostSuffixes == nil ||
		file.DeadStatuses == nil ||
		file.DeadBodyPatterns == nil ||
		file.MismatchBodyPatterns == nil ||
		file.RequiredBodyPatterns == nil ||
		file.AllowedRedirectHostSuffixes == nil {
		return LinkAuditRule{}, fmt.Errorf("decode link audit policy: rules[%d]: all rule arrays are required", index)
	}
	if len(file.HostSuffixes) == 0 {
		return LinkAuditRule{}, fmt.Errorf("decode link audit policy: rules[%d]: host_suffixes must not be empty", index)
	}

	rule := LinkAuditRule{
		HostSuffixes:                make([]string, 0, len(file.HostSuffixes)),
		DeadStatuses:                append([]int(nil), file.DeadStatuses...),
		DeadBodyPatterns:            append([]string(nil), file.DeadBodyPatterns...),
		MismatchBodyPatterns:        append([]string(nil), file.MismatchBodyPatterns...),
		RequiredBodyPatterns:        append([]string(nil), file.RequiredBodyPatterns...),
		AllowedRedirectHostSuffixes: make([]string, 0, len(file.AllowedRedirectHostSuffixes)),
	}
	hostSuffixes, err := validateLinkAuditSuffixes("host_suffixes", file.HostSuffixes)
	if err != nil {
		return LinkAuditRule{}, fmt.Errorf("decode link audit policy: rules[%d]: %w", index, err)
	}
	redirectSuffixes, err := validateLinkAuditSuffixes("allowed_redirect_host_suffixes", file.AllowedRedirectHostSuffixes)
	if err != nil {
		return LinkAuditRule{}, fmt.Errorf("decode link audit policy: rules[%d]: %w", index, err)
	}
	rule.HostSuffixes = hostSuffixes
	rule.AllowedRedirectHostSuffixes = redirectSuffixes
	if err := validateLinkAuditStatuses(file.DeadStatuses); err != nil {
		return LinkAuditRule{}, fmt.Errorf("decode link audit policy: rules[%d]: %w", index, err)
	}
	if err := validateDistinctLinkAuditPatterns(file.DeadBodyPatterns, file.MismatchBodyPatterns, file.RequiredBodyPatterns); err != nil {
		return LinkAuditRule{}, fmt.Errorf("decode link audit policy: rules[%d]: %w", index, err)
	}
	rule.deadBodyRegexps, err = compileLinkAuditPatterns("dead_body_patterns", file.DeadBodyPatterns)
	if err != nil {
		return LinkAuditRule{}, fmt.Errorf("decode link audit policy: rules[%d]: %w", index, err)
	}
	rule.mismatchBodyRegexps, err = compileLinkAuditPatterns("mismatch_body_patterns", file.MismatchBodyPatterns)
	if err != nil {
		return LinkAuditRule{}, fmt.Errorf("decode link audit policy: rules[%d]: %w", index, err)
	}
	rule.requiredBodyRegexps, err = compileLinkAuditPatterns("required_body_patterns", file.RequiredBodyPatterns)
	if err != nil {
		return LinkAuditRule{}, fmt.Errorf("decode link audit policy: rules[%d]: %w", index, err)
	}
	return rule, nil
}

func decodeLinkAuditResult(index int, raw json.RawMessage) (LinkAuditResult, error) {
	if err := rejectDuplicateTopLevelKeys(raw); err != nil {
		return LinkAuditResult{}, fmt.Errorf("decode link audit report: results[%d]: %w", index, err)
	}
	var file linkAuditResultFile
	if err := decodeSingleJSONValue(raw, &file); err != nil {
		return LinkAuditResult{}, fmt.Errorf("decode link audit report: results[%d]: %w", index, err)
	}
	if !linkAuditHashPattern.MatchString(file.SHA256) {
		return LinkAuditResult{}, fmt.Errorf("decode link audit report: results[%d] has invalid sha256", index)
	}
	if !validLinkAuditState(file.State) {
		return LinkAuditResult{}, fmt.Errorf("decode link audit report: results[%d] has invalid state %q", index, file.State)
	}
	if !validLinkAuditReason(file.Reason) {
		return LinkAuditResult{}, fmt.Errorf("decode link audit report: results[%d] has invalid reason %q", index, file.Reason)
	}
	if file.HTTPStatus != nil && (*file.HTTPStatus < 100 || *file.HTTPStatus > 599) {
		return LinkAuditResult{}, fmt.Errorf("decode link audit report: results[%d] has invalid http_status", index)
	}
	if _, err := time.Parse(time.RFC3339, file.CheckedAt); err != nil {
		return LinkAuditResult{}, fmt.Errorf("decode link audit report: results[%d] has invalid checked_at %q: %w", index, file.CheckedAt, err)
	}
	if file.Confirmations < 0 {
		return LinkAuditResult{}, fmt.Errorf("decode link audit report: results[%d] has invalid confirmations", index)
	}
	result := LinkAuditResult{
		SHA256:        file.SHA256,
		State:         file.State,
		Reason:        file.Reason,
		CheckedAt:     file.CheckedAt,
		Confirmations: file.Confirmations,
	}
	if file.HTTPStatus != nil {
		result.HTTPStatus = *file.HTTPStatus
	}
	return result, nil
}

func decodeSingleJSONValue(data []byte, dst any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple json values")
		}
		return err
	}
	return nil
}

func validateLinkAuditSuffixes(field string, values []string) ([]string, error) {
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			return nil, fmt.Errorf("%s contains empty value", field)
		}
		if strings.TrimSpace(value) != value {
			return nil, fmt.Errorf("%s value %q is not trimmed", field, value)
		}
		if strings.ToLower(value) != value {
			return nil, fmt.Errorf("%s value %q is not lowercase", field, value)
		}
		if strings.Contains(value, "://") || strings.ContainsAny(value, "/:*") || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
			return nil, fmt.Errorf("%s value %q is not a host suffix", field, value)
		}
		if !validLinkAuditHostSuffix(value) {
			return nil, fmt.Errorf("%s value %q is not a host suffix", field, value)
		}
		if _, exists := seen[value]; exists {
			return nil, fmt.Errorf("duplicate %s value %q", field, value)
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	return normalized, nil
}

func validLinkAuditHostSuffix(value string) bool {
	if len(value) > 253 {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for index := 0; index < len(label); index++ {
			character := label[index]
			if (character < 'a' || character > 'z') &&
				(character < '0' || character > '9') &&
				character != '-' {
				return false
			}
		}
	}
	return true
}

func validateLinkAuditStatuses(values []int) error {
	seen := make(map[int]struct{}, len(values))
	for _, value := range values {
		if value < 400 || value > 599 {
			return fmt.Errorf("dead_statuses contains invalid status %d", value)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("duplicate dead_statuses value %d", value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validateDistinctLinkAuditPatterns(groups ...[]string) error {
	seen := make(map[string]struct{})
	for _, group := range groups {
		for _, value := range group {
			if _, exists := seen[value]; exists {
				return fmt.Errorf("duplicate pattern value %q", value)
			}
			seen[value] = struct{}{}
		}
	}
	return nil
}

func compileLinkAuditPatterns(field string, values []string) ([]*regexp.Regexp, error) {
	seen := make(map[string]struct{}, len(values))
	patterns := make([]*regexp.Regexp, 0, len(values))
	for _, value := range values {
		if value == "" {
			return nil, fmt.Errorf("%s contains empty value", field)
		}
		if strings.TrimSpace(value) != value {
			return nil, fmt.Errorf("%s value %q is not trimmed", field, value)
		}
		if _, exists := seen[value]; exists {
			return nil, fmt.Errorf("duplicate %s value %q", field, value)
		}
		compiled, err := regexp.Compile(value)
		if err != nil {
			return nil, fmt.Errorf("%s value %q does not compile: %w", field, value, err)
		}
		seen[value] = struct{}{}
		patterns = append(patterns, compiled)
	}
	return patterns, nil
}

func (p *LinkAuditPolicy) matchingRule(host string) *LinkAuditRule {
	if p == nil {
		return nil
	}
	normalized := strings.TrimSuffix(strings.ToLower(host), ".")
	for index := range p.Rules {
		rule := &p.Rules[index]
		for _, suffix := range rule.HostSuffixes {
			if hostMatchesSuffix(normalized, suffix) {
				return rule
			}
		}
	}
	return nil
}

func hostMatchesSuffix(host, suffix string) bool {
	return host == suffix || strings.HasSuffix(host, "."+suffix)
}

func containsInt(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func transportErrorReason(err LinkAuditTransportError) LinkAuditReason {
	switch err {
	case LinkAuditTransportTimeout:
		return LinkAuditReasonTransportTimeout
	case LinkAuditTransportDNS:
		return LinkAuditReasonTransportDNS
	case LinkAuditTransportTLS:
		return LinkAuditReasonTransportTLS
	case LinkAuditTransportConnection:
		return LinkAuditReasonTransportConnection
	default:
		return LinkAuditReasonTransportError
	}
}

func validLinkAuditState(state LinkAuditState) bool {
	switch state {
	case LinkAuditStateLive,
		LinkAuditStateExpired,
		LinkAuditStateContentMismatch,
		LinkAuditStateBlocked,
		LinkAuditStateTransient,
		LinkAuditStateUnknown:
		return true
	default:
		return false
	}
}

func validLinkAuditReason(reason LinkAuditReason) bool {
	switch reason {
	case LinkAuditReasonOK,
		LinkAuditReasonGlobalDeadStatus,
		LinkAuditReasonDeadStatus,
		LinkAuditReasonDeadBody,
		LinkAuditReasonMismatchBody,
		LinkAuditReasonRequiredBodyMissing,
		LinkAuditReasonBlockedStatus,
		LinkAuditReasonServerStatus,
		LinkAuditReasonTransportTimeout,
		LinkAuditReasonTransportDNS,
		LinkAuditReasonTransportTLS,
		LinkAuditReasonTransportConnection,
		LinkAuditReasonTransportError,
		LinkAuditReasonUnhandledHTTPStatus,
		LinkAuditReasonNoMatchingHostRule:
		return true
	default:
		return false
	}
}

func sortLinkAuditResults(results []LinkAuditResult) {
	sort.Slice(results, func(i, j int) bool {
		return results[i].SHA256 < results[j].SHA256
	})
}
