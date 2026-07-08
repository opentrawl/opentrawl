package crawlkit

import "time"

type Query struct {
	Text          string
	Limit         int
	After, Before time.Time
	Who           string
	WhoResolved   *WhoResolved
}

type WhoResolved struct {
	Who         string   `json:"who"`
	Identifiers []string `json:"identifiers"`
}

type SearchResult struct {
	WhoResolved  *WhoResolved `json:"who_resolved,omitempty"`
	Results      []Hit        `json:"results"`
	TotalMatches int          `json:"total_matches"`
	Truncated    bool         `json:"truncated"`
}

type Hit struct {
	Source       string    `json:"source,omitempty"`
	Ref          string    `json:"ref"`
	ShortRef     string    `json:"short_ref,omitempty"`
	Time         time.Time `json:"time"`
	Who          string    `json:"who,omitempty"`
	Where        string    `json:"where,omitempty"`
	Calendar     string    `json:"calendar,omitempty"`
	Snippet      string    `json:"snippet,omitempty"`
	AllDay       bool      `json:"all_day,omitempty"`
	Availability *int64    `json:"availability,omitempty"`
}
