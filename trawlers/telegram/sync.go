package telegram

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/telegramdesktop"
	"github.com/opentrawl/opentrawl/trawlkit"
	cklog "github.com/opentrawl/opentrawl/trawlkit/log"
)

// Sync reports the message changes observed by the archive write.
func (c *Crawler) Sync(ctx context.Context, req *trawlkit.Request) (*trawlkit.SyncReport, error) {
	r := c.handler(ctx, req)
	if c.sync.FullHistory && strings.TrimSpace(c.sync.Chat) != "" {
		return nil, commandErr(1, "invalid_arguments", fmt.Errorf("--full-history cannot be combined with --chat"), "Run trawl sync telegram --full-history without --chat.")
	}
	progress, stopProgress := r.startCommandProgress("sync_progress", "messages", "starting sync")
	defer stopProgress()
	var report *trawlkit.SyncReport
	err := r.withStore(func(st *store.Store) error {
		historyState, err := loadTelegramHistoryState(st.Path())
		if err != nil {
			return err
		}
		preserveCloudProjection := c.cfg.FullHistory || telegramHistoryStarted(historyState)
		var existingMediaSourcePath string
		var existingMediaRefs []telegramdesktop.ExistingMediaRef
		if c.sync.FetchMedia {
			var err error
			existingMediaSourcePath, existingMediaRefs, err = existingMediaRefsForImport(r.ctx, st)
			if err != nil {
				return err
			}
		}
		importStarted := time.Now()
		result, err := telegramdesktop.Import(r.ctx, telegramdesktop.ImportOptions{
			Path:                    c.sync.Path,
			DialogsLimit:            c.sync.DialogsLimit,
			MessagesLimit:           c.sync.MessagesLimit,
			ChatID:                  c.sync.Chat,
			FetchMedia:              c.sync.FetchMedia,
			Progress:                progress,
			ExistingMediaSourcePath: existingMediaSourcePath,
			ExistingMediaRefs:       existingMediaRefs,
		}, st.Path())
		importElapsed := time.Since(importStarted)
		if err != nil {
			return err
		}
		if err := prepareImportResultForWrite(r.ctx, st, &result); err != nil {
			return err
		}
		if preserveCloudProjection {
			if err := preserveArchivedTelegramProjection(r.ctx, st, result.Messages); err != nil {
				return err
			}
		}
		writeStarted := time.Now()
		counts, err := storeImportResult(r.ctx, st, &result, c.sync.Chat)
		if err != nil {
			return err
		}
		if c.sync.FullHistory || (c.cfg.FullHistory && strings.TrimSpace(c.sync.Chat) == "") {
			historyCounts, err := c.syncFullTelegramHistory(r.ctx, r, st, result.Stats.SourcePath, progress)
			if err != nil {
				return err
			}
			counts.Added += historyCounts.Added
			counts.Updated += historyCounts.Updated
			counts.Removed += historyCounts.Removed
		}
		if c.sync.FetchMedia {
			localSourcePKs := make(map[int64]struct{}, len(result.Messages))
			for _, message := range result.Messages {
				localSourcePKs[message.SourcePK] = struct{}{}
			}
			mediaCounts, mediaStats, err := backfillArchivedTelegramMedia(r.ctx, st, result.Stats.SourcePath, c.sync.Chat, localSourcePKs, progress)
			if err != nil {
				return err
			}
			counts.Updated += mediaCounts
			result.Stats.RemoteMediaCandidates += mediaStats.RemoteMediaCandidates
			result.Stats.RemoteMediaAttempted += mediaStats.RemoteMediaAttempted
			result.Stats.RemoteMediaDownloads += mediaStats.RemoteMediaDownloads
			result.Stats.RemoteMediaMissing += mediaStats.RemoteMediaMissing
			result.Stats.RemoteMediaUnavailable += mediaStats.RemoteMediaUnavailable
			result.Stats.RemoteMediaTimeouts += mediaStats.RemoteMediaTimeouts
			result.Stats.RemoteMediaErrors += mediaStats.RemoteMediaErrors
			result.Stats.MediaFiles += mediaStats.MediaFiles
			result.Stats.MediaBytes += mediaStats.MediaBytes
		}
		writeElapsed := time.Since(writeStarted)
		r.logSyncTimings(result.Stats, importElapsed, writeElapsed, c.sync.FetchMedia, c.sync.Chat)
		_ = progress.Report(int64(result.Stats.Messages), "sync complete")
		report = &trawlkit.SyncReport{Added: counts.Added, Updated: counts.Updated, Removed: counts.Removed}
		return nil
	})
	if err != nil {
		return nil, syncImportError(err)
	}
	if report == nil {
		return &trawlkit.SyncReport{}, nil
	}
	return report, nil
}

