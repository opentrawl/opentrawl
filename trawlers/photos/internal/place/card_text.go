package place

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/opentrawl/opentrawl/trawlers/photos/internal/cardformat"
)

func unnamedMapKindUseful(kind string) bool {
	switch kind {
	case "trail", "road", "bridge", "tunnel", "viewpoint", "beach", "cliff", "cave",
		"airport gate", "airport terminal", "pier", "park", "national park", "ferry route":
		return true
	default:
		return false
	}
}

func normalizeMapKind(kind string) string {
	k := strings.ToLower(strings.TrimSpace(kind))
	k = strings.ReplaceAll(k, "_", " ")
	k = strings.ReplaceAll(k, "-", " ")
	k = strings.Join(strings.Fields(k), " ")
	switch {
	case k == "":
		return ""
	case strings.Contains(k, "telephone"):
		return ""
	case strings.Contains(k, "waste"):
		return ""
	case strings.Contains(k, "bench"):
		return ""
	case strings.Contains(k, "atm"):
		return ""
	case strings.Contains(k, "drinking water"):
		return ""
	case strings.Contains(k, "parking"):
		return ""
	case strings.Contains(k, "information") || strings.Contains(k, "guidepost"):
		return ""
	case strings.Contains(k, "house") || strings.Contains(k, "building") || strings.Contains(k, "apartment"):
		return ""
	case strings.HasPrefix(k, "shop"):
		return ""
	case strings.Contains(k, "aeroway gate"):
		return "airport gate"
	case strings.Contains(k, "aeroway terminal"):
		return "airport terminal"
	case strings.Contains(k, "airport"):
		return "airport"
	case strings.Contains(k, "aeroway apron"):
		return ""
	case k == "path" || k == "track" || k == "footway" || k == "cycleway" || k == "bridleway":
		return "trail"
	case strings.Contains(k, "highway path") || strings.Contains(k, "highway footway") ||
		strings.Contains(k, "highway track") || strings.Contains(k, "highway cycleway") ||
		strings.Contains(k, "highway bridleway"):
		return "trail"
	case strings.HasPrefix(k, "highway") || strings.Contains(k, "road"):
		return "road"
	case strings.Contains(k, "railway station"):
		return "rail station"
	case strings.Contains(k, "public transport") || strings.Contains(k, "bus stop") || strings.Contains(k, "tram stop"):
		return "transit stop"
	case strings.Contains(k, "ferry"):
		return "ferry route"
	case strings.Contains(k, "tourism viewpoint"):
		return "viewpoint"
	case strings.Contains(k, "tourism hotel"):
		return "hotel"
	case strings.Contains(k, "cave"):
		return "cave"
	case strings.Contains(k, "cliff"):
		return "cliff"
	case strings.Contains(k, "beach"):
		return "beach"
	case strings.Contains(k, "peak"):
		return "peak"
	case strings.Contains(k, "waterfall"):
		return "waterfall"
	case strings.Contains(k, "water"):
		return "water"
	case strings.Contains(k, "bridge"):
		return "bridge"
	case strings.Contains(k, "tunnel"):
		return "tunnel"
	case strings.Contains(k, "pier"):
		return "pier"
	case strings.Contains(k, "national park"):
		return "national park"
	case strings.Contains(k, "park"):
		return "park"
	case strings.Contains(k, "boundary") || strings.Contains(k, "administrative"):
		return "area"
	case strings.Contains(k, "neighbourhood") || strings.Contains(k, "neighborhood") ||
		strings.Contains(k, "suburb") || strings.Contains(k, "quarter"):
		return "area"
	case strings.HasPrefix(k, "place city") || strings.HasPrefix(k, "place town") ||
		strings.HasPrefix(k, "place village"):
		return "area"
	case strings.Contains(k, "area of interest"):
		return "area"
	default:
		return ""
	}
}

func normalizeRelation(relation string) string {
	relation = strings.ToLower(strings.TrimSpace(relation))
	switch relation {
	case "contains", "on", "on/near", "near", "nearby", "nearest", "area":
		return relation
	default:
		return ""
	}
}

