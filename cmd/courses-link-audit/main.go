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

const maxCatalogBytes = 512 << 20

type config struct {
	CatalogPath    string
	PolicyPath     string
	ReportPath     string
	TombstonesPath string
}

type dependencies struct {
	now       func() time.Time
	newClient func(*courses.LinkAuditPolicy) (courses.LinkAuditHTTPClient, error)
	stdout    io.Writer
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
	flags := flag.NewFlagSet("courses-link-audit", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var result config
	flags.StringVar(&result.CatalogPath, "catalog", "", "catalog JSON gzip path")
	flags.StringVar(&result.PolicyPath, "policy", "", "link audit policy JSON path")
	flags.StringVar(&result.ReportPath, "report", "", "link audit report JSON path")
	flags.StringVar(&result.TombstonesPath, "tombstones", "", "link tombstones JSON path")
	if err := flags.Parse(args); err != nil {
		return config{}, errors.New("invalid arguments")
	}
	if flags.NArg() != 0 {
		return config{}, errors.New("unexpected positional arguments")
	}
	if strings.TrimSpace(result.CatalogPath) == "" ||
		strings.TrimSpace(result.PolicyPath) == "" ||
		strings.TrimSpace(result.ReportPath) == "" ||
		strings.TrimSpace(result.TombstonesPath) == "" {
		return config{}, errors.New("--catalog, --policy, --report, and --tombstones are required")
	}
	if samePath(result.CatalogPath, result.PolicyPath) ||
		samePath(result.CatalogPath, result.ReportPath) ||
		samePath(result.CatalogPath, result.TombstonesPath) ||
		samePath(result.PolicyPath, result.ReportPath) ||
		samePath(result.PolicyPath, result.TombstonesPath) ||
		samePath(result.ReportPath, result.TombstonesPath) {
		return config{}, errors.New("input and output paths must be distinct")
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

	catalog, err := loadCatalog(cfg.CatalogPath)
	if err != nil {
		return errors.New("load catalog: invalid or unavailable input")
	}
	policy, err := loadPolicy(cfg.PolicyPath)
	if err != nil {
		return errors.New("load policy: invalid or unavailable input")
	}
	previous, err := loadOptionalReport(cfg.ReportPath)
	if err != nil {
		return errors.New("load previous report: invalid or unavailable input")
	}
	tombstones, err := loadOptionalTombstones(cfg.TombstonesPath)
	if err != nil {
		return errors.New("load tombstones: invalid or unavailable input")
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	client, err := deps.newClient(policy)
	if err != nil {
		return errors.New("create safe audit client")
	}
	now := deps.now()
	report, err := courses.AuditCatalogLinks(ctx, catalog, policy, previous, client, now)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return errors.New("audit catalog links failed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	added := tombstones.MergeEligibleAuditResults(report, policy.ConfirmationsRequired, now)

	reportJSON, err := marshalPrivateJSON(report)
	if err != nil {
		return errors.New("encode audit report")
	}
	tombstonesJSON, err := marshalPrivateJSON(tombstones)
	if err != nil {
		return errors.New("encode tombstones")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := writeAtomicPair(
		ctx,
		cfg.ReportPath, reportJSON,
		cfg.TombstonesPath, tombstonesJSON,
	); err != nil {
		return errors.New("publish audit outputs")
	}

	counts := make(map[courses.LinkAuditState]int)
	for _, result := range report.Results {
		counts[result.State]++
	}
	fmt.Fprintf(
		deps.stdout,
		"audited=%d live=%d expired=%d content_mismatch=%d blocked=%d transient=%d unknown=%d tombstones_added=%d tombstones_total=%d\n",
		len(report.Results),
		counts[courses.LinkAuditStateLive],
		counts[courses.LinkAuditStateExpired],
		counts[courses.LinkAuditStateContentMismatch],
		counts[courses.LinkAuditStateBlocked],
		counts[courses.LinkAuditStateTransient],
		counts[courses.LinkAuditStateUnknown],
		added,
		tombstones.Len(),
	)
	return nil
}

func loadCatalog(path string) (courses.Catalog, error) {
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

	limited := &io.LimitedReader{R: reader, N: maxCatalogBytes + 1}
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
		return courses.Catalog{}, errors.New("catalog too large")
	}
	if catalog.SchemaVersion != "courses-catalog/v2" || catalog.Entries == nil {
		return courses.Catalog{}, errors.New("unsupported catalog")
	}
	return catalog, nil
}

func loadPolicy(path string) (*courses.LinkAuditPolicy, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return courses.LoadLinkAuditPolicy(file)
}

func loadOptionalReport(path string) (*courses.LinkAuditReport, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return courses.LoadLinkAuditReport(file)
}

func loadOptionalTombstones(path string) (*courses.LinkTombstones, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return courses.NewLinkTombstones(), nil
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return courses.LoadLinkTombstones(file)
}

func marshalPrivateJSON(value any) ([]byte, error) {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

type pendingAtomicFile struct {
	path     string
	tempPath string
}

func writeAtomicPair(ctx context.Context, firstPath string, first []byte, secondPath string, second []byte) error {
	return writeAtomicPairWithRename(ctx, firstPath, first, secondPath, second, os.Rename)
}

func writeAtomicPairWithRename(
	ctx context.Context,
	firstPath string,
	first []byte,
	secondPath string,
	second []byte,
	rename func(string, string) error,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if rename == nil {
		return errors.New("atomic output rename is nil")
	}
	if samePath(firstPath, secondPath) {
		return errors.New("atomic output paths must be distinct")
	}
	if err := validateAtomicTarget(firstPath); err != nil {
		return err
	}
	if err := validateAtomicTarget(secondPath); err != nil {
		return err
	}
	firstPending, err := prepareAtomicFile(firstPath, first)
	if err != nil {
		return err
	}
	defer firstPending.cleanup()
	secondPending, err := prepareAtomicFile(secondPath, second)
	if err != nil {
		return err
	}
	defer secondPending.cleanup()

	firstBackup, firstExisted, err := prepareAtomicBackup(firstPath)
	if err != nil {
		return err
	}
	defer firstBackup.cleanup()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := rename(firstPending.tempPath, firstPending.path); err != nil {
		return err
	}
	firstPending.tempPath = ""
	if err := ctx.Err(); err != nil {
		if rollbackErr := restoreAtomicTarget(firstPath, firstBackup, firstExisted, rename); rollbackErr != nil {
			return errors.Join(err, rollbackErr)
		}
		return err
	}
	if err := rename(secondPending.tempPath, secondPending.path); err != nil {
		if rollbackErr := restoreAtomicTarget(firstPath, firstBackup, firstExisted, rename); rollbackErr != nil {
			return errors.Join(err, rollbackErr)
		}
		return err
	}
	secondPending.tempPath = ""
	return nil
}

func validateAtomicTarget(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("atomic output target must be a regular file")
	}
	return nil
}

func prepareAtomicBackup(path string) (*pendingAtomicFile, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return &pendingAtomicFile{path: path}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	pending, err := prepareAtomicFile(path, data)
	if err != nil {
		return nil, false, err
	}
	return pending, true, nil
}

func restoreAtomicTarget(path string, backup *pendingAtomicFile, existed bool, rename func(string, string) error) error {
	if !existed {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if backup == nil || backup.tempPath == "" {
		return errors.New("atomic output rollback is unavailable")
	}
	if err := rename(backup.tempPath, path); err != nil {
		return err
	}
	backup.tempPath = ""
	return nil
}

func prepareAtomicFile(path string, data []byte) (*pendingAtomicFile, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(dir, ".courses-link-audit-*.tmp")
	if err != nil {
		return nil, err
	}
	pending := &pendingAtomicFile{path: path, tempPath: file.Name()}
	ok := false
	defer func() {
		if !ok {
			_ = file.Close()
			pending.cleanup()
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		return nil, err
	}
	if _, err := file.Write(data); err != nil {
		return nil, err
	}
	if err := file.Sync(); err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	ok = true
	return pending, nil
}

func (p *pendingAtomicFile) cleanup() {
	if p != nil && p.tempPath != "" {
		_ = os.Remove(p.tempPath)
	}
}

func samePath(left, right string) bool {
	leftPath := canonicalPath(left)
	rightPath := canonicalPath(right)
	if leftPath == rightPath {
		return true
	}
	leftInfo, leftErr := os.Stat(leftPath)
	rightInfo, rightErr := os.Stat(rightPath)
	return leftErr == nil && rightErr == nil && os.SameFile(leftInfo, rightInfo)
}

func canonicalPath(path string) string {
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
