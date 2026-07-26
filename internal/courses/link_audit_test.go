package courses

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestLoadLinkAuditPolicyAcceptsStrictPolicy(t *testing.T) {
	policy, err := LoadLinkAuditPolicy(strings.NewReader(validLinkAuditPolicyJSON()))
	if err != nil {
		t.Fatalf("LoadLinkAuditPolicy() error = %v", err)
	}

	if policy.Timeout != 10*time.Second {
		t.Fatalf("Timeout = %v, want 10s", policy.Timeout)
	}
	if policy.MaxBodyBytes != 4096 {
		t.Fatalf("MaxBodyBytes = %d, want 4096", policy.MaxBodyBytes)
	}
	if policy.Concurrency != 4 {
		t.Fatalf("Concurrency = %d, want 4", policy.Concurrency)
	}
	if policy.ConfirmationsRequired != 2 {
		t.Fatalf("ConfirmationsRequired = %d, want 2", policy.ConfirmationsRequired)
	}
	if !policy.MatchRule("cdn.example.test") {
		t.Fatal("MatchRule() = false, want exact host match")
	}
	if !policy.MatchRule("files.cdn.example.test") {
		t.Fatal("MatchRule() = false, want suffix boundary match")
	}
	if policy.MatchRule("evilcdn.example.test") {
		t.Fatal("MatchRule() = true, want attacker suffix rejected")
	}
}