func areaTrail(address *Address, area []AreaLevel) string {
	values := []string{}
	for _, level := range area {
		values = append(values, level.Name)
	}
	if len(values) == 0 && address != nil {
		values = []string{
			address.Country,
			address.AdministrativeArea,
			address.SubAdministrativeArea,
			address.Locality,
			address.SubLocality,
		}
	}
	return strings.Join(compactStrings(values), " > ")
}

func displayAddress(address *Address) string {
	return FormatAddress(address)
}

func FormatAddress(address *Address) string {
	if address == nil {
		return ""
	}
	street := preferredStreetAddress(address)
	if street == "" {
		return dedupeCommaParts(address.Formatted)
	}
	return strings.Join(dedupeAddressParts([]string{
		street,
		address.SubLocality,
		address.Locality,
		address.Country,
	}), ", ")
}

func streetAddress(address *Address) string {
	return strings.TrimSpace(strings.Join(compactStrings([]string{address.SubThoroughfare, address.Thoroughfare}), " "))
}

func preferredStreetAddress(address *Address) string {
	street := streetAddress(address)
	if street == "" {
		return strings.TrimSpace(address.Name)
	}
	for _, part := range strings.Split(address.Formatted, ",") {
		part = strings.TrimSpace(part)
		if sameAddressPart(part, street) {
			return part
		}
	}
	name := strings.TrimSpace(address.Name)
	if sameAddressPart(name, street) {
		return name
	}
	return street
}

func sameAddressPart(left, right string) bool {
	leftKey := normalizedKey(left)
	rightKey := normalizedKey(right)
	if leftKey == "" || rightKey == "" {
		return false
	}
	return leftKey == rightKey ||
		strings.HasPrefix(leftKey, rightKey) ||
		strings.HasPrefix(rightKey, leftKey) ||
		tokenSetKey(leftKey) == tokenSetKey(rightKey)
}

func dedupeCommaParts(value string) string {
	return strings.Join(dedupeAddressParts(strings.Split(value, ",")), ", ")
}

func dedupeAddressParts(parts []string) []string {
	out := []string{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		key := normalizedKey(part)
		if part == "" || key == "" {
			continue
		}
		duplicate := false
		for _, previous := range out {
			if sameAddressPart(previous, part) {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		out = append(out, part)
	}
	return out
}

func shortCategory(category string) string {
	return cardformat.NormalizePOICategory(category)
}

func distanceLabel(distance float64) string {
	if distance <= 0 {
		return ""
	}
	return fmt.Sprintf("%.0fm", distance)
}

func cleanFeatureName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, "yes") {
		return ""
	}
	return name
}

func lowValueMapName(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	return strings.Contains(name, "guidepost") ||
		strings.Contains(name, "information") ||
		strings.Contains(name, "telephone") ||
		strings.Contains(name, "waste basket") ||
		strings.Contains(name, "drinking water") ||
		strings.Contains(name, "atm") ||
		strings.Contains(name, "bench")
}

func containsNormalized(haystack, needle string) bool {
	haystackKey := normalizedKey(haystack)
	needleKey := normalizedKey(needle)
	return haystackKey != "" && needleKey != "" && strings.Contains(haystackKey, needleKey)
}

func poiStem(name, category string) string {
	key := normalizedKey(name)
	if category != "public transport" && category != "rail station" {
		return key
	}
	words := strings.Fields(key)
	out := []string{}
	for _, word := range words {
		switch word {
		case "bus", "stop", "station", "platform", "tram", "metro", "underground":
			continue
		default:
			out = append(out, word)
		}
	}
	return strings.Join(out, " ")
}

func normalizedKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastSpace := true
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastSpace = false
			continue
		}
		if !lastSpace {
			b.WriteByte(' ')
			lastSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

func tokenSetKey(value string) string {
	words := strings.Fields(value)
	sort.Strings(words)
	return strings.Join(words, " ")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
