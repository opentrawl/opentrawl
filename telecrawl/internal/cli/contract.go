package cli

import (
	"os"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/control"
	"github.com/openclaw/crawlkit/render"
	"github.com/openclaw/telecrawl/internal/store"
	"github.com/openclaw/telecrawl/internal/telegramdesktop"
)

const (
	defaultMessageLimit = 50
	defaultSearchLimit  = 20
	maxSearchLimit      = 200
	openContextRadius   = 10

	// Telegram archives older than a day are stale for status surfaces that imply source parity.
	statusFreshFor = 24 * time.Hour
)

type metadataEnvelope struct {
	SchemaVersion   int           `json:"schema_version"`
	ContractVersion int           `json:"contract_version"`
	ID              string        `json:"id"`
	DisplayName     string        `json:"display_name"`
	Version         string        `json:"version"`
	Paths           control.Paths `json:"paths"`
	Capabilities    []string      `json:"capabilities"`
}

type statusEnvelope struct {
	AppID     string                `json:"app_id"`
	State     string                `json:"state"`
	Summary   string                `json:"summary"`
	Freshness freshnessEnvelope     `json:"freshness"`
	Counts    []countEnvelope       `json:"counts"`
	Auth      authEnvelope          `json:"auth"`
	Log       *render.DoctorLogTail `json:"log,omitempty"`
	logTail   render.LogTail        `json:"-"`
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

type doctorOutput struct {
	Checks  []doctorCheck         `json:"checks"`
	Log     *render.DoctorLogTail `json:"log,omitempty"`
	logTail render.LogTail        `json:"-"`
}

type doctorCheck struct {
	ID      string `json:"id"`
	Label   string `json:"label,omitempty"`
	State   string `json:"state"`
	Message string `json:"message,omitempty"`
	Remedy  string `json:"remedy,omitempty"`
}

type searchEnvelope struct {
	Query        string         `json:"query"`
	WhoQuery     string         `json:"-"`
	WhoResolved  *whoResolved   `json:"who_resolved,omitempty"`
	Results      []searchResult `json:"results"`
	TotalMatches int            `json:"total_matches"`
	Truncated    bool           `json:"truncated"`
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
	ShortRef string `json:"-"`
	Time     string `json:"time"`
	Who      string `json:"who,omitempty"`
	Where    string `json:"where,omitempty"`
	Snippet  string `json:"snippet"`
}

type errorEnvelope struct {
	Error contractErrorBody `json:"error"`
}

type contractErrorBody struct {
	Code       string          `json:"code"`
	Message    string          `json:"message"`
	Remedy     string          `json:"remedy"`
	Candidates []whoCandidate  `json:"candidates,omitempty"`
	DidYouMean *[]whoCandidate `json:"did_you_mean,omitempty"`
	Hint       string          `json:"hint,omitempty"`
}

type openEnvelope struct {
	Ref            string        `json:"ref"`
	Chat           openChat      `json:"chat"`
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

func contractMetadata() metadataEnvelope {
	return metadataEnvelope{
		SchemaVersion:   1,
		ContractVersion: 1,
		ID:              "telecrawl",
		DisplayName:     "Telegram",
		Version:         version,
		Paths:           controlManifest().Paths,
		Capabilities:    []string{"metadata", "doctor", "status", "sync", "search", "open", "who", "short_refs", "contacts_export", "backup", "verbose_logs"},
	}
}

func (r *runtime) statusEnvelope() statusEnvelope {
	if info, err := os.Stat(r.dbPath); err != nil {
		if os.IsNotExist(err) {
			return r.newStatusEnvelope("missing", "archive database is missing", store.Status{})
		}
		return r.newStatusEnvelope("error", "archive database cannot be read", store.Status{})
	} else if info.IsDir() {
		return r.newStatusEnvelope("error", "archive database path is a directory", store.Status{})
	}
	st, err := store.OpenReadOnly(r.ctx, r.dbPath)
	if err != nil {
		return r.newStatusEnvelope("error", "archive database cannot be read", store.Status{})
	}
	defer func() { _ = st.Close() }()
	status, err := st.Status(r.ctx)
	if err != nil {
		return r.newStatusEnvelope("error", "archive status cannot be read", store.Status{})
	}
	return r.newStatusEnvelope(statusState(status), statusSummary(status), status)
}

func (r *runtime) newStatusEnvelope(state, summary string, status store.Status) statusEnvelope {
	logTail := r.logTail()
	return statusEnvelope{
		AppID:     "telecrawl",
		State:     state,
		Summary:   summary,
		Freshness: statusFreshness(status),
		Counts: []countEnvelope{
			{ID: "messages", Label: "messages", Value: int64(status.Messages)},
			{ID: "chats", Label: "chats", Value: int64(status.Chats)},
			{ID: "since", Label: "since", Value: oldestMessageYear(status)},
		},
		Auth:    authEnvelope{Authorized: true},
		Log:     render.DoctorLogTailOutput(logTail),
		logTail: logTail,
	}
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
		return "archive is empty"
	case "stale":
		return "archive sync is stale"
	default:
		return "archive is fresh"
	}
}

func statusFreshness(status store.Status) freshnessEnvelope {
	if status.LastImportAt.IsZero() {
		return freshnessEnvelope{}
	}
	return freshnessEnvelope{LastSync: formatLocalTime(status.LastImportAt)}
}

func oldestMessageYear(status store.Status) int64 {
	if status.OldestMessage.IsZero() {
		return 0
	}
	return int64(status.OldestMessage.In(time.Local).Year())
}

func (r *runtime) doctorEnvelope(report telegramdesktop.Report) doctorOutput {
	logTail := r.logTail()
	return doctorOutput{
		Checks:  []doctorCheck{sourceStoreCheck(report), r.archiveCheck()},
		Log:     render.DoctorLogTailOutput(logTail),
		logTail: logTail,
	}
}

func sourceStoreCheck(report telegramdesktop.Report) doctorCheck {
	if report.Exists && report.Accessible && report.Error == "" {
		return doctorCheck{ID: "source_store", Label: "Telegram data", State: "ok", Message: "Telegram source data is readable."}
	}
	check := doctorCheck{
		ID:     "source_store",
		Label:  "Telegram data",
		State:  "missing",
		Remedy: "Install or open Telegram Desktop, or pass --path to a readable Telegram data directory.",
	}
	switch {
	case !report.Exists:
		check.Message = "Telegram source data was not found."
	case report.Error != "":
		check.Message = "Telegram source data could not be read."
	default:
		check.Message = "Telegram source data is not readable."
	}
	return check
}

func (r *runtime) archiveCheck() doctorCheck {
	if info, err := os.Stat(r.dbPath); err != nil {
		return doctorCheck{
			ID:      "archive",
			Label:   "Archive",
			State:   "missing",
			Message: "telecrawl archive has not been created.",
			Remedy:  "Run telecrawl import to create the archive.",
		}
	} else if info.IsDir() {
		return doctorCheck{
			ID:      "archive",
			Label:   "Archive",
			State:   "missing",
			Message: "telecrawl archive path is a directory.",
			Remedy:  "Pass --db with a sqlite archive path, then run telecrawl import.",
		}
	}
	st, err := store.OpenReadOnly(r.ctx, r.dbPath)
	if err != nil {
		return doctorCheck{
			ID:      "archive",
			Label:   "Archive",
			State:   "missing",
			Message: "telecrawl archive cannot be read.",
			Remedy:  "Run telecrawl import to rebuild the archive.",
		}
	}
	defer func() { _ = st.Close() }()
	status, err := st.Status(r.ctx)
	if err != nil {
		return doctorCheck{
			ID:      "archive",
			Label:   "Archive",
			State:   "missing",
			Message: "telecrawl archive status cannot be read.",
			Remedy:  "Run telecrawl import to rebuild the archive.",
		}
	}
	if status.Messages == 0 {
		return doctorCheck{ID: "archive", Label: "Archive", State: "empty", Message: "Archive exists but has no messages.", Remedy: "Run telecrawl import to fill the archive."}
	}
	return doctorCheck{ID: "archive", Label: "Archive", State: "ok", Message: "Archive is readable."}
}

func newSearchEnvelope(query string, messages []store.Message, total int, whoQuery string, resolved *store.WhoCandidate, shortRefs map[string]string) searchEnvelope {
	return searchEnvelope{
		Query:        query,
		WhoQuery:     whoQuery,
		WhoResolved:  newWhoResolved(resolved),
		Results:      searchResults(messages, shortRefs),
		TotalMatches: total,
		Truncated:    total > len(messages),
	}
}

func newWhoEnvelope(query string, candidates []store.WhoCandidate) whoEnvelope {
	return whoEnvelope{Query: query, Candidates: whoCandidates(candidates)}
}

func newWhoResolved(candidate *store.WhoCandidate) *whoResolved {
	if candidate == nil {
		return nil
	}
	return &whoResolved{Who: candidate.Who, Identifiers: append([]string(nil), candidate.Identifiers...)}
}

func whoCandidates(candidates []store.WhoCandidate) []whoCandidate {
	out := make([]whoCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		out = append(out, whoCandidate{
			Who:         outputField(candidate.Who),
			Identifiers: append([]string(nil), candidate.Identifiers...),
			LastSeen:    formatOptionalTime(candidate.LastSeen),
			Messages:    candidate.Messages,
		})
	}
	return out
}

func searchResults(messages []store.Message, shortRefs map[string]string) []searchResult {
	out := make([]searchResult, 0, len(messages))
	for _, message := range messages {
		ref := messageRef(message.SourcePK)
		out = append(out, searchResult{
			Ref:      ref,
			ShortRef: shortRefs[ref],
			Time:     formatLocalTime(message.Timestamp),
			Who:      outputField(messageWho(message)),
			Where:    outputField(messageWhere(message)),
			Snippet:  outputField(messageSnippet(message)),
		})
	}
	return out
}

func messageRef(sourcePK int64) string {
	return store.MessageRef(sourcePK)
}

func newOpenEnvelope(window store.MessageWindow) openEnvelope {
	targetRef := messageRef(window.Target.SourcePK)
	context := make([]openMessage, 0, len(window.Messages))
	targetPosition := -1
	for i, message := range window.Messages {
		isTarget := message.SourcePK == window.Target.SourcePK
		if isTarget {
			targetPosition = i
		}
		context = append(context, openMessageFromStore(message, isTarget))
	}
	return openEnvelope{
		Ref:     targetRef,
		Chat:    openChatFromMessage(window.Target),
		Message: openMessageFromStore(window.Target, true),
		Context: context,
		ContextWindow: openWindow{
			Before:          targetPosition,
			After:           len(context) - targetPosition - 1,
			BeforeTruncated: window.BeforeTruncated,
			AfterTruncated:  window.AfterTruncated,
		},
		TargetPosition: targetPosition,
	}
}

func openMessageFromStore(message store.Message, isTarget bool) openMessage {
	return openMessage{
		Ref:           messageRef(message.SourcePK),
		IsTarget:      isTarget,
		Time:          formatOptionalTime(message.Timestamp),
		EditTime:      formatOptionalTime(message.EditTime),
		Chat:          openChatFromMessage(message),
		Sender:        openSenderFromMessage(message),
		FromMe:        message.FromMe,
		Text:          strings.TrimSpace(message.Text),
		MessageID:     message.MessageID,
		MessageType:   message.MessageType,
		RawType:       message.RawType,
		MediaType:     message.MediaType,
		MediaTitle:    message.MediaTitle,
		MediaPath:     message.MediaPath,
		MediaURL:      message.MediaURL,
		MediaSize:     message.MediaSize,
		MetadataType:  message.MetadataType,
		MetadataTitle: message.MetadataTitle,
		MetadataURL:   message.MetadataURL,
		MetadataJSON:  message.MetadataJSON,
		Starred:       message.Starred,
		TopicID:       message.TopicID,
		ReplyToID:     message.ReplyToID,
		ReplyToChat:   chatRef(message.ReplyToChat),
		ThreadID:      message.ThreadID,
		ForwardJSON:   message.ForwardJSON,
		ReactionsJSON: message.ReactionsJSON,
		Views:         message.Views,
		Forwards:      message.Forwards,
		RepliesCount:  message.RepliesCount,
		Pinned:        message.Pinned,
	}
}

func openChatFromMessage(message store.Message) openChat {
	return openChat{Ref: chatRef(message.ChatJID), Name: messageWhere(message)}
}

func openSenderFromMessage(message store.Message) openParticipant {
	if message.FromMe {
		return openParticipant{DisplayName: messageWho(message)}
	}
	return openParticipant{Ref: chatRef(message.SenderJID), DisplayName: messageWho(message)}
}

func chatRef(jid string) string {
	jid = strings.TrimSpace(jid)
	if jid == "" {
		return ""
	}
	return "telecrawl:chat/" + jid
}

func formatOptionalTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return formatLocalTime(t)
}

func formatLocalTime(t time.Time) string {
	return t.Local().Format(time.RFC3339)
}

func parseRenderTime(value string) time.Time {
	if strings.TrimSpace(value) == "" {
		return time.Time{}
	}
	parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}
	}
	return parsed
}

func messageWho(message store.Message) string {
	if message.FromMe {
		return "me"
	}
	if value := strings.TrimSpace(message.SenderName); value != "" {
		return outputField(value)
	}
	if strings.TrimSpace(message.SenderJID) == "" || strings.TrimSpace(message.SenderJID) == strings.TrimSpace(message.ChatJID) {
		return outputField(messageWhere(message))
	}
	return ""
}

func messageWhere(message store.Message) string {
	if value := strings.TrimSpace(message.ChatName); value != "" {
		return outputField(value)
	}
	return "Telegram chat"
}

func messageSnippet(message store.Message) string {
	if value := strings.TrimSpace(message.Snippet); value != "" {
		return outputField(value)
	}
	return outputField(message.Text)
}

func outputField(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
