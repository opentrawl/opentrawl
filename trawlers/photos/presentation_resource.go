package photos

import (
	"context"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/photos/internal/photos"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	"github.com/opentrawl/opentrawl/trawlkit/presentation"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
)

var _ trawlkit.ResourceResolver = (*Crawler)(nil)

const presentationResourcePrefix = "photos:resource/"
const presentationCurrentResourcePrefix = presentationResourcePrefix + "current/"

type presentationResourceCandidate struct {
	ref         string
	path        string
	contentType string
	label       string
	size        int64
}

func (c *Crawler) presentationResource(ctx context.Context, req *trawlkit.Request, openRef string) (*presentationv1.Resource, error) {
	assetID := archive.AssetID(openRef)
	if assetID == "" || req == nil || req.Store == nil {
		return nil, nil
	}
	candidate, err := c.localPresentationResource(ctx, req, assetID)
	if err != nil || candidate == nil {
		if err != nil {
			return nil, err
		}
		candidate, err = c.cachedCurrentPresentationResource(ctx, req, assetID)
		if err != nil || candidate == nil {
			return nil, err
		}
	}
	return presentationResourceBlock(*candidate), nil
}

func (c *Crawler) ResolveResource(ctx context.Context, req *trawlkit.Request, request *presentationv1.ResourceRequest) (*presentationv1.ResourceResponse, error) {
	if err := openrecord.ValidateResourceRequest(request); err != nil {
		return nil, err
	}
	if request.GetSourceId() != c.Info().ID || !strings.HasPrefix(request.GetResourceRef(), presentationResourcePrefix) {
		return nil, errors.New("resource ref is outside the photos namespace")
	}
	if req == nil || req.Store == nil {
		return nil, errors.New("photos archive is unavailable")
	}
	id := strings.TrimSpace(strings.TrimPrefix(request.GetResourceRef(), presentationResourcePrefix))
	if id == "" {
		return nil, errors.New("resource ref is invalid")
	}
	var candidate *presentationResourceCandidate
	var err error
	if strings.HasPrefix(request.GetResourceRef(), presentationCurrentResourcePrefix) {
		assetID, decodeErr := decodePresentationCurrentResourceRef(request.GetResourceRef())
		if decodeErr != nil {
			return nil, decodeErr
		}
		candidate, err = c.cachedCurrentPresentationResource(ctx, req, assetID)
	} else {
		candidate, err = c.localPresentationResourceByID(ctx, req, id)
	}
	if err != nil {
		return nil, err
	}
	if candidate == nil {
		return nil, errors.New("resource is unavailable")
	}
	return resolvePresentationResource(candidate.path, candidate.contentType, request)
}

func (c *Crawler) localPresentationResource(ctx context.Context, req *trawlkit.Request, assetID string) (*presentationResourceCandidate, error) {
	rows, err := req.Store.DB().QueryContext(ctx, `
select id, uti, original_filename, local_path
from asset_resource
where asset_id = ? and available_locally = 1 and needs_download = 0
order by case when lower(resource_type) = 'local_original' then 0 when lower(resource_type) in ('photo', 'image') then 1 else 2 end, id`, assetID)
	if err != nil {
		return nil, fmt.Errorf("select local presentation resource: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, uti, filename, path string
		if err := rows.Scan(&id, &uti, &filename, &path); err != nil {
			return nil, fmt.Errorf("scan local presentation resource: %w", err)
		}
		if candidate, ok := localPresentationResourceCandidate(presentationResourcePrefix+id, path, uti, filename); ok {
			return &candidate, nil
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read local presentation resource: %w", err)
	}
	return nil, nil
}

func (c *Crawler) localPresentationResourceByID(ctx context.Context, req *trawlkit.Request, id string) (*presentationResourceCandidate, error) {
	var uti, filename, path string
	err := req.Store.DB().QueryRowContext(ctx, `
select uti, original_filename, local_path
from asset_resource
where id = ? and available_locally = 1 and needs_download = 0`, id).Scan(&uti, &filename, &path)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("select local presentation resource: %w", err)
	}
	candidate, ok := localPresentationResourceCandidate(presentationResourcePrefix+id, path, uti, filename)
	if !ok {
		return nil, nil
	}
	return &candidate, nil
}

func localPresentationResourceCandidate(ref, path, uti, filename string) (presentationResourceCandidate, bool) {
	contentType := presentationResourceContentType(uti, filename)
	if !strings.HasPrefix(contentType, "image/") {
		return presentationResourceCandidate{}, false
	}
	size, ok := presentationResourceFileSize(path, int64(openrecord.MaximumResourceBytes))
	if !ok {
		return presentationResourceCandidate{}, false
	}
	return presentationResourceCandidate{ref: ref, path: path, contentType: contentType, label: "Photo preview", size: size}, true
}

func (c *Crawler) cachedCurrentPresentationResource(ctx context.Context, req *trawlkit.Request, assetID string) (*presentationResourceCandidate, error) {
	var sourceLibraryID, localIdentifier, modificationDate string
	err := req.Store.DB().QueryRowContext(ctx, `
select source_library_id, local_identifier, modification_date
from asset where id = ?`, assetID).Scan(&sourceLibraryID, &localIdentifier, &modificationDate)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("select cached presentation resource: %w", err)
	}
	var freshness photos.CurrentStillFreshness
	if strings.TrimSpace(modificationDate) != "" {
		modification, err := photos.ParseCurrentStillModification(modificationDate)
		if err != nil {
			return nil, nil
		}
		freshness, err = photos.CurrentStillFreshnessForModification(modification)
		if err != nil {
			return nil, nil
		}
	} else {
		var fingerprint string
		err := req.Store.DB().QueryRowContext(ctx, `
select seen.source_fingerprint
from crawl_seen_asset seen
join crawl_snapshot snapshot on snapshot.id = seen.last_seen_snapshot_id
where seen.asset_id = ? and seen.source_library_id = ? and snapshot.completeness_state = 'complete'`, assetID, sourceLibraryID).Scan(&fingerprint)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("select cached presentation freshness: %w", err)
		}
		freshness, err = photos.CurrentStillFreshnessForSourceFingerprint(fingerprint)
		if err != nil {
			return nil, nil
		}
	}
	path, fact, _, ok := photos.ReadCachedCurrentStill(archivePaths(req).OriginalsCacheDir(), sourceLibraryID, localIdentifier, freshness)
	if !ok {
		return nil, nil
	}
	contentType := presentationResourceContentType(fact.MediaType, "preview")
	if !strings.HasPrefix(contentType, "image/") {
		return nil, nil
	}
	size, ok := presentationResourceFileSize(path, int64(openrecord.MaximumResourceBytes))
	if !ok {
		return nil, nil
	}
	return &presentationResourceCandidate{
		ref:         presentationCurrentResourcePrefix + base64.RawURLEncoding.EncodeToString([]byte(assetID)),
		path:        path,
		contentType: contentType,
		label:       "Photo preview",
		size:        size,
	}, nil
}

