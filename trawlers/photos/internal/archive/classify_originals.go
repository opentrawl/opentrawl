package archive

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
)

const defaultOriginalsCacheMaxBytes int64 = 4 * 1024 * 1024 * 1024

var exportOriginalResource = photos.ExportOriginalResourceThroughApp

// originalsCache keeps recent PhotoKit originals across runs. Exports are
// serial, active files cannot be evicted, and least-recently-used inactive
// files are removed after the fixed byte budget is exceeded.
type originalsCache struct {
	root     string
	maxBytes int64
	mu       sync.Mutex
	active   map[string]int
}

type originalLease struct {
	cache *originalsCache
	path  string
	once  sync.Once
}

type originalExportResult struct {
	path       string
	pathClass  string
	bytes      int64
	duration   time.Duration
	lease      *originalLease
	downloaded bool
	err        error
}

type originalCacheEntry struct {
	path    string
	size    int64
	modTime time.Time
}

func newOriginalsCache(root string) (*originalsCache, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("originals cache path is required")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("create originals cache: %w", err)
	}
	cache := &originalsCache{
		root:     root,
		maxBytes: defaultOriginalsCacheMaxBytes,
		active:   map[string]int{},
	}
	cache.mu.Lock()
	err := cache.pruneLocked()
	cache.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return cache, nil
}

func (c *originalsCache) export(ctx context.Context, input classifyInput) originalExportResult {
	startedAt := time.Now()
	if strings.TrimSpace(input.LocalIdentifier) == "" {
		return originalExportResult{err: errors.New("asset local identifier is required for original export"), duration: time.Since(startedAt)}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	path := c.path(input)
	if info, _, err := photos.InspectOriginalFile(path); err == nil {
		_ = os.Chtimes(path, time.Now(), time.Now())
		return originalExportResult{
			path:      path,
			pathClass: "cached_photokit_original",
			bytes:     info.Size(),
			duration:  time.Since(startedAt),
			lease:     c.acquireLocked(path),
		}
	}
	_ = os.Remove(path)
	result := originalExportResult{path: path, pathClass: "photokit_original_export"}
	err := exportOriginalResource(ctx, input.originalExportQuery(), path, true)
	result.duration = time.Since(startedAt)
	if err != nil {
		result.err = err
		_ = os.Remove(path)
		_ = os.Remove(path + ".exporting")
		return result
	}
	info, _, err := photos.InspectOriginalFile(path)
	if err != nil {
		result.err = fmt.Errorf("inspect exported original: %w", err)
		_ = os.Remove(path)
		return result
	}
	if info.Size() > c.maxBytes {
		result.err = fmt.Errorf("exported original exceeds cache budget: %d bytes", info.Size())
		_ = os.Remove(path)
		return result
	}
	result.bytes = info.Size()
	result.downloaded = true
	result.lease = c.acquireLocked(path)
	if err := c.pruneLocked(); err != nil {
		c.releaseLocked(path)
		result.lease = nil
		_ = os.Remove(path)
		result.err = err
		return result
	}
	return result
}

func (l *originalLease) Close() {
	if l == nil || l.cache == nil {
		return
	}
	l.once.Do(func() {
		l.cache.mu.Lock()
		defer l.cache.mu.Unlock()
		l.cache.releaseLocked(l.path)
		_ = l.cache.pruneLocked()
	})
}

func (c *originalsCache) path(input classifyInput) string {
	return photos.OriginalCachePath(c.root, input.ModificationDate, input.originalExportQuery())
}

func (c *originalsCache) acquireLocked(path string) *originalLease {
	c.active[path]++
	return &originalLease{cache: c, path: path}
}

func (c *originalsCache) releaseLocked(path string) {
	if c.active[path] <= 1 {
		delete(c.active, path)
		return
	}
	c.active[path]--
}

func (c *originalsCache) pruneLocked() error {
	entries, err := os.ReadDir(c.root)
	if err != nil {
		return fmt.Errorf("read originals cache: %w", err)
	}
	files := make([]originalCacheEntry, 0, len(entries))
	var total int64
	for _, entry := range entries {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect originals cache: %w", err)
		}
		if !info.Mode().IsRegular() {
			continue
		}
		path := filepath.Join(c.root, entry.Name())
		files = append(files, originalCacheEntry{path: path, size: info.Size(), modTime: info.ModTime()})
		total += info.Size()
	}
	sort.Slice(files, func(i, j int) bool {
		if !files[i].modTime.Equal(files[j].modTime) {
			return files[i].modTime.Before(files[j].modTime)
		}
		return files[i].path < files[j].path
	})
	for _, file := range files {
		if total <= c.maxBytes {
			break
		}
		if c.active[file.path] > 0 {
			continue
		}
		if err := os.Remove(file.path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("evict cached original: %w", err)
		}
		total -= file.size
	}
	return nil
}

func (input classifyInput) originalExportQuery() photos.OriginalExportQuery {
	resource := input.preferredOriginalResource()
	return photos.OriginalExportQuery{
		LocalIdentifier:  input.LocalIdentifier,
		CreationDate:     input.CreationDate,
		Width:            input.Width,
		Height:           input.Height,
		OriginalFilename: resource.OriginalFilename,
		OriginalUTI:      resource.UTI,
	}
}

func (input classifyInput) preferredOriginalResource() classifyResource {
	for _, resource := range input.Resources {
		if resource.NeedsDownload && isOriginalResource(resource) {
			return resource
		}
	}
	for _, resource := range input.Resources {
		if isOriginalResource(resource) {
			return resource
		}
	}
	return classifyResource{}
}

func isOriginalResource(resource classifyResource) bool {
	return photos.IsOriginalExtension(filepath.Ext(resource.OriginalFilename)) || photos.IsOriginalUTI(resource.UTI)
}
