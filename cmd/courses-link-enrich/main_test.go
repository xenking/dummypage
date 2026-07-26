package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/xenking/dummypage/internal/courses"
)

func TestRunEnrichesCatalogAndPublishesPrivateCache(t *testing.T) {
	dir := t.TempDir()
	catalogPath := filepath.Join(dir, "catalog.json.gz")
	auditPolicyPath := filepath.Join(dir, "audit-policy.json")
	enrichmentPolicyPath := filepath.Join(dir, "enrichment-policy.json")
	cachePath := filepath.Join(dir, "private", "cache.json")
	privateURL := "https://assets.example.test/private-bundle"
	writeEnrichmentCatalog(t, catalogPath, []string{privateURL}, "private catalog title")
	writeAuditPolicy(t, auditPolicyPath)
	writeEnrichmentPolicy(t, enrichmentPolicyPath)

	client := &fakeCLIEnrichmentClient{
		responses: map[string]fakeCLIEnrichmentResponse{
			privateURL: {
				status: http.StatusOK,
				body:   `<script>{"embeddedPayload":{"name":"Bundle","kind":"folder"}}</script>`,
			},
		},
	}
	var stdout bytes.Buffer
	factoryCalls := 0
	err := run(context.Background(), []string{
		"--catalog", catalogPath,
		"--audit-policy", auditPolicyPath,
		"--enrichment-policy", enrichmentPolicyPath,
		"--cache", cachePath,
	}, dependencies{
		now: func() time.Time {
			return time.Date(2026, 7, 27, 12, 0, 0, 0, time.FixedZone("UTC+10", 10*60*60))
		},
		newClient: func(policy *courses.LinkAuditPolicy) (courses.LinkAuditHTTPClient, error) {
			factoryCalls++
			if policy.Concurrency != 2 {
				t.Fatalf("audit policy concurrency = %d", policy.Concurrency)
			}
			return client, nil
		},
		stdout: &stdout,
	})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if factoryCalls != 1 {
		t.Fatalf("client factory calls = %d, want 1", factoryCalls)
	}
	if stdout.String() != "candidates=1 skipped_fresh=0 fetched=1 extracted=1 not_found=0 failed=0\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	payload := readEnrichmentFile(t, cachePath)
	cache, err := courses.LoadLinkEnrichmentCache(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("LoadLinkEnrichmentCache() error = %v", err)
	}
	content, ok := cache.ContentForURL(privateURL)
	if !ok || content.Name != "Bundle" {
		t.Fatalf("cached content = %+v, %v", content, ok)
	}
	if cache.GeneratedAt != "2026-07-27T02:00:00Z" {
		t.Fatalf("GeneratedAt = %q", cache.GeneratedAt)
	}
	assertEnrichmentMode0600(t, cachePath)
	request := client.onlyRequest(t)
	if request.Method != http.MethodGet ||
		request.Header.Get("Range") != "bytes=0-1023" ||
		request.Header.Get("Accept-Encoding") != "identity" {
		t.Fatalf("request = %s headers=%v", request.Method, request.Header)
	}
	for _, private := range []string{
		privateURL,
		"assets.example.test",
		"private catalog title",
		`<script>`,
	} {
		if strings.Contains(stdout.String(), private) {
			t.Fatalf("stdout leaked %q", private)
		}
	}
}

