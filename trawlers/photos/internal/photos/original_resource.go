package photos

import "strings"

func IsOriginalUTI(uniformTypeIdentifier string) bool {
	switch strings.ToLower(uniformTypeIdentifier) {
	case "public.heic", "public.heif", "public.jpeg", "public.jpg", "public.png", "public.tiff", "com.adobe.raw-image", "com.adobe.raw":
		return true
	default:
		return false
	}
}

func IsOriginalExtension(extension string) bool {
	switch strings.ToLower(extension) {
	case ".heic", ".heif", ".jpg", ".jpeg", ".png", ".tif", ".tiff", ".dng":
		return true
	default:
		return false
	}
}
