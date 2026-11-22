package model

import "time"

type ContactValue struct {
	Value   string `json:"value" yaml:"value"`
	Label   string `json:"label,omitempty" yaml:"label,omitempty"`
	Source  string `json:"source,omitempty" yaml:"source,omitempty"`
	Primary bool   `json:"primary,omitempty" yaml:"primary,omitempty"`
}

type ExternalRef struct {
	ID         string    `json:"id,omitempty" yaml:"id,omitempty"`
	Resource   string    `json:"resource,omitempty" yaml:"resource,omitempty"`
	ETag       string    `json:"etag,omitempty" yaml:"etag,omitempty"`
	LastSeenAt time.Time `json:"last_seen_at,omitzero" yaml:"last_seen_at,omitempty"`
}

type AvatarRef struct {
	Source    string    `json:"source,omitempty" yaml:"source,omitempty"`
	MIME      string    `json:"mime,omitempty" yaml:"mime,omitempty"`
	SHA256    string    `json:"sha256,omitempty" yaml:"sha256,omitempty"`
	Width     int       `json:"width,omitempty" yaml:"width,omitempty"`
	Height    int       `json:"height,omitempty" yaml:"height,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitzero" yaml:"updated_at,omitempty"`
	Data      []byte    `json:"-" yaml:"-"`
}

type PersonSource struct {
	Names                                                 []string            `json:"names,omitempty" yaml:"names,omitempty"`
	Tags                                                  []string            `json:"tags,omitempty" yaml:"tags,omitempty"`
	Emails                                                []string            `json:"emails,omitempty" yaml:"emails,omitempty"`
	Phones                                                []string            `json:"phones,omitempty" yaml:"phones,omitempty"`
	Addresses                                             []string            `json:"addresses,omitempty" yaml:"addresses,omitempty"`
	Accounts                                              map[string][]string `json:"accounts,omitempty" yaml:"accounts,omitempty"`
	LastSeenAt                                            time.Time           `json:"last_seen_at,omitzero" yaml:"last_seen_at,omitempty"`
	LatestArchiveRecordTimeInvolvingPersonInSourceArchive time.Time           `json:"latest_archive_record_time_involving_person_in_source_archive,omitzero" yaml:"latest_archive_record_time_involving_person_in_source_archive,omitempty"`
	MessageCountInvolvingPersonInSourceArchive            uint64              `json:"message_count_involving_person_in_source_archive,omitempty" yaml:"message_count_involving_person_in_source_archive,omitempty"`
}

type PersonRelationshipOrContextDescriptionStatedDate struct {
	CalendarYear        int32 `json:"calendar_year" yaml:"calendar_year"`
	CalendarMonthNumber int32 `json:"calendar_month_number" yaml:"calendar_month_number"`
	CalendarDayOfMonth  int32 `json:"calendar_day_of_month" yaml:"calendar_day_of_month"`
}

type PersonRelationshipOrContextDescription string

func (date PersonRelationshipOrContextDescriptionStatedDate) IsZero() bool {
	return date.CalendarYear == 0 && date.CalendarMonthNumber == 0 && date.CalendarDayOfMonth == 0
}

type Person struct {
	ID                                               string                                           `json:"id" yaml:"id"`
	Name                                             string                                           `json:"name" yaml:"name"`
	SortName                                         string                                           `json:"sort_name,omitempty" yaml:"sort_name,omitempty"`
	AKA                                              []string                                         `json:"aka,omitempty" yaml:"aka,omitempty"`
	Tags                                             []string                                         `json:"tags,omitempty" yaml:"tags,omitempty"`
	Emails                                           []ContactValue                                   `json:"emails,omitempty" yaml:"emails,omitempty"`
	Phones                                           []ContactValue                                   `json:"phones,omitempty" yaml:"phones,omitempty"`
	Addresses                                        []ContactValue                                   `json:"addresses,omitempty" yaml:"addresses,omitempty"`
	Avatar                                           AvatarRef                                        `json:"avatar,omitzero" yaml:"avatar,omitempty"`
	Accounts                                         map[string][]string                              `json:"accounts,omitempty" yaml:"accounts,omitempty"`
	Sources                                          map[string]PersonSource                          `json:"sources,omitempty" yaml:"sources,omitempty"`
	Apple                                            ExternalRef                                      `json:"apple,omitzero" yaml:"apple,omitempty"`
	Google                                           ExternalRef                                      `json:"google,omitzero" yaml:"google,omitempty"`
	PersonRelationshipOrContextDescription           PersonRelationshipOrContextDescription           `json:"person_relationship_or_context_description,omitempty" yaml:"person_relationship_or_context_description,omitempty"`
	PersonRelationshipOrContextDescriptionStatedDate PersonRelationshipOrContextDescriptionStatedDate `json:"person_relationship_or_context_description_stated_date,omitzero" yaml:"person_relationship_or_context_description_stated_date,omitempty"`
	CreatedAt                                        time.Time                                        `json:"created_at" yaml:"created_at"`
	UpdatedAt                                        time.Time                                        `json:"updated_at" yaml:"updated_at"`
	Path                                             string                                           `json:"path,omitempty" yaml:"-"`
	Body                                             string                                           `json:"body,omitempty" yaml:"-"`
	Extra                                            map[string]map[string]any                        `json:"extra,omitempty" yaml:"-"`
}

type Note struct {
	ID         string    `json:"id" yaml:"id"`
	PersonID   string    `json:"person_id" yaml:"person_id"`
	OccurredAt time.Time `json:"occurred_at" yaml:"occurred_at"`
	CapturedAt time.Time `json:"captured_at" yaml:"captured_at"`
	Kind       string    `json:"kind" yaml:"kind"`
	Source     string    `json:"source" yaml:"source"`
	Account    string    `json:"account,omitempty" yaml:"account,omitempty"`
	ExternalID string    `json:"external_id,omitempty" yaml:"external_id,omitempty"`
	Direction  string    `json:"direction,omitempty" yaml:"direction,omitempty"`
	Confidence string    `json:"confidence,omitempty" yaml:"confidence,omitempty"`
	Topics     []string  `json:"topics,omitempty" yaml:"topics,omitempty"`
	FollowUpAt time.Time `json:"follow_up_at,omitzero" yaml:"follow_up_at,omitempty"`
	Privacy    string    `json:"privacy,omitempty" yaml:"privacy,omitempty"`
	Path       string    `json:"path,omitempty" yaml:"-"`
	Body       string    `json:"body,omitempty" yaml:"-"`
}

type SearchHit struct {
	Kind      string    `json:"kind"`
	ID        string    `json:"id"`
	PersonID  string    `json:"person_id,omitempty"`
	Name      string    `json:"name,omitempty"`
	Path      string    `json:"path"`
	Score     int       `json:"score"`
	Snippet   string    `json:"snippet,omitempty"`
	Timestamp time.Time `json:"timestamp,omitzero"`
}