func TestRunLoadsEveryInputBeforeConstructingClientOrWriting(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, paths enrichmentPaths)
	}{
		{
			name: "catalog",
			mutate: func(t *testing.T, paths enrichmentPaths) {
				t.Helper()
				writeEnrichmentFile(t, paths.catalog, "not gzip")
			},
		},
		{
			name: "audit policy",
			mutate: func(t *testing.T, paths enrichmentPaths) {
				t.Helper()
				writeEnrichmentFile(t, paths.auditPolicy, `{"secret":"https://private.example/body"}`)
			},
		},
		{
			name: "enrichment policy",
			mutate: func(t *testing.T, paths enrichmentPaths) {
				t.Helper()
				writeEnrichmentFile(t, paths.enrichmentPolicy, `{"secret":"https://private.example/body"}`)
			},
		},
		{
			name: "previous cache",
			mutate: func(t *testing.T, paths enrichmentPaths) {
				t.Helper()
				writeEnrichmentFile(t, paths.cache, `{"secret":"https://private.example/body"}`)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			paths := writeValidEnrichmentInputs(t, true)
			test.mutate(t, paths)
			mutated := readEnrichmentFile(t, paths.cache)
			factoryCalls := 0
			err := run(context.Background(), paths.args(false), dependencies{
				now: time.Now,
				newClient: func(*courses.LinkAuditPolicy) (courses.LinkAuditHTTPClient, error) {
					factoryCalls++
					return &fakeCLIEnrichmentClient{}, nil
				},
				stdout: io.Discard,
			})
			if err == nil {
				t.Fatal("run() error = nil")
			}
			if strings.Contains(err.Error(), "private.example") {
				t.Fatalf("error leaked private input: %v", err)
			}
			if factoryCalls != 0 {
				t.Fatalf("client factory calls = %d, want 0", factoryCalls)
			}
			if after := readEnrichmentFile(t, paths.cache); !bytes.Equal(after, mutated) {
				t.Fatal("failed preflight changed cache")
			}
		})
	}
}

