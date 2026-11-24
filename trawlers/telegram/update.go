package telegram

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/telegram/directmtproto"
	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/historystate"
	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/telegramdesktop"
	"github.com/opentrawl/opentrawl/trawlkit"
	cklog "github.com/opentrawl/opentrawl/trawlkit/log"
	update "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/update"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"google.golang.org/protobuf/proto"
)

// Update reports the message changes observed by the archive write.
func (c *Crawler) Update(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*update.TrawlerArchiveUpdateReport, error) {
	c.archiveSourcePathUsedByCurrentUpdate = ""
	r := c.handler(ctx, req)
	if c.update.FullHistory && strings.TrimSpace(c.update.LocalConversationShortReferenceAcceptedBySelectedTrawler) != "" {
		return nil, usageErr(render.OptionsCannotBeUsedTogether("full-history", "conversation"))
	}
	providerNativeConversationIdentifier, err := req.ResolveLocalConversationShortReferenceToProviderNativeConversationIdentifier(
		ctx,
		trawlkit.NewLocalTrawlerShortReference(
			c.update.LocalConversationShortReferenceAcceptedBySelectedTrawler,
		),
		store.ChatRefPrefix,
	)
	if errors.Is(err, trawlkit.ErrLocalConversationShortReferenceDoesNotIdentifyConversation) {
		return nil, usageErr(render.LinkIsNotForArchiveRecordKind(render.ArchiveRecordKindNounConversation))
	}
	if errors.Is(err, trawlkit.ErrUnknownShortRef) {
		return nil, commandErr(1, "not_found", render.NoArchiveRecordHasLink(render.ArchiveRecordKindNounConversation))
	}
	if errors.Is(err, trawlkit.ErrAmbiguousShortRef) {
		return nil, commandErr(1, "ambiguous_short_ref", render.SeveralArchiveRecordsHaveLink(render.ArchiveRecordKindNounConversation))
	}
	if err != nil {
		return nil, err
	}
	progress, stopProgress := r.startCommandProgress("update_progress", "messages", "starting update")
	defer stopProgress()
	sessionPath := telegramSessionPath(req)
	if _, err := os.Stat(sessionPath); err == nil && c.update.Path == "" {
		counts, err := directmtproto.SyncDirectMTProto(ctx, sessionPath, r.dbPath, directmtproto.SyncOptions{
			ClientOptions:  c.directClientOptions(req),
			OpenedArchive:  req.OpenedTrawlerArchiveStore,
			FetchMedia:     c.update.FetchMedia,
			ConversationID: providerNativeConversationIdentifier,
			Progress:       progress.Report,
		})
		if err != nil {
			return nil, updateImportError(err)
		}
		c.archiveSourcePathUsedByCurrentUpdate = directmtproto.ArchiveSource
		return &update.TrawlerArchiveUpdateReport{
			ArchiveRecordCountAddedByThisUpdate:   proto.Uint64(uint64(counts.Added)),
			ArchiveRecordCountUpdatedByThisUpdate: proto.Uint64(uint64(counts.Updated)),
			ArchiveRecordCountRemovedByThisUpdate: proto.Uint64(0),
		}, nil
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, updateImportError(err)
	}
	var report *update.TrawlerArchiveUpdateReport
	err = r.withStore(func(st *store.Store) error {
		historyState, err := historystate.LoadTelegramHistoryState(st.Path())
		if err != nil {
			return err
		}
		preserveCloudProjection := c.cfg.FullHistory || historyState.Started()
		var existingMediaSourcePath string
		var existingMediaRefs []telegramdesktop.ExistingMediaRef
		if c.update.FetchMedia {
			var err error
			existingMediaSourcePath, existingMediaRefs, err = existingMediaRefsForImport(r.ctx, st)
			if err != nil {
				return err
			}
		}
		importStarted := time.Now()
		result, err := telegramdesktop.Import(r.ctx, telegramdesktop.ImportOptions{
			HTTPClient:              req.PluginHTTPClient,
			Path:                    c.update.Path,
			DialogsLimit:            c.update.DialogsLimit,
			MessagesLimit:           c.update.MessagesLimit,
			ChatID:                  providerNativeConversationIdentifier,
			FetchMedia:              c.update.FetchMedia,
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
		counts, err := storeImportResult(r.ctx, st, &result, providerNativeConversationIdentifier)
		if err != nil {
			return err
		}
		if c.update.FullHistory || (c.cfg.FullHistory && strings.TrimSpace(providerNativeConversationIdentifier) == "") {
			historyCounts, err := c.updateFullTelegramHistory(r.ctx, r, st, result.Stats.SourcePath, progress)
			if err != nil {
				return err
			}
			counts.Added += historyCounts.Added
			counts.Updated += historyCounts.Updated
			counts.Removed += historyCounts.Removed
		}
		if c.update.FetchMedia {
			localSourcePKs := make(map[int64]struct{}, len(result.Messages))
			for _, message := range result.Messages {
				localSourcePKs[message.SourcePK] = struct{}{}
			}
			mediaCounts, mediaStats, err := backfillArchivedTelegramMedia(r.ctx, st, result.Stats.SourcePath, providerNativeConversationIdentifier, localSourcePKs, req.PluginHTTPClient, progress)
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
		r.logUpdateTimings(result.Stats, importElapsed, writeElapsed, c.update.FetchMedia, providerNativeConversationIdentifier)
		_ = progress.Report(int64(result.Stats.Messages), "update complete")
		report = &update.TrawlerArchiveUpdateReport{
			ArchiveRecordCountAddedByThisUpdate:   proto.Uint64(uint64(counts.Added)),
			ArchiveRecordCountUpdatedByThisUpdate: proto.Uint64(uint64(counts.Updated)),
			ArchiveRecordCountRemovedByThisUpdate: proto.Uint64(uint64(counts.Removed)),
		}
		c.archiveSourcePathUsedByCurrentUpdate = result.Stats.SourcePath
		return nil
	})
	if err != nil {
		return nil, updateImportError(err)
	}
	if report == nil {
		return &update.TrawlerArchiveUpdateReport{}, nil
	}
	return report, nil
}

func (c *Crawler) RecordSuccessfullyCompletedArchiveUpdate(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) error {
	archiveSourcePathUsedByCurrentUpdate := strings.TrimSpace(c.archiveSourcePathUsedByCurrentUpdate)
	if archiveSourcePathUsedByCurrentUpdate == "" {
		return errors.New("telegram update did not report its archive source path")
	}
	archiveStore, err := store.UseExisting(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return err
	}
	defer func() { _ = archiveStore.Close() }()
	return archiveStore.RecordSuccessfullyCompletedArchiveUpdate(ctx, archiveSourcePathUsedByCurrentUpdate, time.Now().UTC())
}

// Once full-history update has established Telegram's remote message semantics,
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
		if strings.TrimSpace(current.SenderJID) != "" || strings.TrimSpace(next.SenderJID) == "" {
			next.SenderJID = current.SenderJID
			next.SenderName = current.SenderName
			next.FromMe = current.FromMe
			next.MessageType = current.MessageType
			if strings.TrimSpace(next.MediaPath) == "" {
				next.MediaType = current.MediaType
				next.MediaTitle = current.MediaTitle
			}
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

func backfillArchivedTelegramMedia(ctx context.Context, st *store.Store, sourcePath, chatID string, excludeSourcePKs map[int64]struct{}, httpClient *http.Client, progress telegramdesktop.ProgressReporter) (int64, store.ImportStats, error) {
	messages, err := telegramdesktop.ArchivedTelegramMediaCandidates(ctx, st, chatID, excludeSourcePKs)
	if err != nil {
		return 0, store.ImportStats{}, err
	}
	result, err := telegramdesktop.BackfillArchiveMedia(ctx, sourcePath, st.Path(), messages, httpClient, progress)
	return telegramdesktop.PersistArchivedTelegramMedia(ctx, st, result, err)
}

func updateImportError(err error) error {
	if telegramdesktop.IsTelegramSessionRejected(err) {
		return commandErr(1, "telegram_session", err)
	}
	return err
}

// logUpdateTimings uses one canonical update event for the one canonical verb.
// The event name is hardcoded so log analysis and verbose output stay stable.
func (r *runtime) logUpdateTimings(stats store.ImportStats, importElapsed, writeElapsed time.Duration, fetchMedia bool, conversationFilter string) {
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
	_ = r.logInfo("update_done", strings.Join(parts, " "))
	_ = r.logDebug("update_phase", strings.Join([]string{
		"source=" + logQuote("telegram"),
		"import_ms=" + elapsedMS(importElapsed),
		"write_ms=" + elapsedMS(writeElapsed),
		"fetch_media=" + strconv.FormatBool(fetchMedia),
		"chat_filter=" + strconv.FormatBool(strings.TrimSpace(conversationFilter) != ""),
	}, " "))
}

type commandProgress struct {
	req      *trawlkit.TrawlerCommandExecutionRequest
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
				_ = progress.Report(0, "update running")
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
	if p.req != nil && p.req.ReportTrawlerCommandProgress != nil {
		p.req.ReportTrawlerCommandProgress(trawlkit.Progress{Phase: p.phase, Done: done, Message: message})
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

func storeImportResult(ctx context.Context, st *store.Store, result *telegramdesktop.ImportResult, conversationFilter string) (store.UpdateStats, error) {
	if strings.TrimSpace(conversationFilter) != "" && len(result.Chats) == 0 {
		return store.UpdateStats{}, fmt.Errorf("telegram import returned no conversations for --conversation %s", conversationFilter)
	}
	return st.MergeObserved(
		ctx,
		result.Contacts,
		result.Chats,
		result.Folders,
		result.FolderChats,
		result.Topics,
		result.Participants,
		result.Messages,
	)
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
	sourcePath := strings.TrimSpace(status.ArchiveSourcePathUsedByLastSuccessfullyCompletedUpdate)
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
