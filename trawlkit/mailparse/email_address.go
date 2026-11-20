package mailparse

import (
	"net/mail"
	"strings"
)

// NormalizedEmailAddress is an addr-spec in lower case without surrounding
// space, quotes or angle brackets.
type NormalizedEmailAddress string

type EmailAddress struct {
	DisplayName            string
	NormalizedEmailAddress NormalizedEmailAddress
}

func NormalizeEmailAddress(value string) NormalizedEmailAddress {
	return NormalizedEmailAddress(strings.ToLower(cleanEmailAddressText(value)))
}

// ParseEmailAddress reads one address from a From-style header. Text that is
// not an address stays in DisplayName; it never becomes the email address.
func ParseEmailAddress(headerValue string) EmailAddress {
	addresses := ParseEmailAddressList(headerValue)
	if len(addresses) > 0 {
		return addresses[0]
	}
	displayName := cleanEmailAddressText(DecodeEmailHeaderText(headerValue))
	return EmailAddress{DisplayName: strings.Join(strings.Fields(displayName), " ")}
}

func ParseEmailAddressList(headerValue string) []EmailAddress {
	headerValue = strings.TrimSpace(DecodeEmailHeaderText(headerValue))
	if headerValue == "" {
		return nil
	}
	if parsedAddresses, err := mail.ParseAddressList(headerValue); err == nil {
		addresses := make([]EmailAddress, 0, len(parsedAddresses))
		for _, parsedAddress := range parsedAddresses {
			if address, ok := newEmailAddress(parsedAddress.Name, parsedAddress.Address); ok {
				addresses = append(addresses, address)
			}
		}
		return addresses
	}
	var addresses []EmailAddress
	for _, listItem := range splitEmailAddressListItems(headerValue) {
		if address, ok := parseEmailAddressListItem(listItem); ok {
			addresses = append(addresses, address)
		}
	}
	return addresses
}

// FormatEmailAddress writes "Name <address>", or whichever part exists.
func FormatEmailAddress(address EmailAddress) string {
	switch {
	case address.NormalizedEmailAddress == "":
		return address.DisplayName
	case address.DisplayName == "":
		return string(address.NormalizedEmailAddress)
	default:
		return (&mail.Address{Name: address.DisplayName, Address: string(address.NormalizedEmailAddress)}).String()
	}
}

func FormatEmailAddressList(addresses []EmailAddress) string {
	formatted := make([]string, 0, len(addresses))
	for _, address := range addresses {
		if text := FormatEmailAddress(address); text != "" {
			formatted = append(formatted, text)
		}
	}
	return strings.Join(formatted, ", ")
}

func IsEmailAddressText(value string) bool {
	value = strings.TrimSpace(value)
	if strings.ContainsAny(value, " \t\r\n<>") {
		return false
	}
	localPart, domain, found := strings.Cut(value, "@")
	return found && strings.TrimSpace(localPart) != "" && strings.TrimSpace(domain) != ""
}

func newEmailAddress(displayName, addressText string) (EmailAddress, bool) {
	displayName = strings.Join(strings.Fields(cleanEmailAddressText(displayName)), " ")
	addressText = cleanEmailAddressText(addressText)
	if !IsEmailAddressText(addressText) {
		if addressText != "" && displayName == "" {
			displayName = addressText
		}
		addressText = ""
	}
	address := EmailAddress{DisplayName: displayName, NormalizedEmailAddress: NormalizeEmailAddress(addressText)}
	if IsEmailAddressText(displayName) && strings.EqualFold(displayName, addressText) {
		address.DisplayName = ""
	}
	return address, address.DisplayName != "" || address.NormalizedEmailAddress != ""
}

func parseEmailAddressListItem(listItem string) (EmailAddress, bool) {
	listItem = strings.TrimSpace(listItem)
	if parsedAddress, err := mail.ParseAddress(listItem); err == nil {
		return newEmailAddress(parsedAddress.Name, parsedAddress.Address)
	}
	if start, end := strings.LastIndex(listItem, "<"), strings.LastIndex(listItem, ">"); start >= 0 && end > start {
		return newEmailAddress(listItem[:start], listItem[start+1:end])
	}
	if cleaned := cleanEmailAddressText(listItem); IsEmailAddressText(cleaned) {
		return newEmailAddress("", cleaned)
	}
	// Free text that is neither an address nor "name <address>" is not a
	// participant: making a person from it creates false people.
	return EmailAddress{}, false
}

func splitEmailAddressListItems(headerValue string) []string {
	var items []string
	start := 0
	inQuote, escaped := false, false
	angleDepth := 0
	for index, character := range headerValue {
		switch {
		case escaped:
			escaped = false
		case inQuote && character == '\\':
			escaped = true
		case character == '"':
			inQuote = !inQuote
		case !inQuote && character == '<':
			angleDepth++
		case !inQuote && character == '>' && angleDepth > 0:
			angleDepth--
		case !inQuote && angleDepth == 0 && character == ',':
			if item := strings.TrimSpace(headerValue[start:index]); item != "" {
				items = append(items, item)
			}
			start = index + 1
		}
	}
	if item := strings.TrimSpace(headerValue[start:]); item != "" {
		items = append(items, item)
	}
	return items
}

func cleanEmailAddressText(value string) string {
	value = strings.TrimSpace(strings.Trim(strings.TrimSpace(value), "<>"))
	for len(value) >= 2 {
		first, last := value[0], value[len(value)-1]
		if (first != '\'' && first != '"') || first != last {
			break
		}
		value = strings.TrimSpace(value[1 : len(value)-1])
	}
	return value
}
