package render

import (
	"strconv"
	"strings"
)

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

func FormatCount(value int64, id, label string) string {
	if countIsYear(id, label) {
		return strconv.FormatInt(value, 10)
	}
	return FormatInteger(value)
}

func countIsYear(id, label string) bool {
	name := strings.ToLower(strings.TrimSpace(label))
	if name == "" {
		name = strings.ToLower(strings.TrimSpace(id))
	}
	return name == "since" || strings.Contains(name, "year")
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

func FormatPhoneList(value string) string {
	parts := strings.Split(value, ",")
	changed := false
	for i, part := range parts {
		trimmed := strings.TrimSpace(part)
		formatted := FormatPhone(trimmed)
		if formatted != trimmed {
			changed = true
		}
		parts[i] = formatted
	}
	if !changed {
		return value
	}
	return strings.Join(parts, ", ")
}

func HumanIdentity(value string) string {
	return FormatPhone(strings.TrimSpace(value))
}

func HumanCell(header, value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}
	if strings.Contains(value, ",") {
		return FormatPhoneList(value)
	}
	return FormatPhone(value)
}

func DisplayLabel(value string) string {
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
