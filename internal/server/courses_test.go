package server

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/phuslu/log"
	"golang.org/x/crypto/bcrypt"
)

func TestCoursesCatalogRequiresPassword(t *testing.T) {
	catalogPath := writeTestCoursesFile(t, `{"schema_version":"courses-catalog/v2"}`)
	password := "correct horse battery staple"
	app := testCoursesApp(t, catalogPath, password)

	for _, body := range []string{
		`{"password":"wrong"}`,
		`{"password":""}`,
		`{`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/courses/api/catalog", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")

		response, err := app.Test(request)
		if err != nil {
			t.Fatalf("test request: %v", err)
		}
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
		}
		if got := response.Header.Get("Cache-Control"); got != "no-store, private" {
			t.Fatalf("Cache-Control = %q", got)
		}
		_ = response.Body.Close()
	}
}

func TestCoursesCatalogRejectsNonJSONContentType(t *testing.T) {
	catalogPath := writeTestCoursesFile(t, `{"schema_version":"courses-catalog/v2"}`)
	app := testCoursesApp(t, catalogPath, "correct horse battery staple")

	request := httptest.NewRequest(http.MethodPost, "/courses/api/catalog", strings.NewReader(`{"password":"correct horse battery staple"}`))
	request.Header.Set("Content-Type", "text/plain")

	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("test request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
}

func TestCoursesCatalogRejectsOversizedBody(t *testing.T) {
	catalogPath := writeTestCoursesFile(t, `{"schema_version":"courses-catalog/v2"}`)
	app := testCoursesApp(t, catalogPath, "correct horse battery staple")

	request := httptest.NewRequest(http.MethodPost, "/courses/api/catalog", strings.NewReader(strings.Repeat("x", 1025)))
	request.Header.Set("Content-Type", "application/json")

	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("test request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
}

func TestCoursesCatalogRejectsPasswordLongerThanBcryptLimit(t *testing.T) {
	catalogPath := writeTestCoursesFile(t, `{"schema_version":"courses-catalog/v2"}`)
	app := testCoursesApp(t, catalogPath, strings.Repeat("a", 72))

	request := httptest.NewRequest(http.MethodPost, "/courses/api/catalog", strings.NewReader(`{"password":"`+strings.Repeat("a", 73)+`"}`))
	request.Header.Set("Content-Type", "application/json")

	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("test request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
}

func TestCoursesCatalogLimitsWrongPasswords(t *testing.T) {
	catalogPath := writeTestCoursesFile(t, `{"schema_version":"courses-catalog/v2"}`)
	app := testCoursesApp(t, catalogPath, "correct horse battery staple")

	for attempt := 1; attempt <= 6; attempt++ {
		request := httptest.NewRequest(http.MethodPost, "/courses/api/catalog", strings.NewReader(`{"password":"wrong"}`))
		request.Header.Set("Content-Type", "application/json")

		response, err := app.Test(request)
		if err != nil {
			t.Fatalf("attempt %d: test request: %v", attempt, err)
		}
		_ = response.Body.Close()

		if attempt <= 5 && response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want %d", attempt, response.StatusCode, http.StatusUnauthorized)
		}
		if attempt == 6 && response.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("attempt %d status = %d, want %d", attempt, response.StatusCode, http.StatusTooManyRequests)
		}
	}
}

func TestCoursesCatalogRejectsCrossOrigin(t *testing.T) {
	catalogPath := writeTestCoursesFile(t, `{"schema_version":"courses-catalog/v2"}`)
	app := testCoursesApp(t, catalogPath, "correct horse battery staple")

	request := httptest.NewRequest(
		http.MethodPost,
		"https://xenking.pro/courses/api/catalog",
		strings.NewReader(`{"password":"correct horse battery staple"}`),
	)
	request.Host = "xenking.pro"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://evil.example")
	request.Header.Set("Sec-Fetch-Site", "cross-site")

	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("test request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusForbidden)
	}
}

func TestCoursesCatalogRejectsCrossSiteFetchMetadata(t *testing.T) {
	catalogPath := writeTestCoursesFile(t, `{"schema_version":"courses-catalog/v2"}`)
	app := testCoursesApp(t, catalogPath, "correct horse battery staple")

	request := httptest.NewRequest(
		http.MethodPost,
		"https://xenking.pro/courses/api/catalog",
		strings.NewReader(`{"password":"correct horse battery staple"}`),
	)
	request.Host = "xenking.pro"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", "cross-site")

	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("test request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusForbidden)
	}
}

func TestCoursesCatalogDownload(t *testing.T) {
	const catalogJSON = `{"schema_version":"courses-catalog/v2","entries":[{"title":"Fixture Course"}]}`
	catalogPath := writeTestCoursesFile(t, catalogJSON)
	password := "correct horse battery staple"
	app := testCoursesApp(t, catalogPath, password)

	request := httptest.NewRequest(
		http.MethodPost,
		"/courses/api/catalog",
		strings.NewReader(`{"password":"`+password+`"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept-Encoding", "identity")

	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("test request: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if got := response.Header.Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if got := response.Header.Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want application/json; charset=utf-8", got)
	}
	if got := response.Header.Get("Content-Disposition"); got != `attachment; filename="courses-catalog.json"` {
		t.Fatalf("Content-Disposition = %q", got)
	}
	if got := response.Header.Get("ETag"); got == "" {
		t.Fatal("ETag is empty")
	}

	compressed, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read compressed response: %v", err)
	}
	expectedCompressed, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatalf("read catalog file: %v", err)
	}
	if !bytes.Equal(compressed, expectedCompressed) {
		t.Fatal("response bytes do not match gzip catalog file")
	}

	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("create gzip reader: %v", err)
	}
	decompressed, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read gzip response: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close gzip reader: %v", err)
	}
	if string(decompressed) != catalogJSON {
		t.Fatalf("catalog = %q, want %q", decompressed, catalogJSON)
	}
}

func TestCoursesCatalogUnlocksWithPasswordHashFile(t *testing.T) {
	const catalogJSON = `{"schema_version":"courses-catalog/v2","entries":[{"title":"Fixture Course"}]}`
	catalogPath := writeTestCoursesFile(t, catalogJSON)
	password := "correct horse battery staple"
	app := New(Config{
		CoursesCatalog:          catalogPath,
		CoursesPasswordHash:     hashTestPassword(t, "wrong inline password"),
		CoursesPasswordHashFile: writeTestPasswordHashFile(t, password),
	}, testLogger())

	request := httptest.NewRequest(
		http.MethodPost,
		"/courses/api/catalog",
		strings.NewReader(`{"password":"`+password+`"}`),
	)
	request.Header.Set("Content-Type", "application/json")

	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("test request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
}

func TestCoursesCatalogDeniesWhenPasswordHashFileIsMissing(t *testing.T) {
	catalogPath := writeTestCoursesFile(t, `{"schema_version":"courses-catalog/v2"}`)
	password := "correct horse battery staple"
	app := New(Config{
		CoursesCatalog:          catalogPath,
		CoursesPasswordHash:     hashTestPassword(t, password),
		CoursesPasswordHashFile: filepath.Join(t.TempDir(), "missing.hash"),
	}, testLogger())

	request := httptest.NewRequest(http.MethodPost, "/courses/api/catalog", strings.NewReader(`{"password":"`+password+`"}`))
	request.Header.Set("Content-Type", "application/json")

	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("test request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
}

func TestCoursesCatalogDeniesWhenPasswordHashFileIsOversized(t *testing.T) {
	catalogPath := writeTestCoursesFile(t, `{"schema_version":"courses-catalog/v2"}`)
	password := "correct horse battery staple"
	app := New(Config{
		CoursesCatalog:          catalogPath,
		CoursesPasswordHash:     hashTestPassword(t, password),
		CoursesPasswordHashFile: writeRawTestPasswordHashFile(t, strings.Repeat("x", 1025)),
	}, testLogger())

	request := httptest.NewRequest(http.MethodPost, "/courses/api/catalog", strings.NewReader(`{"password":"`+password+`"}`))
	request.Header.Set("Content-Type", "application/json")

	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("test request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
}

func TestCoursesCatalogReturnsUnavailableForCorruptGzip(t *testing.T) {
	catalogPath := writeRawTestCoursesFile(t, "not gzip")
	app := testCoursesApp(t, catalogPath, "correct horse battery staple")

	request := httptest.NewRequest(http.MethodPost, "/courses/api/catalog", strings.NewReader(`{"password":"correct horse battery staple"}`))
	request.Header.Set("Content-Type", "application/json")

	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("test request: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusServiceUnavailable)
	}
}

func TestCoursesRejectsCatalogWithoutCurrentSchema(t *testing.T) {
	for _, test := range []struct {
		name    string
		catalog string
	}{
		{
			name:    "stale v1",
			catalog: `{"schema_version":"courses-catalog/v1","entries":[]}`,
		},
		{
			name:    "missing schema",
			catalog: `{"entries":[]}`,
		},
		{
			name:    "schema is not first",
			catalog: `{"entries":[],"schema_version":"courses-catalog/v2"}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			catalogPath := writeTestCoursesFile(t, test.catalog)
			app := testCoursesApp(t, catalogPath, "correct horse battery staple")

			metaRequest := httptest.NewRequest(http.MethodGet, "/courses/api/meta", nil)
			metaResponse, err := app.Test(metaRequest)
			if err != nil {
				t.Fatalf("meta request: %v", err)
			}
			metaBody, err := io.ReadAll(metaResponse.Body)
			_ = metaResponse.Body.Close()
			if err != nil {
				t.Fatalf("read meta response: %v", err)
			}
			if metaResponse.StatusCode != http.StatusOK || !bytes.Contains(metaBody, []byte(`"available":false`)) {
				t.Fatalf("unexpected meta response: status=%d body=%s", metaResponse.StatusCode, metaBody)
			}

			catalogRequest := httptest.NewRequest(
				http.MethodPost,
				"/courses/api/catalog",
				strings.NewReader(`{"password":"correct horse battery staple"}`),
			)
			catalogRequest.Header.Set("Content-Type", "application/json")
			catalogResponse, err := app.Test(catalogRequest)
			if err != nil {
				t.Fatalf("catalog request: %v", err)
			}
			defer catalogResponse.Body.Close()
			if catalogResponse.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("catalog status = %d, want %d", catalogResponse.StatusCode, http.StatusServiceUnavailable)
			}
		})
	}
}

