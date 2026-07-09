package archive

import (
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/contacts/internal/model"
	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

const (
	AppID         = "contacts"
	DisplayName   = "Contacts"
	SchemaVersion = 1
)

type Store struct {
	store *ckstore.Store
	path  string
	owned bool
}

type Status struct {
	ArchivePath   string
	ArchiveBytes  int64
	SchemaVersion int
	People        int64
	Notes         int64
	Sources       int64
	UpdatedAt     time.Time
}

type SearchOptions struct {
	Limit  int
	After  time.Time
	Before time.Time
}

type SearchResult struct {
	Ref      string
	Time     time.Time
	Who      string
	Snippet  string
	PersonID string
	ShortRef string
}

type ImportSummary struct {
	People     int `json:"people"`
	Notes      int `json:"notes"`
	Created    int `json:"created"`
	Updated    int `json:"updated"`
	Unchanged  int `json:"unchanged"`
	DerivedIDs int `json:"derived_ids"`
}

func PersonRef(id string) string {
	return AppID + ":person/" + strings.TrimSpace(id)
}

func PersonIDFromRef(ref string) (string, bool) {
	value := strings.TrimSpace(ref)
	for _, prefix := range []string{AppID + ":person/", "person/", "people/"} {
		if strings.HasPrefix(value, prefix) {
			id := strings.TrimSpace(strings.TrimPrefix(value, prefix))
			return id, id != "" && !strings.ContainsAny(id, "\r\n\t")
		}
	}
	return value, value != "" && !strings.ContainsAny(value, "\r\n\t")
}

type personWithNotes struct {
	Person     model.Person
	Notes      []model.Note
	DerivedIDs int
}
