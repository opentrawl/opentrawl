package telegram

import "github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"

const (
	defaultSearchLimit = 20
	openContextRadius  = 10
)

type searchEnvelope struct {
	Query        string         `json:"query"`
	WhoQuery     string         `json:"-"`
	Limit        int            `json:"-"`
	WhoResolved  *whoResolved   `json:"who_resolved,omitempty"`
	Results      []searchResult `json:"results"`
	TotalMatches int            `json:"total_matches"`
	Truncated    bool           `json:"truncated"`
}

type contactsEnvelope struct {
	Contacts []store.Contact
	Total    int
}

type foldersEnvelope struct {
	Folders []store.Folder
}

type whoResolved struct {
	Who         string   `json:"who"`
	Identifiers []string `json:"identifiers"`
}

type searchResult struct {
	Ref      string `json:"ref"`
	ShortRef string `json:"short_ref"`
	Time     string `json:"time"`
	Who      string `json:"who,omitempty"`
	Where    string `json:"where,omitempty"`
	Snippet  string `json:"snippet"`
}