func TestCoursesMetaIsDataFree(t *testing.T) {
	catalogPath := writeTestCoursesFile(t, `{"schema_version":"courses-catalog/v2","secret":"not metadata"}`)
	app := testCoursesApp(t, catalogPath, "correct horse battery staple")

	request := httptest.NewRequest(http.MethodGet, "/courses/api/meta", nil)
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("test request: %v", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"available":true`)) {
		t.Fatalf("unexpected metadata response: status=%d body=%s", response.StatusCode, body)
	}
	if bytes.Contains(body, []byte("not metadata")) {
		t.Fatalf("metadata leaked catalog content: %s", body)
	}
}

func TestCoursesMetaRefreshesAfterCatalogReplacement(t *testing.T) {
	catalogPath := writeTestCoursesFile(t, `{"schema_version":"courses-catalog/v2","entries":[]}`)
	first, err := readCoursesCatalogMeta(catalogPath)
	if err != nil {
		t.Fatalf("read initial metadata: %v", err)
	}

	writeTestCoursesContent(t, catalogPath, `{"schema_version":"courses-catalog/v2","entries":[{"title":"Updated"}]}`)
	replacedAt := time.Now().Add(time.Second)
	if err := os.Chtimes(catalogPath, replacedAt, replacedAt); err != nil {
		t.Fatalf("set replacement timestamp: %v", err)
	}

	second, err := readCoursesCatalogMeta(catalogPath)
	if err != nil {
		t.Fatalf("read replacement metadata: %v", err)
	}
	if second.Version == first.Version {
		t.Fatal("metadata version did not change after catalog replacement")
	}
	if !second.UpdatedAt.Equal(replacedAt.UTC()) {
		t.Fatalf("UpdatedAt = %s, want %s", second.UpdatedAt, replacedAt.UTC())
	}
}

func TestCoursesMetaLimitsSuccessfulRequests(t *testing.T) {
	catalogPath := writeTestCoursesFile(t, `{"schema_version":"courses-catalog/v2"}`)
	app := testCoursesApp(t, catalogPath, "correct horse battery staple")

	for attempt := 1; attempt <= 61; attempt++ {
		request := httptest.NewRequest(http.MethodGet, "/courses/api/meta", nil)
		response, err := app.Test(request)
		if err != nil {
			t.Fatalf("attempt %d: test request: %v", attempt, err)
		}
		_ = response.Body.Close()

		if attempt <= 60 && response.StatusCode != http.StatusOK {
			t.Fatalf("attempt %d status = %d, want %d", attempt, response.StatusCode, http.StatusOK)
		}
		if attempt == 61 && response.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("attempt %d status = %d, want %d", attempt, response.StatusCode, http.StatusTooManyRequests)
		}
	}
}

func TestCoursesMetaReportsUnavailableForMissingCatalog(t *testing.T) {
	app := testCoursesApp(t, filepath.Join(t.TempDir(), "missing.json.gz"), "correct horse battery staple")

	request := httptest.NewRequest(http.MethodGet, "/courses/api/meta", nil)
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("test request: %v", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"available":false`)) {
		t.Fatalf("unexpected metadata response: status=%d body=%s", response.StatusCode, body)
	}
}

