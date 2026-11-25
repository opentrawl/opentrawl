package cli

import (
	"bytes"
	"encoding/base64"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
)

const (
	remotePhotoMIMETypeJPEG        = "image/jpeg"
	mcpPhotoMaximumDimensionPixels = 1024
	mcpPhotoTargetMaxByteCount     = 350 * 1024
	mcpPhotoHardMaxByteCount       = 400 * 1024
	mcpPhotoMaximumSourceFileBytes = 32 << 20
	mcpPhotoMaximumDecodedPixels   = 64_000_000
	mcpPhotoDefaultJPEGQuality     = 80
	mcpPhotoCompactJPEGQuality     = 65
	mcpPhotoMinimumJPEGQuality     = 45
)

// openVerifiedCachedPhoto opens cachedPhotoPath using os.OpenRoot confined to
// <stateRoot>/photos/picture-cache so symlinks or directory traversal cannot
// escape the picture cache between verification and open.
func openVerifiedCachedPhoto(stateRoot string, cachedPhotoPath string) (*os.File, os.FileInfo, error) {
	trimmedPath := strings.TrimSpace(cachedPhotoPath)
	if trimmedPath == "" || strings.TrimSpace(stateRoot) == "" {
		return nil, nil, errors.New("cached photo path is empty")
	}
	expectedCacheDir := filepath.Join(stateRoot, "photos", "picture-cache")
	resolvedCacheDir, err := filepath.EvalSymlinks(expectedCacheDir)
	if err != nil {
		return nil, nil, err
	}
	resolvedPhotoPath, err := filepath.EvalSymlinks(trimmedPath)
	if err != nil {
		return nil, nil, err
	}
	if filepath.Dir(resolvedPhotoPath) != resolvedCacheDir {
		return nil, nil, errors.New("cached photo path is outside picture-cache")
	}
	fileName := filepath.Base(resolvedPhotoPath)
	if !strings.EqualFold(filepath.Ext(fileName), ".jpg") || strings.ContainsAny(fileName, `/\`) {
		return nil, nil, errors.New("cached photo filename is not a JPEG")
	}
	cacheRoot, err := os.OpenRoot(resolvedCacheDir)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = cacheRoot.Close() }()

	file, err := cacheRoot.Open(fileName)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > mcpPhotoMaximumSourceFileBytes {
		_ = file.Close()
		return nil, nil, errors.New("cached photo file is unavailable or out of bounds")
	}
	return file, info, nil
}

// encodePhotoForMCPImageBlock reads a verified cached JPEG, validates image
// header dimensions before full decode, downscales it so the resulting JPEG is
// strictly within mcpPhotoHardMaxByteCount, and returns base64-encoded bytes.
func encodePhotoForMCPImageBlock(stateRoot string, cachedPhotoPath string) (string, error) {
	file, _, err := openVerifiedCachedPhoto(stateRoot, cachedPhotoPath)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()

	rawBytes, err := io.ReadAll(io.LimitReader(file, mcpPhotoMaximumSourceFileBytes+1))
	if err != nil {
		return "", err
	}
	if len(rawBytes) == 0 || len(rawBytes) > mcpPhotoMaximumSourceFileBytes {
		return "", errors.New("cached photo file size is out of bounds")
	}

	cfg, err := jpeg.DecodeConfig(bytes.NewReader(rawBytes))
	if err != nil {
		return "", err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > mcpPhotoMaximumDecodedPixels {
		return "", errors.New("cached photo dimensions are out of bounds")
	}

	if cfg.Width <= mcpPhotoMaximumDimensionPixels &&
		cfg.Height <= mcpPhotoMaximumDimensionPixels &&
		len(rawBytes) <= mcpPhotoTargetMaxByteCount {
		return base64.StdEncoding.EncodeToString(rawBytes), nil
	}

	decoded, err := jpeg.Decode(bytes.NewReader(rawBytes))
	if err != nil {
		return "", err
	}
	bounds := decoded.Bounds()
	srcWidth := bounds.Dx()
	srcHeight := bounds.Dy()
	if srcWidth <= 0 || srcHeight <= 0 {
		return "", errors.New("cached photo has empty dimensions")
	}

	maxEdge := mcpPhotoMaximumDimensionPixels
	outputImage := decoded
	if srcWidth > maxEdge || srcHeight > maxEdge {
		dstWidth, dstHeight := scaledPhotoDimensions(srcWidth, srcHeight, maxEdge)
		outputImage = downscaleImageBilinear(decoded, dstWidth, dstHeight)
	}

	for _, attempt := range []struct {
		maxEdge int
		quality int
	}{
		{maxEdge: mcpPhotoMaximumDimensionPixels, quality: mcpPhotoDefaultJPEGQuality},
		{maxEdge: mcpPhotoMaximumDimensionPixels, quality: mcpPhotoCompactJPEGQuality},
		{maxEdge: 768, quality: mcpPhotoCompactJPEGQuality},
		{maxEdge: 640, quality: mcpPhotoMinimumJPEGQuality},
	} {
		candidate := outputImage
		candBounds := candidate.Bounds()
		if candBounds.Dx() > attempt.maxEdge || candBounds.Dy() > attempt.maxEdge {
			dstWidth, dstHeight := scaledPhotoDimensions(candBounds.Dx(), candBounds.Dy(), attempt.maxEdge)
			candidate = downscaleImageBilinear(candidate, dstWidth, dstHeight)
		}
		encoded, encErr := encodeJPEGBytes(candidate, attempt.quality)
		if encErr != nil {
			return "", encErr
		}
		if len(encoded) <= mcpPhotoHardMaxByteCount {
			return base64.StdEncoding.EncodeToString(encoded), nil
		}
	}
	return "", errors.New("cached photo exceeded MCP payload size limit after downscaling")
}

func scaledPhotoDimensions(width int, height int, maxEdge int) (int, int) {
	if width >= height {
		scaledHeight := max(1, int(math.Round(float64(height)*float64(maxEdge)/float64(width))))
		return maxEdge, scaledHeight
	}
	scaledWidth := max(1, int(math.Round(float64(width)*float64(maxEdge)/float64(height))))
	return scaledWidth, maxEdge
}

func encodeJPEGBytes(img image.Image, quality int) ([]byte, error) {
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, img, &jpeg.Options{Quality: quality}); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}

func downscaleImageBilinear(src image.Image, dstWidth int, dstHeight int) *image.RGBA {
	srcBounds := src.Bounds()
	srcWidth := srcBounds.Dx()
	srcHeight := srcBounds.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, dstWidth, dstHeight))

	scaleX := float64(srcWidth) / float64(dstWidth)
	scaleY := float64(srcHeight) / float64(dstHeight)

	for dy := range dstHeight {
		sy := (float64(dy)+0.5)*scaleY - 0.5
		if sy < 0 {
			sy = 0
		}
		y0 := int(sy)
		y1 := min(y0+1, srcHeight-1)
		wy := sy - float64(y0)
		if wy < 0 {
			wy = 0
		}
		srcY0 := srcBounds.Min.Y + y0
		srcY1 := srcBounds.Min.Y + y1

		for dx := range dstWidth {
			sx := (float64(dx)+0.5)*scaleX - 0.5
			if sx < 0 {
				sx = 0
			}
			x0 := int(sx)
			x1 := min(x0+1, srcWidth-1)
			wx := sx - float64(x0)
			if wx < 0 {
				wx = 0
			}
			srcX0 := srcBounds.Min.X + x0
			srcX1 := srcBounds.Min.X + x1

			r00, g00, b00, _ := src.At(srcX0, srcY0).RGBA()
			r10, g10, b10, _ := src.At(srcX1, srcY0).RGBA()
			r01, g01, b01, _ := src.At(srcX0, srcY1).RGBA()
			r11, g11, b11, _ := src.At(srcX1, srcY1).RGBA()

			w00 := (1 - wx) * (1 - wy)
			w10 := wx * (1 - wy)
			w01 := (1 - wx) * wy
			w11 := wx * wy

			r := uint8((w00*float64(r00) + w10*float64(r10) + w01*float64(r01) + w11*float64(r11)) / 257.0)
			g := uint8((w00*float64(g00) + w10*float64(g10) + w01*float64(g01) + w11*float64(g11)) / 257.0)
			b := uint8((w00*float64(b00) + w10*float64(b10) + w01*float64(b01) + w11*float64(b11)) / 257.0)
			dst.SetRGBA(dx, dy, color.RGBA{R: r, G: g, B: b, A: 255})
		}
	}
	return dst
}
