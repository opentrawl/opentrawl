package beeper

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/plugins/trawl-beeper/internal/beeperapi"
	"github.com/opentrawl/opentrawl/plugins/trawl-beeper/internal/beeperstore"
	"github.com/opentrawl/opentrawl/trawlkit"
	updatecontract "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/update"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

const (
	defaultMaxChatsPages        = 200
	defaultMaxMessagePagesGuard = 2000
	beeperChatsPageSize         = 50
)

type beeperUpdateFlagValues struct {
	tokenFile       string
	includeNetworks string
	excludeNetworks string
	fullBackfill    bool
	maxPagesPerChat int
}

func (t *BeeperTrawler) bindUpdateFlags(fs *flag.FlagSet) {
	t.updateFlags = beeperUpdateFlagValues{}
	fs.StringVar(&t.updateFlags.tokenFile, "file", "", "Read the Beeper Desktop read-only Bearer token from a local file")
	fs.StringVar(&t.updateFlags.includeNetworks, "include-network", "", "Comma-separated Beeper networks to archive for this update")
	fs.StringVar(&t.updateFlags.excludeNetworks, "exclude-network", "", "Comma-separated Beeper networks to skip for this update")
	fs.BoolVar(&t.updateFlags.fullBackfill, "full", false, "Re-walk older message pages even for chats with completed backfill")
	fs.IntVar(&t.updateFlags.maxPagesPerChat, "max-pages-per-chat", 0, "Maximum message pages to fetch per conversation during this update")
}

