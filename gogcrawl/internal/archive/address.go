package archive

import (
	"net/mail"
	"strings"
)

type parsedAddress struct {
	Name    string
	Address string
}

func parseAddressHeader(value string) (string, string) {
	value = strings.TrimSpace(decodeHeader(value))
	if value == "" {
		return "", ""
	}
	address, err := mail.ParseAddress(value)
	if err != nil {
		return "", strings.TrimSpace(value)
	}
	parsed := cleanParsedAddress(parsedAddress{
		Name:    address.Name,
		Address: address.Address,
	})
	return parsed.Name, parsed.Address
}

func parseAddressListHeader(value string) string {
	addresses := parseAddressList(value)
	if len(addresses) == 0 {
		return ""
	}
	out := make([]string, 0, len(addresses))
	for _, address := range addresses {
		if formatted := formatParsedAddress(address); formatted != "" {
			out = append(out, formatted)
		}
	}
	return strings.Join(out, ", ")
}

func parseAddressList(value string) []parsedAddress {
	value = strings.TrimSpace(decodeHeader(value))
	if value == "" {
		return nil
	}
	if addresses, err := mail.ParseAddressList(value); err == nil {
		return parsedAddressList(addresses)
	}
	parts := splitAddressList(value)
	out := make([]parsedAddress, 0, len(parts))
	for _, part := range parts {
		if address, ok := parseAddressListPart(part); ok {
			out = append(out, address)
		}
	}
	return out
}

func parsedAddressList(addresses []*mail.Address) []parsedAddress {
	out := make([]parsedAddress, 0, len(addresses))
	for _, address := range addresses {
		if address == nil {
			continue
		}
		parsed := cleanParsedAddress(parsedAddress{
			Name:    address.Name,
			Address: address.Address,
		})
		if parsed.Name != "" || parsed.Address != "" {
			out = append(out, parsed)
		}
	}
	return out
}

func parseAddressListPart(value string) (parsedAddress, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return parsedAddress{}, false
	}
	if address, err := mail.ParseAddress(value); err == nil {
		parsed := cleanParsedAddress(parsedAddress{
			Name:    address.Name,
			Address: address.Address,
		})
		return parsed, parsed.Name != "" || parsed.Address != ""
	}
	if name, address, ok := splitAngleAddress(value); ok {
		parsed := cleanParsedAddress(parsedAddress{
			Name:    name,
			Address: address,
		})
		return parsed, parsed.Name != "" || parsed.Address != ""
	}
	value = cleanAddressText(value)
	if value == "" {
		return parsedAddress{}, false
	}
	if isEmailIdentifier(value) {
		return parsedAddress{Address: value}, true
	}
	// Free text that parses as neither an address nor name <address> is
	// not a participant: minting a name identity from malformed header
	// text creates false people (review finding, wave 2).
	return parsedAddress{}, false
}

func splitAddressList(value string) []string {
	var parts []string
	start := 0
	inQuote := false
	escaped := false
	angleDepth := 0
	for i, r := range value {
		switch {
		case escaped:
			escaped = false
		case inQuote && r == '\\':
			escaped = true
		case r == '"':
			inQuote = !inQuote
		case !inQuote && r == '<':
			angleDepth++
		case !inQuote && r == '>' && angleDepth > 0:
			angleDepth--
		case !inQuote && angleDepth == 0 && r == ',':
			if part := strings.TrimSpace(value[start:i]); part != "" {
				parts = append(parts, part)
			}
			start = i + len(string(r))
		}
	}
	if part := strings.TrimSpace(value[start:]); part != "" {
		parts = append(parts, part)
	}
	return parts
}

func splitAngleAddress(value string) (string, string, bool) {
	start := strings.LastIndex(value, "<")
	end := strings.LastIndex(value, ">")
	if start < 0 || end < 0 || end <= start {
		return "", "", false
	}
	return value[:start], value[start+1 : end], true
}

func cleanParsedAddress(address parsedAddress) parsedAddress {
	address.Name = cleanAddressText(address.Name)
	address.Address = cleanAddressText(address.Address)
	if redundantAddressName(address.Name, address.Address) {
		address.Name = ""
	}
	return address
}

func cleanAddressText(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "<>")
	value = strings.TrimSpace(value)
	return unquoteAddressText(value)
}

func unquoteAddressText(value string) string {
	for {
		value = strings.TrimSpace(value)
		if len(value) < 2 {
			return value
		}
		first := value[0]
		last := value[len(value)-1]
		if (first != '\'' && first != '"') || first != last {
			return value
		}
		value = value[1 : len(value)-1]
	}
}

func redundantAddressName(name, address string) bool {
	name = unquoteAddressText(name)
	address = cleanAddressText(address)
	return isEmailIdentifier(name) && strings.EqualFold(name, address)
}

func formatParsedAddress(address parsedAddress) string {
	address = cleanParsedAddress(address)
	switch {
	case address.Address == "":
		return address.Name
	case address.Name == "":
		return address.Address
	default:
		return (&mail.Address{Name: address.Name, Address: address.Address}).String()
	}
}
