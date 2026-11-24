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
	mcpPhotoDefaultJPEGQuality     = 80
	mcpPhotoCompactJPEGQuality     = 68
)

// verifiedCachedPhotoFilePath resolves cachedPhotoPath and confirms it is a
// regular JPEG file directly inside <stateRoot>/photos/picture-cache.
func verifiedCachedPhotoFilePath(stateRoot string, cachedPhotoPath string) (string, bool) {
	trimmedPath := strings.TrimSpace(cachedPhotoPath)
	if trimmedPath == "" || strings.TrimSpace(stateRoot) == "" {
		return "", false
	}
	expectedCacheDir := filepath.Join(stateRoot, "photos", "picture-cache")
	resolvedCacheDir, err := filepath.EvalSymlinks(expectedCacheDir)
	if err != nil {
		return "", false
	}
	resolvedPhotoPath, err := filepath.EvalSymlinks(trimmedPath)
	if err != nil {
		return "", false
	}
	if filepath.Dir(resolvedPhotoPath) != resolvedCacheDir {
		return "", false
	}
	if !strings.EqualFold(filepath.Ext(resolvedPhotoPath), ".jpg") {
		return "", false
	}
	info, err := os.Stat(resolvedPhotoPath)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return resolvedPhotoPath, true
}

// encodePhotoForMCPImageBlock reads a verified cached JPEG, downscales it to at
// most 1024px on the longest edge so base64 payloads stay fast and within MCP
// client limits, and returns base64-encoded JPEG bytes.
func encodePhotoForMCPImageBlock(verifiedPhotoFilePath string) (string, error) {
	// #nosec G304 -- path is already confined to <stateRoot>/photos/picture-cache/*.jpg by verifiedCachedPhotoFilePath.
	file, err := os.Open(verifiedPhotoFilePath)
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

	if srcWidth <= mcpPhotoMaximumDimensionPixels &&
		srcHeight <= mcpPhotoMaximumDimensionPixels &&
		len(rawBytes) <= mcpPhotoTargetMaxByteCount {
		return base64.StdEncoding.EncodeToString(rawBytes), nil
	}

	outputImage := decoded
	if srcWidth > mcpPhotoMaximumDimensionPixels || srcHeight > mcpPhotoMaximumDimensionPixels {
		dstWidth, dstHeight := scaledPhotoDimensions(srcWidth, srcHeight, mcpPhotoMaximumDimensionPixels)
		outputImage = downscaleImageBilinear(decoded, dstWidth, dstHeight)
	}

	encoded, err := encodeJPEGBytes(outputImage, mcpPhotoDefaultJPEGQuality)
	if err != nil {
		return "", err
	}
	if len(encoded) > mcpPhotoHardMaxByteCount {
		compact, compactErr := encodeJPEGBytes(outputImage, mcpPhotoCompactJPEGQuality)
		if compactErr == nil && len(compact) < len(encoded) {
			encoded = compact
		}
	}
	return base64.StdEncoding.EncodeToString(encoded), nil
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
