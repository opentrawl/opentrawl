package telegramdesktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/message/peer"
	"github.com/gotd/td/telegram/query"
	"github.com/gotd/td/telegram/query/dialogs"
	querymessages "github.com/gotd/td/telegram/query/messages"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	postboxpkg "github.com/opentrawl/opentrawl/trawlers/telegram/internal/telegramdesktop/postbox"
)

const postboxHistoryBatchSize = 100

// PostboxHistoryOptions describes the internal, resumable cloud-history pass.
// CompletedDialog keys are opaque checkpoint keys returned to DialogComplete.
// ExistingSourcePKs is used only after an initial backfill is complete: each
// conversation then stops at the first message already present in the archive.
type PostboxHistoryOptions struct {
	CompletedDialogs  map[string]bool
	ResumeOffsets     map[string]int
	ExistingSourcePKs map[int64]struct{}
	Incremental       bool
	Progress          ProgressReporter
	DialogBatch       func(checkpoint string, offset int, complete bool, result ImportResult) error
}

// DownloadPostboxMessageHistory uses the authenticated session already owned
// by Telegram for macOS. It reads Telegram but never mutates Telegram's local
// Postbox. Results are delivered one conversation at a time so OpenTrawl can
// commit progress without holding an entire account history in memory.
func DownloadPostboxMessageHistory(ctx context.Context, sources []postboxpkg.Source, multiAccount bool, opts PostboxHistoryOptions) error {
	sessions := orderedPostboxHistorySessions(sources)
	if len(sessions) == 0 {
		return errors.New("telegram for macOS has no usable authenticated session")
	}
	seenAccounts := map[int64]struct{}{}
	for _, remote := range sessions {
		if err := ctx.Err(); err != nil {
			return err
		}
		loader := postboxHistoryLoader{
			accountID: remote.native.AccountID, multiAccount: multiAccount, opts: opts,
			resumeOffsets: cloneHistoryOffsets(opts.ResumeOffsets),
		}
		var accountID int64
		for {
			client, err := newPostboxHistoryClient(ctx, remote)
			if err != nil {
				return err
			}
			duplicate := false
			err = client.Run(ctx, func(ctx context.Context) error {
				self, err := client.Self(ctx)
				if err != nil {
					return fmt.Errorf("telegram session is not authorised: %w", err)
				}
				if accountID != 0 && accountID != self.ID {
					return errors.New("telegram session identity changed while resuming history")
				}
				accountID = self.ID
				if _, duplicate = seenAccounts[self.ID]; duplicate {
					return nil
				}
				loader.raw = tg.NewClient(client)
				loader.self = self
				return loader.download(ctx)
			})
			if duplicate {
				break
			}
			if err == nil {
				seenAccounts[accountID] = struct{}{}
				break
			}
			if !tgerr.Is(err, "CONNECTION_LAYER_INVALID") {
				return err
			}
			if opts.Progress != nil {
				_ = opts.Progress.Report(0, "reconnecting to Telegram after rate-limit wait")
			}
			if err := telegramFloodWaitSleep(ctx, time.Second); err != nil {
				return err
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	return nil
}

func newPostboxHistoryClient(ctx context.Context, remote postboxRemoteSession) (*telegram.Client, error) {
	storage, err := postboxSessionStorage(ctx, remote.native)
	if err != nil {
		return nil, err
	}
	return telegram.NewClient(telegramMacAPIID, telegramMacAPIHash, telegram.Options{
		DC: remote.native.DCID, SessionStorage: storage, NoUpdates: true, AllowCDN: true,
		Device: telegram.DeviceConfig{
			DeviceModel: "Mac", SystemVersion: "macOS", AppVersion: "11.15",
			SystemLangCode: "en-US", LangPack: "macos", LangCode: "en",
		},
	}), nil
}

func orderedPostboxHistorySessions(sources []postboxpkg.Source) []postboxRemoteSession {
	sessions := postboxNativeSessions(sources)
	out := make([]postboxRemoteSession, 0, len(sessions))
	for _, remote := range sessions {
		out = append(out, remote)
	}
	// Prefer the most recently modified native store. Multiple build lanes can
	// contain the same account; Self() below provides the authoritative dedupe.
	sort.Slice(out, func(i, j int) bool {
		left, leftErr := fileModTime(out[i].source.DBPath)
		right, rightErr := fileModTime(out[j].source.DBPath)
		if leftErr == nil && rightErr == nil && !left.Equal(right) {
			return left.After(right)
		}
		return out[i].native.AccountID < out[j].native.AccountID
	})
	return out
}

var fileModTime = func(path string) (time.Time, error) {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

type postboxHistoryLoader struct {
	raw           *tg.Client
	self          *tg.User
	accountID     string
	multiAccount  bool
	opts          PostboxHistoryOptions
	resumeOffsets map[string]int
	downloaded    int64
}

func (l *postboxHistoryLoader) download(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	err := query.GetDialogs(l.raw).BatchSize(postboxHistoryBatchSize).ForEach(ctx, func(ctx context.Context, elem dialogs.Elem) error {
		if elem.Deleted() {
			return nil
		}
		rawChatID, ok := postboxpkg.TelegramPeerToPostboxID(elem.Dialog.GetPeer())
		if !ok {
			return nil
		}
		checkpoint := fmt.Sprintf("%d:%d", l.self.ID, rawChatID)
		if !l.opts.Incremental && l.opts.CompletedDialogs[checkpoint] {
			return nil
		}
		return l.downloadDialog(ctx, elem, rawChatID, checkpoint)
	})
	if err != nil {
		return err
	}
	return ctx.Err()
}

func (l *postboxHistoryLoader) downloadDialog(ctx context.Context, elem dialogs.Elem, rawChatID int64, checkpoint string) error {
	chatID := postboxpkg.PeerStoreID(l.accountID, rawChatID, l.multiAccount)
	chatInfo := cloudPeerInfo(elem.Dialog.GetPeer(), elem.Entities, l.self)
	contacts := map[string]store.Contact{}
	rememberCloudContact(contacts, chatID, chatInfo)
	var messages []store.Message
	offsetID := l.resumeOffsets[checkpoint]
	for {
		iterator := elem.Messages(l.raw).BatchSize(postboxHistoryBatchSize).OffsetID(offsetID).Iter()
		for iterator.Next(ctx) {
			item := iterator.Value()
			messageID := item.Msg.GetID()
			if messageID <= 0 {
				continue
			}
			offsetID = messageID
			sourcePK := postboxpkg.SourcePK(l.accountID, rawChatID, 0, int32(messageID), l.multiAccount)
			if l.opts.Incremental {
				if _, exists := l.opts.ExistingSourcePKs[sourcePK]; exists {
					return l.flushDialogBatch(checkpoint, offsetID, true, chatID, chatInfo, contacts, messages)
				}
			}
			message := l.convertMessage(item, rawChatID, chatID, chatInfo, contacts)
			messages = append(messages, message)
			l.downloaded++
			if l.opts.Progress != nil && len(messages)%postboxHistoryBatchSize == 0 {
				_ = l.opts.Progress.Report(l.downloaded, "downloading older Telegram messages")
			}
			if len(messages) == postboxHistoryBatchSize {
				if err := l.flushDialogBatch(checkpoint, offsetID, false, chatID, chatInfo, contacts, messages); err != nil {
					return err
				}
				messages = nil
				contacts = map[string]store.Contact{}
				rememberCloudContact(contacts, chatID, chatInfo)
			}
		}
		err := iterator.Err()
		if err == nil {
			break
		}
		if waitErr := waitForPostboxHistoryFlood(ctx, err, l.opts.Progress); waitErr != nil {
			return waitErr
		}
	}
	return l.flushDialogBatch(checkpoint, offsetID, true, chatID, chatInfo, contacts, messages)
}

func cloneHistoryOffsets(offsets map[string]int) map[string]int {
	cloned := make(map[string]int, len(offsets))
	for checkpoint, offset := range offsets {
		if strings.TrimSpace(checkpoint) != "" && offset > 0 {
			cloned[checkpoint] = offset
		}
	}
	return cloned
}

func (l *postboxHistoryLoader) flushDialogBatch(checkpoint string, offset int, complete bool, chatID string, chatInfo cloudPeerDetails, contacts map[string]store.Contact, messages []store.Message) error {
	result := l.finishDialogResult(chatID, chatInfo, contacts, messages)
	if l.opts.DialogBatch != nil {
		if err := l.opts.DialogBatch(checkpoint, offset, complete, result); err != nil {
			return err
		}
	}
	if complete {
		delete(l.resumeOffsets, checkpoint)
	} else {
		l.resumeOffsets[checkpoint] = offset
	}
	return nil
}

func (l *postboxHistoryLoader) finishDialogResult(chatID string, chatInfo cloudPeerDetails, contacts map[string]store.Contact, messages []store.Message) ImportResult {
	sort.Slice(messages, func(i, j int) bool {
		if messages[i].Timestamp.Equal(messages[j].Timestamp) {
			return messages[i].SourcePK < messages[j].SourcePK
		}
		return messages[i].Timestamp.Before(messages[j].Timestamp)
	})
	lastMessageAt := time.Time{}
	if len(messages) > 0 {
		lastMessageAt = messages[len(messages)-1].Timestamp
	}
	result := ImportResult{
		Stats: store.ImportStats{SourcePath: "telegram-cloud-history", Messages: len(messages), Chats: 1, StartedAt: time.Now().UTC(), FinishedAt: time.Now().UTC()},
		Chats: []store.Chat{{
			JID: chatID, Kind: firstNonEmpty(chatInfo.kind, "unknown"), Name: firstNonEmpty(chatInfo.name, chatID),
			Username: chatInfo.username, MessageCount: len(messages), LastMessageAt: lastMessageAt,
		}},
		Messages: messages,
	}
	for _, contact := range contacts {
		result.Contacts = append(result.Contacts, contact)
	}
	sort.Slice(result.Contacts, func(i, j int) bool { return result.Contacts[i].JID < result.Contacts[j].JID })
	result.Participants = groupParticipantsFromMessages(result.Chats, result.Contacts, result.Messages)
	return result
}

func waitForPostboxHistoryFlood(ctx context.Context, err error, progress ProgressReporter) error {
	delay, ok := tgerr.AsFloodWait(err)
	if !ok {
		return err
	}
	wait := delay + telegramFloodWaitSafetyDelay
	if progress != nil {
		_ = progress.Report(0, fmt.Sprintf("Telegram rate limit; waiting %s", wait))
	}
	if err := telegramFloodWaitSleep(ctx, wait); err != nil {
		return fmt.Errorf("wait for telegram rate limit: %w", err)
	}
	return nil
}

func (l *postboxHistoryLoader) convertMessage(item querymessages.Elem, rawChatID int64, chatID string, chatInfo cloudPeerDetails, contacts map[string]store.Contact) store.Message {
	msg := item.Msg
	messageID := msg.GetID()
	senderID, senderName := "", ""
	if from, ok := msg.GetFromID(); ok {
		if rawSenderID, ok := postboxpkg.TelegramPeerToPostboxID(from); ok {
			senderID = postboxpkg.PeerStoreID(l.accountID, rawSenderID, l.multiAccount)
			info := cloudPeerInfo(from, item.Entities, l.self)
			senderName = info.name
			rememberCloudContact(contacts, senderID, info)
		}
	} else if msg.GetOut() {
		rawSelfID, _ := postboxpkg.TelegramPeerToPostboxID(&tg.PeerUser{UserID: l.self.ID})
		senderID = postboxpkg.PeerStoreID(l.accountID, rawSelfID, l.multiAccount)
		senderName = cloudUserInfo(l.self).name
	}
	replyTo, threadID, replyChat, topicID := cloudReplyFields(msg, l.accountID, l.multiAccount)
	return store.Message{
		SourcePK: postboxpkg.SourcePK(l.accountID, rawChatID, 0, int32(messageID), l.multiAccount),
		ChatJID:  chatID, ChatName: firstNonEmpty(chatInfo.name, chatID), MessageID: fmt.Sprintf("0:%d", messageID),
		SenderJID: senderID, SenderName: senderName, Timestamp: time.Unix(int64(msg.GetDate()), 0).UTC(), FromMe: msg.GetOut(),
		Text: cloudMessageText(msg), MessageType: cloudMessageType(msg), MediaType: cloudMediaType(msg), MediaTitle: cloudMediaTitle(msg), MediaSize: cloudMediaSize(msg),
		TopicID: topicID, ReplyToID: replyTo, ThreadID: threadID, ReplyToChat: replyChat,
		EditTime: cloudEditTime(msg), Views: cloudViews(msg), Forwards: cloudForwards(msg), RepliesCount: cloudRepliesCount(msg), Pinned: cloudPinned(msg),
		ForwardJSON: cloudJSON(cloudForward(msg)), ReactionsJSON: cloudJSON(cloudReactions(msg)),
	}
}

type cloudPeerDetails struct {
	kind, name, username, firstName, lastName, fullName, phone string
}

func cloudPeerInfo(id tg.PeerClass, entities peer.Entities, self *tg.User) cloudPeerDetails {
	switch value := id.(type) {
	case *tg.PeerUser:
		if self != nil && value.UserID == self.ID {
			return cloudUserInfo(self)
		}
		if user, ok := entities.User(value.UserID); ok {
			return cloudUserInfo(user)
		}
		return cloudPeerDetails{kind: "user", name: strconv.FormatInt(value.UserID, 10)}
	case *tg.PeerChat:
		if chat, ok := entities.Chat(value.ChatID); ok {
			return cloudPeerDetails{kind: "group", name: chat.Title}
		}
		return cloudPeerDetails{kind: "group", name: strconv.FormatInt(value.ChatID, 10)}
	case *tg.PeerChannel:
		if channel, ok := entities.Channel(value.ChannelID); ok {
			username, _ := channel.GetUsername()
			return cloudPeerDetails{kind: "channel", name: channel.Title, username: username}
		}
		return cloudPeerDetails{kind: "channel", name: strconv.FormatInt(value.ChannelID, 10)}
	default:
		return cloudPeerDetails{}
	}
}

func cloudUserInfo(user *tg.User) cloudPeerDetails {
	first, _ := user.GetFirstName()
	last, _ := user.GetLastName()
	username, _ := user.GetUsername()
	phone, _ := user.GetPhone()
	fullName := strings.TrimSpace(first + " " + last)
	return cloudPeerDetails{kind: "user", name: firstNonEmpty(fullName, username, strconv.FormatInt(user.ID, 10)), username: username, firstName: first, lastName: last, fullName: fullName, phone: phone}
}

func rememberCloudContact(contacts map[string]store.Contact, id string, info cloudPeerDetails) {
	if info.kind != "user" || strings.TrimSpace(id) == "" {
		return
	}
	contacts[id] = store.Contact{JID: id, PeerType: "user", Phone: info.phone, FullName: info.fullName, FirstName: info.firstName, LastName: info.lastName, Username: info.username}
}

func cloudMessageText(msg tg.NotEmptyMessage) string {
	if value, ok := msg.(*tg.Message); ok {
		return value.Message
	}
	return ""
}

func cloudMessageType(msg tg.NotEmptyMessage) string {
	switch msg.(type) {
	case *tg.Message:
		return "message"
	case *tg.MessageService:
		return "service"
	default:
		return strings.ToLower(msg.TypeName())
	}
}

func cloudMediaType(msg tg.NotEmptyMessage) string {
	value, ok := msg.(*tg.Message)
	if !ok || value.Media == nil {
		return ""
	}
	return strings.ToLower(strings.TrimPrefix(value.Media.TypeName(), "messageMedia"))
}

func cloudMediaTitle(msg tg.NotEmptyMessage) string {
	value, ok := msg.(*tg.Message)
	if !ok || value.Media == nil {
		return ""
	}
	switch media := value.Media.(type) {
	case *tg.MessageMediaDocument:
		if doc, ok := media.Document.AsNotEmpty(); ok {
			return firstNonEmpty(telegramDocumentFilename(doc), telegramDocumentAudioTitle(doc))
		}
	case *tg.MessageMediaWebPage:
		if page, ok := media.Webpage.(*tg.WebPage); ok {
			title, _ := page.GetTitle()
			site, _ := page.GetSiteName()
			return firstNonEmpty(title, site, page.URL)
		}
	}
	return ""
}

func cloudMediaSize(msg tg.NotEmptyMessage) int64 {
	value, ok := msg.(*tg.Message)
	if !ok {
		return 0
	}
	media, ok := value.Media.(*tg.MessageMediaDocument)
	if !ok {
		return 0
	}
	doc, ok := media.Document.AsNotEmpty()
	if !ok {
		return 0
	}
	return doc.Size
}

func cloudReplyFields(msg tg.NotEmptyMessage, accountID string, multiAccount bool) (replyTo, threadID, replyChat, topicID string) {
	raw, ok := msg.GetReplyTo()
	if !ok {
		return "", "", "", ""
	}
	reply, ok := raw.(*tg.MessageReplyHeader)
	if !ok {
		return "", "", "", ""
	}
	if value, ok := reply.GetReplyToMsgID(); ok && value != 0 {
		replyTo = fmt.Sprintf("0:%d", value)
	}
	if value, ok := reply.GetReplyToTopID(); ok && value != 0 {
		threadID, topicID = strconv.Itoa(value), strconv.Itoa(value)
	} else if reply.GetForumTopic() && replyTo != "" {
		topicID = strings.TrimPrefix(replyTo, "0:")
	}
	if id, ok := reply.GetReplyToPeerID(); ok {
		if rawID, ok := postboxpkg.TelegramPeerToPostboxID(id); ok {
			replyChat = postboxpkg.PeerStoreID(accountID, rawID, multiAccount)
		}
	}
	return replyTo, threadID, replyChat, topicID
}

func cloudEditTime(msg tg.NotEmptyMessage) time.Time {
	if value, ok := msg.(*tg.Message); ok {
		if edited, ok := value.GetEditDate(); ok {
			return time.Unix(int64(edited), 0).UTC()
		}
	}
	return time.Time{}
}

func cloudViews(msg tg.NotEmptyMessage) int {
	if value, ok := msg.(*tg.Message); ok {
		views, _ := value.GetViews()
		return views
	}
	return 0
}
func cloudForwards(msg tg.NotEmptyMessage) int {
	if value, ok := msg.(*tg.Message); ok {
		forwards, _ := value.GetForwards()
		return forwards
	}
	return 0
}
func cloudRepliesCount(msg tg.NotEmptyMessage) int {
	if value, ok := msg.(*tg.Message); ok {
		if replies, ok := value.GetReplies(); ok {
			return replies.Replies
		}
	}
	return 0
}
func cloudPinned(msg tg.NotEmptyMessage) bool {
	if value, ok := msg.(*tg.Message); ok {
		return value.Pinned
	}
	return false
}
func cloudForward(msg tg.NotEmptyMessage) any {
	if value, ok := msg.(*tg.Message); ok {
		if forward, ok := value.GetFwdFrom(); ok {
			return forward
		}
	}
	return nil
}
func cloudReactions(msg tg.NotEmptyMessage) any {
	switch value := msg.(type) {
	case *tg.Message:
		if reactions, ok := value.GetReactions(); ok {
			return reactions
		}
	case *tg.MessageService:
		if reactions, ok := value.GetReactions(); ok {
			return reactions
		}
	}
	return nil
}
func cloudJSON(value any) string {
	if value == nil {
		return ""
	}
	data, err := json.Marshal(value)
	if err != nil || string(data) == "null" || string(data) == "{}" {
		return ""
	}
	return string(data)
}
