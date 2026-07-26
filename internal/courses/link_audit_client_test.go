package courses

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSafeLinkAuditClientDialUsesValidatedPublicIP(t *testing.T) {
	policy := testLinkAuditPolicy()
	resolver := &fakeLinkAuditResolver{addrs: map[string][]net.IPAddr{
		"files.example.test": {{IP: net.ParseIP("93.184.216.34")}},
	}}
	dialer := &fakeLinkAuditDialer{}

	client, err := newSafeLinkAuditClient(policy, resolver, dialer)
	if err != nil {
		t.Fatalf("newSafeLinkAuditClient() error = %v", err)
	}

	transport := client.Transport.(*http.Transport)
	if transport.MaxResponseHeaderBytes != 64<<10 {
		t.Fatalf("MaxResponseHeaderBytes = %d, want 65536", transport.MaxResponseHeaderBytes)
	}
	conn, err := transport.DialContext(context.Background(), "tcp", "files.example.test:443")
	if err != nil {
		t.Fatalf("DialContext() error = %v", err)
	}
	_ = conn.Close()

	if got := dialer.addresses(); len(got) != 1 || got[0] != "93.184.216.34:443" {
		t.Fatalf("dial addresses = %v, want explicit public IP", got)
	}
	if resolver.callsFor("files.example.test") != 1 {
		t.Fatalf("resolver calls = %d, want 1", resolver.callsFor("files.example.test"))
	}
}

func TestSafeLinkAuditClientDialRejectsUnsafeResolvedAddresses(t *testing.T) {
	tests := []struct {
		name string
		ip   string
	}{
		{"private", "10.0.0.1"},
		{"documentation", "192.0.2.10"},
		{"as112 ipv4", "192.31.196.1"},
		{"amt ipv4", "192.52.193.1"},
		{"carrier grade nat", "100.64.0.1"},
		{"benchmark", "198.18.0.1"},
		{"deprecated relay anycast", "192.88.99.1"},
		{"direct delegation as112 ipv4", "192.175.48.1"},
		{"reserved future", "240.0.0.1"},
		{"ipv4 mapped private", "::ffff:192.168.1.5"},
		{"ipv6 well known nat64", "64:ff9b::c000:201"},
		{"ipv6 local use nat64", "64:ff9b:1::c000:201"},
		{"ipv6 6to4", "2002:c000:0201::1"},
		{"ipv6 documentation", "2001:db8::1"},
		{"direct delegation as112 ipv6", "2620:4f:8000::1"},
		{"ipv6 documentation v2", "3fff::1"},
		{"ipv6 special purpose", "5f00::1"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver := &fakeLinkAuditResolver{addrs: map[string][]net.IPAddr{
				"files.example.test": {{IP: net.ParseIP("93.184.216.34")}, {IP: net.ParseIP(test.ip)}},
			}}
			dialer := &fakeLinkAuditDialer{}
			client, err := newSafeLinkAuditClient(testLinkAuditPolicy(), resolver, dialer)
			if err != nil {
				t.Fatalf("newSafeLinkAuditClient() error = %v", err)
			}

			_, err = client.Transport.(*http.Transport).DialContext(context.Background(), "tcp", "files.example.test:443")
			if err == nil {
				t.Fatal("DialContext() error = nil, want unsafe address rejection")
			}
			if got := dialer.addresses(); len(got) != 0 {
				t.Fatalf("dial addresses = %v, want none", got)
			}
		})
	}
}

func TestSafeLinkAuditClientDialRejectsNoAddressAndDNSError(t *testing.T) {
	tests := []struct {
		name     string
		resolver *fakeLinkAuditResolver
	}{
		{"no address", &fakeLinkAuditResolver{addrs: map[string][]net.IPAddr{"files.example.test": {}}}},
		{"dns error", &fakeLinkAuditResolver{errs: map[string]error{"files.example.test": &net.DNSError{Err: "no such host", Name: "files.example.test"}}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := newSafeLinkAuditClient(testLinkAuditPolicy(), test.resolver, &fakeLinkAuditDialer{})
			if err != nil {
				t.Fatalf("newSafeLinkAuditClient() error = %v", err)
			}
			if _, err := client.Transport.(*http.Transport).DialContext(context.Background(), "tcp", "files.example.test:443"); err == nil {
				t.Fatal("DialContext() error = nil, want error")
			}
		})
	}
}

