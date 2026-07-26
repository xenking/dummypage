package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xenking/dummypage/internal/courses"
)

func TestRunAuditsWithInjectedClientAndAtomicallyMergesPrivateOutputs(t *testing.T) {
	dir := t.TempDir()
	catalogPath := filepath.Join(dir, "catalog.json.gz")
	policyPath := filepath.Join(dir, "policy.json")
	reportPath := filepath.Join(dir, "report.json")
	tombstonesPath := filepath.Join(dir, "tombstones.json")
	expiredURL := "https://files.example.test/expired-secret-path"
	mismatchURL := "https://files.example.test/mismatch-secret-path"
	liveURL := "https://files.example.test/live-secret-path"
	expiredHash := testLinkHash(expiredURL)
	mismatchHash := testLinkHash(mismatchURL)
	existingHash := testLinkHash("https://files.example.test/existing")

	writeCatalog(t, catalogPath, []string{mismatchURL, liveURL, expiredURL}, "private course title")
	writeFile(t, policyPath, `{
		"schema_version":"link-audit-policy/v1",
		"timeout":"2s",
		"max_body_bytes":1024,
		"concurrency":2,
		"confirmations_required":2,
		"rules":[{
			"host_suffixes":["files.example.test"],
			"dead_statuses":[],
			"dead_body_patterns":[],
			"mismatch_body_patterns":["wrong private body"],
			"required_body_patterns":[],
			"allowed_redirect_host_suffixes":[]
		}]
	}`)
	writeFile(t, reportPath, `{
		"schema_version":"link-audit-report/v1",
		"generated_at":"2026-07-26T00:00:00Z",
		"results":[
			{"sha256":"`+expiredHash+`","state":"expired","reason":"global_dead_status","http_status":404,"checked_at":"2026-07-26T00:00:00Z","confirmations":1},
			{"sha256":"`+mismatchHash+`","state":"content_mismatch","reason":"mismatch_body","http_status":200,"checked_at":"2026-07-26T00:00:00Z","confirmations":1}
		]
	}`)
	writeFile(t, tombstonesPath, `{
		"schema_version":"link-tombstones/v1",
		"canonicalization_version":1,
		"links":[{"sha256":"`+existingHash+`","reason":"manual","confirmed_at":"2026-07-25T00:00:00Z"}]
	}`)

	client := &fakeAuditClient{responses: map[string]fakeAuditResponse{
		"HEAD " + expiredURL:  {status: http.StatusNotFound},
		"HEAD " + mismatchURL: {status: http.StatusOK},
		"GET " + mismatchURL:  {status: http.StatusOK, body: "wrong private body"},
		"HEAD " + liveURL:     {status: http.StatusOK},
		"GET " + liveURL:      {status: http.StatusOK, body: "valid"},
	}}
	var stdout bytes.Buffer
	factoryCalls := 0
	err := run(context.Background(), []string{
		"--catalog", catalogPath,
		"--policy", policyPath,
		"--report", reportPath,
		"--tombstones", tombstonesPath,
	}, dependencies{
		now: func() time.Time {
			return time.Date(2026, 7, 27, 1, 2, 3, 0, time.FixedZone("UTC+10", 10*60*60))
		},
		newClient: func(*courses.LinkAuditPolicy) (courses.LinkAuditHTTPClient, error) {
			factoryCalls++
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
	if client.callsCount() != 5 {
		t.Fatalf("HTTP calls = %d, want 5", client.callsCount())
	}

	reportPayload := readFile(t, reportPath)
	tombstonesPayload := readFile(t, tombstonesPath)
	output := stdout.String()
	for _, secret := range []string{
		expiredURL,
		mismatchURL,
		liveURL,
		"files.example.test",
		"private course title",
		"wrong private body",
	} {
		if bytes.Contains(reportPayload, []byte(secret)) ||
			bytes.Contains(tombstonesPayload, []byte(secret)) ||
			strings.Contains(output, secret) {
			t.Fatalf("private value leaked: %q", secret)
		}
	}
	if !strings.Contains(output, "audited=3") ||
		!strings.Contains(output, "tombstones_added=2") ||
		!strings.Contains(output, "tombstones_total=3") {
		t.Fatalf("stdout = %q, want aggregate counts", output)
	}
	assertMode0600(t, reportPath)
	assertMode0600(t, tombstonesPath)

	report, err := courses.LoadLinkAuditReport(bytes.NewReader(reportPayload))
	if err != nil {
		t.Fatalf("LoadLinkAuditReport() error = %v", err)
	}
	if len(report.Results) != 3 {
		t.Fatalf("report results = %d, want 3", len(report.Results))
	}
	assertSortedReport(t, report.Results)

	tombstones, err := courses.LoadLinkTombstones(bytes.NewReader(tombstonesPayload))
	if err != nil {
		t.Fatalf("LoadLinkTombstones() error = %v", err)
	}
	if tombstones.Len() != 3 ||
		!tombstones.ContainsURL(expiredURL) ||
		!tombstones.ContainsURL(mismatchURL) ||
		!tombstones.ContainsURL("https://files.example.test/existing") {
		t.Fatal("merged tombstones did not preserve and add expected hashes")
	}
}

func TestRunAllowsMissingReportAndTombstones(t *testing.T) {
	dir := t.TempDir()
	catalogPath := filepath.Join(dir, "catalog.json.gz")
	policyPath := filepath.Join(dir, "policy.json")
	reportPath := filepath.Join(dir, "new", "report.json")
	tombstonesPath := filepath.Join(dir, "new", "tombstones.json")
	writeCatalog(t, catalogPath, nil, "private title")
	writePolicy(t, policyPath)

	err := run(context.Background(), []string{
		"--catalog", catalogPath,
		"--policy", policyPath,
		"--report", reportPath,
		"--tombstones", tombstonesPath,
	}, dependencies{
		now: func() time.Time { return time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC) },
		newClient: func(*courses.LinkAuditPolicy) (courses.LinkAuditHTTPClient, error) {
			return &fakeAuditClient{}, nil
		},
		stdout: io.Discard,
	})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if _, err := courses.LoadLinkAuditReport(bytes.NewReader(readFile(t, reportPath))); err != nil {
		t.Fatalf("new report invalid: %v", err)
	}
	tombstones, err := courses.LoadLinkTombstones(bytes.NewReader(readFile(t, tombstonesPath)))
	if err != nil {
		t.Fatalf("new tombstones invalid: %v", err)
	}
	if tombstones.Len() != 0 {
		t.Fatalf("new tombstones len = %d, want 0", tombstones.Len())
	}
	assertMode0600(t, reportPath)
	assertMode0600(t, tombstonesPath)
}

func TestRunMalformedExistingStateAbortsBeforeNetworkOrWritesWithoutLeakingValues(t *testing.T) {
	dir := t.TempDir()
	catalogPath := filepath.Join(dir, "catalog.json.gz")
	policyPath := filepath.Join(dir, "policy.json")
	reportPath := filepath.Join(dir, "report.json")
	tombstonesPath := filepath.Join(dir, "tombstones.json")
	writeCatalog(t, catalogPath, []string{"https://files.example.test/private"}, "private title")
	writePolicy(t, policyPath)
	validReport := `{"schema_version":"link-audit-report/v1","generated_at":"2026-07-26T00:00:00Z","results":[]}`
	writeFile(t, reportPath, validReport)
	const secret = "https://secret.example.test/body"
	malformed := `{"schema_version":"link-tombstones/v1","canonicalization_version":1,"links":[{"sha256":"bad","reason":"` + secret + `","confirmed_at":"bad"}]}`
	writeFile(t, tombstonesPath, malformed)
	factoryCalls := 0

	err := run(context.Background(), []string{
		"--catalog", catalogPath,
		"--policy", policyPath,
		"--report", reportPath,
		"--tombstones", tombstonesPath,
	}, dependencies{
		now: time.Now,
		newClient: func(*courses.LinkAuditPolicy) (courses.LinkAuditHTTPClient, error) {
			factoryCalls++
			return &fakeAuditClient{}, nil
		},
		stdout: io.Discard,
	})
	if err == nil {
		t.Fatal("run() error = nil, want malformed tombstones error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked private value: %v", err)
	}
	if factoryCalls != 0 {
		t.Fatalf("client factory calls = %d, want 0", factoryCalls)
	}
	if got := string(readFile(t, reportPath)); got != validReport {
		t.Fatalf("report changed: %q", got)
	}
	if got := string(readFile(t, tombstonesPath)); got != malformed {
		t.Fatalf("tombstones changed: %q", got)
	}
}

func TestRunHonorsCanceledContextWithoutWritingOutputs(t *testing.T) {
	dir := t.TempDir()
	catalogPath := filepath.Join(dir, "catalog.json.gz")
	policyPath := filepath.Join(dir, "policy.json")
	reportPath := filepath.Join(dir, "report.json")
	tombstonesPath := filepath.Join(dir, "tombstones.json")
	writeCatalog(t, catalogPath, []string{"https://files.example.test/private"}, "private title")
	writePolicy(t, policyPath)
	writeFile(t, reportPath, `{"schema_version":"link-audit-report/v1","generated_at":"2026-07-26T00:00:00Z","results":[]}`)
	writeFile(t, tombstonesPath, `{"schema_version":"link-tombstones/v1","canonicalization_version":1,"links":[]}`)
	beforeReport := readFile(t, reportPath)
	beforeTombstones := readFile(t, tombstonesPath)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := run(ctx, []string{
		"--catalog", catalogPath,
		"--policy", policyPath,
		"--report", reportPath,
		"--tombstones", tombstonesPath,
	}, dependencies{
		now: time.Now,
		newClient: func(*courses.LinkAuditPolicy) (courses.LinkAuditHTTPClient, error) {
			return &fakeAuditClient{}, nil
		},
		stdout: io.Discard,
	})
	if err == nil {
		t.Fatal("run() error = nil, want cancellation")
	}
	if !bytes.Equal(readFile(t, reportPath), beforeReport) ||
		!bytes.Equal(readFile(t, tombstonesPath), beforeTombstones) {
		t.Fatal("outputs changed after cancellation")
	}
}

func TestRunDoesNotPublishWhenContextIsCanceledByLastAuditRequest(t *testing.T) {
	dir := t.TempDir()
	catalogPath := filepath.Join(dir, "catalog.json.gz")
	policyPath := filepath.Join(dir, "policy.json")
	reportPath := filepath.Join(dir, "report.json")
	tombstonesPath := filepath.Join(dir, "tombstones.json")
	rawURL := "https://files.example.test/private"
	writeCatalog(t, catalogPath, []string{rawURL}, "private title")
	writePolicy(t, policyPath)
	writeFile(t, reportPath, `{"schema_version":"link-audit-report/v1","generated_at":"2026-07-26T00:00:00Z","results":[]}`)
	writeFile(t, tombstonesPath, `{"schema_version":"link-tombstones/v1","canonicalization_version":1,"links":[]}`)
	beforeReport := readFile(t, reportPath)
	beforeTombstones := readFile(t, tombstonesPath)
	ctx, cancel := context.WithCancel(context.Background())
	client := &fakeAuditClient{
		responses: map[string]fakeAuditResponse{
			"HEAD " + rawURL: {status: http.StatusNotFound},
		},
		onDo: cancel,
	}

	err := run(ctx, []string{
		"--catalog", catalogPath,
		"--policy", policyPath,
		"--report", reportPath,
		"--tombstones", tombstonesPath,
	}, dependencies{
		now: time.Now,
		newClient: func(*courses.LinkAuditPolicy) (courses.LinkAuditHTTPClient, error) {
			return client, nil
		},
		stdout: io.Discard,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("run() error = %v, want context canceled", err)
	}
	if !bytes.Equal(readFile(t, reportPath), beforeReport) ||
		!bytes.Equal(readFile(t, tombstonesPath), beforeTombstones) {
		t.Fatal("outputs changed after cancellation during final audit request")
	}
}

func TestWriteAtomicPairRejectsInvalidSecondTargetBeforePublishingFirst(t *testing.T) {
	dir := t.TempDir()
	firstPath := filepath.Join(dir, "report.json")
	secondPath := filepath.Join(dir, "tombstones")
	writeFile(t, firstPath, "old report")
	if err := os.Mkdir(secondPath, 0o700); err != nil {
		t.Fatalf("mkdir second target: %v", err)
	}

	err := writeAtomicPair(context.Background(), firstPath, []byte("new report"), secondPath, []byte("new tombstones"))
	if err == nil {
		t.Fatal("writeAtomicPair() error = nil")
	}
	if got := string(readFile(t, firstPath)); got != "old report" {
		t.Fatalf("first output changed: %q", got)
	}
	info, statErr := os.Stat(secondPath)
	if statErr != nil || !info.IsDir() {
		t.Fatalf("second target changed: info=%v err=%v", info, statErr)
	}
}

func TestWriteAtomicPairRollsBackFirstOutputWhenSecondRenameFails(t *testing.T) {
	dir := t.TempDir()
	firstPath := filepath.Join(dir, "report.json")
	secondPath := filepath.Join(dir, "tombstones.json")
	writeFile(t, firstPath, "old report")
	writeFile(t, secondPath, "old tombstones")
	renameCalls := 0
	rename := func(oldPath, newPath string) error {
		renameCalls++
		if renameCalls == 2 {
			return errors.New("injected second rename failure")
		}
		return os.Rename(oldPath, newPath)
	}

	err := writeAtomicPairWithRename(context.Background(), firstPath, []byte("new report"), secondPath, []byte("new tombstones"), rename)
	if err == nil {
		t.Fatal("writeAtomicPairWithRename() error = nil")
	}
	if got := string(readFile(t, firstPath)); got != "old report" {
		t.Fatalf("first output changed: %q", got)
	}
	if got := string(readFile(t, secondPath)); got != "old tombstones" {
		t.Fatalf("second output changed: %q", got)
	}
}

func TestWriteAtomicPairCancellationAfterFirstRenameRollsBack(t *testing.T) {
	dir := t.TempDir()
	firstPath := filepath.Join(dir, "report.json")
	secondPath := filepath.Join(dir, "tombstones.json")
	writeFile(t, firstPath, "old report")
	writeFile(t, secondPath, "old tombstones")
	ctx, cancel := context.WithCancel(context.Background())
	renameCalls := 0
	rename := func(oldPath, newPath string) error {
		renameCalls++
		if err := os.Rename(oldPath, newPath); err != nil {
			return err
		}
		if renameCalls == 1 {
			cancel()
		}
		return nil
	}

	err := writeAtomicPairWithRename(ctx, firstPath, []byte("new report"), secondPath, []byte("new tombstones"), rename)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("writeAtomicPairWithRename() error = %v, want context.Canceled", err)
	}
	if got := string(readFile(t, firstPath)); got != "old report" {
		t.Fatalf("first output changed: %q", got)
	}
	if got := string(readFile(t, secondPath)); got != "old tombstones" {
		t.Fatalf("second output changed: %q", got)
	}
}

func TestParseArgsRequiresAllPaths(t *testing.T) {
	if _, err := parseArgs([]string{"--catalog", "catalog"}); err == nil {
		t.Fatal("parseArgs() error = nil, want missing flags error")
	}
	config, err := parseArgs([]string{
		"--catalog", "catalog",
		"--policy", "policy",
		"--report", "report",
		"--tombstones", "tombstones",
	})
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if config.CatalogPath != "catalog" ||
		config.PolicyPath != "policy" ||
		config.ReportPath != "report" ||
		config.TombstonesPath != "tombstones" {
		t.Fatalf("config = %+v", config)
	}
}

func TestParseArgsRejectsEquivalentOutputPaths(t *testing.T) {
	dir := t.TempDir()
	reportPath := filepath.Join(dir, "report.json")
	writeFile(t, reportPath, "{}")
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("working directory: %v", err)
	}
	relativeReportPath, err := filepath.Rel(workingDir, reportPath)
	if err != nil {
		t.Fatalf("relative path: %v", err)
	}
	if _, err := parseArgs([]string{
		"--catalog", filepath.Join(dir, "catalog"),
		"--policy", filepath.Join(dir, "policy"),
		"--report", relativeReportPath,
		"--tombstones", reportPath,
	}); err == nil {
		t.Fatal("parseArgs() accepted relative and absolute aliases")
	}

	symlinkPath := filepath.Join(dir, "report-alias.json")
	if err := os.Symlink(reportPath, symlinkPath); err != nil {
		t.Fatalf("create symlink: %v", err)
	}
	if _, err := parseArgs([]string{
		"--catalog", filepath.Join(dir, "catalog"),
		"--policy", filepath.Join(dir, "policy"),
		"--report", reportPath,
		"--tombstones", symlinkPath,
	}); err == nil {
		t.Fatal("parseArgs() accepted symlink aliases")
	}
}

type fakeAuditResponse struct {
	status int
	body   string
}

type fakeAuditClient struct {
	mu        sync.Mutex
	responses map[string]fakeAuditResponse
	calls     []string
	onDo      func()
}

func (c *fakeAuditClient) Do(req *http.Request) (*http.Response, error) {
	key := req.Method + " " + req.URL.String()
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, key)
	response, ok := c.responses[key]
	if !ok {
		response = fakeAuditResponse{status: http.StatusOK}
	}
	if c.onDo != nil {
		c.onDo()
	}
	return &http.Response{
		StatusCode: response.status,
		Body:       io.NopCloser(strings.NewReader(response.body)),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

func (c *fakeAuditClient) callsCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.calls)
}

