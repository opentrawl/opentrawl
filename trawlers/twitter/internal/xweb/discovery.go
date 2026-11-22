package xweb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	defaultPublicWebBearerToken = "AAAAAAAAAAAAAAAAAAAAANRILgAAAAAAnNwIzUejRCOuH5E6I8xnZz4puTs%3D1Zv7ttfk8LF81IUq16cHjhLTvJu4FA33AGWWjCpTnA"
	defaultQueryIDCacheTTL      = 24 * time.Hour
	maxDiscoveryBundles         = 16
	maxDiscoveryBytes           = 16 * 1024 * 1024
	maxSingleBundleBytes        = 8 * 1024 * 1024
)

var (
	fallbackGraphQLQueryIDs = map[string][]string{
		"Bookmarks":      {"RV1g3b8n_SGOHwkqKYSCFw", "tmd4ifV8RHltzn8ymGg1aw"},
		"Likes":          {"JR2gceKucIKcVNB_9JkhsA"},
		"UserTweets":     {"Wms1GvIiHXAPBaCr9KblaA"},
		"SearchTimeline": {"M1jEez78PEfVfbQLvlWMvQ", "5h0kNbk3ii97rmfY6CdgAA", "Tp1sewRU1AsZpBWhqCZicQ"},
	}

	defaultViewerProvidedVariables = []string{
		"__relay_internal__pv__IsClientUserInGrokImagineUpsellExperiment",
		"__relay_internal__pv__IsClientUserInGrokImagineUpsellV2Experiment",
	}

	discoveryPages = []string{
		"https://x.com",
		"https://x.com/?lang=en",
		"https://x.com/explore",
		"https://x.com/settings/profile",
	}

	absScriptURLPattern = regexp.MustCompile(`https://abs\.twimg\.com/(?:x-web|responsive-web)/[^"'<>\s]+\.js`)
	bearerTokenPattern  = regexp.MustCompile(`\bAAAAA[A-Za-z0-9%]{40,250}`)
	viewerQueryPattern  = regexp.MustCompile(`(?s)params:\{id:["'\x60]([A-Za-z0-9_-]{10,100})["'\x60].{0,250}?name:["'\x60]viewerQuery["'\x60](.{0,250}?providedVariables:\{([^}]{0,900})\})?`)
	relayVarPattern     = regexp.MustCompile(`__relay_internal__pv__[A-Za-z0-9_]+`)
	jsImportPattern     = regexp.MustCompile(`\b(?:from\s*|import\s*\(?\s*)["'\x60]([^"'\x60]+\.js)["'\x60]`)
	queryIDValidPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{10,100}$`)

	graphqlOpPatterns = []struct {
		pattern *regexp.Regexp
		opIdx   int
		idIdx   int
	}{
		{
			pattern: regexp.MustCompile(`e\.exports=\{queryId\s*:\s*["']([^"']+)["']\s*,\s*operationName\s*:\s*["']([^"']+)["']`),
			opIdx:   2,
			idIdx:   1,
		},
		{
			pattern: regexp.MustCompile(`e\.exports=\{operationName\s*:\s*["']([^"']+)["']\s*,\s*queryId\s*:\s*["']([^"']+)["']`),
			opIdx:   1,
			idIdx:   2,
		},
		{
			pattern: regexp.MustCompile(`operationName\s*[:=]\s*["'](Bookmarks|Likes|UserTweets|SearchTimeline)["'][^}]{0,200}?queryId\s*[:=]\s*["']([^"']+)["']`),
			opIdx:   1,
			idIdx:   2,
		},
		{
			pattern: regexp.MustCompile(`queryId\s*[:=]\s*["']([^"']+)["'][^}]{0,200}?operationName\s*[:=]\s*["'](Bookmarks|Likes|UserTweets|SearchTimeline)["']`),
			opIdx:   2,
			idIdx:   1,
		},
	}
)

type discoveredWebClientMetadata struct {
	FetchedAt               time.Time         `json:"fetched_at"`
	BearerToken             string            `json:"bearer_token"`
	ViewerQueryID           string            `json:"viewer_query_id,omitempty"`
	ViewerProvidedVariables []string          `json:"viewer_provided_variables,omitempty"`
	QueryIDs                map[string]string `json:"query_ids"`
}

func defaultDiscoveryCachePath(sessionPath string) string {
	if trimmed := strings.TrimSpace(sessionPath); trimmed != "" {
		return filepath.Join(filepath.Dir(trimmed), "web_query_ids.json")
	}
	return filepath.Join(filepath.Dir(DefaultWebSessionPath("")), "web_query_ids.json")
}

func (c *Client) ensureWebMetadata(ctx context.Context, forceRefresh bool) (discoveredWebClientMetadata, error) {
	if !forceRefresh && c.metadataLoaded && c.now().Sub(c.metadata.FetchedAt) < defaultQueryIDCacheTTL {
		return c.metadata, nil
	}
	if !forceRefresh && c.cachePath != "" {
		if cached, ok := readDiscoveryCache(c.cachePath); ok && c.now().Sub(cached.FetchedAt) < defaultQueryIDCacheTTL {
			c.metadata = cached
			c.metadataLoaded = true
			return c.metadata, nil
		}
	}
	discovered, err := c.discoverFromXBundles(ctx)
	if err != nil {
		if c.metadataLoaded {
			return c.metadata, nil
		}
		fallback := fallbackMetadata(c.now())
		c.metadata = fallback
		c.metadataLoaded = true
		return fallback, err
	}
	c.metadata = discovered
	c.metadataLoaded = true
	if c.cachePath != "" {
		_ = writeDiscoveryCache(c.cachePath, discovered)
	}
	return discovered, nil
}

func fallbackMetadata(now time.Time) discoveredWebClientMetadata {
	ids := make(map[string]string, len(fallbackGraphQLQueryIDs))
	for op, list := range fallbackGraphQLQueryIDs {
		if len(list) > 0 {
			ids[op] = list[0]
		}
	}
	return discoveredWebClientMetadata{
		FetchedAt:               now.UTC(),
		BearerToken:             defaultPublicWebBearerToken,
		ViewerProvidedVariables: append([]string(nil), defaultViewerProvidedVariables...),
		QueryIDs:                ids,
	}
}

func (c *Client) queryIDCandidates(ctx context.Context, operation string) []string {
	meta, _ := c.ensureWebMetadata(ctx, false)
	seen := map[string]struct{}{}
	var out []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if id, ok := meta.QueryIDs[operation]; ok {
		add(id)
	}
	for _, fallbackID := range fallbackGraphQLQueryIDs[operation] {
		add(fallbackID)
	}
	return out
}

func (c *Client) bearerToken(ctx context.Context) string {
	meta, _ := c.ensureWebMetadata(ctx, false)
	if strings.TrimSpace(meta.BearerToken) != "" {
		return meta.BearerToken
	}
	return defaultPublicWebBearerToken
}

func (c *Client) discoverFromXBundles(ctx context.Context) (discoveredWebClientMetadata, error) {
	result := fallbackMetadata(c.now())
	queue := c.discoverInitialBundleQueue(ctx)
	if len(queue) == 0 {
		return result, errors.New("no X web client bundles discovered")
	}

	seen := map[string]struct{}{}
	remainingBytes := maxDiscoveryBytes
	targets := map[string]bool{
		"Bookmarks":      true,
		"Likes":          true,
		"UserTweets":     true,
		"SearchTimeline": true,
	}
	foundOps := map[string]bool{}
	foundBearer := false

	for inspected := 0; len(queue) > 0 && inspected < maxDiscoveryBundles && remainingBytes > 0; inspected++ {
		scriptURL := queue[0]
		queue = queue[1:]
		if _, already := seen[scriptURL]; already {
			continue
		}
		seen[scriptURL] = struct{}{}
		source, err := c.fetchPublicText(ctx, scriptURL, min(maxSingleBundleBytes, remainingBytes))
		if err != nil {
			continue
		}
		remainingBytes -= len(source)

		foundBearer = extractMetadataFromBundle(source, &result, targets, foundOps, foundBearer)
		if foundBearer && result.ViewerQueryID != "" && len(foundOps) == len(targets) {
			break
		}
		queue = appendImportedBundleURLs(queue, scriptURL, source, seen)
	}
	return result, nil
}

func (c *Client) discoverInitialBundleQueue(ctx context.Context) []string {
	bundleSet := map[string]struct{}{}
	for _, pageURL := range discoveryPages {
		html, err := c.fetchPublicText(ctx, pageURL, 4*1024*1024)
		if err != nil {
			continue
		}
		for _, match := range absScriptURLPattern.FindAllString(html, -1) {
			if isAllowedAbsTwimgScript(match) {
				bundleSet[match] = struct{}{}
			}
		}
	}
	queue := make([]string, 0, len(bundleSet))
	for bundleURL := range bundleSet {
		queue = append(queue, bundleURL)
	}
	sortBundlesForDiscovery(queue)
	return queue
}

func extractMetadataFromBundle(
	source string,
	result *discoveredWebClientMetadata,
	targets map[string]bool,
	foundOps map[string]bool,
	foundBearer bool,
) bool {
	if !foundBearer {
		if token := bearerTokenPattern.FindString(source); token != "" && validBearerToken(token) {
			result.BearerToken = token
			foundBearer = true
		}
	}
	if result.ViewerQueryID == "" {
		extractViewerQueryMetadata(source, result)
	}
	extractGraphQLOperationIDs(source, result, targets, foundOps)
	return foundBearer
}

func extractViewerQueryMetadata(source string, result *discoveredWebClientMetadata) {
	m := viewerQueryPattern.FindStringSubmatch(source)
	if len(m) < 2 || !queryIDValidPattern.MatchString(m[1]) {
		return
	}
	result.ViewerQueryID = m[1]
	if len(m) >= 4 {
		if vars := uniqueStrings(relayVarPattern.FindAllString(m[3], -1)); len(vars) > 0 {
			result.ViewerProvidedVariables = vars
		}
	}
}

func extractGraphQLOperationIDs(source string, result *discoveredWebClientMetadata, targets, foundOps map[string]bool) {
	for _, spec := range graphqlOpPatterns {
		for _, m := range spec.pattern.FindAllStringSubmatch(source, -1) {
			if len(m) <= max(spec.opIdx, spec.idIdx) {
				continue
			}
			op, id := m[spec.opIdx], m[spec.idIdx]
			if targets[op] && queryIDValidPattern.MatchString(id) && !foundOps[op] {
				result.QueryIDs[op] = id
				foundOps[op] = true
			}
		}
	}
}

func appendImportedBundleURLs(queue []string, scriptURL, source string, seen map[string]struct{}) []string {
	baseURL, err := url.Parse(scriptURL)
	if err != nil {
		return queue
	}
	added := false
	for _, dep := range jsImportPattern.FindAllStringSubmatch(source, -1) {
		if len(dep) < 2 {
			continue
		}
		resolved, err := baseURL.Parse(dep[1])
		if err != nil {
			continue
		}
		resolvedStr := resolved.String()
		if !isAllowedAbsTwimgScript(resolvedStr) {
			continue
		}
		if _, already := seen[resolvedStr]; !already {
			queue = append(queue, resolvedStr)
			added = true
		}
	}
	if added {
		sortBundlesForDiscovery(queue)
	}
	return queue
}

func (c *Client) fetchPublicText(ctx context.Context, rawURL string, maxBytes int) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "text/html,application/javascript,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxBytes)))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func isAllowedAbsTwimgScript(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if u.Scheme != "https" || u.Host != "abs.twimg.com" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	return (strings.HasPrefix(u.Path, "/x-web/") || strings.HasPrefix(u.Path, "/responsive-web/")) && strings.HasSuffix(u.Path, ".js")
}

func sortBundlesForDiscovery(bundles []string) {
	score := func(s string) int {
		lower := strings.ToLower(s)
		switch {
		case strings.Contains(lower, "main."):
			return 0
		case strings.Contains(lower, "auth") || strings.Contains(lower, "token") || strings.Contains(lower, "viewer") || strings.Contains(lower, "headers"):
			return 1
		case strings.Contains(lower, "bookmark") || strings.Contains(lower, "profile") || strings.Contains(lower, "search"):
			return 2
		default:
			return 3
		}
	}
	sort.SliceStable(bundles, func(i, j int) bool {
		si, sj := score(bundles[i]), score(bundles[j])
		if si != sj {
			return si < sj
		}
		return bundles[i] < bundles[j]
	})
}

func validBearerToken(token string) bool {
	if len(token) < 40 || len(token) > 260 {
		return false
	}
	for i := 0; i < len(token); i++ {
		ch := token[i]
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '%' {
			continue
		}
		return false
	}
	return true
}

func uniqueStrings(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

func readDiscoveryCache(path string) (discoveredWebClientMetadata, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return discoveredWebClientMetadata{}, false
	}
	var cached discoveredWebClientMetadata
	if err := json.Unmarshal(raw, &cached); err != nil {
		return discoveredWebClientMetadata{}, false
	}
	if cached.FetchedAt.IsZero() || len(cached.QueryIDs) == 0 {
		return discoveredWebClientMetadata{}, false
	}
	return cached, true
}

func writeDiscoveryCache(path string, meta discoveredWebClientMetadata) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".web_query_ids-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(encoded, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
