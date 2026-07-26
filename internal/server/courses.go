package server

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v3"
	"golang.org/x/crypto/bcrypt"
)

const (
	coursesSchema             = "courses-catalog/v2"
	maxCoursesFileSize        = 100 << 20
	maxCoursesSchemaProbeSize = 64 << 10
	maxPasswordLength         = 72
)

type coursesUnlockRequest struct {
	Password string `json:"password"`
}

type coursesMetaResponse struct {
	Available bool      `json:"available"`
	Schema    string    `json:"schema,omitempty"`
	Version   string    `json:"version,omitempty"`
	Bytes     int64     `json:"bytes,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

type coursesMetaCacheEntry struct {
	info os.FileInfo
	meta coursesMetaResponse
}

var coursesMetaCache = struct {
	sync.Mutex
	entries map[string]coursesMetaCacheEntry
}{
	entries: make(map[string]coursesMetaCacheEntry),
}

func coursesSecurityHeaders(ctx fiber.Ctx) error {
	ctx.Set("Content-Security-Policy", strings.Join([]string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self'",
		"img-src 'self' data:",
		"connect-src 'self'",
		"worker-src 'self'",
		"object-src 'none'",
		"base-uri 'none'",
		"frame-ancestors 'none'",
		"form-action 'self'",
	}, "; "))
	ctx.Set(fiber.HeaderReferrerPolicy, "no-referrer")
	ctx.Set(fiber.HeaderXFrameOptions, "DENY")
	ctx.Set(fiber.HeaderXContentTypeOptions, "nosniff")
	ctx.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
	return ctx.Next()
}

func handleCoursesMeta(cfg Config) fiber.Handler {
	return func(ctx fiber.Ctx) error {
		ctx.Set(fiber.HeaderCacheControl, "no-store")
		meta, err := readCoursesCatalogMeta(cfg.CoursesCatalog)
		if err != nil {
			return ctx.JSON(coursesMetaResponse{Available: false})
		}
		return ctx.JSON(meta)
	}
}

func readCoursesCatalogMeta(path string) (coursesMetaResponse, error) {
	coursesMetaCache.Lock()
	defer coursesMetaCache.Unlock()

	info, err := statCoursesCatalog(path)
	if err != nil {
		return coursesMetaResponse{}, err
	}
	if cached, ok := coursesMetaCache.entries[path]; ok &&
		cached.info.Size() == info.Size() &&
		cached.info.ModTime().Equal(info.ModTime()) &&
		os.SameFile(cached.info, info) {
		return cached.meta, nil
	}

	_, meta, err := readCoursesCatalog(path)
	if err != nil {
		return coursesMetaResponse{}, err
	}
	currentInfo, err := statCoursesCatalog(path)
	if err != nil {
		return coursesMetaResponse{}, err
	}
	if currentInfo.Size() != meta.Bytes || !currentInfo.ModTime().Equal(meta.UpdatedAt) {
		return coursesMetaResponse{}, errors.New("catalog changed while reading")
	}
	coursesMetaCache.entries[path] = coursesMetaCacheEntry{
		info: currentInfo,
		meta: meta,
	}
	return meta, nil
}

func handleCoursesCatalog(cfg Config) fiber.Handler {
	return func(ctx fiber.Ctx) error {
		ctx.Set(fiber.HeaderCacheControl, "no-store, private")
		ctx.Set(fiber.HeaderPragma, "no-cache")

		if !sameOriginRequest(ctx) {
			return ctx.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error": "request forbidden",
			})
		}
		if !strings.HasPrefix(strings.ToLower(ctx.Get(fiber.HeaderContentType)), fiber.MIMEApplicationJSON) {
			return unauthorizedCourses(ctx)
		}
		if len(ctx.Body()) > 1024 {
			return unauthorizedCourses(ctx)
		}

		var request coursesUnlockRequest
		if err := ctx.Bind().JSON(&request); err != nil {
			return unauthorizedCourses(ctx)
		}
		if len(request.Password) == 0 || len(request.Password) > maxPasswordLength {
			return unauthorizedCourses(ctx)
		}
		if !coursesPasswordMatches(coursesPasswordHash(cfg), request.Password) {
			return unauthorizedCourses(ctx)
		}

		catalog, meta, err := readCoursesCatalog(cfg.CoursesCatalog)
		if err != nil {
			return ctx.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"error": "catalog unavailable",
			})
		}

		ctx.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
		ctx.Set(fiber.HeaderContentEncoding, "gzip")
		ctx.Set(fiber.HeaderContentDisposition, `attachment; filename="courses-catalog.json"`)
		ctx.Set(fiber.HeaderETag, fmt.Sprintf(`"%s"`, meta.Version))
		return ctx.Send(catalog)
	}
}

func coursesPasswordHash(cfg Config) string {
	if strings.TrimSpace(cfg.CoursesPasswordHashFile) == "" {
		return cfg.CoursesPasswordHash
	}

	info, err := os.Stat(cfg.CoursesPasswordHashFile)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 1024 {
		return ""
	}
	hash, err := os.ReadFile(cfg.CoursesPasswordHashFile)
	if err != nil {
		return ""
	}
	return string(hash)
}

func unauthorizedCourses(ctx fiber.Ctx) error {
	return ctx.Status(fiber.StatusUnauthorized).JSON(fiber.Map{
		"error": "invalid password",
	})
}

func coursesPasswordMatches(encodedHash, password string) bool {
	hash, err := base64.RawStdEncoding.DecodeString(strings.TrimSpace(encodedHash))
	if err != nil {
		hash = nil
	}
	return bcrypt.CompareHashAndPassword(hash, []byte(password)) == nil
}

func sameOriginRequest(ctx fiber.Ctx) bool {
	if fetchSite := ctx.Get("Sec-Fetch-Site"); fetchSite != "" && fetchSite != "same-origin" {
		return false
	}
	origin := ctx.Get(fiber.HeaderOrigin)
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	return err == nil && strings.EqualFold(parsed.Host, ctx.Host())
}

func readCoursesCatalog(path string) ([]byte, coursesMetaResponse, error) {
	info, err := statCoursesCatalog(path)
	if err != nil {
		return nil, coursesMetaResponse{}, err
	}

	catalog, err := os.ReadFile(path)
	if err != nil {
		return nil, coursesMetaResponse{}, fmt.Errorf("read catalog: %w", err)
	}
	if len(catalog) < 2 || catalog[0] != 0x1f || catalog[1] != 0x8b {
		return nil, coursesMetaResponse{}, errors.New("catalog is not gzip data")
	}
	if err := validateCoursesCatalogSchema(catalog); err != nil {
		return nil, coursesMetaResponse{}, err
	}

	digest := sha256.Sum256(catalog)
	return catalog, coursesMetaResponse{
		Available: true,
		Schema:    coursesSchema,
		Version:   hex.EncodeToString(digest[:]),
		Bytes:     info.Size(),
		UpdatedAt: info.ModTime().UTC(),
	}, nil
}

func statCoursesCatalog(path string) (os.FileInfo, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("catalog path is empty")
	}
	if filepath.Ext(path) != ".gz" {
		return nil, errors.New("catalog must be gzip encoded")
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat catalog: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("catalog is not a regular file")
	}
	if info.Size() <= 0 || info.Size() > maxCoursesFileSize {
		return nil, fmt.Errorf("catalog size %d is outside allowed range", info.Size())
	}
	return info, nil
}

func validateCoursesCatalogSchema(catalog []byte) error {
	reader, err := gzip.NewReader(bytes.NewReader(catalog))
	if err != nil {
		return fmt.Errorf("open gzip catalog: %w", err)
	}
	defer reader.Close()

	decoder := json.NewDecoder(io.LimitReader(reader, maxCoursesSchemaProbeSize))
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("read catalog object: %w", err)
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return errors.New("catalog root is not an object")
	}
	token, err = decoder.Token()
	if err != nil {
		return fmt.Errorf("read catalog schema field: %w", err)
	}
	field, ok := token.(string)
	if !ok || field != "schema_version" {
		return errors.New("catalog schema_version must be the first field")
	}
	var schema string
	if err := decoder.Decode(&schema); err != nil {
		return fmt.Errorf("decode catalog schema: %w", err)
	}
	if schema != coursesSchema {
		return fmt.Errorf("catalog schema %q, want %q", schema, coursesSchema)
	}
	return nil
}
