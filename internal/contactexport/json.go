package contactexport

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

type ContactExport struct {
	Contacts []Contact `json:"contacts"`
}

type Contact struct {
	DisplayName  string   `json:"display_name"`
	PhoneNumbers []string `json:"phone_numbers"`
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
	contacts := e.Contacts[:0]
	for i := range e.Contacts {
		c := e.Contacts[i]
		name := strings.TrimSpace(c.DisplayName)
		phones := cleanPhones(c.PhoneNumbers)
		if name == "" {
			return fmt.Errorf("contact %d missing display_name", i)
		}
		if len(phones) == 0 {
			return fmt.Errorf("contact %q missing phone_numbers", name)
		}
		c.DisplayName = name
		c.PhoneNumbers = phones
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
