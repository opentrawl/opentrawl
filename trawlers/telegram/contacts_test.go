package telecrawl

import (
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
)

func TestPeopleExportKeepsStablePeerIDAcrossMutableFacts(t *testing.T) {
	older := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)
	newer := older.Add(time.Hour)
	first := exportContacts([]store.Contact{
		{JID: "200", Phone: "+15550100", FullName: "Ada Old", UpdatedAt: older},
		{JID: "100", Phone: "+15550100", FullName: "Ada Current", Username: "ada_old", UpdatedAt: newer},
	})
	second := exportContacts([]store.Contact{
		{JID: "100", Phone: "+15550100", FullName: "Ada Renamed", Username: "ada_new", UpdatedAt: older},
		{JID: "200", Phone: "+15550100", FullName: "Ada Newest", UpdatedAt: newer},
	})
	if len(first) != 1 || len(second) != 1 || first[0].SourceID != "100" || second[0].SourceID != "100" {
		t.Fatalf("source identity changed with preferred facts: first=%#v second=%#v", first, second)
	}
}
