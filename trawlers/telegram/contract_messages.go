package telecrawl

import (
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
)

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
		Ref:          targetRef,
		Chat:         openChatFromMessage(window.Target),
		Participants: append([]string(nil), window.Participants...),
		Message:      openMessageFromStore(window.Target, true),
		Context:      context,
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
	return store.ChatRef(jid)
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

func messageWhereForList(message store.Message) string {
	where := messageWhere(message)
	if where == messageWho(message) {
		return "direct"
	}
	return where
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