func TestLinkAuditRedirectPolicy(t *testing.T) {
	policy := testLinkAuditPolicy()
	client, err := newSafeLinkAuditClient(policy, &fakeLinkAuditResolver{}, &fakeLinkAuditDialer{})
	if err != nil {
		t.Fatalf("newSafeLinkAuditClient() error = %v", err)
	}

	tests := []struct {
		name string
		from string
		to   string
		ok   bool
	}{
		{"same host", "https://cdn.example.test/a", "https://cdn.example.test/b", true},
		{"allowlisted suffix", "https://cdn.example.test/a", "https://sub.files.example.test/b", true},
		{"attacker suffix", "https://cdn.example.test/a", "https://evilfiles.example.test/b", false},
		{"private literal", "https://cdn.example.test/a", "https://192.168.1.1/b", false},
		{"fifth hop", "https://cdn.example.test/a", "https://cdn.example.test/b", true},
		{"sixth hop", "https://cdn.example.test/a", "https://cdn.example.test/b", false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := mustNewRequest(t, test.to)
			via := []*http.Request{mustNewRequest(t, test.from)}
			if test.name == "fifth hop" {
				via = append(via, via[0], via[0], via[0], via[0])
			}
			if test.name == "sixth hop" {
				via = append(via, via[0], via[0], via[0], via[0], via[0])
			}

			err := client.CheckRedirect(req, via)
			if test.ok && err != nil {
				t.Fatalf("CheckRedirect() error = %v", err)
			}
			if !test.ok && err == nil {
				t.Fatal("CheckRedirect() error = nil, want rejection")
			}
		})
	}
}

