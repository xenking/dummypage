package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/xenking/dummypage/internal/courses"
)

const (
	maxEnrichmentCatalogBytes = 512 << 20
	maxEnrichmentCacheBytes   = 64 << 20
)

type config struct {
	CatalogPath          string
	AuditPolicyPath      string
	EnrichmentPolicyPath string
	CachePath            string
	Refresh              bool
}

type dependencies struct {
	now           func() time.Time
	newClient     func(*courses.LinkAuditPolicy) (courses.LinkAuditHTTPClient, error)
	stdout        io.Writer
	beforePublish func()
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := run(ctx, os.Args[1:], dependencies{
		now:       time.Now,
		newClient: courses.NewSafeLinkAuditClient,
		stdout:    os.Stdout,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func parseArgs(args []string) (config, error) {
	flags := flag.NewFlagSet("courses-link-enrich", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var result config
	flags.StringVar(&result.CatalogPath, "catalog", "", "catalog JSON gzip path")
	flags.StringVar(&result.AuditPolicyPath, "audit-policy", "", "link audit policy JSON path")
	flags.StringVar(
		&result.EnrichmentPolicyPath,
		"enrichment-policy",
		"",
		"link enrichment policy JSON path",
	)
	flags.StringVar(&result.CachePath, "cache", "", "private enrichment cache JSON path")
	flags.BoolVar(&result.Refresh, "refresh", false, "refresh fresh cache records")
	if err := flags.Parse(args); err != nil {
		return config{}, errors.New("invalid arguments")
	}
	if flags.NArg() != 0 {
		return config{}, errors.New("unexpected positional arguments")
	}
	if strings.TrimSpace(result.CatalogPath) == "" ||
		strings.TrimSpace(result.AuditPolicyPath) == "" ||
		strings.TrimSpace(result.EnrichmentPolicyPath) == "" ||
		strings.TrimSpace(result.CachePath) == "" {
		return config{}, errors.New(
			"--catalog, --audit-policy, --enrichment-policy, and --cache are required",
		)
	}
	if err := validateEnrichmentOutputPath(result); err != nil {
		return config{}, err
	}
	return result, nil
}

func run(ctx context.Context, args []string, deps dependencies) error {
	cfg, err := parseArgs(args)
	if err != nil {
		return err
	}
	if deps.now == nil || deps.newClient == nil || deps.stdout == nil {
		return errors.New("invalid runtime dependencies")
	}

	catalog, err := loadEnrichmentCatalog(cfg.CatalogPath)
	if err != nil {
		return errors.New("load catalog: invalid or unavailable input")
	}
	auditPolicy, err := loadAuditPolicy(cfg.AuditPolicyPath)
	if err != nil {
		return errors.New("load audit policy: invalid or unavailable input")
	}
	enrichmentPolicy, err := loadEnrichmentPolicy(cfg.EnrichmentPolicyPath)
	if err != nil {
		return errors.New("load enrichment policy: invalid or unavailable input")
	}
	previous, err := loadOptionalEnrichmentCache(cfg.CachePath)
	if err != nil {
		return errors.New("load enrichment cache: invalid or unavailable input")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	client, err := deps.newClient(auditPolicy)
	if err != nil {
		return errors.New("create safe link client")
	}
	now := deps.now()
	cache, stats, err := courses.EnrichCatalogLinks(
		ctx,
		catalog,
		enrichmentPolicy,
		previous,
		client,
		now,
		cfg.Refresh,
		auditPolicy.Concurrency,
	)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return errors.New("enrich catalog links failed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	var output bytes.Buffer
	if err := courses.WriteLinkEnrichmentCache(&output, cache); err != nil {
		return errors.New("encode enrichment cache")
	}
	if deps.beforePublish != nil {
		deps.beforePublish()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	validateOutput := func() error {
		return validateEnrichmentOutputPath(cfg)
	}
	if err := validateOutput(); err != nil {
		return err
	}
	if err := writeAtomicEnrichmentCache(ctx, cfg.CachePath, output.Bytes(), validateOutput); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return errors.New("publish enrichment cache")
	}

	fmt.Fprintf(
		deps.stdout,
		"candidates=%d skipped_fresh=%d fetched=%d extracted=%d not_found=%d failed=%d\n",
		stats.Candidates,
		stats.SkippedFresh,
		stats.Fetched,
		stats.Extracted,
		stats.NotFound,
		stats.Failed,
	)
	return nil
}

func loadEnrichmentCatalog(path string) (courses.Catalog, error) {
	file, err := os.Open(path)
	if err != nil {
		return courses.Catalog{}, err
	}
	defer file.Close()
	reader, err := gzip.NewReader(file)
	if err != nil {
		return courses.Catalog{}, err
	}
	defer reader.Close()

	limited := &io.LimitedReader{R: reader, N: maxEnrichmentCatalogBytes + 1}
	decoder := json.NewDecoder(limited)
	decoder.DisallowUnknownFields()
	var catalog courses.Catalog
	if err := decoder.Decode(&catalog); err != nil {
		return courses.Catalog{}, err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return courses.Catalog{}, errors.New("multiple catalog values")
		}
		return courses.Catalog{}, err
	}
	if limited.N <= 0 {
		return courses.Catalog{}, errors.New("catalog exceeds size limit")
	}
	if catalog.SchemaVersion != "courses-catalog/v2" || catalog.Entries == nil {
		return courses.Catalog{}, errors.New("unsupported catalog")
	}
	return catalog, nil
}

func loadAuditPolicy(path string) (*courses.LinkAuditPolicy, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return courses.LoadLinkAuditPolicy(file)
}

func loadEnrichmentPolicy(path string) (*courses.LinkEnrichmentPolicy, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return courses.LoadLinkEnrichmentPolicy(file)
}

func loadOptionalEnrichmentCache(path string) (*courses.LinkEnrichmentCache, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() > maxEnrichmentCacheBytes {
		return nil, errors.New("cache exceeds size limit")
	}
	limited := &io.LimitedReader{R: file, N: maxEnrichmentCacheBytes + 1}
	cache, err := courses.LoadLinkEnrichmentCache(limited)
	if err != nil {
		return nil, err
	}
	if limited.N <= 0 {
		return nil, errors.New("cache exceeds size limit")
	}
	return cache, nil
}

func writeAtomicEnrichmentCache(
	ctx context.Context,
	path string,
	data []byte,
	validateOutput func() error,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, ".courses-link-enrich-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	closed := false
	defer func() {
		if !closed {
			_ = temp.Close()
		}
	}()
	if err := temp.Chmod(0o600); err != nil {
		return err
	}
	if _, err := temp.Write(data); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	closed = true
	if err := ctx.Err(); err != nil {
		return err
	}
	if validateOutput != nil {
		if err := validateOutput(); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	tempPath = ""
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	syncErr := directory.Sync()
	closeErr := directory.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func validateEnrichmentOutputPath(cfg config) error {
	for _, input := range []string{
		cfg.CatalogPath,
		cfg.AuditPolicyPath,
		cfg.EnrichmentPolicyPath,
	} {
		if sameEnrichmentPath(input, cfg.CachePath) {
			return errors.New("cache output must be distinct from every input")
		}
	}
	return nil
}

func sameEnrichmentPath(left, right string) bool {
	leftPath := canonicalEnrichmentPath(left)
	rightPath := canonicalEnrichmentPath(right)
	if leftPath == rightPath {
		return true
	}
	leftInfo, leftErr := os.Stat(leftPath)
	rightInfo, rightErr := os.Stat(rightPath)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}

func canonicalEnrichmentPath(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
		return filepath.Clean(resolved)
	}
	parent := filepath.Dir(absolute)
	if resolvedParent, err := filepath.EvalSymlinks(parent); err == nil {
		return filepath.Join(resolvedParent, filepath.Base(absolute))
	}
	return filepath.Clean(absolute)
}
