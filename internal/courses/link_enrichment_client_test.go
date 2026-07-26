package courses

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEnrichCatalogLinksFetchesMatchingStaleCandidatesAndPreservesFailures(t *testing.T) {
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.FixedZone("UTC+10", 10*60*60))
	policy := mustLinkEnrichmentPolicy(t, 10)
	policy.MaxBodyBytes = 128
	urls := struct {
		extracted, missing, stale, fresh, freshMissing string
		large, badStatus, transport, malformed         string
		responseError                                  string
	}{
		extracted:     "https://assets.example.test/extracted",
		missing:       "https://assets.example.test/missing",
		stale:         "https://assets.example.test/stale",
		fresh:         "https://assets.example.test/fresh",
		freshMissing:  "https://assets.example.test/fresh-missing",
		large:         "https://assets.example.test/large",
		badStatus:     "https://assets.example.test/status",
		transport:     "https://assets.example.test/transport",
		malformed:     "https://assets.example.test/malformed",
		responseError: "https://assets.example.test/response-error",
	}
	catalog := Catalog{Entries: []CatalogEntry{{Links: []CatalogLink{
		{URL: urls.extracted + "#fragment"},
		{URL: urls.extracted},
		{URL: urls.missing},
		{URL: urls.stale},
		{URL: urls.fresh},
		{URL: urls.freshMissing},
		{URL: urls.large},
		{URL: urls.badStatus},
		{URL: urls.transport},
		{URL: urls.malformed},
		{URL: urls.responseError},
		{URL: "https://other.example.test/outside-policy"},
		{URL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"},
		{URL: "not a url"},
	}}}}

	previous := NewLinkEnrichmentCache(now.Add(-48 * time.Hour))
	putExtractedURL(t, previous, urls.fresh, LinkContent{Name: "fresh old"}, now.Add(-time.Hour))
	putNotFoundURL(t, previous, urls.freshMissing, now.Add(-time.Hour))
	putNotFoundURL(t, previous, urls.stale, now.Add(-48*time.Hour))
	putExtractedURL(t, previous, urls.large, LinkContent{Name: "large old"}, now.Add(-48*time.Hour))
	putNotFoundURL(t, previous, urls.badStatus, now.Add(-48*time.Hour))
	putExtractedURL(t, previous, urls.transport, LinkContent{Name: "transport old"}, now.Add(-48*time.Hour))
	putExtractedURL(t, previous, urls.malformed, LinkContent{Name: "malformed old"}, now.Add(-48*time.Hour))
	unrelatedURL := "https://assets.example.test/unrelated"
	putExtractedURL(t, previous, unrelatedURL, LinkContent{Name: "unrelated old"}, now.Add(-48*time.Hour))
	previousJSON := marshalEnrichmentCache(t, previous)

	client := &fakeEnrichmentHTTPClient{
		responses: map[string]fakeEnrichmentResponse{
			urls.extracted: {status: http.StatusOK, body: enrichmentBody("extracted new")},
			urls.missing:   {status: http.StatusPartialContent, body: "<html>no metadata</html>"},
			urls.stale:     {status: http.StatusOK, body: enrichmentBody("stale new")},
			urls.large:     {status: http.StatusOK, body: strings.Repeat("x", int(policy.MaxBodyBytes)+1)},
			urls.badStatus: {status: http.StatusTooManyRequests, body: "private status body"},
			urls.malformed: {
				status: http.StatusOK,
				body:   `<script>{"embeddedPayload":{"name":42}}</script>`,
			},
			urls.responseError: {status: http.StatusFound, body: "private redirect body"},
		},
		errs: map[string]error{
			urls.transport:     errors.New("private transport https://assets.example.test/secret"),
			urls.responseError: errors.New("private redirect error"),
		},
	}

	cache, stats, err := EnrichCatalogLinks(
		context.Background(),
		catalog,
		policy,
		previous,
		client,
		now,
		false,
		3,
	)
	if err != nil {
		t.Fatalf("EnrichCatalogLinks() error = %v", err)
	}
	if stats != (LinkEnrichmentStats{
		Candidates:   10,
		SkippedFresh: 2,
		Fetched:      8,
		Extracted:    2,
		NotFound:     1,
		Failed:       5,
	}) {
		t.Fatalf("stats = %+v", stats)
	}
	if cache.GeneratedAt != "2026-07-27T02:00:00Z" {
		t.Fatalf("GeneratedAt = %q", cache.GeneratedAt)
	}
	if got := marshalEnrichmentCache(t, previous); !bytes.Equal(got, previousJSON) {
		t.Fatal("previous cache was mutated")
	}
	assertEnrichmentName(t, cache, urls.extracted, "extracted new")
	assertEnrichmentName(t, cache, urls.stale, "stale new")
	assertEnrichmentName(t, cache, urls.fresh, "fresh old")
	assertEnrichmentName(t, cache, urls.large, "large old")
	assertEnrichmentName(t, cache, urls.transport, "transport old")
	assertEnrichmentName(t, cache, urls.malformed, "malformed old")
	assertEnrichmentName(t, cache, unrelatedURL, "unrelated old")
	if _, ok := cache.ContentForURL(urls.missing); ok {
		t.Fatal("not_found candidate returned content")
	}
	if !cache.IsFreshURL(urls.missing, now, policy.StaleAfter) {
		t.Fatal("not_found candidate was not cached")
	}
	if client.callCount() != stats.Fetched {
		t.Fatalf("HTTP calls = %d, want %d", client.callCount(), stats.Fetched)
	}
	if client.closedBodies.Load() != 7 {
		t.Fatalf("closed bodies = %d, want 7", client.closedBodies.Load())
	}
	if client.maxActive.Load() > 3 {
		t.Fatalf("max active = %d, want <= 3", client.maxActive.Load())
	}
	for _, request := range client.requestsSnapshot() {
		if request.Method != http.MethodGet {
			t.Fatalf("method = %q, want GET", request.Method)
		}
		if request.Header.Get("Range") != "bytes=0-127" {
			t.Fatalf("Range = %q", request.Header.Get("Range"))
		}
		if request.Header.Get("Accept-Encoding") != "identity" {
			t.Fatalf("Accept-Encoding = %q", request.Header.Get("Accept-Encoding"))
		}
	}
	assertSortedEnrichmentEntries(t, cache.Entries)

	output := marshalEnrichmentCache(t, cache)
	for _, private := range []string{
		urls.extracted,
		"assets.example.test",
		"private status body",
		"private transport",
	} {
		if bytes.Contains(output, []byte(private)) {
			t.Fatalf("cache leaked private input %q", private)
		}
	}
}

