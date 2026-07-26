package courses

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const maxLinkAuditResponseHeaderBytes = 64 << 10

type LinkAuditResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

type LinkAuditDialer interface {
	DialContext(context.Context, string, string) (net.Conn, error)
}

type LinkAuditHTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type netLinkAuditResolver struct{}

func (netLinkAuditResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

func NewSafeLinkAuditClient(policy *LinkAuditPolicy) (LinkAuditHTTPClient, error) {
	return newSafeLinkAuditClient(policy, nil, nil)
}

func newSafeLinkAuditClient(policy *LinkAuditPolicy, resolver LinkAuditResolver, dialer LinkAuditDialer) (*http.Client, error) {
	if policy == nil {
		return nil, errors.New("link audit client: policy is nil")
	}
	if resolver == nil {
		resolver = netLinkAuditResolver{}
	}
	if dialer == nil {
		dialer = (&net.Dialer{Timeout: policy.Timeout})
	}

	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            safeLinkAuditDialContext(resolver, dialer),
		ForceAttemptHTTP2:      true,
		MaxIdleConns:           policy.Concurrency,
		MaxIdleConnsPerHost:    policy.Concurrency,
		IdleConnTimeout:        30 * time.Second,
		TLSHandshakeTimeout:    minLinkAuditDuration(policy.Timeout, 10*time.Second),
		ResponseHeaderTimeout:  minLinkAuditDuration(policy.Timeout, 10*time.Second),
		ExpectContinueTimeout:  time.Second,
		MaxResponseHeaderBytes: maxLinkAuditResponseHeaderBytes,
	}
	client := &http.Client{
		Transport:     transport,
		Timeout:       policy.Timeout,
		CheckRedirect: safeLinkAuditRedirect(policy),
	}
	return client, nil
}

func safeLinkAuditDialContext(resolver LinkAuditResolver, dialer LinkAuditDialer) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, fmt.Errorf("link audit dial: split hostport: %w", err)
		}
		if net.ParseIP(host) != nil {
			if err := validateSafeLinkAuditIP(host); err != nil {
				return nil, err
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(host, port))
		}

		addrs, err := resolver.LookupIPAddr(ctx, strings.TrimSuffix(host, "."))
		if err != nil {
			return nil, fmt.Errorf("link audit dns: %w", err)
		}
		if len(addrs) == 0 {
			return nil, errors.New("link audit dns: no addresses")
		}
		for _, addr := range addrs {
			if err := validateSafeLinkAuditIP(addr.IP.String()); err != nil {
				return nil, err
			}
		}
		return dialer.DialContext(ctx, network, net.JoinHostPort(addrs[0].IP.String(), port))
	}
}

func safeLinkAuditRedirect(policy *LinkAuditPolicy) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) > 5 {
			return errors.New("link audit redirect rejected")
		}
		if req == nil || req.URL == nil || len(via) == 0 || via[0] == nil || via[0].URL == nil {
			return errors.New("link audit redirect rejected")
		}
		if _, _, rejection := inspectLink(req.URL.String()); rejection != "" {
			return errors.New("link audit redirect rejected")
		}

		originalHost := strings.TrimSuffix(strings.ToLower(via[0].URL.Hostname()), ".")
		redirectHost := strings.TrimSuffix(strings.ToLower(req.URL.Hostname()), ".")
		if redirectHost == originalHost {
			return nil
		}
		rule := policy.matchingRule(originalHost)
		if rule == nil {
			return errors.New("link audit redirect rejected")
		}
		for _, suffix := range rule.AllowedRedirectHostSuffixes {
			if hostMatchesSuffix(redirectHost, suffix) {
				return nil
			}
		}
		return errors.New("link audit redirect rejected")
	}
}

func validateSafeLinkAuditIP(raw string) error {
	addr, err := netip.ParseAddr(raw)
	if err != nil {
		return fmt.Errorf("link audit ip rejected")
	}
	addr = addr.Unmap()
	if !addr.IsGlobalUnicast() {
		return fmt.Errorf("link audit ip rejected")
	}
	if addr.Is6() && !publicLinkAuditIPv6Prefix.Contains(addr) {
		return fmt.Errorf("link audit ip rejected")
	}
	for _, prefix := range unsafeLinkAuditIPPrefixes {
		if prefix.Contains(addr) {
			return fmt.Errorf("link audit ip rejected")
		}
	}
	return nil
}