func TestLoadLinkAuditPolicyRejectsInvalidFiles(t *testing.T) {
	tests := []struct {
		name string
		json string
	}{
		{"invalid utf8", "{\"schema_version\":\"link-audit-policy/v1\",\"timeout\":\"10s\",\"max_body_bytes\":4096,\"concurrency\":4,\"confirmations_required\":2,\"rules\":[\"\xff\"]}"},
		{"multiple json values", validLinkAuditPolicyJSON() + ` {}`},
		{"unknown top-level field", strings.Replace(validLinkAuditPolicyJSON(), `"rules"`, `"extra":true,"rules"`, 1)},
		{"duplicate top-level key", strings.Replace(validLinkAuditPolicyJSON(), `"timeout":"10s"`, `"timeout":"10s","timeout":"10s"`, 1)},
		{"wrong schema", strings.Replace(validLinkAuditPolicyJSON(), `"link-audit-policy/v1"`, `"bad"`, 1)},
		{"missing rules", `{"schema_version":"link-audit-policy/v1","timeout":"10s","max_body_bytes":4096,"concurrency":4,"confirmations_required":2}`},
		{"zero timeout", strings.Replace(validLinkAuditPolicyJSON(), `"timeout":"10s"`, `"timeout":"0s"`, 1)},
		{"too large timeout", strings.Replace(validLinkAuditPolicyJSON(), `"timeout":"10s"`, `"timeout":"121s"`, 1)},
		{"zero max body", strings.Replace(validLinkAuditPolicyJSON(), `"max_body_bytes":4096`, `"max_body_bytes":0`, 1)},
		{"too large max body", strings.Replace(validLinkAuditPolicyJSON(), `"max_body_bytes":4096`, `"max_body_bytes":1048577`, 1)},
		{"zero concurrency", strings.Replace(validLinkAuditPolicyJSON(), `"concurrency":4`, `"concurrency":0`, 1)},
		{"too large concurrency", strings.Replace(validLinkAuditPolicyJSON(), `"concurrency":4`, `"concurrency":65`, 1)},
		{"zero confirmations", strings.Replace(validLinkAuditPolicyJSON(), `"confirmations_required":2`, `"confirmations_required":0`, 1)},
		{"too many confirmations", strings.Replace(validLinkAuditPolicyJSON(), `"confirmations_required":2`, `"confirmations_required":11`, 1)},
		{"unknown rule field", strings.Replace(validLinkAuditPolicyJSON(), `"host_suffixes"`, `"extra":true,"host_suffixes"`, 1)},
		{"duplicate rule key", strings.Replace(validLinkAuditPolicyJSON(), `"host_suffixes":["cdn.example.test"]`, `"host_suffixes":["cdn.example.test"],"host_suffixes":["cdn.example.test"]`, 1)},
		{"missing rule array", strings.Replace(validLinkAuditPolicyJSON(), `"dead_body_patterns":["(?i)not found"],`, ``, 1)},
		{"empty host suffixes", strings.Replace(validLinkAuditPolicyJSON(), `"host_suffixes":["cdn.example.test"]`, `"host_suffixes":[]`, 1)},
		{"trimmed suffix required", strings.Replace(validLinkAuditPolicyJSON(), `"cdn.example.test"`, `" cdn.example.test"`, 1)},
		{"lowercase suffix required", strings.Replace(validLinkAuditPolicyJSON(), `"cdn.example.test"`, `"CDN.example.test"`, 1)},
		{"scheme suffix rejected", strings.Replace(validLinkAuditPolicyJSON(), `"cdn.example.test"`, `"https://cdn.example.test"`, 1)},
		{"path suffix rejected", strings.Replace(validLinkAuditPolicyJSON(), `"cdn.example.test"`, `"cdn.example.test/path"`, 1)},
		{"port suffix rejected", strings.Replace(validLinkAuditPolicyJSON(), `"cdn.example.test"`, `"cdn.example.test:443"`, 1)},
		{"wildcard suffix rejected", strings.Replace(validLinkAuditPolicyJSON(), `"cdn.example.test"`, `"*.cdn.example.test"`, 1)},
		{"empty host label rejected", strings.Replace(validLinkAuditPolicyJSON(), `"cdn.example.test"`, `"bad..example.test"`, 1)},
		{"space in host label rejected", strings.Replace(validLinkAuditPolicyJSON(), `"cdn.example.test"`, `"bad host.example.test"`, 1)},
		{"leading hyphen label rejected", strings.Replace(validLinkAuditPolicyJSON(), `"cdn.example.test"`, `"-bad.example.test"`, 1)},
		{"trailing hyphen label rejected", strings.Replace(validLinkAuditPolicyJSON(), `"cdn.example.test"`, `"bad-.example.test"`, 1)},
		{"long label rejected", strings.Replace(validLinkAuditPolicyJSON(), `"cdn.example.test"`, `"`+strings.Repeat("a", 64)+`.example.test"`, 1)},
		{"long suffix rejected", strings.Replace(validLinkAuditPolicyJSON(), `"cdn.example.test"`, `"`+strings.Repeat("a.", 126)+`example.test"`, 1)},
		{"duplicate suffix", strings.Replace(validLinkAuditPolicyJSON(), `"host_suffixes":["cdn.example.test"]`, `"host_suffixes":["cdn.example.test","cdn.example.test"]`, 1)},
		{"dead status too low", strings.Replace(validLinkAuditPolicyJSON(), `"dead_statuses":[410,499]`, `"dead_statuses":[399]`, 1)},
		{"dead status too high", strings.Replace(validLinkAuditPolicyJSON(), `"dead_statuses":[410,499]`, `"dead_statuses":[600]`, 1)},
		{"duplicate status", strings.Replace(validLinkAuditPolicyJSON(), `"dead_statuses":[410,499]`, `"dead_statuses":[499,499]`, 1)},
		{"duplicate pattern", strings.Replace(validLinkAuditPolicyJSON(), `"dead_body_patterns":["(?i)not found"]`, `"dead_body_patterns":["gone","gone"]`, 1)},
		{"invalid regexp", strings.Replace(validLinkAuditPolicyJSON(), `"dead_body_patterns":["(?i)not found"]`, `"dead_body_patterns":["["]`, 1)},
		{"untrimmed pattern", strings.Replace(validLinkAuditPolicyJSON(), `"dead_body_patterns":["(?i)not found"]`, `"dead_body_patterns":[" gone"]`, 1)},
		{"empty pattern", strings.Replace(validLinkAuditPolicyJSON(), `"dead_body_patterns":["(?i)not found"]`, `"dead_body_patterns":[""]`, 1)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := LoadLinkAuditPolicy(strings.NewReader(test.json)); err == nil {
				t.Fatal("LoadLinkAuditPolicy() error = nil, want error")
			}
		})
	}
}