// Update fetches accounts, conversations, participants, and paginated message
// history from Beeper Desktop's local read-only API into beeper.db.
func (t *BeeperTrawler) Update(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest) (*updatecontract.TrawlerArchiveUpdateReport, error) {
	if t.updateFlags.maxPagesPerChat < 0 {
		return nil, usageFailure(render.OptionMustNotBeNegative("max-pages-per-chat"))
	}

	token, err := t.resolveBeeperAccessToken(req, t.updateFlags.tokenFile)
	if err != nil {
		return nil, err
	}

	archiveStore, err := beeperstore.Use(ctx, req.OpenedTrawlerArchiveStore, req.TrawlerArchivePaths.TrawlerArchivePath)
	if err != nil {
		return nil, archiveFailure(err)
	}

	client, err := beeperapi.NewBeeperDesktopClient(
		t.cfg.EffectiveAPIBaseURL(),
		token,
		req.PluginHTTPClient,
		t.cfg.EffectiveRequestTimeout(),
	)
	if err != nil {
		return nil, usageFailure(err)
	}

	reportProgress := func(done int, total int, message string) {
		if req.ReportTrawlerCommandProgress != nil {
			req.ReportTrawlerCommandProgress(trawlkit.Progress{
				Phase:   "update",
				Done:    int64(done),
				Total:   int64(total),
				Message: message,
			})
		}
	}

	startedAt := time.Now().UTC()
	reportProgress(0, 3, "connecting to Beeper Desktop")

	accounts, err := client.ListAccounts(ctx)
	if err != nil {
		_ = archiveStore.RecordSyncAttemptError(ctx, time.Now().UTC(), err.Error())
		return nil, classifyBeeperAPIError(err)
	}

	accountRecords, accountsByID := convertBeeperAccounts(accounts, startedAt)
	if err := archiveStore.UpsertAccounts(ctx, accountRecords); err != nil {
		return nil, err
	}

	reportProgress(1, 3, "listing Beeper conversations")
	allChats, err := fetchAllBeeperChats(ctx, client)
	if err != nil {
		_ = archiveStore.RecordSyncAttemptError(ctx, time.Now().UTC(), err.Error())
		return nil, classifyBeeperAPIError(err)
	}

	networkFilter := BuildBeeperNetworkFilter(t.cfg, t.updateFlags.includeNetworks, t.updateFlags.excludeNetworks)
	selectedChats := make([]beeperapi.BeeperChat, 0, len(allChats))
	for _, chat := range allChats {
		if strings.TrimSpace(chat.ID) == "" || chat.IsMergedContainer() {
			continue
		}
		network := resolveChatNetworkName(chat, accountsByID)
		if !networkFilter.Allows(network) {
			continue
		}
		selectedChats = append(selectedChats, chat)
	}

	maxPages := t.cfg.MaxMessagePagesPerChat
	if t.updateFlags.maxPagesPerChat > 0 {
		maxPages = t.updateFlags.maxPagesPerChat
	}
	if maxPages <= 0 {
		maxPages = defaultMaxMessagePagesGuard
	}

	totalInserted := 0
	totalUpdated := 0
	chatsSynced := 0
	chatsSkippedUnchanged := 0

	for idx, chat := range selectedChats {
		network := resolveChatNetworkName(chat, accountsByID)
		archivedChat, archivedParticipants, participantNames := convertBeeperChatAndParticipants(chat, network, startedAt)
		if err := archiveStore.UpsertChatAndParticipants(ctx, archivedChat, archivedParticipants); err != nil {
			return nil, err
		}

		reportProgress(
			idx+1,
			max(len(selectedChats), 1),
			fmt.Sprintf("syncing %s conversation %d/%d", firstNonEmpty(network, "Beeper"), idx+1, len(selectedChats)),
		)

		inserted, updated, skipped, syncErr := syncBeeperChatMessages(
			ctx,
			client,
			archiveStore,
			chat,
			archivedChat,
			participantNames,
			t.updateFlags.fullBackfill,
			maxPages,
		)
		if syncErr != nil {
			_ = archiveStore.RecordSyncAttemptError(ctx, time.Now().UTC(), syncErr.Error())
			return nil, classifyBeeperAPIError(syncErr)
		}
		totalInserted += inserted
		totalUpdated += updated
		if skipped {
			chatsSkippedUnchanged++
		} else {
			chatsSynced++
		}
	}

	if totalInserted > 0 || totalUpdated > 0 {
		if err := archiveStore.OptimizeSearchIndex(ctx); err != nil {
			return nil, err
		}
	}
	if err := archiveStore.RecordUpdateCompleted(ctx, time.Now().UTC()); err != nil {
		return nil, err
	}

	if req.TrawlerCommandLog != nil {
		_ = req.TrawlerCommandLog.Info(
			"beeper_update_done",
			fmt.Sprintf(
				"accounts=%d chats_seen=%d chats_selected=%d chats_synced=%d chats_unchanged=%d messages_inserted=%d messages_updated=%d elapsed_ms=%d",
				len(accountRecords),
				len(allChats),
				len(selectedChats),
				chatsSynced,
				chatsSkippedUnchanged,
				totalInserted,
				totalUpdated,
				time.Since(startedAt).Milliseconds(),
			),
		)
	}
	return &updatecontract.TrawlerArchiveUpdateReport{}, nil
}

func fetchAllBeeperChats(ctx context.Context, client *beeperapi.BeeperDesktopClient) ([]beeperapi.BeeperChat, error) {
	var all []beeperapi.BeeperChat
	seenChatIDs := make(map[string]struct{})
	seenCursors := make(map[string]struct{})
	cursor := ""

	for pageNum := 0; pageNum < defaultMaxChatsPages; pageNum++ {
		page, err := client.ListChatsPage(ctx, cursor, beeperChatsPageSize)
		if err != nil {
			return nil, err
		}
		added := 0
		for _, chat := range page.Items {
			id := strings.TrimSpace(chat.ID)
			if id == "" {
				continue
			}
			if _, dup := seenChatIDs[id]; dup {
				continue
			}
			seenChatIDs[id] = struct{}{}
			all = append(all, chat)
			added++
		}
		nextCursor := strings.TrimSpace(page.OldestCursor)
		if !page.HasMore || added == 0 || nextCursor == "" {
			break
		}
		if _, loop := seenCursors[nextCursor]; loop {
			break
		}
		seenCursors[nextCursor] = struct{}{}
		cursor = nextCursor
	}
	return all, nil
}

