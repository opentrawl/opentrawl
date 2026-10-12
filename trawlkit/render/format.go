package render

import (
	"strconv"
	"strings"
)

// formatByteCount writes a size in decimal units, as macOS does: 512 bytes,
// 12.3 MB.
func formatByteCount(byteCount uint64) string {
	const decimalUnitStep = 1000
	if byteCount < decimalUnitStep {
		return strconv.FormatUint(byteCount, 10) + " bytes"
	}
	size := float64(byteCount)
	unitNames := []string{"bytes", "kB", "MB", "GB", "TB", "PB"}
	unitIndex := 0
	for size >= decimalUnitStep && unitIndex < len(unitNames)-1 {
		size /= decimalUnitStep
		unitIndex++
	}
	return strconv.FormatFloat(size, 'f', 1, 64) + " " + unitNames[unitIndex]
}

func FormatInteger(value int64) string {
	sign := ""
	if value < 0 {
		sign = "-"
		value = -value
	}
	digits := strconv.FormatInt(value, 10)
	var groups []string
	for len(digits) > 3 {
		groups = append([]string{digits[len(digits)-3:]}, groups...)
		digits = digits[:len(digits)-3]
	}
	groups = append([]string{digits}, groups...)
	return sign + strings.Join(groups, ",")
}

func FormatPhone(value string) string {
	raw := strings.TrimSpace(value)
	if raw == "" || !strings.HasPrefix(raw, "+") {
		return value
	}
	digits := raw[1:]
	if len(digits) < 8 || len(digits) > 15 || !allDigits(digits) {
		return value
	}
	if strings.HasPrefix(digits, "1") && len(digits) == 11 {
		return "+1 (" + digits[1:4] + ") " + digits[4:7] + "-" + digits[7:]
	}
	if len(digits) <= 3 {
		return value
	}
	country, subscriber := digits[:genericCountryCodeWidth(digits)], digits[genericCountryCodeWidth(digits):]
	return "+" + country + " " + groupSubscriberNumber(subscriber)
}

func HumanIdentity(value string) string {
	return FormatPhone(strings.TrimSpace(value))
}

func displayLabel(value string) string {
	parts := strings.Fields(strings.ReplaceAll(strings.TrimSpace(value), "_", " "))
	if len(parts) == 0 {
		return ""
	}
	out := strings.Join(parts, " ")
	if out[0] >= 'a' && out[0] <= 'z' {
		out = string(out[0]-('a'-'A')) + out[1:]
	}
	return out
}

func genericCountryCodeWidth(digits string) int {
	if strings.HasPrefix(digits, "1") {
		return 1
	}
	return 2
}

func groupSubscriberNumber(value string) string {
	if len(value) <= 4 {
		return value
	}
	var groups []string
	for len(value) > 4 {
		groups = append(groups, value[:3])
		value = value[3:]
	}
	groups = append(groups, value)
	return strings.Join(groups, " ")
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
