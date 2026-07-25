package contacts

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
	"github.com/opentrawl/opentrawl/trawlkit"
	ckoutput "github.com/opentrawl/opentrawl/trawlkit/output"
)

func TestPersonJSONProjectsStablePublicFields(t *testing.T) {
	person := model.Person{
		ID:        "person-42",
		Name:      "Avery Example",
		Emails:    []model.ContactValue{{Value: "avery@example.com", Label: "work", Source: "provider-private", Primary: true}},
		Accounts:  map[string][]string{"telegram": {"avery_example"}},
		Apple:     model.ExternalRef{ID: "apple-private"},
		Google:    model.ExternalRef{ID: "google-private"},
		Avatar:    model.AvatarRef{SHA256: "private-hash"},
		Path:      "/private/archive",
		Body:      "private body",
		Extra:     map[string]map[string]any{"private": {"token": "hidden"}},
		CreatedAt: time.Date(2026, 7, 20, 9, 30, 0, 0, time.UTC),
	}
	var stdout bytes.Buffer
	req := &trawlkit.Request{Format: ckoutput.JSON, Out: &stdout}
	if err := writePerson(req, person); err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	for _, want := range []string{`"ref": "contacts:person/person-42"`, `"name": "Avery Example"`, `"value": "avery@example.com"`, `"telegram": [`, `"avery_example"`} {
		if !strings.Contains(output, want) {
			t.Fatalf("public JSON omitted %s: %s", want, output)
		}
	}
	for _, forbidden := range []string{"provider-private", "apple-private", "google-private", "private-hash", "/private/archive", "private body", "token", `"id"`, `"created_at"`, `"updated_at"`, `"sources"`} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("public JSON exposed private storage value %q: %s", forbidden, output)
		}
	}
}
