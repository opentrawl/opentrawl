package photos

import (
	"strings"
)

// PreferredOriginalResource returns PhotoKit's camera-original photo resource.
// Full-size and alternate photo resources are edits, not substitutes.
func PreferredOriginalResource(resources []Resource) (Resource, bool) {
	for _, resource := range resources {
		if strings.EqualFold(strings.TrimSpace(resource.ResourceTypeProjection), "photo") {
			return resource, true
		}
	}
	return Resource{}, false
}

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