func TestRunCancellationDoesNotPublish(t *testing.T) {
	t.Run("during fetch", func(t *testing.T) {
		paths := writeValidEnrichmentInputs(t, true)
		before := readEnrichmentFile(t, paths.cache)
		ctx, cancel := context.WithCancel(context.Background())
		client := &fakeCLIEnrichmentClient{
			responses: map[string]fakeCLIEnrichmentResponse{
				paths.rawURL: {
					status: http.StatusOK,
					body:   `<script>{"embeddedPayload":{"name":"new"}}</script>`,
				},
			},
			onDo: cancel,
		}
		var stdout bytes.Buffer
		err := run(ctx, paths.args(true), dependencies{
			now: time.Now,
			newClient: func(*courses.LinkAuditPolicy) (courses.LinkAuditHTTPClient, error) {
				return client, nil
			},
			stdout: &stdout,
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("run() error = %v, want context.Canceled", err)
		}
		if after := readEnrichmentFile(t, paths.cache); !bytes.Equal(after, before) {
			t.Fatal("canceled fetch changed cache")
		}
		if stdout.Len() != 0 {
			t.Fatalf("stdout = %q, want empty", stdout.String())
		}
	})

	t.Run("immediately before publish", func(t *testing.T) {
		paths := writeValidEnrichmentInputs(t, true)
		before := readEnrichmentFile(t, paths.cache)
		ctx, cancel := context.WithCancel(context.Background())
		client := &fakeCLIEnrichmentClient{responses: map[string]fakeCLIEnrichmentResponse{
			paths.rawURL: {
				status: http.StatusOK,
				body:   `<script>{"embeddedPayload":{"name":"new"}}</script>`,
			},
		}}
		err := run(ctx, paths.args(true), dependencies{
			now: time.Now,
			newClient: func(*courses.LinkAuditPolicy) (courses.LinkAuditHTTPClient, error) {
				return client, nil
			},
			stdout:        io.Discard,
			beforePublish: cancel,
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("run() error = %v, want context.Canceled", err)
		}
		if after := readEnrichmentFile(t, paths.cache); !bytes.Equal(after, before) {
			t.Fatal("pre-publish cancellation changed cache")
		}
	})
}

func TestParseArgsRejectsCacheInputAliases(t *testing.T) {
	dir := t.TempDir()
	catalogPath := filepath.Join(dir, "catalog")
	auditPolicyPath := filepath.Join(dir, "audit-policy")
	enrichmentPolicyPath := filepath.Join(dir, "enrichment-policy")
	cachePath := filepath.Join(dir, "cache")
	for _, path := range []string{catalogPath, auditPolicyPath, enrichmentPolicyPath, cachePath} {
		writeEnrichmentFile(t, path, "{}")
	}

	if _, err := parseArgs([]string{
		"--catalog", catalogPath,
		"--audit-policy", auditPolicyPath,
		"--enrichment-policy", enrichmentPolicyPath,
		"--cache", cachePath,
	}); err != nil {
		t.Fatalf("parseArgs() rejected distinct existing cache: %v", err)
	}
	if _, err := parseArgs([]string{
		"--catalog", catalogPath,
		"--audit-policy", auditPolicyPath,
		"--enrichment-policy", enrichmentPolicyPath,
		"--cache", catalogPath,
	}); err == nil {
		t.Fatal("parseArgs() accepted exact alias")
	}

	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd(): %v", err)
	}
	relativeCatalog, err := filepath.Rel(workingDir, catalogPath)
	if err != nil {
		t.Fatalf("Rel(): %v", err)
	}
	if _, err := parseArgs([]string{
		"--catalog", relativeCatalog,
		"--audit-policy", auditPolicyPath,
		"--enrichment-policy", enrichmentPolicyPath,
		"--cache", catalogPath,
	}); err == nil {
		t.Fatal("parseArgs() accepted relative alias")
	}

	symlinkPath := filepath.Join(dir, "catalog-symlink")
	if err := os.Symlink(catalogPath, symlinkPath); err != nil {
		t.Fatalf("Symlink(): %v", err)
	}
	if _, err := parseArgs([]string{
		"--catalog", catalogPath,
		"--audit-policy", auditPolicyPath,
		"--enrichment-policy", enrichmentPolicyPath,
		"--cache", symlinkPath,
	}); err == nil {
		t.Fatal("parseArgs() accepted symlink alias")
	}

	hardlinkPath := filepath.Join(dir, "catalog-hardlink")
	if err := os.Link(catalogPath, hardlinkPath); err != nil {
		t.Fatalf("Link(): %v", err)
	}
	if _, err := parseArgs([]string{
		"--catalog", catalogPath,
		"--audit-policy", auditPolicyPath,
		"--enrichment-policy", enrichmentPolicyPath,
		"--cache", hardlinkPath,
	}); err == nil {
		t.Fatal("parseArgs() accepted hardlink alias")
	}
}

func TestParseArgsRequiresExpectedFlagsAndAcceptsRefresh(t *testing.T) {
	cfg, err := parseArgs([]string{
		"--catalog", "catalog",
		"--audit-policy", "audit",
		"--enrichment-policy", "enrichment",
		"--cache", "cache",
		"--refresh",
	})
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if !cfg.Refresh {
		t.Fatal("Refresh = false")
	}
	if _, err := parseArgs([]string{"--catalog", "catalog"}); err == nil {
		t.Fatal("parseArgs() accepted missing required flags")
	}
	if _, err := parseArgs([]string{
		"--catalog", "catalog",
		"--audit-policy", "audit",
		"--enrichment-policy", "enrichment",
		"--cache", "cache",
		"extra",
	}); err == nil {
		t.Fatal("parseArgs() accepted positional argument")
	}
}

type enrichmentPaths struct {
	catalog          string
	auditPolicy      string
	enrichmentPolicy string
	cache            string
	rawURL           string
}

func writeValidEnrichmentInputs(t *testing.T, withCache bool) enrichmentPaths {
	t.Helper()
	dir := t.TempDir()
	paths := enrichmentPaths{
		catalog:          filepath.Join(dir, "catalog.json.gz"),
		auditPolicy:      filepath.Join(dir, "audit-policy.json"),
		enrichmentPolicy: filepath.Join(dir, "enrichment-policy.json"),
		cache:            filepath.Join(dir, "cache.json"),
		rawURL:           "https://assets.example.test/bundle",
	}
	writeEnrichmentCatalog(t, paths.catalog, []string{paths.rawURL}, "private title")
	writeAuditPolicy(t, paths.auditPolicy)
	writeEnrichmentPolicy(t, paths.enrichmentPolicy)
	if withCache {
		cache := courses.NewLinkEnrichmentCache(time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC))
		if err := cache.PutExtracted(
			testEnrichmentHash(paths.rawURL),
			courses.LinkContent{Name: "old"},
			time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC),
		); err != nil {
			t.Fatalf("PutExtracted() error = %v", err)
		}
		var output bytes.Buffer
		if err := courses.WriteLinkEnrichmentCache(&output, cache); err != nil {
			t.Fatalf("WriteLinkEnrichmentCache() error = %v", err)
		}
		writeEnrichmentFile(t, paths.cache, output.String())
	}
	return paths
}

