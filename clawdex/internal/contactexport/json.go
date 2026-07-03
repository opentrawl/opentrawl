package contactexport

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

type ContactExport struct {
	Contacts                  []Contact `json:"contacts"`
	SkippedWithoutIdentifiers int       `json:"-"`
}

type Contact struct {
	DisplayName  string              `json:"display_name"`
	PhoneNumbers []string            `json:"phone_numbers,omitempty"`
	Emails       []string            `json:"emails,omitempty"`
	Addresses    []string            `json:"addresses,omitempty"`
	Accounts     map[string][]string `json:"accounts,omitempty"`
	Handles      map[string][]string `json:"handles,omitempty"`
}

func Decode(r io.Reader) (ContactExport, error) {
	var out ContactExport
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&out); err != nil {
		return ContactExport{}, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return ContactExport{}, errors.New("contact export must contain exactly one JSON value")
		}
		return ContactExport{}, err
	}
	if err := out.Normalize(); err != nil {
		return ContactExport{}, err
	}
	return out, nil
}

func (e *ContactExport) Normalize() error {
	if e == nil {
		return errors.New("contact export is nil")
	}
	if e.Contacts == nil {
		return errors.New("contact export missing contacts")
	}
	e.SkippedWithoutIdentifiers = 0
	contacts := e.Contacts[:0]
	for i := range e.Contacts {
		c := e.Contacts[i]
		name := strings.TrimSpace(c.DisplayName)
		phones := cleanPhones(c.PhoneNumbers)
		emails := cleanEmails(c.Emails)
		addresses := cleanAddresses(c.Addresses)
		accounts := cleanAccounts(c.Accounts)
		handles := cleanAccounts(c.Handles)
		if len(phones) == 0 && len(emails) == 0 && len(accounts) == 0 && len(handles) == 0 {
			e.SkippedWithoutIdentifiers++
			continue
		}
		if name == "" {
			return fmt.Errorf("contact %d missing display_name", i)
		}
		c.DisplayName = name
		c.PhoneNumbers = phones
		c.Emails = emails
		c.Addresses = addresses
		c.Accounts = accounts
		c.Handles = handles
		contacts = append(contacts, c)
	}
	e.Contacts = contacts
	return nil
}

func cleanPhones(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func cleanEmails(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func cleanAddresses(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = cleanAddress(value)
		key := strings.ToLower(strings.Join(strings.Fields(value), " "))
		if value == "" || key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, value)
	}
	return out
}

func cleanAddress(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	lines := strings.Split(value, "\n")
	out := lines[:0]
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func cleanAccounts(accounts map[string][]string) map[string][]string {
	if len(accounts) == 0 {
		return nil
	}
	out := map[string][]string{}
	for service, values := range accounts {
		service = strings.TrimSpace(strings.ToLower(service))
		if service == "" {
			continue
		}
		cleaned := cleanAccountValues(values)
		if len(cleaned) > 0 {
			out[service] = cleaned
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func cleanAccountValues(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
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