var unsafeLinkAuditIPPrefixes = mustLinkAuditPrefixes(
	"0.0.0.0/8",
	"10.0.0.0/8",
	"100.64.0.0/10",
	"127.0.0.0/8",
	"169.254.0.0/16",
	"172.16.0.0/12",
	"192.0.0.0/24",
	"192.0.2.0/24",
	"192.31.196.0/24",
	"192.52.193.0/24",
	"192.168.0.0/16",
	"192.88.99.0/24",
	"192.175.48.0/24",
	"198.18.0.0/15",
	"198.51.100.0/24",
	"203.0.113.0/24",
	"224.0.0.0/4",
	"240.0.0.0/4",
	"::/128",
	"::1/128",
	"64:ff9b::/96",
	"64:ff9b:1::/48",
	"100::/64",
	"2001::/23",
	"2001:2::/48",
	"2001:db8::/32",
	"2002::/16",
	"2620:4f:8000::/48",
	"3fff::/20",
	"fc00::/7",
	"fe80::/10",
	"ff00::/8",
)

// IANA currently allocates public IPv6 unicast addresses from 2000::/3.
var publicLinkAuditIPv6Prefix = netip.MustParsePrefix("2000::/3")

func mustLinkAuditPrefixes(values ...string) []netip.Prefix {
	prefixes := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			panic(err)
		}
		prefixes = append(prefixes, prefix)
	}
	return prefixes
}

func AuditCatalogLinks(ctx context.Context, catalog Catalog, policy *LinkAuditPolicy, previous *LinkAuditReport, client LinkAuditHTTPClient, now time.Time) (*LinkAuditReport, error) {
	if policy == nil {
		return nil, errors.New("audit catalog links: policy is nil")
	}
	if client == nil {
		return nil, errors.New("audit catalog links: client is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	candidates := auditCandidates(catalog)
	previousByHash := make(map[string]LinkAuditResult)
	if previous != nil {
		for _, result := range previous.Results {
			previousByHash[result.SHA256] = result
		}
	}

	checkedAt := now.UTC().Format(time.RFC3339)
	report := &LinkAuditReport{
		SchemaVersion: linkAuditReportSchema,
		GeneratedAt:   checkedAt,
		Results:       make([]LinkAuditResult, len(candidates)),
	}
	jobs := make(chan int)
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex
	workerCount := policy.Concurrency
	if workerCount < 1 {
		workerCount = 1
	}
	for worker := 0; worker < workerCount; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				candidate := candidates[index]
				result, err := auditCatalogLink(ctx, candidate, policy, client, checkedAt)
				if err != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					errMu.Unlock()
					continue
				}
				if previous, ok := previousByHash[result.SHA256]; ok {
					result = mergeAuditResult(&previous, result)
				} else {
					result = mergeAuditResult(nil, result)
				}
				report.Results[index] = result
			}
		}()
	}
	for index := range candidates {
		if err := ctx.Err(); err != nil {
			close(jobs)
			wg.Wait()
			return nil, err
		}
		jobs <- index
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	sortLinkAuditResults(report.Results)
	return report, nil
}

type linkAuditCandidate struct {
	hash      string
	rawURL    string
	canonical string
	host      string
}

func auditCandidates(catalog Catalog) []linkAuditCandidate {
	byHash := make(map[string]linkAuditCandidate)
	for _, entry := range catalog.Entries {
		for _, link := range entry.Links {
			parsed, canonical, rejection := inspectLink(link.URL)
			if rejection != "" || parsed == nil {
				continue
			}
			if parsed.Scheme != "http" && parsed.Scheme != "https" {
				continue
			}
			hash, err := linkHash(link.URL)
			if err != nil {
				continue
			}
			candidate := linkAuditCandidate{
				hash:      hash,
				rawURL:    canonical,
				canonical: canonical,
				host:      strings.TrimSuffix(strings.ToLower(parsed.Hostname()), "."),
			}
			if existing, ok := byHash[hash]; !ok || candidate.canonical < existing.canonical {
				byHash[hash] = candidate
			}
		}
	}
	candidates := make([]linkAuditCandidate, 0, len(byHash))
	for _, candidate := range byHash {
		candidates = append(candidates, candidate)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].hash < candidates[j].hash
	})
	return candidates
}

func auditCatalogLink(ctx context.Context, candidate linkAuditCandidate, policy *LinkAuditPolicy, client LinkAuditHTTPClient, checkedAt string) (LinkAuditResult, error) {
	obs, err := requestLinkAuditObservation(ctx, candidate, policy, client)
	if err != nil {
		if (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) && ctx.Err() != nil {
			return LinkAuditResult{}, ctx.Err()
		}
		obs = LinkAuditObservation{TransportError: classifyLinkAuditTransportError(err)}
	}
	result := policy.Classify(candidate.host, obs)
	result.SHA256 = candidate.hash
	result.CheckedAt = checkedAt
	return result, nil
}

