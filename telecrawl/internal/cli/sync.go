package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	cklog "github.com/openclaw/crawlkit/log"
	"github.com/openclaw/telecrawl/internal/store"
	"github.com/openclaw/telecrawl/internal/telegramdesktop"
)

func (r *runtime) runImport(command string, args []string) error {
	fs := flag.NewFlagSet("telecrawl "+command, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	path := fs.String("path", r.source, "")
	dialogsLimit := fs.Int("dialogs-limit", telegramdesktop.DefaultDialogsLimit, "")
	messagesLimit := fs.Int("messages-limit", telegramdesktop.DefaultMessagesLimit, "")
	chat := fs.String("chat", "", "")
	fetchMedia := fs.Bool("fetch-media", false, "")
	if err := fs.Parse(args); err != nil {
		return usageErr(err)
	}
	if fs.NArg() != 0 {
		return usageErr(errors.New("import takes flags only"))
	}
	progress, stopProgress := r.startCommandProgress("sync_progress", "starting sync")
	defer stopProgress()
	return r.withStore(func(st *store.Store) error {
		var existingMediaSourcePath string
		var existingMediaRefs []telegramdesktop.ExistingMediaRef
		if *fetchMedia {
			var err error
			existingMediaSourcePath, existingMediaRefs, err = existingMediaRefsForImport(r.ctx, st)
			if err != nil {
				return err
			}
		}
		importStarted := time.Now()
		result, err := telegramdesktop.Import(r.ctx, telegramdesktop.ImportOptions{
			Path:                    *path,
			DialogsLimit:            *dialogsLimit,
			MessagesLimit:           *messagesLimit,
			ChatID:                  *chat,
			FetchMedia:              *fetchMedia,
			Progress:                progress,
			ExistingMediaSourcePath: existingMediaSourcePath,
			ExistingMediaRefs:       existingMediaRefs,
		}, st.Path())
		importElapsed := time.Since(importStarted)
		if err != nil {
			return err
		}
		writeStarted := time.Now()
		if err := storeImportResult(r.ctx, st, &result, *chat); err != nil {
			return err
		}
		writeElapsed := time.Since(writeStarted)
		r.logImportTimings(command, result.Stats, importElapsed, writeElapsed, *fetchMedia, *chat)
		_ = progress.Report(int64(result.Stats.Messages), "sync complete")
		return r.print(result.Stats)
	})
}

func (r *runtime) logImportTimings(command string, stats store.ImportStats, importElapsed, writeElapsed time.Duration, fetchMedia bool, chatFilter string) {
	totalElapsed := stats.FinishedAt.Sub(stats.StartedAt)
	if totalElapsed <= 0 {
		totalElapsed = importElapsed + writeElapsed
	}
	_ = r.logInfo(command+"_done", strings.Join([]string{
		"messages=" + strconv.Itoa(stats.Messages),
		"chats=" + strconv.Itoa(stats.Chats),
		"media_messages=" + strconv.Itoa(stats.MediaMessages),
		"media_files=" + strconv.Itoa(stats.MediaFiles),
		"elapsed_ms=" + elapsedMS(totalElapsed),
	}, " "))
	_ = r.logDebug(command+"_phase", strings.Join([]string{
		"source=" + logQuote("telegram"),
		"import_ms=" + elapsedMS(importElapsed),
		"write_ms=" + elapsedMS(writeElapsed),
		"fetch_media=" + strconv.FormatBool(fetchMedia),
		"chat_filter=" + strconv.FormatBool(strings.TrimSpace(chatFilter) != ""),
	}, " "))
}

type commandProgress struct {
	progress *cklog.Progress
	done     chan struct{}
}

func (r *runtime) startCommandProgress(event, firstMessage string) (*commandProgress, func()) {
	progress := &commandProgress{
		progress: r.log.Progress(cklog.ProgressOptions{Event: event, Unit: "messages"}),
		done:     make(chan struct{}),
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
	if p == nil || p.progress == nil {
		return nil
	}
	return p.progress.Report(done, message)
}

func storeImportResult(ctx context.Context, st *store.Store, result *telegramdesktop.ImportResult, chatFilter string) error {
	if err := preserveExistingMediaRefs(ctx, st, result.Stats.SourcePath, result.Messages); err != nil {
		return err
	}
	refreshImportMediaStats(result)
	if strings.TrimSpace(chatFilter) == "" {
		if err := st.ReplaceAll(ctx, result.Stats, result.Contacts, result.Chats, result.Folders, result.FolderChats, result.Topics, result.Participants, result.Messages); err != nil {
			return err
		}
		return st.RebuildShortRefs(ctx)
	}
	if len(result.Chats) == 0 {
		return fmt.Errorf("telegram import returned no chats for --chat %s", chatFilter)
	}
	for _, chat := range result.Chats {
		partial := importResultForChat(*result, chat.JID)
		if err := st.UpsertChat(ctx, partial.Stats, chat.JID, partial.Contacts, partial.Chats, partial.Folders, partial.FolderChats, partial.Topics, partial.Participants, partial.Messages); err != nil {
			return err
		}
	}
	return st.RebuildShortRefs(ctx)
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

func importResultForChat(result telegramdesktop.ImportResult, chatJID string) telegramdesktop.ImportResult {
	out := telegramdesktop.ImportResult{Stats: result.Stats, Folders: result.Folders}
	for _, chat := range result.Chats {
		if chat.JID == chatJID {
			out.Chats = append(out.Chats, chat)
		}
	}
	for _, folderChat := range result.FolderChats {
		if folderChat.ChatJID == chatJID {
			out.FolderChats = append(out.FolderChats, folderChat)
		}
	}
	for _, topic := range result.Topics {
		if topic.ChatJID == chatJID {
			out.Topics = append(out.Topics, topic)
		}
	}
	for _, participant := range result.Participants {
		if participant.GroupJID == chatJID {
			out.Participants = append(out.Participants, participant)
		}
	}
	for _, message := range result.Messages {
		if message.ChatJID == chatJID {
			out.Messages = append(out.Messages, message)
		}
	}
	out.Contacts = contactsForMessages(result.Contacts, out.Messages, out.Participants, chatJID)
	return out
}

func contactsForMessages(contacts []store.Contact, messages []store.Message, participants []store.GroupParticipant, chatJID string) []store.Contact {
	peerIDs := map[string]struct{}{}
	if strings.TrimSpace(chatJID) != "" {
		peerIDs[chatJID] = struct{}{}
	}
	for _, message := range messages {
		if strings.TrimSpace(message.ChatJID) != "" {
			peerIDs[message.ChatJID] = struct{}{}
		}
		if strings.TrimSpace(message.SenderJID) != "" {
			peerIDs[message.SenderJID] = struct{}{}
		}
	}
	for _, participant := range participants {
		if strings.TrimSpace(participant.UserJID) != "" {
			peerIDs[participant.UserJID] = struct{}{}
		}
	}
	out := make([]store.Contact, 0, len(peerIDs))
	for _, contact := range contacts {
		if _, ok := peerIDs[contact.JID]; ok {
			out = append(out, contact)
		}
	}
	return out
}