func syncBeeperChatMessages(
	ctx context.Context,
	client *beeperapi.BeeperDesktopClient,
	archiveStore *beeperstore.BeeperArchiveStore,
	apiChat beeperapi.BeeperChat,
	archivedChat beeperstore.BeeperArchivedChat,
	participantNames map[string]string,
	fullBackfill bool,
	maxPages int,
) (inserted int, updated int, skipped bool, err error) {
	syncState, hasState, err := archiveStore.ReadChatSyncState(ctx, archivedChat.ChatID)
	if err != nil {
		return 0, 0, false, err
	}
	if !hasState {
		syncState = beeperstore.BeeperChatSyncState{ChatID: archivedChat.ChatID}
	}

	if !fullBackfill && canSkipUnchangedBeeperChat(ctx, archiveStore, apiChat, archivedChat, syncState) {
		return 0, 0, true, nil
	}

	pagesFetched := 0
	seenCursors := make(map[string]struct{})
	newestSortKey := syncState.NewestSortKey
	newestMessageID := syncState.NewestMessageID
	oldestCursor := syncState.OldestCursor
	backfillComplete := syncState.BackfillComplete
	if fullBackfill {
		oldestCursor = ""
		backfillComplete = false
	}

	// Phase 1: Pull from the newest page backwards until we reach messages we
	// already archived (incremental high-water mark), or until the chat ends.
	cursor := ""
	reachedKnownHistory := false
	for pagesFetched < maxPages {
		page, fetchErr := client.ListChatMessagesPage(ctx, archivedChat.ChatID, cursor, "before")
		if fetchErr != nil {
			return inserted, updated, false, fetchErr
		}
		pagesFetched++

		converted := make([]beeperstore.BeeperArchivedMessage, 0, len(page.Items))
		sawKnownAtOrBelowHWM := false
		for _, apiMsg := range page.Items {
			msg := convertBeeperMessage(apiMsg, archivedChat, participantNames)
			if msg.MessageID == "" {
				continue
			}
			if newestSortKey == "" || msg.SortKey > newestSortKey {
				if msg.SortKey != "" {
					newestSortKey = msg.SortKey
				}
				newestMessageID = msg.MessageID
			}
			if hasState && !fullBackfill && (syncState.NewestSortKey == "" || msg.SortKey == "" || msg.SortKey <= syncState.NewestSortKey) {
				exists, existsErr := archiveStore.MessageExists(ctx, msg.MessageID)
				if existsErr != nil {
					return inserted, updated, false, existsErr
				}
				if exists {
					sawKnownAtOrBelowHWM = true
				}
			}
			converted = append(converted, msg)
		}

		nextCursor := strings.TrimSpace(page.OldestCursor)
		if !hasState || fullBackfill {
			oldestCursor = nextCursor
		}

		reachedEnd := !page.HasMore || len(page.Items) == 0 || nextCursor == ""
		if reachedEnd && (!hasState || fullBackfill || backfillComplete) {
			backfillComplete = true
			oldestCursor = ""
		}

		nextState := beeperstore.BeeperChatSyncState{
			ChatID:             archivedChat.ChatID,
			NewestSortKey:      newestSortKey,
			NewestMessageID:    newestMessageID,
			OldestCursor:       oldestCursor,
			BackfillComplete:   backfillComplete,
			LastSyncedActivity: archivedChat.LastActivity,
			LastSyncedAt:       time.Now().UTC(),
		}
		ins, upd, writeErr := archiveStore.UpsertMessagesAndSyncState(ctx, converted, archivedChat.Title, &nextState)
		if writeErr != nil {
			return inserted, updated, false, writeErr
		}
		inserted += ins
		updated += upd

		if reachedEnd {
			break
		}
		if hasState && !fullBackfill && sawKnownAtOrBelowHWM {
			reachedKnownHistory = true
			break
		}
		if _, loop := seenCursors[nextCursor]; loop {
			break
		}
		seenCursors[nextCursor] = struct{}{}
		cursor = nextCursor
	}

	// Phase 2: If historical backfill was incomplete from an earlier interrupted
	// run and Phase 1 stopped at known messages, continue backfilling older pages
	// from the saved oldest_cursor.
	if reachedKnownHistory && !backfillComplete && oldestCursor != "" && pagesFetched < maxPages {
		resumeCursor := oldestCursor
		for pagesFetched < maxPages && resumeCursor != "" {
			if _, loop := seenCursors[resumeCursor]; loop {
				break
			}
			seenCursors[resumeCursor] = struct{}{}

			page, fetchErr := client.ListChatMessagesPage(ctx, archivedChat.ChatID, resumeCursor, "before")
			if fetchErr != nil {
				return inserted, updated, false, fetchErr
			}
			pagesFetched++

			converted := make([]beeperstore.BeeperArchivedMessage, 0, len(page.Items))
			for _, apiMsg := range page.Items {
				msg := convertBeeperMessage(apiMsg, archivedChat, participantNames)
				if msg.MessageID != "" {
					converted = append(converted, msg)
				}
			}

			nextCursor := strings.TrimSpace(page.OldestCursor)
			reachedEnd := !page.HasMore || len(page.Items) == 0 || nextCursor == ""
			if reachedEnd {
				backfillComplete = true
				oldestCursor = ""
			} else {
				oldestCursor = nextCursor
			}

			nextState := beeperstore.BeeperChatSyncState{
				ChatID:             archivedChat.ChatID,
				NewestSortKey:      newestSortKey,
				NewestMessageID:    newestMessageID,
				OldestCursor:       oldestCursor,
				BackfillComplete:   backfillComplete,
				LastSyncedActivity: archivedChat.LastActivity,
				LastSyncedAt:       time.Now().UTC(),
			}
			ins, upd, writeErr := archiveStore.UpsertMessagesAndSyncState(ctx, converted, archivedChat.Title, &nextState)
			if writeErr != nil {
				return inserted, updated, false, writeErr
			}
			inserted += ins
			updated += upd

			if reachedEnd {
				break
			}
			resumeCursor = nextCursor
		}
	}

	return inserted, updated, false, nil
}

