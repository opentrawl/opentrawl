package telecrawl

import (
	"context"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/telegramdesktop"
	postboxpkg "github.com/opentrawl/opentrawl/trawlers/telegram/internal/telegramdesktop/postbox"
)

const telegramDialogCompletionVersion = 1

func (c *Crawler) syncFullTelegramHistory(ctx context.Context, r *runtime, st *store.Store, sourcePath string, legacyCompletedChatIDs map[string]bool, progress telegramdesktop.ProgressReporter) (store.SyncStats, error) {
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
	if progress != nil {
		_ = progress.Report(0, "preparing the existing Telegram archive")
	}
	existingChats, err := st.ListChats(ctx, -1, false)
	if err != nil {
		return store.SyncStats{}, err
	}
	chatCounts := make(map[string]int, len(existingChats))
	chatLatest := make(map[string]time.Time, len(existingChats))
	for _, chat := range existingChats {
		chatCounts[chat.JID] = chat.MessageCount
		chatLatest[chat.JID] = chat.LastMessageAt
	}
	existingContacts, err := st.ListContacts(ctx, -1)
	if err != nil {
		return store.SyncStats{}, err
	}
	contactsByJID := make(map[string]store.Contact, len(existingContacts))
	for _, contact := range existingContacts {
		contactsByJID[contact.JID] = contact
	}
	if progress != nil {
		_ = progress.Report(0, "connecting to Telegram for older messages")
	}
	sources, err := postboxpkg.DiscoverSources(sourcePath)
	if err != nil {
		return store.SyncStats{}, err
	}
	multiAccount := len(sources) > 1
	completed := state.completedSet()
	// The first implementation erased per-dialog completion after setting the
	// global complete bit. That lost the information needed to distinguish an
	// existing conversation from a newly discovered one. The caller snapshots
	// chat IDs before the local import, so the one-time migration can treat only
	// those chats as complete. A chat first seen by that local import still gets
	// its full older history. New checkpoints use exact per-dialog completion.
	var total store.SyncStats
	err = telegramdesktop.DownloadPostboxMessageHistory(ctx, sources, multiAccount, telegramdesktop.PostboxHistoryOptions{
		CompletedDialogs:       completed,
		LegacyCompletedChatIDs: legacyCompletedChatIDs,
		ResumeOffsets:          state.DialogOffsets,
		MessageExists:          st.MessageExists,
		Incremental:            state.Complete,
		Progress:               progress,
		DialogBatch: func(checkpoint string, offset int, complete bool, result telegramdesktop.ImportResult) error {
			result.Stats.SourcePath = sourcePath
			sourcePKs := make([]int64, len(result.Messages))
			for index := range result.Messages {
				sourcePKs[index] = result.Messages[index].SourcePK
			}
			existing, err := st.MessagesBySourcePKs(ctx, sourcePKs)
			if err != nil {
				return err
			}
			existingByPK := make(map[int64]store.Message, len(existing))
			for _, message := range existing {
				existingByPK[message.SourcePK] = message
			}
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
			recordTelegramHistoryBatch(&state, completed, checkpoint, offset, complete)
			if err := saveTelegramHistoryState(st.Path(), state); err != nil {
				return err
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
	state.DialogCompletionVersion = telegramDialogCompletionVersion
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

func needsTelegramDialogCheckpointMigration(state telegramHistoryState) bool {
	return state.Complete && state.DialogCompletionVersion < telegramDialogCompletionVersion
}

// recordTelegramHistoryBatch persists completion per conversation even after
// the account-wide initial backfill has completed. That distinction lets later
// syncs update known conversations cheaply without truncating older history in
// a conversation discovered for the first time.
func recordTelegramHistoryBatch(state *telegramHistoryState, completed map[string]bool, checkpoint string, offset int, complete bool) {
	if state.DialogOffsets == nil {
		state.DialogOffsets = map[string]int{}
	}
	if !complete {
		state.DialogOffsets[checkpoint] = offset
		return
	}
	delete(state.DialogOffsets, checkpoint)
	if !completed[checkpoint] {
		completed[checkpoint] = true
		state.CompletedDialogs = append(state.CompletedDialogs, checkpoint)
	}
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

// Cloud history and Postbox describe the same message. Telegram's current
// message is authoritative for whether media exists; local Postbox remains
// authoritative for a cached file path and richer local-only metadata.
func preserveCloudMessageFields(next *store.Message, current store.Message) {
	if next == nil {
		return
	}
	if next.MediaType != "" && strings.TrimSpace(next.MediaPath) == "" {
		next.MediaPath = current.MediaPath
		next.MediaSize = current.MediaSize
	}
	if next.MediaType != "" && next.MediaTitle == "" {
		next.MediaTitle = current.MediaTitle
	}
	if next.MetadataType == "" {
		next.MetadataType = current.MetadataType
		next.MetadataTitle = current.MetadataTitle
		next.MetadataURL = current.MetadataURL
		next.MetadataJSON = current.MetadataJSON
	}
}