// Once full-history sync has established Telegram's remote message semantics,
// a later local Postbox scan may add cached files and local metadata but must
// not overwrite authorship, direction, conversation structure, or Telegram's
// media classification. New local messages have no archived projection yet;
// the incremental cloud pass immediately establishes it.
func preserveArchivedTelegramProjection(ctx context.Context, st *store.Store, messages []store.Message) error {
	if len(messages) == 0 {
		return nil
	}
	sourcePKs := make([]int64, len(messages))
	for index := range messages {
		sourcePKs[index] = messages[index].SourcePK
	}
	existing, err := st.MessagesBySourcePKs(ctx, sourcePKs)
	if err != nil {
		return err
	}
	byPK := make(map[int64]store.Message, len(existing))
	for _, message := range existing {
		byPK[message.SourcePK] = message
	}
	for index := range messages {
		current, ok := byPK[messages[index].SourcePK]
		if !ok {
			continue
		}
		next := &messages[index]
		next.SenderJID = current.SenderJID
		next.SenderName = current.SenderName
		next.FromMe = current.FromMe
		next.MessageType = current.MessageType
		if strings.TrimSpace(next.MediaPath) == "" {
			next.MediaType = current.MediaType
			next.MediaTitle = current.MediaTitle
		}
		next.TopicID = current.TopicID
		next.ReplyToID = current.ReplyToID
		next.ReplyToChat = current.ReplyToChat
		next.ThreadID = current.ThreadID
		next.EditTime = current.EditTime
		next.ForwardJSON = current.ForwardJSON
		next.ReactionsJSON = current.ReactionsJSON
		next.Views = current.Views
		next.Forwards = current.Forwards
		next.RepliesCount = current.RepliesCount
		next.Pinned = current.Pinned
	}
	return nil
}

func backfillArchivedTelegramMedia(ctx context.Context, st *store.Store, sourcePath, chatID string, excludeSourcePKs map[int64]struct{}, progress telegramdesktop.ProgressReporter) (int64, store.ImportStats, error) {
	messages, err := archivedTelegramMediaCandidates(ctx, st, chatID, excludeSourcePKs)
	if err != nil {
		return 0, store.ImportStats{}, err
	}
	result, err := telegramdesktop.BackfillArchiveMedia(ctx, sourcePath, st.Path(), messages, progress)
	return persistArchivedTelegramMedia(ctx, st, result, err)
}

func persistArchivedTelegramMedia(ctx context.Context, st *store.Store, result telegramdesktop.ArchiveMediaResult, backfillErr error) (int64, store.ImportStats, error) {
	updates := make([]store.MessageMediaUpdate, 0, len(result.Updates))
	for _, update := range result.Updates {
		updates = append(updates, store.MessageMediaUpdate{
			SourcePK: update.SourcePK, MediaPath: update.MediaPath, MediaSize: update.MediaSize,
		})
	}
	if len(updates) == 0 {
		return 0, result.Stats, backfillErr
	}
	updateCtx := ctx
	if backfillErr != nil {
		// A cancelled remote session can still have completed downloads. Persist
		// that bounded work before returning cancellation; media_path is the
		// durable resume marker, so the next run skips these attachments.
		updateCtx = context.WithoutCancel(ctx)
	}
	updated, updateErr := st.UpdateMessageMedia(updateCtx, updates)
	if updateErr != nil {
		return 0, result.Stats, errors.Join(backfillErr, updateErr)
	}
	return int64(updated), result.Stats, backfillErr
}

