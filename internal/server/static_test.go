package server

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
)

func TestCoursesAssetsShareStableCacheVersion(t *testing.T) {
	config := Config{
		ViewsFolder:  filepath.Join("..", "..", "static", "templates"),
		ViewsExt:     ".html",
		StaticFolder: filepath.Join("..", "..", "static"),
		StaticPrefix: "/",
	}
	app := New(config, testLogger())

	first := requestBody(t, app, "/courses", "")
	second := requestBody(t, app, "/courses", "")

	cssVersion := assetVersionFromHTML(t, first, `href="/css/courses\.css\?v=([^"]+)"`)
	jsVersion := assetVersionFromHTML(t, first, `src="/js/courses\.js\?v=([^"]+)"`)
	if cssVersion != jsVersion {
		t.Fatalf("asset versions differ: css=%q js=%q", cssVersion, jsVersion)
	}
	if got := assetVersionFromHTML(t, second, `src="/js/courses\.js\?v=([^"]+)"`); got != jsVersion {
		t.Fatalf("asset version changed within one server: first=%q second=%q", jsVersion, got)
	}

	nextApp := New(config, testLogger())
	nextVersion := assetVersionFromHTML(
		t,
		requestBody(t, nextApp, "/courses", ""),
		`src="/js/courses\.js\?v=([^"]+)"`,
	)
	if nextVersion == jsVersion {
		t.Fatalf("asset version was reused across servers: %q", jsVersion)
	}
}

func TestBrotliStaticResponseMatchesSource(t *testing.T) {
	staticDir := t.TempDir()
	source := bytes.Repeat([]byte("const catalog = 'current';\n"), 256)
	if err := os.WriteFile(filepath.Join(staticDir, "asset.js"), source, 0o600); err != nil {
		t.Fatalf("write static source: %v", err)
	}

	app := New(Config{
		StaticFolder: staticDir,
		StaticPrefix: "/",
	}, testLogger())
	request := httptest.NewRequest(http.MethodGet, "/asset.js?v=deploy-token", nil)
	request.Header.Set("Accept-Encoding", "br")
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("request compressed asset: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if got := response.Header.Get("Content-Encoding"); got != "br" {
		t.Fatalf("Content-Encoding = %q, want br", got)
	}
	decompressed, err := io.ReadAll(brotli.NewReader(response.Body))
	if err != nil {
		t.Fatalf("decompress response: %v", err)
	}
	if !bytes.Equal(decompressed, source) {
		t.Fatal("decompressed response does not match source")
	}
}

func requestBody(t *testing.T, app *Server, path, acceptEncoding string) string {
	t.Helper()

	request := httptest.NewRequest(http.MethodGet, path, nil)
	if acceptEncoding != "" {
		request.Header.Set("Accept-Encoding", acceptEncoding)
	}
	response, err := app.Test(request)
	if err != nil {
		t.Fatalf("request %s: %v", path, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("%s status = %d, want %d", path, response.StatusCode, http.StatusOK)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read %s response: %v", path, err)
	}
	return string(body)
}

func assetVersionFromHTML(t *testing.T, body, pattern string) string {
	t.Helper()

	match := regexp.MustCompile(pattern).FindStringSubmatch(body)
	if len(match) != 2 || strings.TrimSpace(match[1]) == "" {
		t.Fatalf("versioned asset %q not found in HTML", pattern)
	}
	return match[1]
}
