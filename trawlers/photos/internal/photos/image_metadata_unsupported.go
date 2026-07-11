//go:build !darwin

package photos

import (
	"context"
	"errors"
)

func ImageMetadataRecord(ctx context.Context, sourcePath string) ([]byte, error) {
	return nil, errors.New("image metadata extraction requires macOS ImageIO")
}