func (p enrichmentPaths) args(refresh bool) []string {
	args := []string{
		"--catalog", p.catalog,
		"--audit-policy", p.auditPolicy,
		"--enrichment-policy", p.enrichmentPolicy,
		"--cache", p.cache,
	}
	if refresh {
		args = append(args, "--refresh")
	}
	return args
}

type fakeCLIEnrichmentResponse struct {
	status int
	body   string
}

type fakeCLIEnrichmentClient struct {
	mu        sync.Mutex
	responses map[string]fakeCLIEnrichmentResponse
	requests  []*http.Request
	onDo      func()
	closed    atomic.Int32
}

func (f *fakeCLIEnrichmentClient) Do(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	f.mu.Lock()
	f.requests = append(f.requests, clone)
	response, ok := f.responses[request.URL.String()]
	f.mu.Unlock()
	if f.onDo != nil {
		f.onDo()
	}
	if !ok {
		response = fakeCLIEnrichmentResponse{status: http.StatusOK, body: "<html>missing</html>"}
	}
	return &http.Response{
		StatusCode: response.status,
		Body: &countingCLIEnrichmentBody{
			Reader: strings.NewReader(response.body),
			closed: &f.closed,
		},
	}, nil
}

func (f *fakeCLIEnrichmentClient) onlyRequest(t *testing.T) *http.Request {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(f.requests))
	}
	return f.requests[0]
}

type countingCLIEnrichmentBody struct {
	*strings.Reader
	closed *atomic.Int32
}

func (b *countingCLIEnrichmentBody) Close() error {
	b.closed.Add(1)
	return nil
}

func writeEnrichmentCatalog(t *testing.T, path string, urls []string, title string) {
	t.Helper()
	var entries strings.Builder
	for index, rawURL := range urls {
		if index > 0 {
			entries.WriteByte(',')
		}
		entries.WriteString(`{"id":"entry-`)
		entries.WriteString(testEnrichmentHash(rawURL)[:8])
		entries.WriteString(`","title":"`)
		entries.WriteString(title)
		entries.WriteString(`","links":[{"url":"`)
		entries.WriteString(rawURL)
		entries.WriteString(`"}]}`)
	}
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := io.WriteString(
		writer,
		`{"schema_version":"courses-catalog/v2","entries":[`+entries.String()+`]}`,
	); err != nil {
		t.Fatalf("write gzip: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	if err := os.WriteFile(path, compressed.Bytes(), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
}

func writeAuditPolicy(t *testing.T, path string) {
	t.Helper()
	writeEnrichmentFile(t, path, `{
		"schema_version":"link-audit-policy/v1",
		"timeout":"2s",
		"max_body_bytes":1024,
		"concurrency":2,
		"confirmations_required":1,
		"rules":[{
			"host_suffixes":["assets.example.test"],
			"dead_statuses":[],
			"dead_body_patterns":[],
			"mismatch_body_patterns":[],
			"required_body_patterns":[],
			"allowed_redirect_host_suffixes":[]
		}]
	}`)
}

func writeEnrichmentPolicy(t *testing.T, path string) {
	t.Helper()
	writeEnrichmentFile(t, path, `{
		"schema_version":"link-enrichment-policy/v1",
		"max_body_bytes":1024,
		"max_items":10,
		"stale_after":"24h",
		"rules":[{
			"host_suffixes":["assets.example.test"],
			"json_object_markers":["\"embeddedPayload\":"],
			"fields":{"name":"name","kind":"kind"}
		}]
	}`)
}

func testEnrichmentHash(rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	return hex.EncodeToString(sum[:])
}

func readEnrichmentFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q): %v", path, err)
	}
	return data
}

func writeEnrichmentFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
}

func assertEnrichmentMode0600(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q): %v", path, err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
}
