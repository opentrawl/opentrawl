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

var exportOriginalResource = photos.ExportOriginalResourceMatching

// originalsCache is a per-run scratch directory. Disk usage is bounded by the
// pipeline shape itself: downloads are serial, every file is deleted as soon
// as its card is written, and the whole run directory is removed on Close.
type originalsCache struct {
	root   string
	runDir string
}

type originalLease struct {
	path string
	once sync.Once
}

type originalExportResult struct {
	path      string
	pathClass string
	bytes     int64
	duration  time.Duration
	lease     *originalLease
	attempts  int
	err       error
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
	return &originalsCache{root: root, runDir: runDir}, nil
}

func (c *originalsCache) export(ctx context.Context, input classifyInput) originalExportResult {
	startedAt := time.Now()
	if strings.TrimSpace(input.LocalIdentifier) == "" {
		return originalExportResult{err: errors.New("asset local identifier is required for original export"), duration: time.Since(startedAt)}
	}
	path := filepath.Join(c.runDir, safePathComponent(input.AssetID)+originalExtension(input))
	lease := &originalLease{path: path}
	result := originalExportResult{path: path, pathClass: "downloaded_original", lease: lease}
	var err error
	for attempt := 1; attempt <= 2; attempt++ {
		result.attempts = attempt
		err = exportOriginalResource(ctx, input.originalExportQuery(), path, true)
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
	info, err := os.Stat(path)
	if err != nil {
		result.err = fmt.Errorf("stat exported original: %w", err)
		lease.Close()
		return result
	}
	result.bytes = info.Size()
	return result
}

func (c *originalsCache) Close() {
	_ = os.RemoveAll(c.runDir)
}

func (l *originalLease) Close() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		_ = os.Remove(l.path)
	})
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
