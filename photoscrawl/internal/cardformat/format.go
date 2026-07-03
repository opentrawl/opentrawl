package cardformat

import (
	"fmt"
	"math"
	"strings"
	"unicode"
)

type Camera struct {
	Make            string
	Model           string
	LensModel       string
	FocalLengthMM   float64
	FocalLength35MM float64
	Aperture        float64
	ShutterSpeed    float64
	ISO             int64
}

func Round(value float64, places int) float64 {
	if value == 0 {
		return 0
	}
	scale := math.Pow10(places)
	return math.Round(value*scale) / scale
}

func Coordinate(value float64) float64 {
	return Round(value, 5)
}

func Meters(value float64) float64 {
	return Round(value, 0)
}

func FocalLength(value float64) float64 {
	return Round(value, 2)
}

func Aperture(value float64) float64 {
	return Round(value, 1)
}

func FormatCoordinate(value float64) string {
	return fmt.Sprintf("%.5f", Coordinate(value))
}

func FormatMeters(value float64) string {
	return fmt.Sprintf("%.0f", Meters(value))
}

func NormalizePOICategory(category string) string {
	category = strings.TrimSpace(category)
	if category == "" {
		return ""
	}
	category = strings.TrimPrefix(category, "MKPOICategory")
	category = strings.ReplaceAll(category, "_", " ")
	category = strings.ReplaceAll(category, "-", " ")
	category = splitCamel(category)
	category = strings.ToLower(strings.Join(strings.Fields(category), " "))
	switch category {
	case "store":
		return "shop"
	case "fitness center":
		return "fitness centre"
	default:
		return category
	}
}

func CameraDisplay(camera Camera) string {
	parts := []string{}
	if device := cameraDevice(camera.Make, camera.Model); device != "" {
		parts = append(parts, device)
	}
	if focal := FocalLengthLabel(camera.FocalLengthMM, camera.FocalLength35MM); focal != "" {
		parts = append(parts, focal)
	}
	if camera.Aperture > 0 {
		parts = append(parts, fmt.Sprintf("f/%.1f", Aperture(camera.Aperture)))
	}
	if shutter := ShutterSpeedLabel(camera.ShutterSpeed); shutter != "" {
		parts = append(parts, shutter)
	}
	if camera.ISO > 0 {
		parts = append(parts, fmt.Sprintf("ISO %d", camera.ISO))
	}
	return strings.Join(parts, ", ")
}

func FocalLengthLabel(focalLengthMM, focalLength35MM float64) string {
	if focalLength35MM > 0 {
		return fmt.Sprintf("%.0fmm equiv", Meters(focalLength35MM))
	}
	if focalLengthMM <= 0 {
		return ""
	}
	rounded := FocalLength(focalLengthMM)
	if rounded == math.Round(rounded) {
		return fmt.Sprintf("%.0fmm", rounded)
	}
	return fmt.Sprintf("%.1fmm", Round(focalLengthMM, 1))
}

func ShutterSpeedLabel(value float64) string {
	if value <= 0 {
		return ""
	}
	var seconds float64
	switch {
	case value >= 32:
		seconds = 1 / value
	case value > 1:
		seconds = 1 / math.Pow(2, value)
	default:
		seconds = value
	}
	if seconds <= 0 || math.IsInf(seconds, 0) || math.IsNaN(seconds) {
		return ""
	}
	if seconds >= 1 {
		rounded := Round(seconds, 1)
		if rounded == math.Round(rounded) {
			return fmt.Sprintf("%.0fs", rounded)
		}
		return fmt.Sprintf("%.1fs", rounded)
	}
	denominator := math.Round(1 / seconds)
	if denominator <= 0 {
		return ""
	}
	// Cameras shoot standard stops; 1/121 is a stored-float artifact of 1/120.
	if denominator > 30 {
		denominator = math.Round(denominator/5) * 5
	}
	return fmt.Sprintf("1/%.0fs", denominator)
}

func cameraDevice(make, model string) string {
	make = strings.Join(strings.Fields(strings.TrimSpace(make)), " ")
	model = strings.Join(strings.Fields(strings.TrimSpace(model)), " ")
	switch {
	case make == "":
		return model
	case model == "":
		return make
	case strings.EqualFold(make, model):
		return make
	case strings.HasPrefix(strings.ToLower(model), strings.ToLower(make)+" "):
		return model
	default:
		return make + " " + model
	}
}

func splitCamel(value string) string {
	var out []rune
	var previous rune
	for i, r := range value {
		if i > 0 && unicode.IsUpper(r) && (unicode.IsLower(previous) || unicode.IsDigit(previous)) {
			out = append(out, ' ')
		}
		out = append(out, r)
		previous = r
	}
	return string(out)
}
