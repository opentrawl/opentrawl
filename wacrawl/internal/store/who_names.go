package store

import (
	"sort"
	"strings"
	"unicode"

	"github.com/openclaw/crawlkit/whomatch"
)

// chooseWhoName keeps wacrawl's source-precedence ladder — contact full name
// beats push name beats any other observed name — because that ordering is
// crawler input knowledge. Which spelling wins inside each tier is
// centralized in crawlkit; the structural rules and the rules.md §1.5
// carve-out are documented on whomatch.BestDisplayName.
func chooseWhoName(names map[string]*whoNameEvidence, identifiers []string) string {
	contact := map[string]int{}
	push := map[string]int{}
	other := map[string]int{}
	for _, name := range names {
		if !humanWhoName(name.value) {
			continue
		}
		switch {
		case name.contactFull:
			contact[name.value]++
		case name.pushCount > 0:
			push[name.value] = name.pushCount
		default:
			other[name.value]++
		}
	}
	for _, tier := range []map[string]int{contact, push, other} {
		if name := whomatch.BestDisplayName(tier, identifiers); name != "" {
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

func humanWhoName(value string) bool {
	value = normalizeWhoIdentity(value)
	if value == "" || strings.HasPrefix(value, "@") || strings.Contains(value, "@") || looksLikeIdentifierPhone(value) {
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
