package xweb

import (
	"encoding/json"
	"html"
	"sort"
	"strconv"
	"strings"
	"time"
)

// WebDirectMessageInboxPage holds parsed conversations, messages, profiles and
// pagination cursors from X's DM v1.1 inbox endpoints.
type WebDirectMessageInboxPage struct {
	Conversations   []WebDirectMessageConversation
	Messages        []WebDirectMessage
	Profiles        []WebProfile
	TrustedCursor   string
	UntrustedCursor string
	RateLimit       RateLimitObservation
}

type WebDirectMessageConversation struct {
	ID                   string
	InboxKind            string // "accepted" or "request"
	ConversationKind     string // "direct" or "group"
	Title                string
	ParticipantAuthorID  string
	ParticipantAuthorIDs []string
	UnreadCount          int
	LastMessageAt        time.Time
}

type WebDirectMessage struct {
	ID             string
	ConversationID string
	CreatedAt      time.Time
	SenderAuthorID string
	Direction      string // "inbound" or "outbound"
	Text           string
	MediaCount     int
	RawJSON        string
}

type rawDMInboxEnvelope struct {
	InboxInitialState *rawDMInboxPayload `json:"inbox_initial_state"`
	InboxTimeline     *rawDMInboxPayload `json:"inbox_timeline"`
}

type rawDMInboxPayload struct {
	Status         string                            `json:"status"`
	MinEntryID     string                            `json:"min_entry_id"`
	InboxTimelines map[string]rawDMInboxTimelineMeta `json:"inbox_timelines"`
	Users          map[string]rawDMUser              `json:"users"`
	Conversations  map[string]rawDMConversation      `json:"conversations"`
	Entries        []rawDMInboxEntry                 `json:"entries"`
}

type rawDMInboxTimelineMeta struct {
	Status     string `json:"status"`
	MinEntryID string `json:"min_entry_id"`
}

type rawDMUser struct {
	IDStr      string `json:"id_str"`
	ID         any    `json:"id"`
	ScreenName string `json:"screen_name"`
	Name       string `json:"name"`
}

type rawDMConversation struct {
	ConversationID string             `json:"conversation_id"`
	Type           string             `json:"type"`
	Name           string             `json:"name"`
	Trusted        *bool              `json:"trusted"`
	SortTimestamp  string             `json:"sort_timestamp"`
	UnreadCount    int                `json:"unread_count"`
	Participants   []rawDMParticipant `json:"participants"`
}

type rawDMParticipant struct {
	UserID string `json:"user_id"`
}

type rawDMInboxEntry struct {
	Message *rawDMMessageEntry `json:"message"`
}

type rawDMMessageEntry struct {
	ID             string            `json:"id"`
	Time           string            `json:"time"`
	ConversationID string            `json:"conversation_id"`
	MessageData    *rawDMMessageData `json:"message_data"`
}

type rawDMMessageData struct {
	ID          string           `json:"id"`
	Time        string           `json:"time"`
	SenderID    string           `json:"sender_id"`
	RecipientID string           `json:"recipient_id"`
	Text        string           `json:"text"`
	Attachment  *json.RawMessage `json:"attachment"`
}

func parseDMInboxPayload(raw []byte, ownerUserID string) (WebDirectMessageInboxPage, error) {
	var envelope rawDMInboxEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return WebDirectMessageInboxPage{}, err
	}
	payload := envelope.InboxInitialState
	if payload == nil {
		payload = envelope.InboxTimeline
	}
	if payload == nil {
		return WebDirectMessageInboxPage{}, nil
	}

	trustedCursor, untrustedCursor := extractDMInboxCursors(payload)
	profiles, profilesByID := parseDMInboxProfiles(payload.Users)
	messages, convLatest := parseDMInboxMessages(payload.Entries, ownerUserID)
	conversations := parseDMInboxConversations(payload.Conversations, ownerUserID, profilesByID, convLatest)
	return WebDirectMessageInboxPage{
		Conversations:   conversations,
		Messages:        messages,
		Profiles:        profiles,
		TrustedCursor:   trustedCursor,
		UntrustedCursor: untrustedCursor,
	}, nil
}