func TestEnrichCatalogLinksRefreshesFreshCandidates(t *testing.T) {
	now := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	policy := mustLinkEnrichmentPolicy(t, 10)
	rawURL := "https://assets.example.test/fresh"
	previous := NewLinkEnrichmentCache(now.Add(-time.Hour))
	putExtractedURL(t, previous, rawURL, LinkContent{Name: "old"}, now.Add(-time.Hour))
	catalog := Catalog{Entries: []CatalogEntry{{Links: []CatalogLink{{URL: rawURL}}}}}
	client := &fakeEnrichmentHTTPClient{responses: map[string]fakeEnrichmentResponse{
		rawURL: {status: http.StatusOK, body: enrichmentBody("new")},
	}}

	cache, stats, err := EnrichCatalogLinks(
		context.Background(), catalog, policy, previous, client, now, true, 1,
	)
	if err != nil {
		t.Fatalf("EnrichCatalogLinks() error = %v", err)
	}
	if stats.Candidates != 1 || stats.SkippedFresh != 0 || stats.Fetched != 1 || stats.Extracted != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	assertEnrichmentName(t, cache, rawURL, "new")
}

func TestEnrichCatalogLinksDowngradesInvalidExtractedContentAndKeepsCache(t *testing.T) {
	now := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	policy := mustLinkEnrichmentPolicy(t, 10)
	urls := struct {
		valid, locator, angle, control, empty string
	}{
		valid:   "https://assets.example.test/valid",
		locator: "https://assets.example.test/locator",
		angle:   "https://assets.example.test/angle",
		control: "https://assets.example.test/control",
		empty:   "https://assets.example.test/empty",
	}
	previous := NewLinkEnrichmentCache(now.Add(-48 * time.Hour))
	putExtractedURL(t, previous, urls.locator, LinkContent{Name: "locator old"}, now.Add(-48*time.Hour))
	putExtractedURL(t, previous, urls.angle, LinkContent{Name: "angle old"}, now.Add(-48*time.Hour))
	putExtractedURL(t, previous, urls.control, LinkContent{Name: "control old"}, now.Add(-48*time.Hour))
	putExtractedURL(t, previous, urls.empty, LinkContent{Name: "empty old"}, now.Add(-48*time.Hour))
	previousJSON := marshalEnrichmentCache(t, previous)

	client := &fakeEnrichmentHTTPClient{responses: map[string]fakeEnrichmentResponse{
		urls.valid:   {status: http.StatusOK, body: enrichmentBody("valid new")},
		urls.locator: {status: http.StatusOK, body: `<script>{"embeddedPayload":{"name":"https://locator.example.test/value"}}</script>`},
		urls.angle:   {status: http.StatusOK, body: `<script>{"embeddedPayload":{"name":"<metadata>"}}</script>`},
		urls.control: {status: http.StatusOK, body: `<script>{"embeddedPayload":{"name":"bad\u0001value"}}</script>`},
		urls.empty:   {status: http.StatusOK, body: `<script>{"embeddedPayload":{}}</script>`},
	}}

	cache, stats, err := EnrichCatalogLinks(
		context.Background(),
		Catalog{Entries: []CatalogEntry{{Links: []CatalogLink{
			{URL: urls.valid},
			{URL: urls.locator},
			{URL: urls.angle},
			{URL: urls.control},
			{URL: urls.empty},
		}}}},
		policy,
		previous,
		client,
		now,
		false,
		2,
	)
	if err != nil {
		t.Fatalf("EnrichCatalogLinks() error = %v", err)
	}
	if stats != (LinkEnrichmentStats{
		Candidates: 5,
		Fetched:    5,
		Extracted:  1,
		Failed:     4,
	}) {
		t.Fatalf("stats = %+v", stats)
	}
	if got := marshalEnrichmentCache(t, previous); !bytes.Equal(got, previousJSON) {
		t.Fatal("previous cache was mutated")
	}
	assertEnrichmentName(t, cache, urls.valid, "valid new")
	assertEnrichmentName(t, cache, urls.locator, "locator old")
	assertEnrichmentName(t, cache, urls.angle, "angle old")
	assertEnrichmentName(t, cache, urls.control, "control old")
	assertEnrichmentName(t, cache, urls.empty, "empty old")
}