func archivedTelegramMediaCandidates(ctx context.Context, st *store.Store, chatID string, excludeSourcePKs map[int64]struct{}) ([]store.Message, error) {
	filter := store.MessageFilter{HasMedia: true, Limit: int(^uint(0) >> 1)}
	filter.ChatJID = strings.TrimSpace(chatID)
	messages, err := st.Messages(ctx, filter)
	if err != nil {
		return nil, err
	}
	result := messages[:0]
	for _, message := range messages {
		if strings.TrimSpace(message.MediaPath) != "" {
			continue
		}
		if _, excluded := excludeSourcePKs[message.SourcePK]; excluded {
			continue
		}
		result = append(result, message)
	}
	return result, nil
}

func syncImportError(err error) error {
	if telegramdesktop.IsTelegramSessionRejected(err) {
		return commandErr(1, "telegram_session", err, telegramdesktop.TelegramSessionRejectedRemedy)
	}
	return err
}

// logSyncTimings uses one canonical sync event for the one canonical verb.
// The event name is hardcoded so log analysis and verbose output stay stable.
func (r *runtime) logSyncTimings(stats store.ImportStats, importElapsed, writeElapsed time.Duration, fetchMedia bool, chatFilter string) {
	totalElapsed := stats.FinishedAt.Sub(stats.StartedAt)
	if totalElapsed <= 0 {
		totalElapsed = importElapsed + writeElapsed
	}
	parts := []string{
		"messages=" + strconv.Itoa(stats.Messages),
		"chats=" + strconv.Itoa(stats.Chats),
		"media_messages=" + strconv.Itoa(stats.MediaMessages),
		"media_files=" + strconv.Itoa(stats.MediaFiles),
		"elapsed_ms=" + elapsedMS(totalElapsed),
	}
	if fetchMedia {
		parts = append(parts,
			"remote_media_candidates="+strconv.Itoa(stats.RemoteMediaCandidates),
			"remote_media_attempted="+strconv.Itoa(stats.RemoteMediaAttempted),
			"remote_media_downloads="+strconv.Itoa(stats.RemoteMediaDownloads),
			"remote_media_missing="+strconv.Itoa(stats.RemoteMediaMissing),
			"remote_media_unavailable="+strconv.Itoa(stats.RemoteMediaUnavailable),
			"remote_media_timeouts="+strconv.Itoa(stats.RemoteMediaTimeouts),
			"remote_media_errors="+strconv.Itoa(stats.RemoteMediaErrors),
		)
	}
	_ = r.logInfo("sync_done", strings.Join(parts, " "))
	_ = r.logDebug("sync_phase", strings.Join([]string{
		"source=" + logQuote("telegram"),
		"import_ms=" + elapsedMS(importElapsed),
		"write_ms=" + elapsedMS(writeElapsed),
		"fetch_media=" + strconv.FormatBool(fetchMedia),
		"chat_filter=" + strconv.FormatBool(strings.TrimSpace(chatFilter) != ""),
	}, " "))
}

type commandProgress struct {
	req      *trawlkit.Request
	phase    string
	progress *cklog.Progress
	done     chan struct{}
}

func (r *runtime) startCommandProgress(event, phase, firstMessage string) (*commandProgress, func()) {
	progress := &commandProgress{
		req:   r.req,
		phase: phase,
		done:  make(chan struct{}),
	}
	if r.log != nil {
		progress.progress = r.log.Progress(cklog.ProgressOptions{Event: event, Unit: "messages"})
	}
	_ = progress.Report(0, firstMessage)
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				_ = progress.Report(0, "sync running")
			case <-progress.done:
				return
			case <-r.ctx.Done():
				return
			}
		}
	}()
	return progress, func() {
		close(progress.done)
	}
}

func (p *commandProgress) Report(done int64, message string) error {
	if p == nil {
		return nil
	}
	if p.req != nil && p.req.Progress != nil {
		p.req.Progress(trawlkit.Progress{Phase: p.phase, Done: done, Message: message})
	}
	if p.progress == nil {
		return nil
	}
	return p.progress.Report(done, message)
}

func prepareImportResultForWrite(ctx context.Context, st *store.Store, result *telegramdesktop.ImportResult) error {
	if err := preserveExistingMediaRefs(ctx, st, result.Stats.SourcePath, result.Messages); err != nil {
		return err
	}
	refreshImportMediaStats(result)
	return nil
}