func extractDMInboxCursors(payload *rawDMInboxPayload) (trusted, untrusted string) {
	if payload.InboxTimelines != nil {
		if meta, ok := payload.InboxTimelines["trusted"]; ok && meta.Status == "HAS_MORE" && isDigits(meta.MinEntryID) {
			trusted = meta.MinEntryID
		}
		if meta, ok := payload.InboxTimelines["untrusted"]; ok && meta.Status == "HAS_MORE" && isDigits(meta.MinEntryID) {
			untrusted = meta.MinEntryID
		}
		return trusted, untrusted
	}
	if payload.Status == "HAS_MORE" && isDigits(payload.MinEntryID) {
		return payload.MinEntryID, payload.MinEntryID
	}
	return "", ""
}

func parseDMInboxProfiles(users map[string]rawDMUser) ([]WebProfile, map[string]WebProfile) {
	profilesByID := make(map[string]WebProfile, len(users))
	for key, u := range users {
		id := strings.TrimSpace(u.IDStr)
		if id == "" {
			id = strings.TrimSpace(key)
		}
		id = strings.TrimPrefix(id, "profile_user_")
		if !isDigits(id) {
			continue
		}
		profilesByID[id] = WebProfile{
			AuthorID:    id,
			Handle:      strings.TrimPrefix(strings.TrimSpace(u.ScreenName), "@"),
			DisplayName: strings.TrimSpace(u.Name),
		}
	}
	profiles := make([]WebProfile, 0, len(profilesByID))
	for _, p := range profilesByID {
		profiles = append(profiles, p)
	}
	sort.Slice(profiles, func(i, j int) bool {
		return profiles[i].AuthorID < profiles[j].AuthorID
	})
	return profiles, profilesByID
}

func parseDMInboxMessages(entries []rawDMInboxEntry, ownerUserID string) ([]WebDirectMessage, map[string]time.Time) {
	convLatest := map[string]time.Time{}
	seenMessages := map[string]struct{}{}
	messages := make([]WebDirectMessage, 0, len(entries))
	for _, entry := range entries {
		parsed, ok := parseSingleDMInboxEntry(entry, ownerUserID, seenMessages)
		if !ok {
			continue
		}
		messages = append(messages, parsed)
		if parsed.CreatedAt.After(convLatest[parsed.ConversationID]) {
			convLatest[parsed.ConversationID] = parsed.CreatedAt
		}
	}
	sort.Slice(messages, func(i, j int) bool {
		if !messages[i].CreatedAt.Equal(messages[j].CreatedAt) {
			return messages[i].CreatedAt.After(messages[j].CreatedAt)
		}
		return messages[i].ID > messages[j].ID
	})
	return messages, convLatest
}

func parseSingleDMInboxEntry(entry rawDMInboxEntry, ownerUserID string, seenMessages map[string]struct{}) (WebDirectMessage, bool) {
	if entry.Message == nil || entry.Message.MessageData == nil {
		return WebDirectMessage{}, false
	}
	msg := entry.Message
	data := msg.MessageData
	msgID := firstNonEmptyString(msg.ID, data.ID)
	convID := strings.TrimSpace(msg.ConversationID)
	if msgID == "" || convID == "" {
		return WebDirectMessage{}, false
	}
	if _, dup := seenMessages[msgID]; dup {
		return WebDirectMessage{}, false
	}
	seenMessages[msgID] = struct{}{}

	mediaCount := 0
	if data.Attachment != nil && len(*data.Attachment) > 0 && string(*data.Attachment) != "null" {
		mediaCount = 1
	}
	text := html.UnescapeString(strings.TrimSpace(data.Text))
	if text == "" && mediaCount == 0 {
		return WebDirectMessage{}, false
	}
	senderID := strings.TrimPrefix(strings.TrimSpace(data.SenderID), "profile_user_")
	direction := "inbound"
	if ownerUserID != "" && senderID == ownerUserID {
		direction = "outbound"
	}
	rawEntryBytes, _ := json.Marshal(msg)
	return WebDirectMessage{
		ID:             msgID,
		ConversationID: convID,
		CreatedAt:      parseEpochMillis(firstNonEmptyString(data.Time, msg.Time)),
		SenderAuthorID: senderID,
		Direction:      direction,
		Text:           text,
		MediaCount:     mediaCount,
		RawJSON:        string(rawEntryBytes),
	}, true
}