func TestEnrichCatalogLinksAcceptsCanonicalizableHTTPSCase(t *testing.T) {
	policy := mustLinkEnrichmentPolicy(t, 10)
	rawURL := "HTTPS://ASSETS.EXAMPLE.TEST/bundle#fragment"
	canonicalURL := "https://assets.example.test/bundle"
	client := &fakeEnrichmentHTTPClient{responses: map[string]fakeEnrichmentResponse{
		canonicalURL: {status: http.StatusOK, body: enrichmentBody("bundle")},
	}}

	cache, stats, err := EnrichCatalogLinks(
		context.Background(),
		Catalog{Entries: []CatalogEntry{{Links: []CatalogLink{{URL: rawURL}}}}},
		policy,
		nil,
		client,
		time.Now(),
		false,
		1,
	)
	if err != nil {
		t.Fatalf("EnrichCatalogLinks() error = %v", err)
	}
	if stats.Candidates != 1 || stats.Fetched != 1 || stats.Extracted != 1 {
		t.Fatalf("stats = %+v", stats)
	}
	assertEnrichmentName(t, cache, canonicalURL, "bundle")
}

func TestEnrichCatalogLinksCancellationReturnsNoCacheAndLeavesPreviousUntouched(t *testing.T) {
	now := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	policy := mustLinkEnrichmentPolicy(t, 10)
	rawURL := "https://assets.example.test/cancel"
	previous := NewLinkEnrichmentCache(now.Add(-48 * time.Hour))
	putExtractedURL(t, previous, rawURL, LinkContent{Name: "old"}, now.Add(-48*time.Hour))
	before := marshalEnrichmentCache(t, previous)
	ctx, cancel := context.WithCancel(context.Background())
	client := &fakeEnrichmentHTTPClient{
		responses: map[string]fakeEnrichmentResponse{
			rawURL: {status: http.StatusOK, body: enrichmentBody("new")},
		},
		onDo: cancel,
	}

	cache, _, err := EnrichCatalogLinks(
		ctx,
		Catalog{Entries: []CatalogEntry{{Links: []CatalogLink{{URL: rawURL}}}}},
		policy,
		previous,
		client,
		now,
		false,
		1,
	)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if cache != nil {
		t.Fatal("canceled run returned cache")
	}
	if after := marshalEnrichmentCache(t, previous); !bytes.Equal(after, before) {
		t.Fatal("canceled run mutated previous cache")
	}
}

