package telecrawl

import (
	"context"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/telegramdesktop"
	postboxpkg "github.com/opentrawl/opentrawl/trawlers/telegram/internal/telegramdesktop/postbox"
)

func (c *Crawler) syncFullTelegramHistory(ctx context.Context, r *runtime, st *store.Store, sourcePath string, progress telegramdesktop.ProgressReporter) (store.SyncStats, error) {
	state, err := loadTelegramHistoryState(st.Path())
	if err != nil {
		return store.SyncStats{}, err
	}
	// A durable public opt-in can only be written after an initial download
	// completes. If its restart checkpoint was removed, the archive itself is
	// still sufficient for incremental acquisition.
	if c.cfg.FullHistory {
		state.Complete = true
	}
	existing, err := st.Messages(ctx, store.MessageFilter{Limit: int(^uint(0) >> 1)})
	if err != nil {
		return store.SyncStats{}, err
	}
	existingByPK := make(map[int64]store.Message, len(existing))
	existingPKs := make(map[int64]struct{}, len(existing))
	chatCounts := make(map[string]int)
	chatLatest := make(map[string]time.Time)
	for _, message := range existing {
		existingByPK[message.SourcePK] = message
		existingPKs[message.SourcePK] = struct{}{}
		chatCounts[message.ChatJID]++
		if message.Timestamp.After(chatLatest[message.ChatJID]) {
			chatLatest[message.ChatJID] = message.Timestamp
		}
	}
	existingContacts, err := st.ListContacts(ctx, -1)
	if err != nil {
		return store.SyncStats{}, err
	}
	contactsByJID := make(map[string]store.Contact, len(existingContacts))
	for _, contact := range existingContacts {
		contactsByJID[contact.JID] = contact
	}
	sources, err := postboxpkg.DiscoverSources(sourcePath)
	if err != nil {
		return store.SyncStats{}, err
	}
	multiAccount := len(sources) > 1
	completed := state.completedSet()
	var total store.SyncStats
	err = telegramdesktop.DownloadPostboxMessageHistory(ctx, sources, multiAccount, telegramdesktop.PostboxHistoryOptions{
		CompletedDialogs:  completed,
		ResumeOffsets:     state.DialogOffsets,
		ExistingSourcePKs: existingPKs,
		Incremental:       state.Complete,
		Progress:          progress,
		DialogBatch: func(checkpoint string, offset int, complete bool, result telegramdesktop.ImportResult) error {
			result.Stats.SourcePath = sourcePath
			for index := range result.Contacts {
				result.Contacts[index] = preserveLocalContactFields(result.Contacts[index], contactsByJID[result.Contacts[index].JID])
				contactsByJID[result.Contacts[index].JID] = result.Contacts[index]
			}
			for index := range result.Messages {
				current, existed := existingByPK[result.Messages[index].SourcePK]
				if existed {
					preserveCloudMessageFields(&result.Messages[index], current)
				} else {
					chatCounts[result.Messages[index].ChatJID]++
				}
				existingByPK[result.Messages[index].SourcePK] = result.Messages[index]
				existingPKs[result.Messages[index].SourcePK] = struct{}{}
				if result.Messages[index].Timestamp.After(chatLatest[result.Messages[index].ChatJID]) {
					chatLatest[result.Messages[index].ChatJID] = result.Messages[index].Timestamp
				}
			}
			for index := range result.Chats {
				result.Chats[index].MessageCount = chatCounts[result.Chats[index].JID]
				result.Chats[index].LastMessageAt = chatLatest[result.Chats[index].JID]
			}
			if len(result.Messages) > 0 || !state.Complete {
				if err := prepareImportResultForWrite(ctx, st, &result); err != nil {
					return err
				}
				counts, err := storeImportResult(ctx, st, &result, "")
				if err != nil {
					return err
				}
				total.Added += counts.Added
				total.Updated += counts.Updated
				total.Removed += counts.Removed
			}
			if !state.Complete {
				if state.DialogOffsets == nil {
					state.DialogOffsets = map[string]int{}
				}
				if complete {
					delete(state.DialogOffsets, checkpoint)
					if !completed[checkpoint] {
						completed[checkpoint] = true
						state.CompletedDialogs = append(state.CompletedDialogs, checkpoint)
					}
				} else {
					state.DialogOffsets[checkpoint] = offset
				}
				if err := saveTelegramHistoryState(st.Path(), state); err != nil {
					return err
				}
			}
			return nil
		},
	})
	if err != nil {
		return total, err
	}
	if err := ctx.Err(); err != nil {
		return total, err
	}
	state.Complete = true
	state.CompletedDialogs = nil
	state.DialogOffsets = nil
	if err := saveTelegramHistoryState(st.Path(), state); err != nil {
		return total, err
	}
	if !c.cfg.FullHistory {
		c.cfg.FullHistory = true
		if err := writeTelegramConfig(r.configPath, c.cfg); err != nil {
			return total, err
		}
	}
	return total, nil
}

// Cloud history carries current Telegram identity fields but not every field
// decoded from the local Postbox. Preserve local-only data when merging the
// two projections of the same contact.
func preserveLocalContactFields(next, current store.Contact) store.Contact {
	if next.PeerType == "" {
		next.PeerType = current.PeerType
	}
	if next.Phone == "" {
		next.Phone = current.Phone
	}
	if next.FullName == "" {
		next.FullName = current.FullName
	}
	if next.FirstName == "" {
		next.FirstName = current.FirstName
	}
	if next.LastName == "" {
		next.LastName = current.LastName
	}
	if next.BusinessName == "" {
		next.BusinessName = current.BusinessName
	}
	if next.Username == "" {
		next.Username = current.Username
	}
	if next.LID == "" {
		next.LID = current.LID
	}
	if next.AboutText == "" {
		next.AboutText = current.AboutText
	}
	if next.AvatarPath == "" {
		next.AvatarPath = current.AvatarPath
	}
	if next.UpdatedAt.IsZero() {
		next.UpdatedAt = current.UpdatedAt
	}
	return next
}

// Cloud history and Postbox describe the same message. Cloud delivery must not
// erase attachment paths or richer local projection fields when their values
// are absent from the network response.
func preserveCloudMessageFields(next *store.Message, current store.Message) {
	if next == nil {
		return
	}
	if strings.TrimSpace(next.MediaPath) == "" {
		next.MediaPath = current.MediaPath
		next.MediaSize = current.MediaSize
	}
	if next.MediaType == "" {
		next.MediaType = current.MediaType
	}
	if next.MediaTitle == "" {
		next.MediaTitle = current.MediaTitle
	}
	if next.MetadataType == "" {
		next.MetadataType = current.MetadataType
		next.MetadataTitle = current.MetadataTitle
		next.MetadataURL = current.MetadataURL
		next.MetadataJSON = current.MetadataJSON
	}
}
