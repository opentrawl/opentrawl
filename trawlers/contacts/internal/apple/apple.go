package apple

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
)

func ActionableReadError(err error) error {
	switch sourceStateForError(err) {
	case SourceNeedsFullDiskAccess:
		return fmt.Errorf("cannot read Apple Contacts; grant OpenTrawl Full Disk Access in System Settings > Privacy & Security > Full Disk Access: %w", err)
	case SourceInvalid:
		return fmt.Errorf("could not read Apple Contacts data: %w", err)
	default:
		return fmt.Errorf("could not access Apple Contacts: %w", err)
	}
}

type SourceState string

const (
	SourceReady               SourceState = "ready"
	SourceNeedsFullDiskAccess SourceState = "needs_full_disk_access"
	SourceUnavailable         SourceState = "unavailable"
	SourceInvalid             SourceState = "invalid"
)

type invalidSourceError struct {
	Path string
	Err  error
}

func (e invalidSourceError) Error() string {
	return "invalid Apple AddressBook source " + e.Path + ": " + e.Err.Error()
}

func (e invalidSourceError) Unwrap() error {
	return e.Err
}

func isSourcePermissionError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, os.ErrPermission) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "permission denied") ||
		strings.Contains(message, "operation not permitted") ||
		strings.Contains(message, "not authorized") ||
		strings.Contains(message, "authorization denied")
}

func sourceStateForError(err error) SourceState {
	if err == nil {
		return SourceReady
	}
	var invalidErr invalidSourceError
	if errors.As(err, &invalidErr) {
		return SourceInvalid
	}
	if isSourcePermissionError(err) {
		return SourceNeedsFullDiskAccess
	}
	return SourceUnavailable
}

type Contact struct {
	Identifier string          `json:"identifier"`
	FirstName  string          `json:"first_name"`
	LastName   string          `json:"last_name"`
	FullName   string          `json:"full_name"`
	Emails     []string        `json:"emails"`
	Phones     []string        `json:"phones"`
	Addresses  []PostalAddress `json:"addresses,omitempty"`
	AvatarData []byte          `json:"avatar_data,omitempty"`
}

type PostalAddress struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

func (a *PostalAddress) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err == nil {
		*a = PostalAddress{Value: value}
		return nil
	}
	type postalAddress PostalAddress
	var parsed postalAddress
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	*a = PostalAddress(parsed)
	return nil
}

func (c Contact) Name() string {
	if strings.TrimSpace(c.FullName) != "" {
		return strings.TrimSpace(c.FullName)
	}
	return strings.TrimSpace(strings.Join([]string{c.FirstName, c.LastName}, " "))
}

func (c Contact) SourceContact(includeAvatar bool) model.SourceContact {
	out := model.SourceContact{Source: "apple", ExternalID: c.Identifier, Name: c.Name()}
	for i, email := range c.Emails {
		if strings.TrimSpace(email) != "" {
			out.Emails = append(out.Emails, model.ContactValue{Value: email, Label: "other", Source: "apple", Primary: i == 0})
		}
	}
	for i, phone := range c.Phones {
		if strings.TrimSpace(phone) != "" {
			out.Phones = append(out.Phones, model.ContactValue{Value: phone, Label: "other", Source: "apple", Primary: i == 0})
		}
	}
	for i, address := range c.Addresses {
		value := strings.TrimSpace(address.Value)
		if value != "" {
			out.Addresses = append(out.Addresses, model.ContactValue{Value: value, Label: addressLabel(address.Label), Source: "apple", Primary: i == 0})
		}
	}
	if includeAvatar && len(c.AvatarData) > 0 {
		out.Avatar = &model.SourceAvatar{Data: append([]byte(nil), c.AvatarData...)}
	}
	return out
}

func addressLabel(label string) string {
	normalized := strings.ToLower(strings.TrimSpace(label))
	normalized = strings.TrimPrefix(normalized, "_$!<")
	normalized = strings.TrimSuffix(normalized, ">!$_")
	switch normalized {
	case "home":
		return "home"
	case "work":
		return "work"
	default:
		return "other"
	}
}

func ReadFile(path string) ([]Contact, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return Decode(f)
}

func Decode(r io.Reader) ([]Contact, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return nil, nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var contacts []Contact
		if err := json.Unmarshal([]byte(trimmed), &contacts); err != nil {
			return nil, err
		}
		return contacts, nil
	}
	var contacts []Contact
	scanner := bufio.NewScanner(strings.NewReader(trimmed))
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var c Contact
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			return nil, err
		}
		contacts = append(contacts, c)
	}
	return contacts, scanner.Err()
}

func ToSourceContacts(contacts []Contact, includeAvatars bool) []model.SourceContact {
	out := make([]model.SourceContact, 0, len(contacts))
	for _, contact := range contacts {
		if strings.TrimSpace(contact.Name()) == "" {
			continue
		}
		out = append(out, contact.SourceContact(includeAvatars))
	}
	return out
}