func TestAuditCatalogLinksHTTPExecution(t *testing.T) {
	now := time.Date(2026, 7, 26, 12, 0, 0, 0, time.FixedZone("UTC+10", 10*60*60))
	policy := testLinkAuditPolicy()
	policy.Concurrency = 2
	policy.MaxBodyBytes = 4
	policy.Rules[0].DeadStatuses = append(policy.Rules[0].DeadStatuses, http.StatusMethodNotAllowed)
	catalog := Catalog{Entries: []CatalogEntry{{
		Title: "secret title",
		Links: []CatalogLink{
			{URL: "https://cdn.example.test/body"},
			{URL: "https://cdn.example.test/body#fragment"},
			{URL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"},
			{URL: "https://cdn.example.test/gone"},
			{URL: "https://cdn.example.test/head-not-allowed"},
			{URL: "https://other.example.test/error"},
		},
	}}}
	previousHash := mustLinkHash(t, "https://cdn.example.test/body")
	previous := &LinkAuditReport{Results: []LinkAuditResult{{
		SHA256:        previousHash,
		State:         LinkAuditStateContentMismatch,
		Reason:        LinkAuditReasonRequiredBodyMissing,
		CheckedAt:     "2026-07-25T00:00:00Z",
		Confirmations: 3,
	}}}
	client := &fakeAuditHTTPClient{
		responses: map[string][]fakeHTTPResponse{
			"HEAD https://cdn.example.test/body":             {{status: 200, body: "ignored"}},
			"GET https://cdn.example.test/body":              {{status: 200, body: "nope-with-extra-bytes"}},
			"HEAD https://cdn.example.test/gone":             {{status: 404, body: "gone"}},
			"GET https://cdn.example.test/gone":              {{status: 404, body: "gone"}},
			"HEAD https://cdn.example.test/head-not-allowed": {{status: 405, body: "no head"}},
			"GET https://cdn.example.test/head-not-allowed":  {{status: 200, body: "course page download"}},
		},
		errs: map[string]error{
			"HEAD https://other.example.test/error": &net.DNSError{Err: "no such host", Name: "other.example.test"},
		},
	}

	report, err := AuditCatalogLinks(context.Background(), catalog, policy, previous, client, now)
	if err != nil {
		t.Fatalf("AuditCatalogLinks() error = %v", err)
	}

	if report.GeneratedAt != "2026-07-26T02:00:00Z" {
		t.Fatalf("GeneratedAt = %q, want UTC RFC3339", report.GeneratedAt)
	}
	if len(report.Results) != 4 {
		t.Fatalf("results = %d, want 4: %+v", len(report.Results), report.Results)
	}
	assertSortedAuditResults(t, report.Results)
	if client.maxActive.Load() > int32(policy.Concurrency) {
		t.Fatalf("max active = %d, want <= %d", client.maxActive.Load(), policy.Concurrency)
	}
	if got := client.count("GET https://cdn.example.test/gone"); got != 1 {
		t.Fatalf("GET after HEAD 404 count = %d, want 1", got)
	}
	bodyReq := client.request("GET https://cdn.example.test/body")
	if bodyReq == nil {
		t.Fatal("GET body request missing")
	}
	if got := bodyReq.Header.Get("Range"); got != "bytes=0-3" {
		t.Fatalf("Range = %q, want bytes=0-3", got)
	}
	if got := bodyReq.Header.Get("Accept-Encoding"); got != "identity" {
		t.Fatalf("Accept-Encoding = %q, want identity", got)
	}
	if got := client.count("GET https://cdn.example.test/body"); got != 1 {
		t.Fatalf("body-rule GET requests = %d, want 1", got)
	}
	goneReq := client.request("GET https://cdn.example.test/gone")
	if goneReq == nil {
		t.Fatal("dead-status verification GET request missing")
	}
	if got := goneReq.Header.Get("Range"); got != "bytes=0-3" {
		t.Fatalf("dead-status verification Range = %q, want bytes=0-3", got)
	}
	if got := goneReq.Header.Get("Accept-Encoding"); got != "identity" {
		t.Fatalf("dead-status verification Accept-Encoding = %q, want identity", got)
	}
	fallbackReq := client.request("GET https://cdn.example.test/head-not-allowed")
	if fallbackReq == nil {
		t.Fatal("fallback GET request missing")
	}
	if got := client.count("GET https://cdn.example.test/head-not-allowed"); got != 1 {
		t.Fatalf("overlapping fallback/dead-status GET requests = %d, want 1", got)
	}
	if got := fallbackReq.Header.Get("Range"); got != "bytes=0-3" {
		t.Fatalf("fallback Range = %q, want bytes=0-3", got)
	}
	if got := fallbackReq.Header.Get("Accept-Encoding"); got != "identity" {
		t.Fatalf("fallback Accept-Encoding = %q, want identity", got)
	}
	if got := client.closedBodies.Load(); got < 5 {
		t.Fatalf("closed bodies = %d, want all responses closed", got)
	}

	byHash := resultsByHash(report.Results)
	body := byHash[previousHash]
	if body.State != LinkAuditStateContentMismatch || body.Confirmations != 4 {
		t.Fatalf("body result = %+v, want merged content mismatch confirmation", body)
	}
	if body.CheckedAt != "2026-07-26T02:00:00Z" {
		t.Fatalf("CheckedAt = %q, want UTC now", body.CheckedAt)
	}
	fallback := byHash[mustLinkHash(t, "https://cdn.example.test/head-not-allowed")]
	if fallback.State != LinkAuditStateContentMismatch || fallback.Reason != LinkAuditReasonRequiredBodyMissing {
		t.Fatalf("fallback result = %+v, want required-body mismatch", fallback)
	}
	if result := byHash[mustLinkHash(t, "https://other.example.test/error")]; result.Reason != LinkAuditReasonTransportDNS {
		t.Fatalf("transport result = %+v, want DNS transient", result)
	}
}

func TestAuditCatalogLinksValidatesInputsAndCancellation(t *testing.T) {
	catalog := Catalog{Entries: []CatalogEntry{{Links: []CatalogLink{{URL: "https://cdn.example.test/a"}}}}}
	if _, err := AuditCatalogLinks(context.Background(), catalog, nil, nil, &fakeAuditHTTPClient{}, time.Now()); err == nil {
		t.Fatal("nil policy error = nil")
	}
	if _, err := AuditCatalogLinks(context.Background(), catalog, testLinkAuditPolicy(), nil, nil, time.Now()); err == nil {
		t.Fatal("nil client error = nil")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := AuditCatalogLinks(ctx, catalog, testLinkAuditPolicy(), nil, &fakeAuditHTTPClient{}, time.Now())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error = %v, want context.Canceled", err)
	}
}

func TestLinkAuditStatusOnlyFallbackUsesOneByteGET(t *testing.T) {
	for _, headStatus := range []int{http.StatusMethodNotAllowed, http.StatusNotImplemented} {
		t.Run(http.StatusText(headStatus), func(t *testing.T) {
			policy := &LinkAuditPolicy{
				MaxBodyBytes: 64,
				Rules: []LinkAuditRule{{
					HostSuffixes: []string{"127.0.0.1"},
					DeadStatuses: []int{http.StatusNotFound},
				}},
			}
			getHeaders := make(chan http.Header, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					w.WriteHeader(headStatus)
					return
				}
				getHeaders <- r.Header.Clone()
				_, _ = io.WriteString(w, strings.Repeat("x", int(policy.MaxBodyBytes)))
			}))
			t.Cleanup(server.Close)

			serverURL, err := url.Parse(server.URL)
			if err != nil {
				t.Fatalf("url.Parse(%q): %v", server.URL, err)
			}
			var readBytes atomic.Int64
			client := &bodyReadCountingClient{client: server.Client(), readBytes: &readBytes}

			observation, err := requestLinkAuditObservation(
				context.Background(),
				linkAuditCandidate{rawURL: server.URL, host: serverURL.Hostname()},
				policy,
				client,
			)
			if err != nil {
				t.Fatalf("requestLinkAuditObservation() error = %v", err)
			}
			if observation.HTTPStatus != http.StatusOK {
				t.Fatalf("HTTPStatus = %d, want 200", observation.HTTPStatus)
			}
			if len(observation.Body) != 0 {
				t.Fatalf("Body length = %d, want 0 for status-only audit", len(observation.Body))
			}

			var headers http.Header
			select {
			case headers = <-getHeaders:
			case <-time.After(time.Second):
				t.Fatal("GET request missing")
			}
			if got := headers.Get("Range"); got != "bytes=0-0" {
				t.Fatalf("Range = %q, want bytes=0-0", got)
			}
			if got := headers.Get("Accept-Encoding"); got != "identity" {
				t.Fatalf("Accept-Encoding = %q, want identity", got)
			}
			if got := readBytes.Load(); got > 1 {
				t.Fatalf("response body bytes read = %d, want <= 1", got)
			}
		})
	}
}

