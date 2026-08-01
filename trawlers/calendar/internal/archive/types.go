package archive

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"

	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

type CalendarIdentifier string

type CalendarOwnerOrPurposeAnnotation struct {
	CalendarOwnerOrPurposeDescription           string
	CalendarOwnerOrPurposeDescriptionStatedDate CalendarOwnerOrPurposeDescriptionStatedDate
}

type CalendarOwnerOrPurposeDescriptionStatedDate struct {
	CalendarYear        int32
	CalendarMonthNumber int32
	CalendarDayOfMonth  int32
}

const (
	AppID       = "calendar"
	DisplayName = "Calendar"

	DefaultSearchLimit = 20
)

type Calendar struct {
	ID                               CalendarIdentifier
	SourceRowID                      int64
	Title                            string
	Type                             int64
	ExternalID                       string
	StoreID                          int64
	AccountName                      string
	AccountType                      int64
	AccountDisabled                  bool
	CalendarOwnerOrPurposeAnnotation *CalendarOwnerOrPurposeAnnotation
	EventCount                       int64
	ActiveOrFutureEventCount         int64
}

type Person struct {
	DisplayName string `json:"display_name,omitempty"`
	Email       string `json:"email,omitempty"`
	PhoneNumber string `json:"phone_number,omitempty"`
}

type Attendee struct {
	DisplayName string `json:"display_name,omitempty"`
	Email       string `json:"email,omitempty"`
	PhoneNumber string `json:"phone_number,omitempty"`
	Address     string `json:"address,omitempty"`
	RSVPStatus  string `json:"rsvp_status,omitempty"`
	Role        string `json:"role,omitempty"`
	Self        bool   `json:"self,omitempty"`
	Comment     string `json:"comment,omitempty"`
}

type WhoCandidate struct {
	Who         string   `json:"who"`
	Identifiers []string `json:"identifiers"`
	LastSeen    string   `json:"last_seen"`
	Messages    int64    `json:"messages"`
	// filterIdentifiers are the identifiers that belong to exactly this
	// entity. A shared mailbox stays in Identifiers for display and query
	// matching, but filtering events by it would pull in the other entities
	// it fronts, so Filter() matches on these only.
	filterIdentifiers []string
	// names are the entity's own raw display spellings, used by Filter()
	// alongside filterIdentifiers so an event reachable only by name (a
	// name-joined row, or a shared-mailbox event whose identifier is
	// filter-excluded) still matches. Raw spellings, not the cleaned Who
	// label: a stored name clusters into exactly one entity, so the set is
	// disjoint and cannot pull in another entity's events.
	names []string
}

type WhoResolved struct {
	Who         string   `json:"who"`
	Identifiers []string `json:"identifiers"`
}

type CalendarPersonFilter struct {
	resolvedCalendarPersonFilter *resolvedCalendarPersonFilter
	exactPersonFilterIdentifiers []*person.ExactPersonFilterIdentifier
}

type resolvedCalendarPersonFilter struct {
	personDisplayName string
	identifiers       []string
	names             []string
}

func NewCalendarPersonFilterFromExactPersonFilterIdentifiers(
	exactPersonFilterIdentifiers []*person.ExactPersonFilterIdentifier,
) *CalendarPersonFilter {
	return &CalendarPersonFilter{exactPersonFilterIdentifiers: append(
		[]*person.ExactPersonFilterIdentifier(nil),
		exactPersonFilterIdentifiers...,
	)}
}

type Location struct {
	Title   string `json:"title,omitempty"`
	Address string `json:"address,omitempty"`
}

type CalendarProvenance struct {
	ID         CalendarIdentifier `json:"id"`
	Title      string             `json:"title"`
	Type       int64              `json:"type"`
	ExternalID string             `json:"external_id,omitempty"`
}

type AccountProvenance struct {
	Name string `json:"name"`
	Type int64  `json:"type"`
}

type Event struct {
	UID                string
	SourceRowID        int64
	UUID               string
	UniqueIdentifier   string
	Calendar           CalendarProvenance
	Account            AccountProvenance
	Start              string
	End                string
	StartUnix          int64
	EndUnix            int64
	AllDay             bool
	Summary            string
	Description        string
	Status             string
	URL                string
	HasRecurrences     bool
	Availability       *int64
	Organizer          Person
	Location           Location
	Attendees          []Attendee
	ParticipantsText   string
	LocationSearchText string
}

type UpdateStats struct {
	Calendars        int
	Events           int
	NewEvents        int
	ChangedEvents    int
	UnchangedEvents  int
	DeletedEvents    int
	SourcePath       string
	SourceModifiedAt string
	ArchivePath      string
	UpdatedAt        string
}

type Status struct {
	ArchivePath      string
	ArchiveBytes     int64
	LastUpdateAt     string
	SourceModifiedAt string
	Calendars        int64
	Events           int64
	EarliestUnix     int64
	LatestUnix       int64
}