func TestCoursesMetaReportsUnavailableForCorruptGzip(t *testing.T) {
	catalogPath := writeRawTestCoursesFile(t, "not gzip")
	app := testCoursesApp(t, catalogPath, "correct horse battery staple")

	request := httptest.NewRequest(http.MethodGet, "/courses/api/meta", nil)
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("test request: %v", err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte(`"available":false`)) {
		t.Fatalf("unexpected metadata response: status=%d body=%s", response.StatusCode, body)
	}
}

func TestCoursesRoutesSetSecurityHeaders(t *testing.T) {
	app := New(Config{}, testLogger())

	request := httptest.NewRequest(http.MethodGet, "/courses/api/meta", nil)
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("test request: %v", err)
	}
	defer response.Body.Close()

	if got := response.Header.Get("Content-Security-Policy"); !strings.Contains(got, "frame-ancestors 'none'") {
		t.Fatalf("unexpected Content-Security-Policy: %q", got)
	}
	if got := response.Header.Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q", got)
	}
}

func testCoursesApp(t *testing.T, catalogPath, password string) *Server {
	t.Helper()

	return New(Config{
		CoursesCatalog:      catalogPath,
		CoursesPasswordHash: hashTestPassword(t, password),
	}, testLogger())
}

func hashTestPassword(t *testing.T, password string) string {
	t.Helper()

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	return base64.RawStdEncoding.EncodeToString(hash)
}

func testLogger() *log.Logger {
	return &log.Logger{Writer: log.IOWriter{Writer: io.Discard}}
}

func writeTestCoursesFile(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "catalog.json.gz")
	writeTestCoursesContent(t, path, content)
	return path
}

func writeTestCoursesContent(t *testing.T, path, content string) {
	t.Helper()

	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create catalog: %v", err)
	}
	writer := gzip.NewWriter(file)
	if _, err := writer.Write([]byte(content)); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close catalog: %v", err)
	}
}

func writeRawTestCoursesFile(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "catalog.json.gz")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write catalog: %v", err)
	}
	return path
}

func writeTestPasswordHashFile(t *testing.T, password string) string {
	t.Helper()

	return writeRawTestPasswordHashFile(t, hashTestPassword(t, password))
}

func writeRawTestPasswordHashFile(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "courses-password.hash")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write password hash: %v", err)
	}
	return path
}
