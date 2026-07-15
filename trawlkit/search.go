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

type ResultSummary struct {
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
}

type ArchiveContext struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
}

const MatchAnchorID = "match"

type TextRun struct {
	Text    string `json:"text"`
	Matched bool   `json:"matched"`
}

type EvidenceFragment struct {
	Label    string            `json:"label"`
	Text     *TextEvidence     `json:"text,omitempty"`
	Field    *FieldEvidence    `json:"field,omitempty"`
	Media    *MediaEvidence    `json:"media,omitempty"`
	Relation *RelationEvidence `json:"relation,omitempty"`
}

type TextEvidence struct {
	Runs []TextRun `json:"runs"`
}
type FieldEvidence struct {
	Name  string    `json:"name"`
	Value []TextRun `json:"value"`
}
type MediaEvidence struct {
	ResourceRef string    `json:"resource_ref,omitempty"`
	Description []TextRun `json:"description"`
}
type RelationEvidence struct {
	Relation string    `json:"relation"`
	Target   []TextRun `json:"target"`
}

func TextMatch(label, text string) EvidenceFragment {
	return EvidenceFragment{Label: label, Text: &TextEvidence{Runs: matchedRuns(text)}}
}

func FieldMatch(label, name, value string) EvidenceFragment {
	return EvidenceFragment{Label: label, Field: &FieldEvidence{Name: name, Value: matchedRuns(value)}}
}

func MediaMatch(label, resourceRef, description string) EvidenceFragment {
	return EvidenceFragment{Label: label, Media: &MediaEvidence{ResourceRef: resourceRef, Description: matchedRuns(description)}}
}

func RelationMatch(label, relation, target string) EvidenceFragment {
	return EvidenceFragment{Label: label, Relation: &RelationEvidence{Relation: relation, Target: matchedRuns(target)}}
}

func matchedRuns(value string) []TextRun {
	return []TextRun{{Text: value, Matched: true}}
}

type Hit struct {
	Source       string             `json:"source,omitempty"`
	Ref          string             `json:"ref"`
	ShortRef     string             `json:"short_ref,omitempty"`
	Time         time.Time          `json:"time"`
	AnchorID     string             `json:"anchor_id"`
	Summary      ResultSummary      `json:"summary"`
	Archive      []ArchiveContext   `json:"archive_context,omitempty"`
	Evidence     []EvidenceFragment `json:"evidence"`
	AllDay       bool               `json:"all_day,omitempty"`
	Availability *int64             `json:"availability,omitempty"`
	// Unread is nil for a surface that stores no read state, so the field
	// drops out of JSON rather than reporting a fake false (mirrors
	// ChatQuery/Chat.Unread's optional-fact convention in contracts.go).
	Unread *bool `json:"unread,omitempty"`
}