func TestLinkAuditStatusOnlyDeadHEADRetriesRangeNotSatisfiableOnce(t *testing.T) {
	tests := []struct {
		name        string
		retryStatus int
		wantState   LinkAuditState
		wantReason  LinkAuditReason
	}{
		{
			name:        "empty resource is live",
			retryStatus: http.StatusOK,
			wantState:   LinkAuditStateLive,
			wantReason:  LinkAuditReasonOK,
		},
		{
			name:        "second range failure is not retried",
			retryStatus: http.StatusRequestedRangeNotSatisfiable,
			wantState:   LinkAuditStateUnknown,
			wantReason:  LinkAuditReasonUnhandledHTTPStatus,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := &LinkAuditPolicy{MaxBodyBytes: 64}
			var getRequests atomic.Int32
			getHeaders := make(chan http.Header, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				requestNumber := getRequests.Add(1)
				select {
				case getHeaders <- r.Header.Clone():
				default:
				}
				if requestNumber == 1 {
					w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
					_, _ = io.WriteString(w, "range not satisfiable")
					return
				}
				w.WriteHeader(test.retryStatus)
				_, _ = io.WriteString(w, "ordinary response")
			}))
			t.Cleanup(server.Close)

			serverURL, err := url.Parse(server.URL)
			if err != nil {
				t.Fatalf("url.Parse(%q): %v", server.URL, err)
			}
			var readBytes atomic.Int64
			client := &bodyReadCountingClient{client: server.Client(), readBytes: &readBytes}

			result, err := auditCatalogLink(
				context.Background(),
				linkAuditCandidate{rawURL: server.URL, host: serverURL.Hostname()},
				policy,
				client,
				"2026-07-27T00:00:00Z",
			)
			if err != nil {
				t.Fatalf("auditCatalogLink() error = %v", err)
			}
			if result.State != test.wantState || result.Reason != test.wantReason {
				t.Fatalf("result = %+v, want state %q reason %q", result, test.wantState, test.wantReason)
			}
			if result.HTTPStatus != test.retryStatus {
				t.Fatalf("HTTPStatus = %d, want retry GET status %d", result.HTTPStatus, test.retryStatus)
			}
			if got := getRequests.Load(); got != 2 {
				t.Fatalf("GET requests = %d, want 2", got)
			}
			first := <-getHeaders
			second := <-getHeaders
			if got := first.Get("Range"); got != "bytes=0-0" {
				t.Fatalf("first Range = %q, want bytes=0-0", got)
			}
			if got := second.Get("Range"); got != "" {
				t.Fatalf("second Range = %q, want empty", got)
			}
			for index, headers := range []http.Header{first, second} {
				if got := headers.Get("Accept-Encoding"); got != "identity" {
					t.Fatalf("GET %d Accept-Encoding = %q, want identity", index+1, got)
				}
			}
			if got := readBytes.Load(); got != 0 {
				t.Fatalf("response body bytes read = %d, want 0", got)
			}
		})
	}
}

func TestLinkAuditBodyRuleDeadHEADRetriesRangeNotSatisfiableOnce(t *testing.T) {
	tests := []struct {
		name         string
		deadBody     string
		requiredBody string
		retryBody    string
		wantState    LinkAuditState
		wantReason   LinkAuditReason
		wantRead     int64
	}{
		{
			name:         "empty body fails required pattern",
			requiredBody: "course page",
			wantState:    LinkAuditStateContentMismatch,
			wantReason:   LinkAuditReasonRequiredBodyMissing,
		},
		{
			name:       "empty body does not match dead pattern",
			deadBody:   "gone",
			wantState:  LinkAuditStateLive,
			wantReason: LinkAuditReasonOK,
		},
		{
			name:       "ordinary response remains locally capped",
			deadBody:   "gone",
			retryBody:  strings.Repeat("x", 128),
			wantState:  LinkAuditStateLive,
			wantReason: LinkAuditReasonOK,
			wantRead:   65,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rule := LinkAuditRule{HostSuffixes: []string{"127.0.0.1"}}
			if test.deadBody != "" {
				rule.DeadBodyPatterns = []string{test.deadBody}
				rule.deadBodyRegexps = mustCompileLinkAuditPatterns(test.deadBody)
			}
			if test.requiredBody != "" {
				rule.RequiredBodyPatterns = []string{test.requiredBody}
				rule.requiredBodyRegexps = mustCompileLinkAuditPatterns(test.requiredBody)
			}
			policy := &LinkAuditPolicy{
				MaxBodyBytes: 64,
				Rules:        []LinkAuditRule{rule},
			}
			var getRequests atomic.Int32
			getHeaders := make(chan http.Header, 2)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				requestNumber := getRequests.Add(1)
				select {
				case getHeaders <- r.Header.Clone():
				default:
				}
				if requestNumber == 1 {
					w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
					_, _ = io.WriteString(w, "gone")
					return
				}
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, test.retryBody)
			}))
			t.Cleanup(server.Close)

			serverURL, err := url.Parse(server.URL)
			if err != nil {
				t.Fatalf("url.Parse(%q): %v", server.URL, err)
			}
			var readBytes atomic.Int64
			client := &bodyReadCountingClient{client: server.Client(), readBytes: &readBytes}

			result, err := auditCatalogLink(
				context.Background(),
				linkAuditCandidate{rawURL: server.URL, host: serverURL.Hostname()},
				policy,
				client,
				"2026-07-27T00:00:00Z",
			)
			if err != nil {
				t.Fatalf("auditCatalogLink() error = %v", err)
			}
			if result.State != test.wantState || result.Reason != test.wantReason {
				t.Fatalf("result = %+v, want state %q reason %q", result, test.wantState, test.wantReason)
			}
			if result.HTTPStatus != http.StatusOK {
				t.Fatalf("HTTPStatus = %d, want retry GET status 200", result.HTTPStatus)
			}
			if got := getRequests.Load(); got != 2 {
				t.Fatalf("GET requests = %d, want 2", got)
			}
			first := <-getHeaders
			second := <-getHeaders
			if got := first.Get("Range"); got != "bytes=0-63" {
				t.Fatalf("first Range = %q, want bytes=0-63", got)
			}
			if got := second.Get("Range"); got != "" {
				t.Fatalf("second Range = %q, want empty", got)
			}
			for index, headers := range []http.Header{first, second} {
				if got := headers.Get("Accept-Encoding"); got != "identity" {
					t.Fatalf("GET %d Accept-Encoding = %q, want identity", index+1, got)
				}
			}
			if got := readBytes.Load(); got != test.wantRead {
				t.Fatalf("response body bytes read = %d, want %d", got, test.wantRead)
			}
		})
	}
}

