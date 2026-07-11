package imagemetadata

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

func recordedDate(value, offset string, hasOffset bool) (string, bool) {
	for _, layout := range []string{"2006:01:02 15:04:05.999999999", "2006:01:02 15:04:05"} {
		if hasOffset {
			seconds, ok := offsetSeconds(offset)
			if !ok {
				return "", false
			}
			parsed, err := time.ParseInLocation(layout, value, time.FixedZone(offset, seconds))
			if err == nil {
				return humanTime(parsed) + " " + offset, true
			}
			continue
		}
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return humanTime(parsed) + " (timezone not recorded)", true
		}
	}
	return "", false
}

func dateValue(value string) string {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return value
	}
	return humanTime(parsed) + " " + parsed.Format("-07:00")
}

func humanTime(value time.Time) string {
	return value.Format("2 January 2006 at 15:04:05.999")
}

func offsetSeconds(value string) (int, bool) {
	if len(value) != 6 || (value[0] != '+' && value[0] != '-') || value[3] != ':' {
		return 0, false
	}
	hours, hourErr := strconv.Atoi(value[1:3])
	minutes, minuteErr := strconv.Atoi(value[4:6])
	if hourErr != nil || minuteErr != nil || hours > 14 || minutes > 59 {
		return 0, false
	}
	seconds := hours*3600 + minutes*60
	if value[0] == '-' {
		seconds = -seconds
	}
	return seconds, true
}

func gpsAccuracy(values map[string]Value) (float64, string, bool) {
	for _, key := range []string{"HPositioningError", "GPSHPositioningError", "HorizontalAccuracy"} {
		if value, ok := numberField(values[key]); ok && value > 0 {
			return value, key, true
		}
	}
	return 0, "", false
}

func coordinateDigits(accuracy, metresPerDegree float64, hasAccuracy bool) int {
	if !hasAccuracy {
		return 4
	}
	digits := int(math.Floor(math.Log10(metresPerDegree / accuracy)))
	if digits < 0 {
		return 0
	}
	if digits > 5 {
		return 5
	}
	return digits
}

func longitudeMetresPerDegree(latitude float64) float64 {
	metres := 111320 * math.Cos(latitude*math.Pi/180)
	if metres < 11132 {
		return 11132
	}
	return metres
}

func formatCoordinate(value float64, digits int) string {
	return strconv.FormatFloat(value, 'f', digits, 64)
}

func applyCoordinateRef(value float64, ref, positive, negative string) float64 {
	switch strings.ToUpper(strings.TrimSpace(ref)) {
	case positive:
		return math.Abs(value)
	case negative:
		return -math.Abs(value)
	default:
		return value
	}
}

func numberField(value Value) (float64, bool) {
	var raw string
	switch value.Type {
	case ValueSigned:
		raw = stringValue(value.Signed)
	case ValueUnsigned:
		raw = stringValue(value.Unsigned)
	case ValueDecimal:
		raw = stringValue(value.Decimal)
	default:
		return 0, false
	}
	parsed, err := strconv.ParseFloat(raw, 64)
	return parsed, err == nil && !math.IsNaN(parsed) && !math.IsInf(parsed, 0)
}

func stringField(value Value) (string, bool) {
	if value.Type != ValueString || strings.TrimSpace(stringValue(value.String)) == "" {
		return "", false
	}
	return stringValue(value.String), true
}

func formatDistance(value float64) string {
	return significant(value, 2) + " m"
}

func humanBytes(raw string) string {
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value < 1024 {
		return raw + " bytes"
	}
	units := []string{"bytes", "KB", "MB", "GB", "TB"}
	amount := float64(value)
	index := 0
	for amount >= 1024 && index < len(units)-1 {
		amount /= 1024
		index++
	}
	if amount >= 1024 {
		return raw + " bytes"
	}
	return significant(amount, 3) + " " + units[index]
}

func shutterSpeed(seconds float64) string {
	if seconds <= 0 {
		return significant(seconds, 6) + " s"
	}
	if seconds < 1 {
		denominator := math.Round(1 / seconds)
		if denominator >= 2 && math.Abs(seconds-(1/denominator))/seconds < 0.01 {
			return fmt.Sprintf("1/%.0f s", denominator)
		}
	}
	return significant(seconds, 3) + " s"
}

func significant(value float64, digits int) string {
	return strconv.FormatFloat(value, 'g', digits, 64)
}

func flashLabel(value int64) string {
	parts := []string{"did not fire"}
	if value&1 != 0 {
		parts[0] = "fired"
	}
	switch value & 6 {
	case 4:
		parts = append(parts, "return not detected")
	case 6:
		parts = append(parts, "return detected")
	}
	switch value & 24 {
	case 8:
		parts = append(parts, "compulsory mode")
	case 16:
		parts = append(parts, "compulsory suppression")
	case 24:
		parts = append(parts, "automatic mode")
	}
	if value&32 != 0 {
		parts = append(parts, "flash function unavailable")
	}
	if value&64 != 0 {
		parts = append(parts, "red-eye reduction")
	}
	return strings.Join(parts, "; ")
}

func orientationLabel(value int64) (string, bool) {
	labels := map[int64]string{
		1: "upright",
		2: "mirrored horizontally",
		3: "rotated 180 degrees",
		4: "mirrored vertically",
		5: "mirrored horizontally and rotated 270 degrees",
		6: "rotated 90 degrees clockwise",
		7: "mirrored horizontally and rotated 90 degrees",
		8: "rotated 270 degrees clockwise",
	}
	label, ok := labels[value]
	return label, ok
}
