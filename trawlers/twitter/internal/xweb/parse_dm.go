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

	page := WebDirectMessageInboxPage{}
	if payload.InboxTimelines != nil {
		if meta, ok := payload.InboxTimelines["trusted"]; ok && meta.Status == "HAS_MORE" && isDigits(meta.MinEntryID) {
			page.TrustedCursor = meta.MinEntryID
		}
		if meta, ok := payload.InboxTimelines["untrusted"]; ok && meta.Status == "HAS_MORE" && isDigits(meta.MinEntryID) {
			page.UntrustedCursor = meta.MinEntryID
		}
	} else if payload.Status == "HAS_MORE" && isDigits(payload.MinEntryID) {
		page.TrustedCursor = payload.MinEntryID
		page.UntrustedCursor = payload.MinEntryID
	}

	profilesByID := make(map[string]WebProfile, len(payload.Users))
	for key, u := range payload.Users {
		id := strings.TrimSpace(u.IDStr)
		if id == "" {
			id = strings.TrimSpace(key)
		}
		if !isDigits(id) {
			continue
		}
		profilesByID[id] = WebProfile{
			AuthorID:    id,
			Handle:      strings.TrimPrefix(strings.TrimSpace(u.ScreenName), "@"),
			DisplayName: strings.TrimSpace(u.Name),
		}
	}
	for _, p := range profilesByID {
		page.Profiles = append(page.Profiles, p)
	}
	sort.Slice(page.Profiles, func(i, j int) bool {
		return page.Profiles[i].AuthorID < page.Profiles[j].AuthorID
	})

	convLatest := map[string]time.Time{}
	seenMessages := map[string]struct{}{}
	for _, entry := range payload.Entries {
		if entry.Message == nil || entry.Message.MessageData == nil {
			continue
		}
		msg := entry.Message
		data := msg.MessageData
		msgID := firstNonEmptyString(msg.ID, data.ID)
		convID := strings.TrimSpace(msg.ConversationID)
		if msgID == "" || convID == "" {
			continue
		}
		if _, dup := seenMessages[msgID]; dup {
			continue
		}
		seenMessages[msgID] = struct{}{}

		mediaCount := 0
		if data.Attachment != nil && len(*data.Attachment) > 0 && string(*data.Attachment) != "null" {
			mediaCount = 1
		}
		text := html.UnescapeString(strings.TrimSpace(data.Text))
		if text == "" && mediaCount == 0 {
			continue
		}
		createdAt := parseEpochMillis(firstNonEmptyString(data.Time, msg.Time))
		senderID := strings.TrimSpace(data.SenderID)
		direction := "inbound"
		if ownerUserID != "" && senderID == ownerUserID {
			direction = "outbound"
		}
		rawEntryBytes, _ := json.Marshal(msg)
		page.Messages = append(page.Messages, WebDirectMessage{
			ID:             msgID,
			ConversationID: convID,
			CreatedAt:      createdAt,
			SenderAuthorID: senderID,
			Direction:      direction,
			Text:           text,
			MediaCount:     mediaCount,
			RawJSON:        string(rawEntryBytes),
		})
		if createdAt.After(convLatest[convID]) {
			convLatest[convID] = createdAt
		}
	}
	sort.Slice(page.Messages, func(i, j int) bool {
		if !page.Messages[i].CreatedAt.Equal(page.Messages[j].CreatedAt) {
			return page.Messages[i].CreatedAt.After(page.Messages[j].CreatedAt)
		}
		return page.Messages[i].ID > page.Messages[j].ID
	})

	for key, conv := range payload.Conversations {
		convID := firstNonEmptyString(conv.ConversationID, key)
		if convID == "" {
			continue
		}
		inboxKind := "accepted"
		if conv.Trusted != nil && !*conv.Trusted {
			inboxKind = "request"
		}
		convKind := "direct"
		if strings.EqualFold(strings.TrimSpace(conv.Type), "GROUP_DM") {
			convKind = "group"
		}
		var participantIDs []string
		otherAuthorID := ""
		for _, p := range conv.Participants {
			uid := strings.TrimSpace(p.UserID)
			if uid == "" {
				continue
			}
			participantIDs = append(participantIDs, uid)
			if ownerUserID != "" && uid != ownerUserID && otherAuthorID == "" {
				otherAuthorID = uid
			}
		}
		if convKind == "direct" && len(participantIDs) > 2 {
			convKind = "group"
		}
		if otherAuthorID == "" && convKind == "direct" && strings.Contains(convID, "-") && ownerUserID != "" {
			for _, part := range strings.Split(convID, "-") {
				if part != "" && part != ownerUserID {
					otherAuthorID = part
					break
				}
			}
		}
		lastAt := parseEpochMillis(conv.SortTimestamp)
		if latest, ok := convLatest[convID]; ok && latest.After(lastAt) {
			lastAt = latest
		}
		title := strings.TrimSpace(conv.Name)
		if title == "" && otherAuthorID != "" {
			if pr, ok := profilesByID[otherAuthorID]; ok {
				if pr.DisplayName != "" {
					title = pr.DisplayName
				} else if pr.Handle != "" {
					title = "@" + pr.Handle
				}
			}
		}
		page.Conversations = append(page.Conversations, WebDirectMessageConversation{
			ID:                   convID,
			InboxKind:            inboxKind,
			ConversationKind:     convKind,
			Title:                title,
			ParticipantAuthorID:  otherAuthorID,
			ParticipantAuthorIDs: uniqueStrings(participantIDs),
			UnreadCount:          max(conv.UnreadCount, 0),
			LastMessageAt:        lastAt,
		})
	}
	sort.Slice(page.Conversations, func(i, j int) bool {
		if !page.Conversations[i].LastMessageAt.Equal(page.Conversations[j].LastMessageAt) {
			return page.Conversations[i].LastMessageAt.After(page.Conversations[j].LastMessageAt)
		}
		return page.Conversations[i].ID < page.Conversations[j].ID
	})
	return page, nil
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
