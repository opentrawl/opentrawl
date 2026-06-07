package contactexport

import (
	"strings"
	"testing"
)

func TestDecodeNormalizesContacts(t *testing.T) {
	got, err := Decode(strings.NewReader(`{"contacts":[{"display_name":" Ada Lovelace ","phone_numbers":[" +1 555 0100 ","","+1 555 0100"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Contacts) != 1 {
		t.Fatalf("contacts = %#v", got.Contacts)
	}
	if got.Contacts[0].DisplayName != "Ada Lovelace" {
		t.Fatalf("name = %q", got.Contacts[0].DisplayName)
	}
	if got.Contacts[0].PhoneNumbers[0] != "+1 555 0100" || len(got.Contacts[0].PhoneNumbers) != 1 {
		t.Fatalf("phones = %#v", got.Contacts[0].PhoneNumbers)
	}
}

func TestDecodeRejectsBadContacts(t *testing.T) {
	for _, input := range []string{
		`{`,
		`{}`,
		`{"contacts":null}`,
		`{"contacts":[{}]}`,
		`{"contacts":[{"display_name":"Ada","phone_numbers":[]}]}`,
		`{"contacts":[{"display_name":"","phone_numbers":["123"]}]}`,
		`{"contacts":[{"display_name":"Ada","phone_numbers":["123"],"extra":"x"}]}`,
		`{"contacts":[]}{"contacts":[]}`,
		`{"contacts":[]}
private junk`,
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := Decode(strings.NewReader(input)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}
