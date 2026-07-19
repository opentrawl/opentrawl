package archive

import (
	"database/sql"
	"strings"
	"time"

	ckstore "github.com/opentrawl/opentrawl/trawlkit/store"
)

const (
	AppID         = "contacts"
	DisplayName   = "Contacts"
	SchemaVersion = 2
)

type Store struct {
	store *ckstore.Store
	tx    *sql.Tx
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
	AnchorID string
	Ref      string
	Time     time.Time
	Who      string
	Snippet  string
	PersonID string
	ShortRef string
	Matches  []SearchMatch
}

type SearchMatch struct {
	Field string
	Runs  []SearchTextRun
}

type SearchTextRun struct {
	Text    string
	Matched bool
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