func TestEnrichCatalogLinksBoundsConcurrency(t *testing.T) {
	policy := mustLinkEnrichmentPolicy(t, 10)
	catalog := Catalog{Entries: []CatalogEntry{{Links: []CatalogLink{
		{URL: "https://assets.example.test/1"},
		{URL: "https://assets.example.test/2"},
		{URL: "https://assets.example.test/3"},
	}}}}
	started := make(chan struct{}, 3)
	release := make(chan struct{})
	client := &fakeEnrichmentHTTPClient{
		responses: map[string]fakeEnrichmentResponse{},
		started:   started,
		release:   release,
	}
	type result struct {
		stats LinkEnrichmentStats
		err   error
	}
	done := make(chan result, 1)
	go func() {
		_, stats, err := EnrichCatalogLinks(
			context.Background(), catalog, policy, nil, client, time.Now(), false, 2,
		)
		done <- result{stats: stats, err: err}
	}()

	<-started
	<-started
	select {
	case <-started:
		t.Fatal("third request started before a worker was released")
	default:
	}
	close(release)
	outcome := <-done
	if outcome.err != nil {
		t.Fatalf("EnrichCatalogLinks() error = %v", outcome.err)
	}
	if outcome.stats.Fetched != 3 {
		t.Fatalf("stats = %+v", outcome.stats)
	}
	if client.maxActive.Load() != 2 {
		t.Fatalf("max active = %d, want 2", client.maxActive.Load())
	}
}

