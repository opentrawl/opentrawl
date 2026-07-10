//go:build !darwin

package photos

import (
	"context"
	"errors"
)

func ExportOriginalResourceMatching(ctx context.Context, query OriginalExportQuery, destinationPath string, allowNetwork bool) error {
	return errors.New("PhotoKit original export requires macOS")
}

func RequestPhotoLibraryAuthorization(ctx context.Context) (string, error) {
	return "", errors.New("Photos authorization requests require macOS")
}

func RenderCanonicalJPEG(ctx context.Context, sourcePath, destinationPath string, quality float64) error {
	return errors.New("canonical JPEG rendering requires macOS ImageIO/CoreImage")
}

func ImageMetadata(ctx context.Context, sourcePath string) (map[string]any, error) {
	return nil, errors.New("image metadata extraction requires macOS ImageIO")
}
