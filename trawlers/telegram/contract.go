package telegram

import (
	"strconv"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
)

const (
	defaultMessageLimit = 50
	defaultSearchLimit  = 20
	openContextRadius   = 10

	// Telegram archives older than a day are stale for status surfaces that imply source parity.
	statusFreshFor = 24 * time.Hour
)

type statusEnvelope struct {
	AppID     string            `json:"app_id"`
	State     string            `json:"state"`
	Summary   string            `json:"summary"`
	Freshness freshnessEnvelope `json:"freshness"`
	Counts    []countEnvelope   `json:"counts"`
	Auth      authEnvelope      `json:"auth"`
}

type freshnessEnvelope struct {
	LastSync string `json:"last_sync,omitempty"`
}

type countEnvelope struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Value int64  `json:"value"`
}

type authEnvelope struct {
	Authorized bool    `json:"authorized"`
	Expires    *string `json:"expires"`
}

type searchEnvelope struct {
	Query        string         `json:"query"`
	WhoQuery     string         `json:"-"`
	Limit        int            `json:"-"`
	WhoResolved  *whoResolved   `json:"who_resolved,omitempty"`
	Results      []searchResult `json:"results"`
	TotalMatches int            `json:"total_matches"`
	Truncated    bool           `json:"truncated"`
}

type topicsEnvelope struct {
	Topics []store.Topic
	Total  int
	ChatID string
}

type messagesEnvelope struct {
	Messages  []store.Message
	Total     int
	ShortRefs map[string]string
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

type whoEnvelope struct {
	Query      string         `json:"query"`
	Candidates []whoCandidate `json:"candidates"`
}

type whoCandidate struct {
	Who         string   `json:"who"`
	Identifiers []string `json:"identifiers"`
	LastSeen    string   `json:"last_seen"`
	Messages    int      `json:"messages"`
}

type searchResult struct {
	Ref      string `json:"ref"`
	ShortRef string `json:"short_ref"`
	Time     string `json:"time"`
	Who      string `json:"who,omitempty"`
	Where    string `json:"where,omitempty"`
	Snippet  string `json:"snippet"`
}

func statusState(status store.Status) string {
	switch {
	case status.Messages == 0:
		return "empty"
	case status.LastImportAt.IsZero():
		return "stale"
	case time.Since(status.LastImportAt) > statusFreshFor:
		return "stale"
	default:
		return "ok"
	}
}

func statusSummary(status store.Status) string {
	switch statusState(status) {
	case "empty":
		return "Telegram has no messages yet."
	case "stale":
		if status.LastImportAt.IsZero() {
			return "Telegram has never been synced; run trawl sync telegram to refresh."
		}
		return "Telegram was synced " + agePhrase(time.Since(status.LastImportAt)) + " ago; run trawl sync telegram to refresh."
	default:
		return "Recently synced."
	}
}

func agePhrase(age time.Duration) string {
	if age < 0 {
		age = 0
	}
	days := int(age.Hours()) / 24
	if days > 0 {
		if days == 1 {
			return "1 day"
		}
		return strconv.Itoa(days) + " days"
	}
	hours := int(age.Hours())
	if hours > 0 {
		if hours == 1 {
			return "1 hour"
		}
		return strconv.Itoa(hours) + " hours"
	}
	return "less than 1 hour"
}

func oldestMessageYear(status store.Status) int64 {
	if status.OldestMessage.IsZero() {
		return 0
	}
	return int64(status.OldestMessage.In(time.Local).Year())
}