func TestEnrichCatalogLinksValidatesInputs(t *testing.T) {
	policy := mustLinkEnrichmentPolicy(t, 10)
	catalog := Catalog{}
	client := &fakeEnrichmentHTTPClient{}
	if _, _, err := EnrichCatalogLinks(context.Background(), catalog, nil, nil, client, time.Now(), false, 1); err == nil {
		t.Fatal("nil policy error = nil")
	}
	if _, _, err := EnrichCatalogLinks(context.Background(), catalog, policy, nil, nil, time.Now(), false, 1); err == nil {
		t.Fatal("nil client error = nil")
	}
	if _, _, err := EnrichCatalogLinks(context.Background(), catalog, policy, nil, client, time.Now(), false, 0); err == nil {
		t.Fatal("zero concurrency error = nil")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := EnrichCatalogLinks(ctx, catalog, policy, nil, client, time.Now(), false, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled error = %v", err)
	}
}

type fakeEnrichmentResponse struct {
	status int
	body   string
}

type fakeEnrichmentHTTPClient struct {
	mu           sync.Mutex
	responses    map[string]fakeEnrichmentResponse
	errs         map[string]error
	requests     []*http.Request
	onDo         func()
	started      chan<- struct{}
	release      <-chan struct{}
	active       atomic.Int32
	maxActive    atomic.Int32
	closedBodies atomic.Int32
}

func (f *fakeEnrichmentHTTPClient) Do(request *http.Request) (*http.Response, error) {
	active := f.active.Add(1)
	defer f.active.Add(-1)
	for {
		maxActive := f.maxActive.Load()
		if active <= maxActive || f.maxActive.CompareAndSwap(maxActive, active) {
			break
		}
	}

	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	f.mu.Lock()
	f.requests = append(f.requests, clone)
	response, ok := f.responses[request.URL.String()]
	err := f.errs[request.URL.String()]
	f.mu.Unlock()
	if f.onDo != nil {
		f.onDo()
	}
	if f.started != nil {
		f.started <- struct{}{}
	}
	if f.release != nil {
		<-f.release
	}
	if err != nil {
		if !ok {
			return nil, err
		}
		return &http.Response{
			StatusCode: response.status,
			Body: closeCountingEnrichmentBody{
				Reader: bytes.NewReader([]byte(response.body)),
				closed: &f.closedBodies,
			},
		}, err
	}
	if !ok {
		response = fakeEnrichmentResponse{
			status: http.StatusOK,
			body:   "<html>no metadata</html>",
		}
	}
	return &http.Response{
		StatusCode: response.status,
		Body: closeCountingEnrichmentBody{
			Reader: bytes.NewReader([]byte(response.body)),
			closed: &f.closedBodies,
		},
	}, nil
}

func (f *fakeEnrichmentHTTPClient) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fakeEnrichmentHTTPClient) requestsSnapshot() []*http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*http.Request(nil), f.requests...)
}

type closeCountingEnrichmentBody struct {
	*bytes.Reader
	closed *atomic.Int32
}

func (b closeCountingEnrichmentBody) Close() error {
	b.closed.Add(1)
	return nil
}

func enrichmentBody(name string) string {
	return `<script>{"embeddedPayload":{"name":"` + name + `"}}</script>`
}

func putExtractedURL(t *testing.T, cache *LinkEnrichmentCache, rawURL string, content LinkContent, checkedAt time.Time) {
	t.Helper()
	if err := cache.PutExtracted(mustLinkHash(t, rawURL), content, checkedAt); err != nil {
		t.Fatalf("PutExtracted() error = %v", err)
	}
}

func putNotFoundURL(t *testing.T, cache *LinkEnrichmentCache, rawURL string, checkedAt time.Time) {
	t.Helper()
	if err := cache.PutNotFound(mustLinkHash(t, rawURL), checkedAt); err != nil {
		t.Fatalf("PutNotFound() error = %v", err)
	}
}

func marshalEnrichmentCache(t *testing.T, cache *LinkEnrichmentCache) []byte {
	t.Helper()
	var output bytes.Buffer
	if err := WriteLinkEnrichmentCache(&output, cache); err != nil {
		t.Fatalf("WriteLinkEnrichmentCache() error = %v", err)
	}
	return output.Bytes()
}

func assertEnrichmentName(t *testing.T, cache *LinkEnrichmentCache, rawURL, want string) {
	t.Helper()
	content, ok := cache.ContentForURL(rawURL)
	if !ok {
		t.Fatalf("ContentForURL(%q) missing", rawURL)
	}
	if content.Name != want {
		t.Fatalf("ContentForURL(%q).Name = %q, want %q", rawURL, content.Name, want)
	}
}

func assertSortedEnrichmentEntries(t *testing.T, entries []LinkEnrichmentEntry) {
	t.Helper()
	for index := 1; index < len(entries); index++ {
		if entries[index-1].SHA256 > entries[index].SHA256 {
			t.Fatalf("entries not sorted at %d", index)
		}
	}
}

var _ io.ReadCloser = closeCountingEnrichmentBody{}