func TestClassifyLinkAuditPrecedenceAndStates(t *testing.T) {
	policy := mustLoadLinkAuditPolicy(t)
	tests := []struct {
		name string
		host string
		obs  LinkAuditObservation
		want LinkAuditState
	}{
		{"global 404 expired without rule", "other.example.test", LinkAuditObservation{HTTPStatus: 404, Body: []byte("anything")}, LinkAuditStateExpired},
		{"global 410 expired without rule", "other.example.test", LinkAuditObservation{HTTPStatus: 410}, LinkAuditStateExpired},
		{"dead status before mismatch", "cdn.example.test", LinkAuditObservation{HTTPStatus: 499, Body: []byte("wrong content")}, LinkAuditStateExpired},
		{"dead body before mismatch", "cdn.example.test", LinkAuditObservation{HTTPStatus: 200, Body: []byte("not found and wrong content")}, LinkAuditStateExpired},
		{"mismatch pattern", "cdn.example.test", LinkAuditObservation{HTTPStatus: 200, Body: []byte("wrong content")}, LinkAuditStateContentMismatch},
		{"missing required pattern", "cdn.example.test", LinkAuditObservation{HTTPStatus: 200, Body: []byte("course page")}, LinkAuditStateContentMismatch},
		{"all required patterns live", "cdn.example.test", LinkAuditObservation{HTTPStatus: 200, Body: []byte("course page download")}, LinkAuditStateLive},
		{"blocked unauthorized", "cdn.example.test", LinkAuditObservation{HTTPStatus: 401}, LinkAuditStateBlocked},
		{"blocked forbidden", "cdn.example.test", LinkAuditObservation{HTTPStatus: 403}, LinkAuditStateBlocked},
		{"blocked legal", "cdn.example.test", LinkAuditObservation{HTTPStatus: 451}, LinkAuditStateBlocked},
		{"blocked rate limit", "cdn.example.test", LinkAuditObservation{HTTPStatus: 429}, LinkAuditStateBlocked},
		{"server error transient", "cdn.example.test", LinkAuditObservation{HTTPStatus: 503}, LinkAuditStateTransient},
		{"timeout transient", "cdn.example.test", LinkAuditObservation{TransportError: LinkAuditTransportTimeout}, LinkAuditStateTransient},
		{"dns transient", "cdn.example.test", LinkAuditObservation{TransportError: LinkAuditTransportDNS}, LinkAuditStateTransient},
		{"tls transient", "cdn.example.test", LinkAuditObservation{TransportError: LinkAuditTransportTLS}, LinkAuditStateTransient},
		{"connection transient", "cdn.example.test", LinkAuditObservation{TransportError: LinkAuditTransportConnection}, LinkAuditStateTransient},
		{"redirect final status is final observation", "cdn.example.test", LinkAuditObservation{HTTPStatus: 302}, LinkAuditStateUnknown},
		{"unmatched host has no provider body rules", "evilcdn.example.test", LinkAuditObservation{HTTPStatus: 200, Body: []byte("not found wrong content")}, LinkAuditStateLive},
		{"other status unknown", "cdn.example.test", LinkAuditObservation{HTTPStatus: 304}, LinkAuditStateUnknown},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := policy.Classify(test.host, test.obs)
			if result.State != test.want {
				t.Fatalf("State = %q, want %q (reason=%q)", result.State, test.want, result.Reason)
			}
		})
	}
}

func TestLoadLinkAuditReportAcceptsHashOnlyAndSorts(t *testing.T) {
	firstHash := strings.Repeat("1", 64)
	secondHash := strings.Repeat("2", 64)

	report, err := LoadLinkAuditReport(strings.NewReader(`{
		"schema_version":"link-audit-report/v1",
		"generated_at":"2026-07-26T12:00:00Z",
		"results":[
			{"sha256":"` + secondHash + `","state":"live","reason":"ok","http_status":200,"checked_at":"2026-07-26T12:00:02Z","confirmations":0},
			{"sha256":"` + firstHash + `","state":"expired","reason":"dead_status","http_status":404,"checked_at":"2026-07-26T12:00:01Z","confirmations":2}
		]
	}`))
	if err != nil {
		t.Fatalf("LoadLinkAuditReport() error = %v", err)
	}
	if got := report.Results[0].SHA256; got != firstHash {
		t.Fatalf("first sorted hash = %q, want %q", got, firstHash)
	}

	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("Marshal report: %v", err)
	}
	text := string(encoded)
	if strings.Contains(text, "http://") || strings.Contains(text, "example.test") || strings.Contains(text, "title") || strings.Contains(text, "body") {
		t.Fatalf("report JSON leaked raw fields: %s", text)
	}
	if strings.Index(text, firstHash) > strings.Index(text, secondHash) {
		t.Fatalf("report JSON is not sorted by sha256: %s", text)
	}
}