func requestLinkAuditObservation(ctx context.Context, candidate linkAuditCandidate, policy *LinkAuditPolicy, client LinkAuditHTTPClient) (LinkAuditObservation, error) {
	head, err := doLinkAuditRequest(ctx, client, http.MethodHead, candidate.rawURL, 0)
	if err != nil {
		return LinkAuditObservation{}, err
	}
	defer func() {
		_ = head.Body.Close()
	}()

	status := head.StatusCode
	rule := policy.matchingRule(candidate.host)
	fallback := status == http.StatusMethodNotAllowed || status == http.StatusNotImplemented
	verifyDeadStatus := status == http.StatusNotFound ||
		status == http.StatusGone ||
		(rule != nil && containsInt(rule.DeadStatuses, status))
	headOK := status >= 200 && status <= 299
	requestBody := ruleNeedsLinkAuditBody(rule) && (fallback || verifyDeadStatus || headOK)
	if fallback || verifyDeadStatus || requestBody {
		bodyLimit := int64(1)
		if requestBody {
			bodyLimit = policy.MaxBodyBytes
		}
		get, err := doLinkAuditRequest(ctx, client, http.MethodGet, candidate.rawURL, bodyLimit)
		if err != nil {
			return LinkAuditObservation{}, err
		}
		if bodyLimit > 0 && get.StatusCode == http.StatusRequestedRangeNotSatisfiable {
			_ = get.Body.Close()
			get, err = doLinkAuditRequest(ctx, client, http.MethodGet, candidate.rawURL, 0)
			if err != nil {
				return LinkAuditObservation{}, err
			}
		}
		defer func() {
			_ = get.Body.Close()
		}()
		readBody := ruleNeedsLinkAuditDeadBody(rule) ||
			(get.StatusCode >= 200 && get.StatusCode <= 299 && ruleNeedsLinkAuditSuccessBody(rule))
		if !readBody {
			return LinkAuditObservation{HTTPStatus: get.StatusCode}, nil
		}
		body, err := io.ReadAll(io.LimitReader(get.Body, bodyLimit+1))
		if err != nil {
			return LinkAuditObservation{}, err
		}
		if int64(len(body)) > bodyLimit {
			body = body[:bodyLimit]
		}
		return LinkAuditObservation{HTTPStatus: get.StatusCode, Body: body}, nil
	}
	return LinkAuditObservation{HTTPStatus: status}, nil
}

func doLinkAuditRequest(ctx context.Context, client LinkAuditHTTPClient, method, rawURL string, maxBodyBytes int64) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if method == http.MethodGet {
		req.Header.Set("Accept-Encoding", "identity")
		if maxBodyBytes > 0 {
			req.Header.Set("Range", "bytes=0-"+strconv.FormatInt(maxBodyBytes-1, 10))
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.Body == nil {
		resp.Body = http.NoBody
	}
	return resp, nil
}

func ruleNeedsLinkAuditBody(rule *LinkAuditRule) bool {
	return ruleNeedsLinkAuditDeadBody(rule) || ruleNeedsLinkAuditSuccessBody(rule)
}

func ruleNeedsLinkAuditDeadBody(rule *LinkAuditRule) bool {
	return rule != nil && len(rule.deadBodyRegexps) > 0
}

func ruleNeedsLinkAuditSuccessBody(rule *LinkAuditRule) bool {
	return rule != nil && (len(rule.mismatchBodyRegexps) > 0 || len(rule.requiredBodyRegexps) > 0)
}

func classifyLinkAuditTransportError(err error) LinkAuditTransportError {
	if err == nil {
		return LinkAuditTransportNone
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return LinkAuditTransportTimeout
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return LinkAuditTransportDNS
	}
	var tlsRecord tls.RecordHeaderError
	if errors.As(err, &tlsRecord) {
		return LinkAuditTransportTLS
	}
	var tlsVerification *tls.CertificateVerificationError
	if errors.As(err, &tlsVerification) {
		return LinkAuditTransportTLS
	}
	var tlsUnknown x509.UnknownAuthorityError
	if errors.As(err, &tlsUnknown) {
		return LinkAuditTransportTLS
	}
	var tlsHostname x509.HostnameError
	if errors.As(err, &tlsHostname) {
		return LinkAuditTransportTLS
	}
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		return LinkAuditTransportConnection
	}
	return LinkAuditTransportOther
}

func minLinkAuditDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
