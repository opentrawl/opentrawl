package model

import (
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var slugDash = regexp.MustCompile(`-+`)

// Keep this list small: strip one trunk zero only for country codes where
// contacts commonly keep the national prefix after an international code.
var trunkZeroCountryCodes = []string{
	"31", "44", "49", "33", "39", "34", "46", "47", "45", "32", "43", "41", "48",
}

func Slug(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			b.WriteRune(r)
		case unicode.IsSpace(r), r == '-', r == '_', r == '\'', r == '.':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(slugDash.ReplaceAllString(b.String(), "-"), "-")
	if out == "" {
		return "person"
	}
	return out
}

func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

func NormalizePhone(phone string) string {
	var b strings.Builder
	for _, r := range phone {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	out := b.String()
	out = strings.TrimPrefix(out, "00")
	return stripCountryCodeTrunkZero(out)
}

func NormalizeAddress(address string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(address)), " "))
}

func stripCountryCodeTrunkZero(phone string) string {
	for _, countryCode := range trunkZeroCountryCodes {
		prefix := countryCode + "0"
		if strings.HasPrefix(phone, prefix) && plausibleSubscriberLength(len(phone)-len(prefix)) {
			return countryCode + phone[len(prefix):]
		}
	}
	return phone
}

func plausibleSubscriberLength(length int) bool {
	return length >= 6 && length <= 12
}

func NormalizeName(name string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(name))), " ")
}

func PathSlug(path string) string {
	return filepath.Base(filepath.Dir(path))
}
