package photoscrawl

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	"github.com/opentrawl/opentrawl/trawlkit/presentation"
	presentationv1 "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation/v1"
)

var _ trawlkit.ResourceResolver = (*Crawler)(nil)

const presentationResourcePrefix = "photos:resource/"

func (c *Crawler) presentationResource(ctx context.Context, req *trawlkit.Request, openRef string) (*presentationv1.Resource, error) {
	assetID := archive.AssetID(openRef)
	if assetID == "" || req == nil || req.Store == nil {
		return nil, nil
	}
	var id, resourceType, uti, filename string
	var size int64
	err := req.Store.DB().QueryRowContext(ctx, `
select id, resource_type, uti, original_filename, file_size
from asset_resource
where asset_id = ?
  and available_locally = 1
  and needs_download = 0
  and trim(local_path) <> ''
  and file_size > 0
  and file_size <= ?
order by case
  when lower(resource_type) in ('photo', 'image', 'local_original') then 0
  when lower(resource_type) = 'video' then 1
  when lower(resource_type) = 'audio' then 2
  else 3
end, id
limit 1`, assetID, openrecord.MaximumResourceBytes).Scan(&id, &resourceType, &uti, &filename, &size)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("select bounded presentation resource: %w", err)
	}
	label := strings.TrimSpace(filename)
	if label == "" {
		label = "Photo preview"
	}
	return &presentationv1.Resource{
		Kind:  presentationResourceKind(resourceType, uti),
		Label: label,
		Ref:   presentationResourcePrefix + id,
		Metadata: []*presentationv1.Field{
			{Label: "Size", Display: presentation.Bytes(size)},
		},
	}, nil
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
	var uti, filename, path string
	err := req.Store.DB().QueryRowContext(ctx, `
select uti, original_filename, local_path
from asset_resource
where id = ? and available_locally = 1 and needs_download = 0`, id).Scan(&uti, &filename, &path)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("resource is unavailable")
	}
	if err != nil {
		return nil, fmt.Errorf("resolve presentation resource: %w", err)
	}
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
	contentType := presentationResourceContentType(uti, filename)
	if contentType == "" {
		return nil, errors.New("resource content type is unsupported")
	}
	return &presentationv1.ResourceResponse{ResourceRef: request.GetResourceRef(), ContentType: contentType, Data: data}, nil
}

func presentationResourceKind(resourceType, uti string) presentationv1.Resource_Kind {
	value := strings.ToLower(strings.TrimSpace(resourceType + " " + uti))
	switch {
	case strings.Contains(value, "video"), strings.Contains(value, "movie"):
		return presentationv1.Resource_KIND_VIDEO
	case strings.Contains(value, "audio"):
		return presentationv1.Resource_KIND_AUDIO
	case strings.Contains(value, "photo"), strings.Contains(value, "image"), strings.Contains(value, "jpeg"), strings.Contains(value, "png"), strings.Contains(value, "heic"), strings.Contains(value, "heif"):
		return presentationv1.Resource_KIND_IMAGE
	default:
		return presentationv1.Resource_KIND_FILE
	}
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