func TestLinkAuditDeadHEADStatusIsVerifiedByGET(t *testing.T) {
	tests := []struct {
		name         string
		headStatus   int
		getStatus    int
		body         string
		deadStatuses []int
		noRule       bool
		deadBody     string
		mismatchBody string
		requiredBody string
		wantState    LinkAuditState
		wantReason   LinkAuditReason
		wantRange    string
		wantBodyRead bool
	}{
		{
			name:         "status-only live GET",
			getStatus:    http.StatusOK,
			body:         strings.Repeat("x", 64),
			deadStatuses: []int{http.StatusNotFound},
			wantState:    LinkAuditStateLive,
			wantReason:   LinkAuditReasonOK,
			wantRange:    "bytes=0-0",
		},
		{
			name:         "status-only dead GET",
			getStatus:    http.StatusNotFound,
			body:         strings.Repeat("x", 64),
			deadStatuses: []int{http.StatusNotFound},
			wantState:    LinkAuditStateExpired,
			wantReason:   LinkAuditReasonGlobalDeadStatus,
			wantRange:    "bytes=0-0",
		},
		{
			name:         "status-only other GET",
			getStatus:    http.StatusServiceUnavailable,
			body:         strings.Repeat("x", 64),
			deadStatuses: []int{http.StatusNotFound},
			wantState:    LinkAuditStateTransient,
			wantReason:   LinkAuditReasonServerStatus,
			wantRange:    "bytes=0-0",
		},
		{
			name:         "required body present",
			getStatus:    http.StatusOK,
			body:         "course page",
			deadStatuses: []int{http.StatusNotFound},
			requiredBody: "course page",
			wantState:    LinkAuditStateLive,
			wantReason:   LinkAuditReasonOK,
			wantRange:    "bytes=0-63",
			wantBodyRead: true,
		},
		{
			name:         "required body missing",
			getStatus:    http.StatusOK,
			body:         "unrelated",
			deadStatuses: []int{http.StatusNotFound},
			requiredBody: "course page",
			wantState:    LinkAuditStateContentMismatch,
			wantReason:   LinkAuditReasonRequiredBodyMissing,
			wantRange:    "bytes=0-63",
			wantBodyRead: true,
		},
		{
			name:         "body-rule dead GET",
			getStatus:    http.StatusNotFound,
			body:         strings.Repeat("x", 64),
			deadStatuses: []int{http.StatusNotFound},
			requiredBody: "course page",
			wantState:    LinkAuditStateExpired,
			wantReason:   LinkAuditReasonGlobalDeadStatus,
			wantRange:    "bytes=0-63",
		},
		{
			name:         "body-rule other GET",
			getStatus:    http.StatusServiceUnavailable,
			body:         strings.Repeat("x", 64),
			deadStatuses: []int{http.StatusNotFound},
			requiredBody: "course page",
			wantState:    LinkAuditStateTransient,
			wantReason:   LinkAuditReasonServerStatus,
			wantRange:    "bytes=0-63",
		},
		{
			name:         "dead body on blocked GET",
			getStatus:    http.StatusForbidden,
			body:         "gone" + strings.Repeat("x", 128),
			deadStatuses: []int{http.StatusNotFound},
			deadBody:     "gone",
			wantState:    LinkAuditStateExpired,
			wantReason:   LinkAuditReasonDeadBody,
			wantRange:    "bytes=0-63",
			wantBodyRead: true,
		},
		{
			name:         "dead body on server-error GET",
			getStatus:    http.StatusServiceUnavailable,
			body:         "gone" + strings.Repeat("x", 128),
			deadStatuses: []int{http.StatusNotFound},
			deadBody:     "gone",
			wantState:    LinkAuditStateExpired,
			wantReason:   LinkAuditReasonDeadBody,
			wantRange:    "bytes=0-63",
			wantBodyRead: true,
		},
		{
			name:         "mismatch body is not read on blocked GET",
			getStatus:    http.StatusForbidden,
			body:         "wrong page",
			deadStatuses: []int{http.StatusNotFound},
			mismatchBody: "wrong",
			wantState:    LinkAuditStateBlocked,
			wantReason:   LinkAuditReasonBlockedStatus,
			wantRange:    "bytes=0-63",
		},
		{
			name:         "successful HEAD body-rule dead GET",
			headStatus:   http.StatusOK,
			getStatus:    http.StatusNotFound,
			body:         strings.Repeat("x", 64),
			deadStatuses: []int{},
			requiredBody: "course page",
			wantState:    LinkAuditStateExpired,
			wantReason:   LinkAuditReasonGlobalDeadStatus,
			wantRange:    "bytes=0-63",
		},
		{
			name:         "successful HEAD body-rule other GET",
			headStatus:   http.StatusOK,
			getStatus:    http.StatusServiceUnavailable,
			body:         strings.Repeat("x", 64),
			deadStatuses: []int{},
			requiredBody: "course page",
			wantState:    LinkAuditStateTransient,
			wantReason:   LinkAuditReasonServerStatus,
			wantRange:    "bytes=0-63",
		},
		{
			name:         "fallback HEAD body-rule dead GET",
			headStatus:   http.StatusMethodNotAllowed,
			getStatus:    http.StatusNotFound,
			body:         strings.Repeat("x", 64),
			deadStatuses: []int{http.StatusMethodNotAllowed},
			requiredBody: "course page",
			wantState:    LinkAuditStateExpired,
			wantReason:   LinkAuditReasonGlobalDeadStatus,
			wantRange:    "bytes=0-63",
		},
		{
			name:         "fallback HEAD body-rule other GET",
			headStatus:   http.StatusMethodNotAllowed,
			getStatus:    http.StatusServiceUnavailable,
			body:         strings.Repeat("x", 64),
			deadStatuses: []int{http.StatusMethodNotAllowed},
			requiredBody: "course page",
			wantState:    LinkAuditStateTransient,
			wantReason:   LinkAuditReasonServerStatus,
			wantRange:    "bytes=0-63",
		},
		{
			name:         "global 404 with empty rule dead statuses",
			getStatus:    http.StatusOK,
			body:         strings.Repeat("x", 64),
			deadStatuses: []int{},
			wantState:    LinkAuditStateLive,
			wantReason:   LinkAuditReasonOK,
			wantRange:    "bytes=0-0",
		},
		{
			name:       "global 410 without matching rule",
			headStatus: http.StatusGone,
			getStatus:  http.StatusOK,
			body:       strings.Repeat("x", 64),
			noRule:     true,
			wantState:  LinkAuditStateLive,
			wantReason: LinkAuditReasonOK,
			wantRange:  "bytes=0-0",
		},
		{
			name:         "global 404 body rule with nil dead statuses",
			getStatus:    http.StatusOK,
			body:         "course page",
			requiredBody: "course page",
			wantState:    LinkAuditStateLive,
			wantReason:   LinkAuditReasonOK,
			wantRange:    "bytes=0-63",
			wantBodyRead: true,
		},
		{
			name:         "global 410 body rule with empty dead statuses",
			headStatus:   http.StatusGone,
			getStatus:    http.StatusOK,
			body:         "unrelated",
			deadStatuses: []int{},
			requiredBody: "course page",
			wantState:    LinkAuditStateContentMismatch,
			wantReason:   LinkAuditReasonRequiredBodyMissing,
			wantRange:    "bytes=0-63",
			wantBodyRead: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rule := LinkAuditRule{
				HostSuffixes: []string{"127.0.0.1"},
				DeadStatuses: test.deadStatuses,
			}
			if test.requiredBody != "" {
				rule.RequiredBodyPatterns = []string{test.requiredBody}
				rule.requiredBodyRegexps = mustCompileLinkAuditPatterns(test.requiredBody)
			}
			if test.deadBody != "" {
				rule.DeadBodyPatterns = []string{test.deadBody}
				rule.deadBodyRegexps = mustCompileLinkAuditPatterns(test.deadBody)
			}
			if test.mismatchBody != "" {
				rule.MismatchBodyPatterns = []string{test.mismatchBody}
				rule.mismatchBodyRegexps = mustCompileLinkAuditPatterns(test.mismatchBody)
			}
			rules := []LinkAuditRule{rule}
			if test.noRule {
				rules = []LinkAuditRule{}
			}
			policy := &LinkAuditPolicy{
				MaxBodyBytes: 64,
				Rules:        rules,
			}
			getHeaders := make(chan http.Header, 1)
			var getRequests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					headStatus := test.headStatus
					if headStatus == 0 {
						headStatus = http.StatusNotFound
					}
					w.WriteHeader(headStatus)
					return
				}
				getRequests.Add(1)
				select {
				case getHeaders <- r.Header.Clone():
				default:
				}
				w.WriteHeader(test.getStatus)
				_, _ = io.WriteString(w, test.body)
			}))
			t.Cleanup(server.Close)

			serverURL, err := url.Parse(server.URL)
			if err != nil {
				t.Fatalf("url.Parse(%q): %v", server.URL, err)
			}
			var readBytes atomic.Int64
			client := &bodyReadCountingClient{client: server.Client(), readBytes: &readBytes}

			result, err := auditCatalogLink(
				context.Background(),
				linkAuditCandidate{rawURL: server.URL, host: serverURL.Hostname()},
				policy,
				client,
				"2026-07-27T00:00:00Z",
			)
			if err != nil {
				t.Fatalf("auditCatalogLink() error = %v", err)
			}
			if result.State != test.wantState || result.Reason != test.wantReason {
				t.Fatalf("result = %+v, want state %q reason %q", result, test.wantState, test.wantReason)
			}
			if result.HTTPStatus != test.getStatus {
				t.Fatalf("HTTPStatus = %d, want GET status %d", result.HTTPStatus, test.getStatus)
			}
			if got := getRequests.Load(); got != 1 {
				t.Fatalf("GET requests = %d, want 1", got)
			}

			var headers http.Header
			select {
			case headers = <-getHeaders:
			case <-time.After(time.Second):
				t.Fatal("GET request missing")
			}
			if got := headers.Get("Range"); got != test.wantRange {
				t.Fatalf("Range = %q, want %s", got, test.wantRange)
			}
			if got := headers.Get("Accept-Encoding"); got != "identity" {
				t.Fatalf("Accept-Encoding = %q, want identity", got)
			}
			gotRead := readBytes.Load()
			if test.wantBodyRead && (gotRead == 0 || gotRead > policy.MaxBodyBytes+1) {
				t.Fatalf("response body bytes read = %d, want 1..%d", gotRead, policy.MaxBodyBytes+1)
			}
			if !test.wantBodyRead && gotRead != 0 {
				t.Fatalf("response body bytes read = %d, want 0", gotRead)
			}
		})
	}
}