func storeImportResult(ctx context.Context, st *store.Store, result *telegramdesktop.ImportResult, chatFilter string) (store.SyncStats, error) {
	if strings.TrimSpace(chatFilter) != "" && len(result.Chats) == 0 {
		return store.SyncStats{}, fmt.Errorf("telegram import returned no chats for --chat %s", chatFilter)
	}
	return st.MergeObserved(ctx, result.Stats, result.Contacts, result.Chats, result.Folders, result.FolderChats, result.Topics, result.Participants, result.Messages)
}

func refreshImportMediaStats(result *telegramdesktop.ImportResult) {
	result.Stats.MediaMessages = 0
	result.Stats.MediaFiles = 0
	result.Stats.MediaBytes = 0
	mediaFiles := map[string]int64{}
	for _, message := range result.Messages {
		if strings.TrimSpace(message.MediaType) != "" {
			result.Stats.MediaMessages++
		}
		path := strings.TrimSpace(message.MediaPath)
		if path == "" {
			continue
		}
		if _, ok := mediaFiles[path]; !ok {
			mediaFiles[path] = message.MediaSize
		}
	}
	for _, size := range mediaFiles {
		result.Stats.MediaFiles++
		result.Stats.MediaBytes += size
	}
}

func existingMediaRefsForImport(ctx context.Context, st *store.Store) (string, []telegramdesktop.ExistingMediaRef, error) {
	sourcePath, refsByPK, err := existingMediaRefs(ctx, st)
	if err != nil || len(refsByPK) == 0 {
		return sourcePath, nil, err
	}
	refs := make([]telegramdesktop.ExistingMediaRef, 0, len(refsByPK))
	for _, ref := range refsByPK {
		refs = append(refs, ref)
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].SourcePK < refs[j].SourcePK })
	return sourcePath, refs, nil
}

func preserveExistingMediaRefs(ctx context.Context, st *store.Store, sourcePath string, messages []store.Message) error {
	sourcePath = strings.TrimSpace(sourcePath)
	if sourcePath == "" {
		return nil
	}
	existingSourcePath, refs, err := existingMediaRefs(ctx, st)
	if err != nil || existingSourcePath != sourcePath {
		return err
	}
	if len(refs) == 0 {
		return nil
	}
	for i := range messages {
		if strings.TrimSpace(messages[i].MediaPath) != "" {
			continue
		}
		ref, ok := refs[messages[i].SourcePK]
		if !ok {
			continue
		}
		if messages[i].MediaType == "" {
			messages[i].MediaType = ref.MediaType
		}
		if messages[i].MediaTitle == "" {
			messages[i].MediaTitle = ref.MediaTitle
		}
		messages[i].MediaPath = ref.MediaPath
		messages[i].MediaSize = ref.MediaSize
	}
	return nil
}

func existingMediaRefs(ctx context.Context, st *store.Store) (string, map[int64]telegramdesktop.ExistingMediaRef, error) {
	status, err := st.Status(ctx)
	if err != nil {
		return "", nil, err
	}
	sourcePath := strings.TrimSpace(status.LastSource)
	if sourcePath == "" {
		return "", nil, nil
	}
	existing, err := st.Messages(ctx, store.MessageFilter{HasMedia: true, Limit: int(^uint(0) >> 1)})
	if err != nil {
		return "", nil, err
	}
	refs := make(map[int64]telegramdesktop.ExistingMediaRef)
	for _, msg := range existing {
		path := strings.TrimSpace(msg.MediaPath)
		if path == "" {
			continue
		}
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			continue
		}
		refs[msg.SourcePK] = telegramdesktop.ExistingMediaRef{
			SourcePK:   msg.SourcePK,
			MediaType:  msg.MediaType,
			MediaTitle: msg.MediaTitle,
			MediaPath:  path,
			MediaSize:  msg.MediaSize,
		}
	}
	return sourcePath, refs, nil
}

func logQuote(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return strconv.Quote("")
	}
	if strings.ContainsAny(value, " \t\r\n\"") {
		return strconv.Quote(value)
	}
	return value
}

func elapsedMS(value time.Duration) string {
	return strconv.FormatInt(value.Milliseconds(), 10)
}
