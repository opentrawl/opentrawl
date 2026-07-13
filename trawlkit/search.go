package trawlkit

import "time"

type Query struct {
	Text  string
	Limit int
	// BoundedTotals requests a lower-bound total when a source finds a probe row.
	BoundedTotals bool
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
	// TotalIsLowerBound reports that TotalMatches is at least Limit plus one.
	TotalIsLowerBound bool `json:"total_is_lower_bound,omitempty"`
	Truncated         bool `json:"truncated"`
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
	// Unread is nil for a surface that stores no read state, so the field
	// drops out of JSON rather than reporting a fake false (mirrors
	// ChatQuery/Chat.Unread's optional-fact convention in contracts.go).
	Unread *bool `json:"unread,omitempty"`
}
