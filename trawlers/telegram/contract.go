package telecrawl

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

type openEnvelope struct {
	Ref            string        `json:"ref"`
	Chat           openChat      `json:"chat"`
	Participants   []string      `json:"participants,omitempty"`
	Message        openMessage   `json:"message"`
	Context        []openMessage `json:"context"`
	ContextWindow  openWindow    `json:"context_window"`
	TargetPosition int           `json:"target_position"`
}

type openWindow struct {
	Before          int  `json:"before"`
	After           int  `json:"after"`
	BeforeTruncated bool `json:"before_truncated"`
	AfterTruncated  bool `json:"after_truncated"`
}

type openChat struct {
	Ref  string `json:"ref"`
	Name string `json:"name"`
}

type openParticipant struct {
	Ref         string `json:"ref,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

type openMessage struct {
	Ref           string          `json:"ref"`
	IsTarget      bool            `json:"is_target,omitempty"`
	Time          string          `json:"time"`
	EditTime      string          `json:"edit_time,omitempty"`
	Chat          openChat        `json:"chat"`
	Sender        openParticipant `json:"sender"`
	FromMe        bool            `json:"from_me"`
	Text          string          `json:"text,omitempty"`
	MessageID     string          `json:"message_id"`
	MessageType   string          `json:"message_type,omitempty"`
	RawType       int             `json:"raw_type,omitempty"`
	MediaType     string          `json:"media_type,omitempty"`
	MediaTitle    string          `json:"media_title,omitempty"`
	MediaPath     string          `json:"media_path,omitempty"`
	MediaURL      string          `json:"media_url,omitempty"`
	MediaSize     int64           `json:"media_size,omitempty"`
	MetadataType  string          `json:"metadata_type,omitempty"`
	MetadataTitle string          `json:"metadata_title,omitempty"`
	MetadataURL   string          `json:"metadata_url,omitempty"`
	MetadataJSON  string          `json:"metadata_json,omitempty"`
	Starred       bool            `json:"starred,omitempty"`
	TopicID       string          `json:"topic_id,omitempty"`
	ReplyToID     string          `json:"reply_to_message_id,omitempty"`
	ReplyToChat   string          `json:"reply_to_chat_ref,omitempty"`
	ThreadID      string          `json:"thread_id,omitempty"`
	ForwardJSON   string          `json:"forward_json,omitempty"`
	ReactionsJSON string          `json:"reactions_json,omitempty"`
	Views         int             `json:"views,omitempty"`
	Forwards      int             `json:"forwards,omitempty"`
	RepliesCount  int             `json:"replies_count,omitempty"`
	Pinned        bool            `json:"pinned,omitempty"`
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
			return "Telegram has never been synced; run trawl telegram sync to refresh."
		}
		return "Telegram was synced " + agePhrase(time.Since(status.LastImportAt)) + " ago; run trawl telegram sync to refresh."
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
