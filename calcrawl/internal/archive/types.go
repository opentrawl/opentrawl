package archive

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
)

const (
	AppID         = "calcrawl"
	DisplayName   = "Calendar"
	SchemaVersion = 2

	DefaultSearchLimit = 20
)

type Calendar struct {
	ID              string
	SourceRowID     int64
	Title           string
	Type            int64
	ExternalID      string
	StoreID         int64
	AccountName     string
	AccountType     int64
	AccountDisabled bool
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
}

type WhoResolved struct {
	Who         string   `json:"who"`
	Identifiers []string `json:"identifiers"`
}

type WhoFilter struct {
	Who         string
	Identifiers []string
}

type Location struct {
	Title   string `json:"title,omitempty"`
	Address string `json:"address,omitempty"`
}

type CalendarProvenance struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Type       int64  `json:"type"`
	ExternalID string `json:"external_id,omitempty"`
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
	Organizer          Person
	Location           Location
	Attendees          []Attendee
	ParticipantsText   string
	LocationSearchText string
}

type SyncStats struct {
	Calendars        int
	Events           int
	NewEvents        int
	ChangedEvents    int
	UnchangedEvents  int
	DeletedEvents    int
	SourcePath       string
	SourceModifiedAt string
	ArchivePath      string
	SyncedAt         string
}

type Status struct {
	ArchivePath      string
	ArchiveBytes     int64
	SchemaVersion    int
	LastSyncAt       string
	SourceModifiedAt string
	Calendars        int64
	Events           int64
	EarliestUnix     int64
	LatestUnix       int64
}

type SearchResult struct {
	Ref      string `json:"ref"`
	ShortRef string `json:"short_ref"`
	Time     string `json:"time"`
	Who      string `json:"who"`
	Where    string `json:"where"`
	Snippet  string `json:"snippet"`
}

type EventDetail struct {
	Ref                  string     `json:"ref"`
	UUID                 string     `json:"uuid"`
	UniqueIdentifier     string     `json:"unique_identifier,omitempty"`
	Title                string     `json:"title"`
	Description          string     `json:"description,omitempty"`
	DescriptionTruncated bool       `json:"description_truncated,omitempty"`
	Start                string     `json:"start"`
	End                  string     `json:"end"`
	AllDay               bool       `json:"all_day"`
	Calendar             string     `json:"calendar"`
	Account              string     `json:"account"`
	Location             *Location  `json:"location,omitempty"`
	Organizer            Person     `json:"organizer,omitempty"`
	Attendees            []Attendee `json:"attendees,omitempty"`
	URL                  string     `json:"url,omitempty"`
	Status               string     `json:"status,omitempty"`
	HasRecurrences       bool       `json:"has_recurrences"`
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

func UIDFromRef(ref string) (string, bool) {
	const prefix = AppID + ":event/"
	value := strings.TrimSpace(ref)
	if !strings.HasPrefix(value, prefix) {
		return "", false
	}
	uid := strings.TrimSpace(strings.TrimPrefix(value, prefix))
	if uid == "" || strings.ContainsAny(uid, "\r\n\t") {
		return "", false
	}
	return uid, true
}