func writeCatalog(t *testing.T, path string, links []string, title string) {
	t.Helper()
	var payload bytes.Buffer
	writer := gzip.NewWriter(&payload)
	catalogLinks := make([]map[string]any, 0, len(links))
	for _, rawURL := range links {
		catalogLinks = append(catalogLinks, map[string]any{"url": rawURL})
	}
	if err := json.NewEncoder(writer).Encode(map[string]any{
		"schema_version": "courses-catalog/v2",
		"entries": []any{map[string]any{
			"title": title,
			"links": catalogLinks,
		}},
	}); err != nil {
		t.Fatalf("encode catalog: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close catalog: %v", err)
	}
	if err := os.WriteFile(path, payload.Bytes(), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
}

func writePolicy(t *testing.T, path string) {
	t.Helper()
	writeFile(t, path, `{
		"schema_version":"link-audit-policy/v1",
		"timeout":"2s",
		"max_body_bytes":1024,
		"concurrency":1,
		"confirmations_required":2,
		"rules":[{
			"host_suffixes":["files.example.test"],
			"dead_statuses":[],
			"dead_body_patterns":[],
			"mismatch_body_patterns":[],
			"required_body_patterns":[],
			"allowed_redirect_host_suffixes":[]
		}]
	}`)
}

func writeFile(t *testing.T, path, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	value, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	return value
}

func testLinkHash(rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	return hex.EncodeToString(sum[:])
}

func assertMode0600(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 600", got)
	}
}

func assertSortedReport(t *testing.T, results []courses.LinkAuditResult) {
	t.Helper()
	for index := 1; index < len(results); index++ {
		if results[index-1].SHA256 >= results[index].SHA256 {
			t.Fatalf("results not sorted: %+v", results)
		}
	}
}