func TestLinkAuditHTTPErrorClassification(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want LinkAuditReason
	}{
		{"timeout", context.DeadlineExceeded, LinkAuditReasonTransportTimeout},
		{"per-link canceled", context.Canceled, LinkAuditReasonTransportError},
		{"dns", &net.DNSError{Err: "no such host"}, LinkAuditReasonTransportDNS},
		{"tls", &url.Error{Err: tls.RecordHeaderError{}}, LinkAuditReasonTransportTLS},
		{"tls verification", &tls.CertificateVerificationError{Err: x509.CertificateInvalidError{Reason: x509.Expired}}, LinkAuditReasonTransportTLS},
		{"connection", &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("refused")}, LinkAuditReasonTransportConnection},
		{"other", errors.New("boom"), LinkAuditReasonTransportError},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog := Catalog{Entries: []CatalogEntry{{Links: []CatalogLink{{URL: "https://cdn.example.test/a"}}}}}
			client := &fakeAuditHTTPClient{errs: map[string]error{"HEAD https://cdn.example.test/a": test.err}}
			report, err := AuditCatalogLinks(context.Background(), catalog, testLinkAuditPolicy(), nil, client, time.Date(2026, 7, 26, 0, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatalf("AuditCatalogLinks() error = %v", err)
			}
			if got := report.Results[0].Reason; got != test.want {
				t.Fatalf("Reason = %q, want %q", got, test.want)
			}
		})
	}
}

