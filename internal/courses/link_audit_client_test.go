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
	"net/url"
	"regexp"
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
	if got := client.count("GET https://cdn.example.test/gone"); got != 0 {
		t.Fatalf("GET after HEAD 404 count = %d, want 0", got)
	}
	bodyReq := client.request("GET https://cdn.example.test/body")
	if bodyReq == nil {
		t.Fatal("GET body request missing")
	}
	if got := bodyReq.Header.Get("Range"); got != "bytes=0-3" {
		t.Fatalf("Range = %q, want bytes=0-3", got)
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
