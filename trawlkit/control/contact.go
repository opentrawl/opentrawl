package control

import (
	"fmt"
	"strings"
)

type PeopleSnapshot struct {
	Contacts []Contact `json:"contacts"`
}

type Contact struct {
	// SourceID is the source-local identity of this contact. It is opaque to
	// People and exists only so repeated snapshots update the same source node
	// when a display name or identifier changes.
	SourceID       string              `json:"source_id,omitempty"`
	DisplayName    string              `json:"display_name"`
	EmailAddresses []string            `json:"email_addresses,omitempty"`
	PhoneNumbers   []string            `json:"phone_numbers,omitempty"`
	Accounts       map[string][]string `json:"accounts,omitempty"`
}

func ValidatePeopleSnapshot(value PeopleSnapshot) error {
	seenSourceIDs := map[string]struct{}{}
	for i, contact := range value.Contacts {
		if sourceID := strings.TrimSpace(contact.SourceID); sourceID != "" {
			if _, ok := seenSourceIDs[sourceID]; ok {
				return fmt.Errorf("contact %d repeats source id %q", i, sourceID)
			}
			seenSourceIDs[sourceID] = struct{}{}
		}
		if strings.TrimSpace(contact.DisplayName) == "" {
			return fmt.Errorf("contact %d display name is required", i)
		}
		if len(contact.EmailAddresses) == 0 && len(contact.PhoneNumbers) == 0 && len(contact.Accounts) == 0 {
			return fmt.Errorf("contact %d requires at least one identifier", i)
		}
		seenEmails := map[string]struct{}{}
		for _, email := range contact.EmailAddresses {
			email = strings.ToLower(strings.TrimSpace(email))
			if email == "" {
				return fmt.Errorf("contact %d contains an empty email address", i)
			}
			if _, ok := seenEmails[email]; ok {
				return fmt.Errorf("contact %d contains duplicate email address %q", i, email)
			}
			seenEmails[email] = struct{}{}
		}
		seen := map[string]struct{}{}
		for _, phone := range contact.PhoneNumbers {
			phone = strings.TrimSpace(phone)
			if phone == "" {
				return fmt.Errorf("contact %d contains an empty phone number", i)
			}
			if _, ok := seen[phone]; ok {
				return fmt.Errorf("contact %d contains duplicate phone number %q", i, phone)
			}
			seen[phone] = struct{}{}
		}
		seenProviders := map[string]struct{}{}
		for provider, values := range contact.Accounts {
			provider = strings.TrimSpace(provider)
			if provider == "" {
				return fmt.Errorf("contact %d contains an empty account provider", i)
			}
			providerKey := strings.ToLower(provider)
			if _, ok := seenProviders[providerKey]; ok {
				return fmt.Errorf("contact %d contains duplicate account provider %q", i, provider)
			}
			seenProviders[providerKey] = struct{}{}
			if len(values) == 0 {
				return fmt.Errorf("contact %d contains no %s accounts", i, provider)
			}
			seen := map[string]struct{}{}
			for _, value := range values {
				value = strings.TrimSpace(value)
				if value == "" {
					return fmt.Errorf("contact %d contains an empty %s account", i, provider)
				}
				key := strings.ToLower(value)
				if _, ok := seen[key]; ok {
					return fmt.Errorf("contact %d contains duplicate %s account %q", i, provider, value)
				}
				seen[key] = struct{}{}
			}
		}
	}
	return nil
}
