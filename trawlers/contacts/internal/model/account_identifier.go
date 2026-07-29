package model

import (
	"strings"
	"unicode"
)

func AccountIdentifierForHumanPresentation(
	accountServiceName string,
	accountIdentifier string,
) string {
	switch strings.ToLower(strings.TrimSpace(accountServiceName)) {
	case "calendar", "imessage", "whatsapp":
		return ""
	case "telegram":
		if !strings.HasPrefix(strings.TrimSpace(accountIdentifier), "@") {
			return ""
		}
	}
	accountIdentifier = strings.TrimSpace(accountIdentifier)
	if accountIdentifier == "" {
		return ""
	}
	containsLetter := false
	for _, character := range accountIdentifier {
		switch {
		case unicode.IsLetter(character):
			containsLetter = true
		case unicode.IsDigit(character),
			character == '@',
			character == '.',
			character == '_',
			character == '-':
		default:
			return ""
		}
	}
	if !containsLetter || opaqueHexadecimalAccountIdentifier(accountIdentifier) {
		return ""
	}
	return accountIdentifier
}

func opaqueHexadecimalAccountIdentifier(accountIdentifier string) bool {
	compactAccountIdentifier := strings.ToLower(
		strings.ReplaceAll(accountIdentifier, "-", ""),
	)
	if len(compactAccountIdentifier) < 16 {
		return false
	}
	for _, character := range compactAccountIdentifier {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}