func TestLoadLinkAuditReportRejectsInvalidFiles(t *testing.T) {
	validHash := strings.Repeat("a", 64)
	tests := []struct {
		name string
		json string
	}{
		{"invalid utf8", "{\"schema_version\":\"link-audit-report/v1\",\"generated_at\":\"2026-07-26T00:00:00Z\",\"results\":[\"\xff\"]}"},
		{"multiple json values", validLinkAuditReportJSON(validHash) + ` {}`},
		{"unknown top-level field", strings.Replace(validLinkAuditReportJSON(validHash), `"results"`, `"extra":true,"results"`, 1)},
		{"duplicate top-level key", strings.Replace(validLinkAuditReportJSON(validHash), `"generated_at":"2026-07-26T00:00:00Z"`, `"generated_at":"2026-07-26T00:00:00Z","generated_at":"2026-07-26T00:00:00Z"`, 1)},
		{"wrong schema", strings.Replace(validLinkAuditReportJSON(validHash), `"link-audit-report/v1"`, `"bad"`, 1)},
		{"missing results", `{"schema_version":"link-audit-report/v1","generated_at":"2026-07-26T00:00:00Z"}`},
		{"invalid generated at", strings.Replace(validLinkAuditReportJSON(validHash), `"generated_at":"2026-07-26T00:00:00Z"`, `"generated_at":"bad"`, 1)},
		{"unknown result field", strings.Replace(validLinkAuditReportJSON(validHash), `"sha256"`, `"url":"https://files.example.test/course","sha256"`, 1)},
		{"duplicate result key", strings.Replace(validLinkAuditReportJSON(validHash), `"sha256":"`+validHash+`"`, `"sha256":"`+validHash+`","sha256":"`+validHash+`"`, 1)},
		{"uppercase hash", validLinkAuditReportJSON(strings.Repeat("A", 64))},
		{"short hash", validLinkAuditReportJSON("abc")},
		{"duplicate hash", `{"schema_version":"link-audit-report/v1","generated_at":"2026-07-26T00:00:00Z","results":[{"sha256":"` + validHash + `","state":"live","reason":"ok","checked_at":"2026-07-26T00:00:01Z","confirmations":0},{"sha256":"` + validHash + `","state":"live","reason":"ok","checked_at":"2026-07-26T00:00:02Z","confirmations":0}]}`},
		{"unknown state", strings.Replace(validLinkAuditReportJSON(validHash), `"state":"live"`, `"state":"bad"`, 1)},
		{"uppercase state", strings.Replace(validLinkAuditReportJSON(validHash), `"state":"live"`, `"state":"Live"`, 1)},
		{"empty reason", strings.Replace(validLinkAuditReportJSON(validHash), `"reason":"ok"`, `"reason":""`, 1)},
		{"untrimmed reason", strings.Replace(validLinkAuditReportJSON(validHash), `"reason":"ok"`, `"reason":" ok"`, 1)},
		{"uppercase reason", strings.Replace(validLinkAuditReportJSON(validHash), `"reason":"ok"`, `"reason":"OK"`, 1)},
		{"unknown reason", strings.Replace(validLinkAuditReportJSON(validHash), `"reason":"ok"`, `"reason":"made_up_reason"`, 1)},
		{"negative status", strings.Replace(validLinkAuditReportJSON(validHash), `"http_status":200,`, `"http_status":-1,`, 1)},
		{"too large status", strings.Replace(validLinkAuditReportJSON(validHash), `"http_status":200,`, `"http_status":1000,`, 1)},
		{"invalid checked at", strings.Replace(validLinkAuditReportJSON(validHash), `"checked_at":"2026-07-26T00:00:01Z"`, `"checked_at":"bad"`, 1)},
		{"negative confirmations", strings.Replace(validLinkAuditReportJSON(validHash), `"confirmations":0`, `"confirmations":-1`, 1)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := LoadLinkAuditReport(strings.NewReader(test.json)); err == nil {
				t.Fatal("LoadLinkAuditReport() error = nil, want error")
			}
		})
	}
}

