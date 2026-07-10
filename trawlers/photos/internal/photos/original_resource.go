package photos

import (
	"strings"
)

// PreferredOriginalResource returns PhotoKit's camera-original photo resource.
// Full-size and alternate photo resources are edits, not substitutes.
func PreferredOriginalResource(resources []Resource) (Resource, bool) {
	for _, resource := range resources {
		if strings.EqualFold(strings.TrimSpace(resource.Type), "photo") {
			return resource, true
		}
	}
	return Resource{}, false
}