func canSkipUnchangedBeeperChat(
	ctx context.Context,
	archiveStore *beeperstore.BeeperArchiveStore,
	apiChat beeperapi.BeeperChat,
	archivedChat beeperstore.BeeperArchivedChat,
	syncState beeperstore.BeeperChatSyncState,
) bool {
	if !syncState.BackfillComplete {
		return false
	}
	if apiChat.Preview != nil && strings.TrimSpace(apiChat.Preview.ID) != "" {
		exists, err := archiveStore.MessageExists(ctx, apiChat.Preview.ID)
		if err != nil || !exists {
			return false
		}
		return !archivedChat.LastActivity.After(syncState.LastSyncedActivity)
	}
	if archivedChat.LastActivity.IsZero() || syncState.LastSyncedActivity.IsZero() {
		return false
	}
	return !archivedChat.LastActivity.After(syncState.LastSyncedActivity)
}

func convertBeeperAccounts(accounts []beeperapi.BeeperAccount, now time.Time) ([]beeperstore.BeeperAccountRecord, map[string]beeperstore.BeeperAccountRecord) {
	records := make([]beeperstore.BeeperAccountRecord, 0, len(accounts))
	byID := make(map[string]beeperstore.BeeperAccountRecord, len(accounts))
	for _, acc := range accounts {
		id := strings.TrimSpace(acc.AccountID)
		if id == "" {
			continue
		}
		record := beeperstore.BeeperAccountRecord{
			AccountID:    id,
			Network:      resolveAccountNetworkName(acc),
			Status:       strings.TrimSpace(acc.Status),
			StatusText:   strings.TrimSpace(acc.StatusText),
			LoginID:      strings.TrimSpace(acc.LoginID),
			SelfUserID:   strings.TrimSpace(acc.User.ID),
			SelfFullName: strings.TrimSpace(acc.User.FullName),
			SelfUsername: strings.TrimSpace(acc.User.Username),
			UpdatedAt:    now,
		}
		records = append(records, record)
		byID[id] = record
	}
	return records, byID
}