func TestMergeAuditResultConfirmationsAndEligibility(t *testing.T) {
	hash := strings.Repeat("b", 64)
	first := LinkAuditResult{
		SHA256:        hash,
		State:         LinkAuditStateExpired,
		Reason:        LinkAuditReasonDeadStatus,
		HTTPStatus:    404,
		CheckedAt:     "2026-07-26T00:00:01Z",
		Confirmations: 99,
	}
	merged := mergeAuditResult(nil, first)
	if merged.Confirmations != 1 {
		t.Fatalf("first expired confirmations = %d, want 1", merged.Confirmations)
	}
	if merged.EligibleForTombstone(2) {
		t.Fatal("one confirmation is eligible, want false")
	}

	second := first
	second.CheckedAt = "2026-07-26T00:00:02Z"
	second.HTTPStatus = 410
	merged = mergeAuditResult(&merged, second)
	if merged.Confirmations != 2 || !merged.EligibleForTombstone(2) || merged.HTTPStatus != 410 || merged.CheckedAt != second.CheckedAt {
		t.Fatalf("same deletable merge = %+v, want confirmations=2 eligible current evidence", merged)
	}

	mismatch := second
	mismatch.State = LinkAuditStateContentMismatch
	mismatch.Reason = LinkAuditReasonMismatchBody
	merged = mergeAuditResult(&merged, mismatch)
	if merged.Confirmations != 1 || merged.EligibleForTombstone(2) {
		t.Fatalf("changed deletable merge = %+v, want reset to 1 and ineligible", merged)
	}

	blocked := mismatch
	blocked.State = LinkAuditStateBlocked
	blocked.Reason = LinkAuditReasonBlockedStatus
	merged = mergeAuditResult(&merged, blocked)
	if merged.Confirmations != 0 || merged.EligibleForTombstone(1) {
		t.Fatalf("blocked merge = %+v, want confirmations=0 and never eligible", merged)
	}

	transient := blocked
	transient.State = LinkAuditStateTransient
	transient.Reason = LinkAuditReasonTransportTimeout
	transient.Confirmations = 100
	if mergeAuditResult(&merged, transient).EligibleForTombstone(1) {
		t.Fatal("transient result is eligible, want false")
	}
}

func validLinkAuditPolicyJSON() string {
	return `{
		"schema_version":"link-audit-policy/v1",
		"timeout":"10s",
		"max_body_bytes":4096,
		"concurrency":4,
		"confirmations_required":2,
		"rules":[{
			"host_suffixes":["cdn.example.test"],
			"dead_statuses":[410,499],
			"dead_body_patterns":["(?i)not found"],
			"mismatch_body_patterns":["wrong content"],
			"required_body_patterns":["course page","download"],
			"allowed_redirect_host_suffixes":["files.example.test"]
		}]
	}`
}

func mustLoadLinkAuditPolicy(t *testing.T) *LinkAuditPolicy {
	t.Helper()

	policy, err := LoadLinkAuditPolicy(strings.NewReader(validLinkAuditPolicyJSON()))
	if err != nil {
		t.Fatalf("LoadLinkAuditPolicy() error = %v", err)
	}
	return policy
}

func validLinkAuditReportJSON(hash string) string {
	return `{"schema_version":"link-audit-report/v1","generated_at":"2026-07-26T00:00:00Z","results":[{"sha256":"` + hash + `","state":"live","reason":"ok","http_status":200,"checked_at":"2026-07-26T00:00:01Z","confirmations":0}]}`
}
