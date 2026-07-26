package courses

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

type LinkEnrichmentStats struct {
	Candidates   int
	SkippedFresh int
	Fetched      int
	Extracted    int
	NotFound     int
	Failed       int
}

type linkEnrichmentCandidate struct {
	canonicalURL string
	host         string
	hash         string
}

type linkEnrichmentOutcome struct {
	hash    string
	state   string
	content LinkContent
}

func EnrichCatalogLinks(
	ctx context.Context,
	catalog Catalog,
	policy *LinkEnrichmentPolicy,
	previous *LinkEnrichmentCache,
	client LinkAuditHTTPClient,
	now time.Time,
	refresh bool,
	concurrency int,
) (*LinkEnrichmentCache, LinkEnrichmentStats, error) {
	var stats LinkEnrichmentStats
	if policy == nil {
		return nil, stats, errors.New("enrich catalog links: policy is nil")
	}
	if client == nil {
		return nil, stats, errors.New("enrich catalog links: client is nil")
	}
	if concurrency < 1 {
		return nil, stats, errors.New("enrich catalog links: concurrency must be positive")
	}
	if err := ctx.Err(); err != nil {
		return nil, stats, err
	}

	candidates := collectLinkEnrichmentCandidates(catalog, policy)
	stats.Candidates = len(candidates)
	pending := make([]linkEnrichmentCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if !refresh && previous != nil &&
			previous.IsFreshURL(candidate.canonicalURL, now, policy.StaleAfter) {
			stats.SkippedFresh++
			continue
		}
		pending = append(pending, candidate)
	}

	outcomes := fetchLinkEnrichmentCandidates(ctx, pending, policy, client, concurrency)
	for _, outcome := range outcomes {
		stats.Fetched++
		switch outcome.state {
		case linkEnrichmentStateExtracted:
			stats.Extracted++
		case linkEnrichmentStateNotFound:
			stats.NotFound++
		default:
			stats.Failed++
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, stats, err
	}

	next := cloneLinkEnrichmentCache(previous, now)
	sort.Slice(outcomes, func(i, j int) bool {
		return outcomes[i].hash < outcomes[j].hash
	})
	for _, outcome := range outcomes {
		if err := ctx.Err(); err != nil {
			return nil, stats, err
		}
		var err error
		switch outcome.state {
		case linkEnrichmentStateExtracted:
			err = next.PutExtracted(outcome.hash, outcome.content, now)
		case linkEnrichmentStateNotFound:
			err = next.PutNotFound(outcome.hash, now)
		}
		if err != nil {
			return nil, stats, errors.New("enrich catalog links: apply result")
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, stats, err
	}
	next.GeneratedAt = now.UTC().Format(time.RFC3339)
	sortLinkEnrichmentEntries(next.Entries)
	next.reindex()
	if err := ctx.Err(); err != nil {
		return nil, stats, err
	}
	return next, stats, nil
}

func collectLinkEnrichmentCandidates(
	catalog Catalog,
	policy *LinkEnrichmentPolicy,
) []linkEnrichmentCandidate {
	byHash := make(map[string]linkEnrichmentCandidate)
	for _, entry := range catalog.Entries {
		for _, link := range entry.Links {
			parsed, canonical, rejection := inspectLink(link.URL)
			if rejection != "" || parsed == nil ||
				(!strings.EqualFold(parsed.Scheme, "http") &&
					!strings.EqualFold(parsed.Scheme, "https")) {
				continue
			}
			host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
			if policy.matchingRule(host) == nil {
				continue
			}
			hash, err := linkHash(canonical)
			if err != nil {
				continue
			}
			if _, exists := byHash[hash]; exists {
				continue
			}
			byHash[hash] = linkEnrichmentCandidate{
				canonicalURL: canonical,
				host:         host,
				hash:         hash,
			}
		}
	}
	candidates := make([]linkEnrichmentCandidate, 0, len(byHash))
	for _, candidate := range byHash {
		candidates = append(candidates, candidate)
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].hash < candidates[j].hash
	})
	return candidates
}

func fetchLinkEnrichmentCandidates(
	ctx context.Context,
	candidates []linkEnrichmentCandidate,
	policy *LinkEnrichmentPolicy,
	client LinkAuditHTTPClient,
	concurrency int,
) []linkEnrichmentOutcome {
	if len(candidates) == 0 {
		return nil
	}
	workerCount := min(concurrency, len(candidates))
	jobs := make(chan linkEnrichmentCandidate, len(candidates))
	results := make(chan linkEnrichmentOutcome, len(candidates))
	for _, candidate := range candidates {
		jobs <- candidate
	}
	close(jobs)

	var workers sync.WaitGroup
	workers.Add(workerCount)
	for range workerCount {
		go func() {
			defer workers.Done()
			for candidate := range jobs {
				if ctx.Err() != nil {
					return
				}
				results <- fetchLinkEnrichmentCandidate(ctx, candidate, policy, client)
			}
		}()
	}
	workers.Wait()
	close(results)

	outcomes := make([]linkEnrichmentOutcome, 0, len(results))
	for result := range results {
		outcomes = append(outcomes, result)
	}
	return outcomes
}

func fetchLinkEnrichmentCandidate(
	ctx context.Context,
	candidate linkEnrichmentCandidate,
	policy *LinkEnrichmentPolicy,
	client LinkAuditHTTPClient,
) linkEnrichmentOutcome {
	failed := linkEnrichmentOutcome{hash: candidate.hash}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, candidate.canonicalURL, nil)
	if err != nil {
		return failed
	}
	request.Header.Set(
		"Range",
		fmt.Sprintf("bytes=0-%d", policy.MaxBodyBytes-1),
	)
	request.Header.Set("Accept-Encoding", "identity")
	response, err := client.Do(request)
	if err != nil {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return failed
	}
	if response == nil || response.Body == nil {
		return failed
	}

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		_ = response.Body.Close()
		return failed
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, policy.MaxBodyBytes+1))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || int64(len(body)) > policy.MaxBodyBytes {
		return failed
	}
	content, found, err := policy.Extract(candidate.host, body)
	if err != nil {
		return failed
	}
	if !found {
		return linkEnrichmentOutcome{
			hash:  candidate.hash,
			state: linkEnrichmentStateNotFound,
		}
	}
	if content == nil {
		return failed
	}
	if err := validateCachedLinkContent(*content); err != nil {
		return failed
	}
	return linkEnrichmentOutcome{
		hash:    candidate.hash,
		state:   linkEnrichmentStateExtracted,
		content: cloneLinkContent(*content),
	}
}

func cloneLinkEnrichmentCache(
	previous *LinkEnrichmentCache,
	now time.Time,
) *LinkEnrichmentCache {
	next := NewLinkEnrichmentCache(now)
	if previous == nil {
		return next
	}
	next.Entries = make([]LinkEnrichmentEntry, len(previous.Entries))
	for index, entry := range previous.Entries {
		next.Entries[index] = entry
		if entry.Content != nil {
			content := cloneLinkContent(*entry.Content)
			next.Entries[index].Content = &content
		}
	}
	next.reindex()
	return next
}
