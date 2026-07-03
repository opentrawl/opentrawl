package archive

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/openclaw/photoscrawl/internal/photos"
)

const (
	defaultOriginalsCacheMaxBytes int64 = 4 * 1024 * 1024 * 1024
	downloadConcurrency                 = 1
)

var exportOriginalResource = photos.ExportOriginalResourceMatching

type originalsCache struct {
	root      string
	runDir    string
	maxBytes  int64
	mu        sync.Mutex
	reserved  int64
	used      int64
	highWater int64
	closed    bool
}

type originalLease struct {
	cache *originalsCache
	path  string
	size  int64
	once  sync.Once
}

type originalExportResult struct {
	path          string
	pathClass     string
	bytes         int64
	duration      time.Duration
	lease         *originalLease
	attempts      int
	err           error
	cacheHighMark int64
}

func newOriginalsCache(root, runID string) (*originalsCache, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("originals cache path is required")
	}
	runDir := filepath.Join(root, safePathComponent(runID))
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return nil, fmt.Errorf("create originals cache: %w", err)
	}
	return &originalsCache{
		root:     root,
		runDir:   runDir,
		maxBytes: defaultOriginalsCacheMaxBytes,
	}, nil
}

func (c *originalsCache) export(ctx context.Context, input classifyInput) originalExportResult {
	startedAt := time.Now()
	lease, err := c.reserve(input)
	if err != nil {
		return originalExportResult{err: err, duration: time.Since(startedAt)}
	}
	result := originalExportResult{path: lease.path, pathClass: "downloaded_original", lease: lease}
	for attempt := 1; attempt <= 2; attempt++ {
		result.attempts = attempt
		err = exportOriginalResource(ctx, input.originalExportQuery(), lease.path, true)
		if err == nil {
			break
		}
		if attempt == 1 {
			select {
			case <-ctx.Done():
				err = ctx.Err()
			case <-time.After(500 * time.Millisecond):
			}
		}
	}
	result.duration = time.Since(startedAt)
	if err != nil {
		result.err = err
		lease.Close()
		return result
	}
	info, err := os.Stat(lease.path)
	if err != nil {
		result.err = fmt.Errorf("stat exported original: %w", err)
		lease.Close()
		return result
	}
	result.bytes = info.Size()
	if err := c.commit(lease, result.bytes); err != nil {
		result.err = err
		lease.Close()
		return result
	}
	result.cacheHighMark = c.HighWater()
	return result
}

func (c *originalsCache) reserve(input classifyInput) (*originalLease, error) {
	if strings.TrimSpace(input.LocalIdentifier) == "" {
		return nil, errors.New("asset local identifier is required for original export")
	}
	expectedBytes := input.expectedOriginalBytes()
	if expectedBytes <= 0 {
		return nil, errors.New("original byte size is unknown; refusing download under fixed cache budget")
	}
	if expectedBytes > c.maxBytes {
		return nil, fmt.Errorf("original is larger than cache budget: %d bytes", expectedBytes)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, errors.New("originals cache is closed")
	}
	if c.reserved+expectedBytes > c.maxBytes {
		return nil, fmt.Errorf("originals cache budget exceeded: %d of %d bytes reserved", c.reserved, c.maxBytes)
	}
	c.reserved += expectedBytes
	path := filepath.Join(c.runDir, safePathComponent(input.AssetID)+originalExtension(input))
	return &originalLease{cache: c, path: path, size: expectedBytes}, nil
}

func (c *originalsCache) commit(lease *originalLease, actualBytes int64) error {
	if lease == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reserved -= lease.size
	if c.reserved < 0 {
		c.reserved = 0
	}
	if actualBytes > c.maxBytes-c.used {
		return fmt.Errorf("exported original exceeds cache budget: %d bytes with %d of %d bytes used", actualBytes, c.used, c.maxBytes)
	}
	c.used += actualBytes
	lease.size = actualBytes
	if c.used > c.highWater {
		c.highWater = c.used
	}
	return nil
}

func (c *originalsCache) release(path string, size int64) {
	_ = os.Remove(path)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.used -= size
	if c.used < 0 {
		c.used = 0
	}
}

func (c *originalsCache) Close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	_ = os.RemoveAll(c.runDir)
}

func (c *originalsCache) HighWater() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.highWater
}

func (c *originalsCache) MaxBytes() int64 {
	return c.maxBytes
}

func (l *originalLease) Close() {
	if l == nil || l.cache == nil {
		return
	}
	l.once.Do(func() {
		l.cache.release(l.path, l.size)
	})
}

func (input classifyInput) expectedOriginalBytes() int64 {
	var best int64
	for _, resource := range input.Resources {
		if !resource.NeedsDownload || !classifiableResourceText(resource) {
			continue
		}
		if best == 0 || resource.FileSize < best {
			best = resource.FileSize
		}
	}
	return best
}

func (input classifyInput) originalExportQuery() photos.OriginalExportQuery {
	return photos.OriginalExportQuery{
		LocalIdentifier:  input.LocalIdentifier,
		CreationDate:     input.CreationDate,
		Width:            input.Width,
		Height:           input.Height,
		OriginalFilename: input.preferredOriginalFilename(),
	}
}

func (input classifyInput) preferredOriginalFilename() string {
	for _, resource := range input.Resources {
		if resource.NeedsDownload && classifiableResourceText(resource) {
			return resource.OriginalFilename
		}
	}
	for _, resource := range input.Resources {
		if classifiableResourceText(resource) {
			return resource.OriginalFilename
		}
	}
	return ""
}

func originalExtension(input classifyInput) string {
	for _, resource := range input.Resources {
		if !classifiableResourceText(resource) {
			continue
		}
		if ext := strings.ToLower(filepath.Ext(resource.OriginalFilename)); classifiableImagePath("x" + ext) {
			return ext
		}
		switch strings.ToLower(resource.UTI) {
		case "public.jpeg", "jpeg", "jpg":
			return ".jpg"
		case "public.png", "png":
			return ".png"
		case "public.heic", "heic":
			return ".heic"
		}
	}
	return ".jpg"
}

func classifiableResourceText(resource classifyResource) bool {
	value := strings.ToLower(strings.Join([]string{resource.ResourceType, resource.UTI, resource.OriginalFilename}, " "))
	return strings.Contains(value, "image") ||
		strings.Contains(value, "photo") ||
		strings.Contains(value, "jpeg") ||
		strings.Contains(value, "jpg") ||
		strings.Contains(value, "png") ||
		strings.Contains(value, "heic")
}

func safePathComponent(value string) string {
	value = strings.TrimSpace(value)
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		default:
			builder.WriteByte('_')
		}
	}
	out := strings.Trim(builder.String(), "_")
	if out == "" {
		return "run"
	}
	return out
}