func resolveChatNetworkName(chat beeperapi.BeeperChat, accountsByID map[string]beeperstore.BeeperAccountRecord) string {
	if direct := strings.TrimSpace(chat.Network); direct != "" {
		return direct
	}
	if acc, ok := accountsByID[strings.TrimSpace(chat.AccountID)]; ok && acc.Network != "" {
		return acc.Network
	}
	return strings.TrimSpace(chat.AccountID)
}

func convertBeeperChatAndParticipants(
	chat beeperapi.BeeperChat,
	network string,
	now time.Time,
) (beeperstore.BeeperArchivedChat, []beeperstore.BeeperArchivedParticipant, map[string]string) {
	participantNames := make(map[string]string, len(chat.Participants.Items))
	participants := make([]beeperstore.BeeperArchivedParticipant, 0, len(chat.Participants.Items))
	fallbackTitle := ""
	for _, item := range chat.Participants.Items {
		userID := strings.TrimSpace(item.ID)
		fullName := strings.TrimSpace(item.FullName)
		username := strings.TrimSpace(item.Username)
		phone := strings.TrimSpace(item.PhoneNumber)
		email := strings.TrimSpace(item.Email)
		display := firstNonEmpty(fullName, username, phone, email, userID)
		if userID != "" && display != "" {
			participantNames[userID] = display
		}
		if !item.IsSelf && fallbackTitle == "" && display != "" {
			fallbackTitle = display
		}
		if userID == "" {
			continue
		}
		participants = append(participants, beeperstore.BeeperArchivedParticipant{
			ChatID:       strings.TrimSpace(chat.ID),
			UserID:       userID,
			FullName:     fullName,
			Username:     username,
			PhoneNumber:  phone,
			Email:        email,
			IsSelf:       item.IsSelf,
			IsAdmin:      item.IsAdmin,
			IsPending:    item.IsPending,
			IsNetworkBot: item.IsNetworkBot,
		})
	}

	title := strings.TrimSpace(chat.Title)
	if title == "" {
		title = fallbackTitle
	}
	chatType := strings.ToLower(strings.TrimSpace(chat.Type))
	if chatType == "" {
		chatType = "single"
	}
	lastActivity := beeperapi.ParseBeeperTimestamp(chat.LastActivity)
	if lastActivity.IsZero() && chat.Preview != nil {
		lastActivity = beeperapi.ParseBeeperTimestamp(chat.Preview.Timestamp)
	}

	archived := beeperstore.BeeperArchivedChat{
		ChatID:        strings.TrimSpace(chat.ID),
		LocalChatID:   strings.TrimSpace(chat.LocalChatID),
		AccountID:     strings.TrimSpace(chat.AccountID),
		Network:       network,
		Title:         title,
		ChatType:      chatType,
		Description:   strings.TrimSpace(chat.Description),
		IsArchived:    chat.IsArchived,
		IsMuted:       chat.IsMuted,
		IsPinned:      chat.IsPinned,
		IsReadOnly:    chat.IsReadOnly,
		IsLowPriority: chat.IsLowPriority,
		UnreadCount:   int(max(chat.UnreadCount, 0)),
		LastActivity:  lastActivity,
		UpdatedAt:     now,
	}
	return archived, participants, participantNames
}