type SearchResult struct {
	Ref          string        `json:"ref"`
	ShortRef     string        `json:"short_ref"`
	Time         string        `json:"time"`
	Title        string        `json:"title"`
	Calendar     string        `json:"calendar"`
	Account      string        `json:"account,omitempty"`
	Location     *Location     `json:"location,omitempty"`
	Organizer    Person        `json:"organizer,omitempty"`
	Attendees    []Attendee    `json:"attendees,omitempty"`
	AllDay       bool          `json:"all_day"`
	Availability *int64        `json:"availability,omitempty"`
	Matches      []SearchMatch `json:"-"`
}

type EventListItem struct {
	Ref                              string `json:"ref"`
	Start                            string `json:"start"`
	End                              string `json:"end"`
	AllDay                           bool   `json:"all_day"`
	Title                            string `json:"title"`
	Calendar                         string `json:"calendar,omitempty"`
	Account                          string `json:"account,omitempty"`
	CalendarOwnerOrPurposeAnnotation *CalendarOwnerOrPurposeAnnotation
	Location                         *Location  `json:"location,omitempty"`
	Organizer                        Person     `json:"organizer,omitempty"`
	Attendees                        []Attendee `json:"attendees,omitempty"`
}

type SearchMatch struct {
	Field string
	Runs  []store.FTS5TextRun
}

type EventDetail struct {
	Ref                              string `json:"ref"`
	UUID                             string `json:"uuid"`
	UniqueIdentifier                 string `json:"unique_identifier,omitempty"`
	Title                            string `json:"title"`
	Description                      string `json:"description,omitempty"`
	DescriptionTruncated             bool   `json:"description_truncated,omitempty"`
	Start                            string `json:"start"`
	End                              string `json:"end"`
	AllDay                           bool   `json:"all_day"`
	Calendar                         string `json:"calendar"`
	Account                          string `json:"account"`
	CalendarOwnerOrPurposeAnnotation *CalendarOwnerOrPurposeAnnotation
	Availability                     *int64     `json:"availability,omitempty"`
	Location                         *Location  `json:"location,omitempty"`
	Organizer                        Person     `json:"organizer,omitempty"`
	Attendees                        []Attendee `json:"attendees,omitempty"`
	URL                              string     `json:"url,omitempty"`
	Status                           string     `json:"status,omitempty"`
	HasRecurrences                   bool       `json:"has_recurrences"`
}

func (e Event) Fingerprint() string {
	value := struct {
		UUID             string
		UniqueIdentifier string
		Calendar         CalendarProvenance
		Account          AccountProvenance
		Start            string
		End              string
		AllDay           bool
		Summary          string
		Description      string
		Status           string
		URL              string
		HasRecurrences   bool
		Availability     *int64
		Organizer        Person
		Location         Location
		Attendees        []Attendee
	}{
		UUID:             e.UUID,
		UniqueIdentifier: e.UniqueIdentifier,
		Calendar:         e.Calendar,
		Account:          e.Account,
		Start:            e.Start,
		End:              e.End,
		AllDay:           e.AllDay,
		Summary:          e.Summary,
		Description:      e.Description,
		Status:           e.Status,
		URL:              e.URL,
		HasRecurrences:   e.HasRecurrences,
		Availability:     e.Availability,
		Organizer:        e.Organizer,
		Location:         e.Location,
		Attendees:        e.Attendees,
	}
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func RefForUID(uid string) string {
	return AppID + ":event/" + strings.TrimSpace(uid)
}

func CalendarCanonicalRecordReferenceForIdentifier(calendarIdentifier CalendarIdentifier) string {
	return AppID + ":calendar/" + strings.TrimSpace(string(calendarIdentifier))
}

func CalendarIdentifierFromCanonicalRecordReference(
	canonicalCalendarRecordReference string,
) (CalendarIdentifier, bool) {
	value := strings.TrimSpace(canonicalCalendarRecordReference)
	prefix := AppID + ":calendar/"
	if !strings.HasPrefix(value, prefix) {
		return CalendarIdentifier(""), false
	}
	calendarIdentifierText := strings.TrimSpace(strings.TrimPrefix(value, prefix))
	if calendarIdentifierText == "" || strings.ContainsAny(calendarIdentifierText, "\r\n\t") {
		return CalendarIdentifier(""), false
	}
	return CalendarIdentifier(calendarIdentifierText), true
}

func UIDFromRef(ref string) (string, bool) {
	value := strings.TrimSpace(ref)
	prefix := AppID + ":event/"
	if !strings.HasPrefix(value, prefix) {
		return "", false
	}
	uid := strings.TrimSpace(strings.TrimPrefix(value, prefix))
	if uid == "" || strings.ContainsAny(uid, "\r\n\t") {
		return "", false
	}
	return uid, true
}
