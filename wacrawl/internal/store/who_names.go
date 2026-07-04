package store

import (
	"sort"
	"strings"
	"unicode"
)

func chooseWhoName(names map[string]*whoNameEvidence, identifiers []string) string {
	if name := firstCleanContactFullName(names); name != "" {
		return name
	}
	if name := mostFrequentCleanPushName(names); name != "" {
		return name
	}
	for _, name := range sortedWhoNameValues(names) {
		if humanWhoName(name) {
			return name
		}
	}
	for _, identifier := range identifiers {
		if strings.HasPrefix(identifier, "@") {
			return identifier
		}
	}
	for _, identifier := range identifiers {
		if !strings.Contains(identifier, "@") {
			return identifier
		}
	}
	if len(identifiers) > 0 {
		return identifiers[0]
	}
	if names := sortedWhoNameValues(names); len(names) > 0 {
		return names[0]
	}
	return ""
}

func firstCleanContactFullName(names map[string]*whoNameEvidence) string {
	var choices []string
	for _, name := range names {
		if name.contactFull && humanWhoName(name.value) {
			choices = append(choices, name.value)
		}
	}
	sort.Strings(choices)
	if len(choices) == 0 {
		return ""
	}
	return choices[0]
}

func mostFrequentCleanPushName(names map[string]*whoNameEvidence) string {
	type choice struct {
		value string
		count int
	}
	var choices []choice
	for _, name := range names {
		if name.pushCount > 0 && humanWhoName(name.value) {
			choices = append(choices, choice{value: name.value, count: name.pushCount})
		}
	}
	sort.SliceStable(choices, func(i, j int) bool {
		if choices[i].count != choices[j].count {
			return choices[i].count > choices[j].count
		}
		left := strings.ToLower(choices[i].value)
		right := strings.ToLower(choices[j].value)
		if left != right {
			return left < right
		}
		return choices[i].value < choices[j].value
	})
	if len(choices) == 0 {
		return ""
	}
	return choices[0].value
}

func humanWhoName(value string) bool {
	value = normalizeWhoIdentity(value)
	if value == "" || strings.HasPrefix(value, "@") || strings.Contains(value, "@") || looksLikeIdentifierPhone(value) {
		return false
	}
	if looksLikeBase64Name(value) {
		return false
	}
	hasLetter := false
	for _, r := range value {
		if !unicode.IsPrint(r) {
			return false
		}
		if unicode.IsLetter(r) {
			hasLetter = true
		}
	}
	if !hasLetter {
		return false
	}
	return true
}

// HumanWhoName reports whether value is safe to display as a person's name.
func HumanWhoName(value string) bool {
	return humanWhoName(value)
}

func looksLikeBase64Name(value string) bool {
	if strings.Contains(value, " ") || len(value) < 4 {
		return false
	}
	hasBase64Punctuation := false
	for _, r := range value {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
		case r == '+', r == '/', r == '=':
			hasBase64Punctuation = true
		default:
			return false
		}
	}
	return hasBase64Punctuation
}

func looksLikeIdentifierPhone(value string) bool {
	digits := 0
	other := 0
	for _, r := range value {
		switch {
		case unicode.IsDigit(r):
			digits++
		case strings.ContainsRune(" +()-.", r):
		default:
			other++
		}
	}
	return digits >= 5 && other == 0
}

func sortedWhoNameValues(names map[string]*whoNameEvidence) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		out = append(out, name.value)
	}
	sort.SliceStable(out, func(i, j int) bool {
		left := strings.ToLower(out[i])
		right := strings.ToLower(out[j])
		if left != right {
			return left < right
		}
		return out[i] < out[j]
	})
	return out
}

func sortedValues(values map[string]string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	sort.SliceStable(out, func(i, j int) bool {
		left := strings.ToLower(out[i])
		right := strings.ToLower(out[j])
		if left != right {
			return left < right
		}
		return out[i] < out[j]
	})
	return out
}

func sortedUniqueValues(values []string) []string {
	byValue := map[string]string{}
	for _, value := range values {
		value = normalizeWhoIdentity(value)
		if value == "" {
			continue
		}
		byValue[strings.ToLower(value)] = value
	}
	return sortedValues(byValue)
}

func uniqueStrings(values []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = normalizeWhoIdentity(value)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func normalizeWhoIdentity(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func normalizeWhoIdentifier(value string) string {
	value = normalizeWhoIdentity(value)
	for {
		lower := strings.ToLower(value)
		if !strings.HasSuffix(lower, "@lid@lid") {
			return value
		}
		value = value[:len(value)-len("@lid")]
	}
}