func decodePresentationCurrentResourceRef(ref string) (string, error) {
	encoded := strings.TrimPrefix(ref, presentationCurrentResourcePrefix)
	assetID, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || strings.TrimSpace(string(assetID)) == "" {
		return "", errors.New("resource ref is invalid")
	}
	return string(assetID), nil
}

func presentationResourceBlock(candidate presentationResourceCandidate) *presentationv1.Resource {
	return &presentationv1.Resource{
		Kind:  presentationv1.Resource_KIND_IMAGE,
		Label: candidate.label,
		Ref:   candidate.ref,
		Metadata: []*presentationv1.Field{
			{Label: "Size", Display: presentation.Bytes(candidate.size)},
		},
	}
}

func presentationResourceFileSize(path string, maximum int64) (int64, bool) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maximum {
		return 0, false
	}
	return info.Size(), true
}

func resolvePresentationResource(path, contentType string, request *presentationv1.ResourceRequest) (*presentationv1.ResourceResponse, error) {
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 || before.Size() <= 0 || before.Size() > int64(request.GetMaxBytes()) {
		return nil, errors.New("resource is unavailable within the requested bound")
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, errors.New("resource is unavailable")
	}
	file := os.NewFile(uintptr(fd), path)
	defer func() { _ = file.Close() }()
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return nil, errors.New("resource changed while it was being opened")
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(request.GetMaxBytes())+1))
	if err != nil {
		return nil, errors.New("resource could not be read")
	}
	if len(data) == 0 || len(data) > int(request.GetMaxBytes()) {
		return nil, errors.New("resource exceeds the requested bound")
	}
	return &presentationv1.ResourceResponse{ResourceRef: request.GetResourceRef(), ContentType: contentType, Data: data}, nil
}

func presentationResourceContentType(uti, filename string) string {
	switch strings.ToLower(strings.TrimSpace(uti)) {
	case "public.heic":
		return "image/heic"
	case "public.heif":
		return "image/heif"
	case "public.jpeg", "public.jpg":
		return "image/jpeg"
	case "public.png":
		return "image/png"
	case "public.tiff":
		return "image/tiff"
	case "public.mpeg-4":
		return "video/mp4"
	case "com.apple.quicktime-movie":
		return "video/quicktime"
	case "public.mp3":
		return "audio/mpeg"
	case "com.apple.m4a-audio", "public.mpeg-4-audio":
		return "audio/mp4"
	case "com.microsoft.waveform-audio", "public.wav":
		return "audio/wav"
	}
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".heic":
		return "image/heic"
	case ".heif":
		return "image/heif"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".tif", ".tiff":
		return "image/tiff"
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".mp3":
		return "audio/mpeg"
	case ".m4a":
		return "audio/mp4"
	case ".wav":
		return "audio/wav"
	default:
		return ""
	}
}