func parseDMInboxConversations(
	rawConvs map[string]rawDMConversation,
	ownerUserID string,
	profilesByID map[string]WebProfile,
	convLatest map[string]time.Time,
) []WebDirectMessageConversation {
	conversations := make([]WebDirectMessageConversation, 0, len(rawConvs))
	for key, conv := range rawConvs {
		convID := firstNonEmptyString(conv.ConversationID, key)
		if convID == "" {
			continue
		}
		conversations = append(conversations, buildDMInboxConversation(convID, conv, ownerUserID, profilesByID, convLatest))
	}
	sort.Slice(conversations, func(i, j int) bool {
		if !conversations[i].LastMessageAt.Equal(conversations[j].LastMessageAt) {
			return conversations[i].LastMessageAt.After(conversations[j].LastMessageAt)
		}
		return conversations[i].ID < conversations[j].ID
	})
	return conversations
}

func buildDMInboxConversation(
	convID string,
	conv rawDMConversation,
	ownerUserID string,
	profilesByID map[string]WebProfile,
	convLatest map[string]time.Time,
) WebDirectMessageConversation {
	inboxKind := "accepted"
	if conv.Trusted != nil && !*conv.Trusted {
		inboxKind = "request"
	}
	convKind := "direct"
	if strings.EqualFold(strings.TrimSpace(conv.Type), "GROUP_DM") {
		convKind = "group"
	}
	participantIDs, otherAuthorID := extractDMConversationParticipants(convID, convKind, conv.Participants, ownerUserID)
	if convKind == "direct" && len(participantIDs) > 2 {
		convKind = "group"
	}
	lastAt := parseEpochMillis(conv.SortTimestamp)
	if latest, ok := convLatest[convID]; ok && latest.After(lastAt) {
		lastAt = latest
	}
	title := dmConversationTitle(conv.Name, otherAuthorID, profilesByID)
	return WebDirectMessageConversation{
		ID:                   convID,
		InboxKind:            inboxKind,
		ConversationKind:     convKind,
		Title:                title,
		ParticipantAuthorID:  otherAuthorID,
		ParticipantAuthorIDs: participantIDs,
		UnreadCount:          max(conv.UnreadCount, 0),
		LastMessageAt:        lastAt,
	}
}

func extractDMConversationParticipants(convID, convKind string, participants []rawDMParticipant, ownerUserID string) ([]string, string) {
	var participantIDs []string
	otherAuthorID := ""
	for _, p := range participants {
		uid := strings.TrimPrefix(strings.TrimSpace(p.UserID), "profile_user_")
		if uid == "" {
			continue
		}
		participantIDs = append(participantIDs, uid)
		if ownerUserID != "" && uid != ownerUserID && otherAuthorID == "" {
			otherAuthorID = uid
		}
	}
	participantIDs = uniqueStrings(participantIDs)
	if otherAuthorID == "" && convKind == "direct" && strings.Contains(convID, "-") && ownerUserID != "" {
		for _, part := range strings.Split(convID, "-") {
			if part != "" && part != ownerUserID {
				otherAuthorID = part
				break
			}
		}
	}
	return participantIDs, otherAuthorID
}

func dmConversationTitle(rawTitle, otherAuthorID string, profilesByID map[string]WebProfile) string {
	if title := strings.TrimSpace(rawTitle); title != "" {
		return title
	}
	pr, ok := profilesByID[otherAuthorID]
	if !ok {
		return ""
	}
	if pr.DisplayName != "" {
		return pr.DisplayName
	}
	if pr.Handle != "" {
		return "@" + pr.Handle
	}
	return ""
}

func parseEpochMillis(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	if ms, err := strconv.ParseInt(raw, 10, 64); err == nil && ms > 0 {
		return time.UnixMilli(ms).UTC()
	}
	return parseTwitterCreatedAt(raw)
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		if value[i] < '0' || value[i] > '9' {
			return false
		}
	}
	return true
}
