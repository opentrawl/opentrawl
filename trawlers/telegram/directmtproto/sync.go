package directmtproto

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/historystate"
	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/telegramdesktop"
	archiveStore "github.com/opentrawl/opentrawl/trawlkit/store"
)

const ArchiveSource = "telegram-mtproto"

type SyncOptions struct {
	ClientOptions
	OpenedArchive  *archiveStore.Store
	ConversationID string
	FetchMedia     bool
	Progress       func(int64, string) error
}

type SyncReport struct {
	Added, Updated int64
}

type progressCallback func(int64, string) error

func (callback progressCallback) Report(count int64, message string) error {
	if callback == nil {
		return nil
	}
	return callback(count, message)
}

// SyncDirectMTProto reads Telegram cloud dialogs and history into the normal Telegram archive.
func SyncDirectMTProto(ctx context.Context, sessionPath, archiveDBPath string, options SyncOptions) (SyncReport, error) {
	var report SyncReport
	if _, err := os.Stat(sessionPath); err != nil {
		return report, err
	}
	client, err := newClient(sessionPath, options.ClientOptions, nil)
	if err != nil {
		return report, err
	}
	var archive *store.Store
	if options.OpenedArchive == nil {
		archive, err = store.Open(ctx, archiveDBPath)
	} else {
		archive, err = store.Use(ctx, options.OpenedArchive, archiveDBPath)
	}
	if err != nil {
		return report, err
	}
	defer func() { _ = archive.Close() }()

	checkpointPath := archiveDBPath + ".direct-mtproto"
	historyState, err := historystate.LoadTelegramHistoryState(checkpointPath)
	if err != nil {
		return report, err
	}
	completedDialogs := historyState.CompletedDialogSet()
	if historyState.NewestArchivedMessageIDs == nil {
		historyState.NewestArchivedMessageIDs = make(map[string]int)
	}
	chats, err := archive.ListChats(ctx, -1, false)
	if err != nil {
		return report, err
	}
	chatsByID := make(map[string]store.Chat, len(chats))
	for _, chat := range chats {
		chatsByID[chat.JID] = chat
	}

	err = runTelegramSession(ctx, client, func(ctx context.Context) error {
		self, err := client.Self(ctx)
		if err != nil {
			return err
		}
		foundConversation := options.ConversationID == ""
		err = telegramdesktop.DownloadAuthenticatedTelegramHistory(ctx, client, self, telegramdesktop.PostboxHistoryOptions{
			ConversationID:           options.ConversationID,
			CompletedDialogs:         completedDialogs,
			ResumeOffsets:            historyState.DialogOffsets,
			NewestArchivedMessageIDs: historyState.NewestArchivedMessageIDs,
			Incremental:              true,
			Progress:                 progressCallback(options.Progress),
			DialogBatch: func(checkpoint telegramdesktop.CloudHistoryCheckpoint, batch telegramdesktop.ImportResult) error {
				foundConversation = true
				wasComplete := completedDialogs[checkpoint.Dialog]
				if wasComplete && len(batch.Messages) == 0 && checkpoint.NewestMessageID == 0 {
					return nil
				}
				messagePKs := make([]int64, 0, len(batch.Messages))
				for _, message := range batch.Messages {
					messagePKs = append(messagePKs, message.SourcePK)
				}
				currentMessages, err := archive.MessagesBySourcePKs(ctx, messagePKs)
				if err != nil {
					return err
				}
				currentMessagesByPK := make(map[int64]store.Message, len(currentMessages))
				for _, message := range currentMessages {
					currentMessagesByPK[message.SourcePK] = message
				}
				chat := &batch.Chats[0]
				currentChat := chatsByID[chat.JID]
				chat.MessageCount = currentChat.MessageCount
				chat.LastMessageAt = currentChat.LastMessageAt
				chat.UnreadCount = currentChat.UnreadCount
				chat.FolderID = currentChat.FolderID
				chat.Forum = currentChat.Forum
				for index := range batch.Messages {
					message := &batch.Messages[index]
					if current, exists := currentMessagesByPK[message.SourcePK]; exists {
						if message.MediaType != "" {
							message.MediaPath = current.MediaPath
							if current.MediaPath != "" {
								message.MediaSize = current.MediaSize
							}
						}
					} else {
						chat.MessageCount++
					}
					if message.Timestamp.After(chat.LastMessageAt) {
						chat.LastMessageAt = message.Timestamp
					}
				}
				counts, err := archive.MergeObserved(ctx, batch.Contacts, batch.Chats, nil, nil, nil, batch.Participants, batch.Messages)
				if err != nil {
					return err
				}
				chatsByID[chat.JID] = *chat
				report.Added += counts.Added
				report.Updated += counts.Updated

				// Keep the prior upper bound until all new messages are stored.
				// Otherwise an interrupted write can hide older unprocessed pages.
				if wasComplete && !checkpoint.Complete {
					return nil
				}
				historyState.NewestArchivedMessageIDs[checkpoint.Dialog] = max(
					historyState.NewestArchivedMessageIDs[checkpoint.Dialog], checkpoint.NewestMessageID)
				historyState.RecordHistoryBatch(completedDialogs, checkpoint.Dialog, checkpoint.OldestMessageID, checkpoint.Complete)
				return historystate.SaveTelegramHistoryState(checkpointPath, historyState)
			},
		})
		if err != nil {
			return err
		}
		if !foundConversation {
			return errors.New("conversation is unavailable in this Telegram session")
		}
		if !options.FetchMedia {
			return nil
		}
		messages, err := telegramdesktop.ArchivedTelegramMediaCandidates(ctx, archive, options.ConversationID, nil)
		if err != nil {
			return err
		}
		media, err := telegramdesktop.BackfillAuthenticatedArchiveMedia(ctx, client, archiveDBPath, messages, progressCallback(options.Progress))
		updated, _, err := telegramdesktop.PersistArchivedTelegramMedia(ctx, archive, media, err)
		report.Updated += updated
		return err
	})
	if err == nil && options.OpenedArchive == nil {
		err = archive.RecordSuccessfullyCompletedArchiveUpdate(ctx, ArchiveSource, time.Now().UTC())
	}
	return report, err
}