type fakeLinkAuditResolver struct {
	mu    sync.Mutex
	addrs map[string][]net.IPAddr
	errs  map[string]error
	calls map[string]int
}

func (f *fakeLinkAuditResolver) LookupIPAddr(_ context.Context, host string) ([]net.IPAddr, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = make(map[string]int)
	}
	f.calls[host]++
	if err := f.errs[host]; err != nil {
		return nil, err
	}
	return append([]net.IPAddr(nil), f.addrs[host]...), nil
}

func (f *fakeLinkAuditResolver) callsFor(host string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[host]
}

type fakeLinkAuditDialer struct {
	mu    sync.Mutex
	addrs []string
	err   error
}

func (f *fakeLinkAuditDialer) DialContext(_ context.Context, _ string, address string) (net.Conn, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addrs = append(f.addrs, address)
	if f.err != nil {
		return nil, f.err
	}
	return fakeNetConn{}, nil
}

func (f *fakeLinkAuditDialer) addresses() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.addrs...)
}

type fakeNetConn struct{}

func (fakeNetConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (fakeNetConn) Write(p []byte) (int, error)      { return len(p), nil }
func (fakeNetConn) Close() error                     { return nil }
func (fakeNetConn) LocalAddr() net.Addr              { return fakeNetAddr("local") }
func (fakeNetConn) RemoteAddr() net.Addr             { return fakeNetAddr("remote") }
func (fakeNetConn) SetDeadline(time.Time) error      { return nil }
func (fakeNetConn) SetReadDeadline(time.Time) error  { return nil }
func (fakeNetConn) SetWriteDeadline(time.Time) error { return nil }

type fakeNetAddr string

func (f fakeNetAddr) Network() string { return string(f) }
func (f fakeNetAddr) String() string  { return string(f) }

type fakeAuditHTTPClient struct {
	mu           sync.Mutex
	responses    map[string][]fakeHTTPResponse
	errs         map[string]error
	requests     map[string][]*http.Request
	active       atomic.Int32
	maxActive    atomic.Int32
	closedBodies atomic.Int32
}

type fakeHTTPResponse struct {
	status int
	body   string
}

type bodyReadCountingClient struct {
	client    *http.Client
	readBytes *atomic.Int64
}

func (c *bodyReadCountingClient) Do(req *http.Request) (*http.Response, error) {
	resp, err := c.client.Do(req)
	if resp != nil && resp.Body != nil {
		resp.Body = &readCountingBody{ReadCloser: resp.Body, readBytes: c.readBytes}
	}
	return resp, err
}

type readCountingBody struct {
	io.ReadCloser
	readBytes *atomic.Int64
}

func (b *readCountingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.readBytes.Add(int64(n))
	return n, err
}

func (f *fakeAuditHTTPClient) Do(req *http.Request) (*http.Response, error) {
	active := f.active.Add(1)
	defer f.active.Add(-1)
	for {
		maxActive := f.maxActive.Load()
		if active <= maxActive || f.maxActive.CompareAndSwap(maxActive, active) {
			break
		}
	}

	key := req.Method + " " + req.URL.String()
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()

	f.mu.Lock()
	if f.requests == nil {
		f.requests = make(map[string][]*http.Request)
	}
	f.requests[key] = append(f.requests[key], clone)
	err := f.errs[key]
	queue := f.responses[key]
	if len(queue) > 0 {
		f.responses[key] = queue[1:]
	}
	f.mu.Unlock()

	if err != nil {
		return nil, err
	}
	if len(queue) == 0 {
		return &http.Response{StatusCode: 200, Body: closeCountingBody{Reader: bytes.NewReader([]byte("course page download")), closed: &f.closedBodies}}, nil
	}
	resp := queue[0]
	return &http.Response{
		StatusCode: resp.status,
		Body:       closeCountingBody{Reader: bytes.NewReader([]byte(resp.body)), closed: &f.closedBodies},
	}, nil
}

func (f *fakeAuditHTTPClient) count(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests[key])
}