func convertBeeperMessage(
	apiMsg beeperapi.BeeperMessage,
	chat beeperstore.BeeperArchivedChat,
	participantNames map[string]string,
) beeperstore.BeeperArchivedMessage {
	senderID := strings.TrimSpace(apiMsg.SenderID)
	senderName := strings.TrimSpace(apiMsg.SenderName)
	if senderName == "" && senderID != "" {
		senderName = participantNames[senderID]
	}

	mediaKind, mediaTitle, mediaMime, mediaURL, mediaSize, transcript := summarizeBeeperAttachments(apiMsg.Attachments)
	linksSummary := summarizeBeeperLinks(apiMsg.Links)
	reactionsSummary := summarizeBeeperReactions(apiMsg.Reactions)

	return beeperstore.BeeperArchivedMessage{
		MessageID:        strings.TrimSpace(apiMsg.ID),
		ChatID:           firstNonEmpty(apiMsg.ChatID, chat.ChatID),
		AccountID:        firstNonEmpty(apiMsg.AccountID, chat.AccountID),
		Network:          chat.Network,
		ChatTitle:        chat.Title,
		ChatType:         chat.ChatType,
		SortKey:          strings.TrimSpace(apiMsg.SortKey),
		SenderID:         senderID,
		SenderName:       senderName,
		IsSender:         apiMsg.IsSender,
		Timestamp:        beeperapi.ParseBeeperTimestamp(apiMsg.Timestamp),
		EditedTimestamp:  beeperapi.ParseBeeperTimestamp(apiMsg.EditedTimestamp),
		MessageType:      strings.TrimSpace(apiMsg.Type),
		Text:             apiMsg.Text,
		LinkedMessageID:  strings.TrimSpace(apiMsg.LinkedMessageID),
		IsDeleted:        apiMsg.IsDeleted,
		IsHidden:         apiMsg.IsHidden,
		MediaKind:        mediaKind,
		MediaTitle:       mediaTitle,
		MediaMimeType:    mediaMime,
		MediaURL:         mediaURL,
		MediaSizeBytes:   mediaSize,
		MediaTranscript:  transcript,
		LinksSummary:     linksSummary,
		ReactionsSummary: reactionsSummary,
	}
}

func summarizeBeeperAttachments(attachments []beeperapi.BeeperAttachment) (kind string, title string, mimeType string, srcURL string, sizeBytes int64, transcript string) {
	if len(attachments) == 0 {
		return "", "", "", "", 0, ""
	}
	first := attachments[0]
	kind = classifyBeeperAttachmentKind(first)
	title = strings.TrimSpace(first.FileName)
	mimeType = strings.TrimSpace(first.MimeType)
	srcURL = strings.TrimSpace(first.SrcURL)
	if first.FileSize > 0 {
		sizeBytes = int64(first.FileSize)
	}
	var transcripts []string
	for _, att := range attachments {
		if text := strings.TrimSpace(att.Transcription.Transcription); text != "" {
			transcripts = append(transcripts, text)
		}
	}
	transcript = strings.Join(transcripts, "\n")
	return kind, title, mimeType, srcURL, sizeBytes, transcript
}

func classifyBeeperAttachmentKind(att beeperapi.BeeperAttachment) string {
	switch {
	case att.IsVoiceNote:
		return "voice"
	case att.IsGif:
		return "gif"
	case att.IsSticker:
		return "sticker"
	case strings.EqualFold(att.Type, "img"):
		return "image"
	case strings.EqualFold(att.Type, "video"):
		return "video"
	case strings.EqualFold(att.Type, "audio"):
		return "audio"
	case strings.TrimSpace(att.FileName) != "" || strings.TrimSpace(att.MimeType) != "":
		return "file"
	default:
		return "attachment"
	}
}

func summarizeBeeperLinks(links []beeperapi.BeeperLinkPreview) string {
	if len(links) == 0 {
		return ""
	}
	var parts []string
	for _, link := range links {
		if label := firstNonEmpty(link.Title, link.URL); label != "" {
			parts = append(parts, label)
		}
	}
	return strings.Join(parts, " · ")
}

func summarizeBeeperReactions(reactions []beeperapi.BeeperReaction) string {
	if len(reactions) == 0 {
		return ""
	}
	var parts []string
	for _, reaction := range reactions {
		if key := strings.TrimSpace(reaction.ReactionKey); key != "" {
			parts = append(parts, key)
		}
	}
	return strings.Join(parts, " ")
}

func classifyBeeperAPIError(err error) error {
	var authErr *beeperapi.BeeperAPIAuthError
	if errors.As(err, &authErr) {
		return render.AccountProviderNoLongerAcceptsConnection(beeperProviderDisplayName, beeperTrawlerDisplayName)
	}
	var unreachableErr *beeperapi.BeeperAPIUnreachableError
	if errors.As(err, &unreachableErr) {
		return render.CommandFailureFromTrawler(
			render.CommandFailureKindArchiveOrOperationFailed,
			"Beeper Desktop did not respond on 127.0.0.1:23373; open or wake Beeper Desktop and retry, while the existing local archive remains searchable",
		)
	}
	return err
}
