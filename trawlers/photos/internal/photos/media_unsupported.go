//go:build !darwin

package photos

import (
	"context"
	"errors"
)

func ExportOriginalResource(ctx context.Context, localIdentifier, destinationPath string, allowNetwork bool) error {
	return errors.New("PhotoKit original export requires macOS")
}

func ExportOriginalResourceMatching(ctx context.Context, query OriginalExportQuery, destinationPath string, allowNetwork bool) error {
	return errors.New("PhotoKit original export requires macOS")
}

func RenderCanonicalJPEG(ctx context.Context, sourcePath, destinationPath string, quality float64) error {
	return errors.New("canonical JPEG rendering requires macOS ImageIO/CoreImage")
}

func ImageMetadata(ctx context.Context, sourcePath string) (map[string]any, error) {
	return nil, errors.New("image metadata extraction requires macOS ImageIO")
}