func (f *fakeAuditHTTPClient) request(key string) *http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests[key]) == 0 {
		return nil
	}
	return f.requests[key][0]
}

type closeCountingBody struct {
	*bytes.Reader
	closed *atomic.Int32
}

func (c closeCountingBody) Close() error {
	c.closed.Add(1)
	return nil
}

func testLinkAuditPolicy() *LinkAuditPolicy {
	return &LinkAuditPolicy{
		Timeout:               2 * time.Second,
		MaxBodyBytes:          8,
		Concurrency:           1,
		ConfirmationsRequired: 2,
		Rules: []LinkAuditRule{{
			HostSuffixes:                []string{"cdn.example.test"},
			DeadStatuses:                []int{499},
			DeadBodyPatterns:            []string{"gone"},
			MismatchBodyPatterns:        []string{"wrong"},
			RequiredBodyPatterns:        []string{"course page", "download"},
			AllowedRedirectHostSuffixes: []string{"files.example.test"},
			deadBodyRegexps:             mustCompileLinkAuditPatterns("gone"),
			mismatchBodyRegexps:         mustCompileLinkAuditPatterns("wrong"),
			requiredBodyRegexps:         mustCompileLinkAuditPatterns("course page", "download"),
		}},
	}
}

func mustCompileLinkAuditPatterns(values ...string) []*regexp.Regexp {
	patterns, err := compileLinkAuditPatterns("test", values)
	if err != nil {
		panic(err)
	}
	return patterns
}

func mustNewRequest(t *testing.T, raw string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, raw, nil)
	if err != nil {
		t.Fatalf("NewRequest(%q): %v", raw, err)
	}
	return req
}

func mustLinkHash(t *testing.T, raw string) string {
	t.Helper()
	hash, err := linkHash(raw)
	if err != nil {
		t.Fatalf("linkHash(%q): %v", raw, err)
	}
	return hash
}

func assertSortedAuditResults(t *testing.T, results []LinkAuditResult) {
	t.Helper()
	for index := 1; index < len(results); index++ {
		if results[index-1].SHA256 > results[index].SHA256 {
			t.Fatalf("results not sorted at %d: %+v", index, results)
		}
	}
}

func resultsByHash(results []LinkAuditResult) map[string]LinkAuditResult {
	byHash := make(map[string]LinkAuditResult, len(results))
	for _, result := range results {
		byHash[result.SHA256] = result
	}
	return byHash
}
