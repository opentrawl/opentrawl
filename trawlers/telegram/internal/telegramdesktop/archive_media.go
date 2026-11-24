package telegramdesktop

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/query"
	"github.com/gotd/td/telegram/query/dialogs"
	querymessages "github.com/gotd/td/telegram/query/messages"
	"github.com/gotd/td/tg"
	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	postboxpkg "github.com/opentrawl/opentrawl/trawlers/telegram/internal/telegramdesktop/postbox"
	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/telegramnetwork"
)

// ArchiveMediaUpdate is the only archive mutation produced by attachment
// backfill. Message content and metadata remain exactly as first imported.
type ArchiveMediaUpdate struct {
	SourcePK  int64
	MediaPath string
	MediaSize int64
}

// ArchiveMediaResult reports attachment paths recovered for messages already
// present in the OpenTrawl archive.
type ArchiveMediaResult struct {
	Updates []ArchiveMediaUpdate
	Stats   store.ImportStats
}

type archiveMediaSessionResult struct {
	messages   []store.Message
	attempted  int
	downloaded int
	timeouts   int
	errors     int
}

// BackfillArchiveMedia resolves current Telegram messages again before
// downloading their attachments. This refreshes expiring Telegram file
// references while keeping the existing archive message as the authority.
// A cancellation or later session error may accompany completed Updates;
// callers must persist those updates before returning the error so media_path
// remains the resume marker.
func BackfillArchiveMedia(ctx context.Context, sourcePath, dbPath string, messages []store.Message, httpClient *http.Client, progress ProgressReporter) (ArchiveMediaResult, error) {
	candidates := archiveMediaCandidates(messages)
	result := ArchiveMediaResult{Stats: store.ImportStats{
		RemoteMediaCandidates: len(candidates),
		RemoteMediaMissing:    len(candidates),
	}}
	if len(candidates) == 0 {
		return result, nil
	}
	sources, err := postboxpkg.DiscoverSources(sourcePath)
	if err != nil {
		return ArchiveMediaResult{}, err
	}
	mediaTempDir, err := os.MkdirTemp("", "telegram-archive-media-*")
	if err != nil {
		return ArchiveMediaResult{}, err
	}
	defer func() { _ = os.RemoveAll(mediaTempDir) }()

	result, err = backfillArchiveMedia(ctx, sources, len(sources) > 1, candidates, dbPath, mediaTempDir, httpClient, progress)
	return result, err
}

func backfillArchiveMedia(ctx context.Context, sources []postboxpkg.Source, multiAccount bool, candidates []store.Message, dbPath, mediaTempDir string, httpClient *http.Client, progress ProgressReporter) (ArchiveMediaResult, error) {
	result := ArchiveMediaResult{Stats: store.ImportStats{
		RemoteMediaCandidates: len(candidates),
		RemoteMediaMissing:    len(candidates),
	}}
	if len(candidates) == 0 {
		return result, nil
	}
	if err := ctx.Err(); err != nil {
		return ArchiveMediaResult{}, err
	}
	sessions := orderedPostboxHistorySessions(sources)
	if len(sessions) == 0 {
		result.Stats.RemoteMediaUnavailable = len(candidates)
		return result, nil
	}

	remaining := append([]store.Message(nil), candidates...)
	var downloaded []store.Message
	var terminalErr error
	for _, remote := range sessions {
		if err := ctx.Err(); err != nil {
			terminalErr = err
			break
		}
		sessionResult, err := downloadArchiveMediaSessionWithRefresh(ctx, remote, multiAccount, remaining, mediaTempDir, httpClient, progress)
		result.Stats.RemoteMediaAttempted += sessionResult.attempted
		result.Stats.RemoteMediaDownloads += sessionResult.downloaded
		result.Stats.RemoteMediaTimeouts += sessionResult.timeouts
		result.Stats.RemoteMediaErrors += sessionResult.errors
		downloaded = append(downloaded, sessionResult.messages...)
		remaining = archiveMediaWithoutSourcePKs(remaining, sessionResult.messages)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				terminalErr = ctxErr
				break
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				terminalErr = err
				break
			}
			reportPostboxRemoteMediaUnavailable(progress, err)
			result.Stats.RemoteMediaErrors += len(remaining)
			continue
		}
		if len(remaining) == 0 {
			break
		}
	}
	result.Stats.RemoteMediaUnavailable = len(remaining)
	if terminalErr == nil {
		terminalErr = ctx.Err()
	}

	return commitDownloadedArchiveMedia(downloaded, dbPath, result, terminalErr)
}

func commitDownloadedArchiveMedia(downloaded []store.Message, dbPath string, result ArchiveMediaResult, terminalErr error) (ArchiveMediaResult, error) {
	copyStats := store.ImportStats{}
	if err := copyImportedMedia(downloaded, mediaArchiveDir(dbPath), &copyStats); err != nil {
		return ArchiveMediaResult{}, err
	}
	for _, message := range downloaded {
		if strings.TrimSpace(message.MediaPath) == "" {
			continue
		}
		result.Updates = append(result.Updates, ArchiveMediaUpdate{
			SourcePK: message.SourcePK, MediaPath: message.MediaPath, MediaSize: message.MediaSize,
		})
	}
	result.Stats.MediaFiles = copyStats.MediaFiles
	result.Stats.MediaBytes = copyStats.MediaBytes
	result.Stats.RemoteMediaMissing = result.Stats.RemoteMediaCandidates - len(result.Updates)
	return result, terminalErr
}

// BackfillAuthenticatedArchiveMedia attaches files using an already connected client.
func BackfillAuthenticatedArchiveMedia(ctx context.Context, client *telegram.Client, dbPath string, messages []store.Message, progress ProgressReporter) (ArchiveMediaResult, error) {
	candidates := archiveMediaCandidates(messages)
	if len(candidates) == 0 {
		return ArchiveMediaResult{}, nil
	}
	mediaTempDir, err := os.MkdirTemp("", "telegram-archive-media-*")
	if err != nil {
		return ArchiveMediaResult{}, err
	}
	defer func() { _ = os.RemoveAll(mediaTempDir) }()
	downloaded, err := downloadArchiveMediaFromDialogs(ctx, client.API(), "", false, candidates, mediaTempDir, progress)
	result := ArchiveMediaResult{Stats: store.ImportStats{
		RemoteMediaCandidates:  len(candidates),
		RemoteMediaAttempted:   downloaded.attempted,
		RemoteMediaDownloads:   downloaded.downloaded,
		RemoteMediaUnavailable: len(candidates) - downloaded.downloaded,
		RemoteMediaTimeouts:    downloaded.timeouts,
		RemoteMediaErrors:      downloaded.errors,
	}}
	return commitDownloadedArchiveMedia(downloaded.messages, dbPath, result, err)
}

func downloadArchiveMediaSessionWithRefresh(ctx context.Context, remote postboxRemoteSession, multiAccount bool, candidates []store.Message, mediaTempDir string, httpClient *http.Client, progress ProgressReporter) (archiveMediaSessionResult, error) {
	result, err := downloadArchiveMediaSession(ctx, remote, multiAccount, candidates, mediaTempDir, httpClient, progress)
	if !isPostboxAuthKeyUnregistered(err) {
		return result, err
	}
	if progress != nil {
		_ = progress.Report(0, "refreshing Telegram media session")
	}
	refreshed, refreshErr := refreshPostboxRemoteSession(remote.source)
	if refreshErr != nil {
		return result, refreshErr
	}
	remaining := archiveMediaWithoutSourcePKs(append([]store.Message(nil), candidates...), result.messages)
	refreshedResult, err := downloadArchiveMediaSession(ctx, *refreshed, multiAccount, remaining, mediaTempDir, httpClient, progress)
	result.messages = append(result.messages, refreshedResult.messages...)
	result.attempted += refreshedResult.attempted
	result.downloaded += refreshedResult.downloaded
	result.timeouts += refreshedResult.timeouts
	result.errors += refreshedResult.errors
	return result, err
}

func archiveMediaCandidates(messages []store.Message) []store.Message {
	result := make([]store.Message, 0, len(messages))
	for _, message := range messages {
		if strings.TrimSpace(message.MediaType) == "" || strings.TrimSpace(message.MediaPath) != "" {
			continue
		}
		if _, ok := archiveCloudMessageID(message.MessageID); !ok {
			continue
		}
		result = append(result, message)
	}
	return result
}

func archiveCloudMessageID(value string) (int, bool) {
	parts := strings.SplitN(strings.TrimSpace(value), ":", 2)
	if len(parts) != 2 || parts[0] != "0" {
		return 0, false
	}
	id, err := strconv.Atoi(parts[1])
	return id, err == nil && id > 0
}

func archiveMediaWithoutSourcePKs(messages, resolved []store.Message) []store.Message {
	if len(resolved) == 0 {
		return messages
	}
	seen := make(map[int64]struct{}, len(resolved))
	for _, message := range resolved {
		seen[message.SourcePK] = struct{}{}
	}
	remaining := messages[:0]
	for _, message := range messages {
		if _, ok := seen[message.SourcePK]; !ok {
			remaining = append(remaining, message)
		}
	}
	return remaining
}

func downloadArchiveMediaSession(ctx context.Context, remote postboxRemoteSession, multiAccount bool, candidates []store.Message, mediaTempDir string, httpClient *http.Client, progress ProgressReporter) (archiveMediaSessionResult, error) {
	storage, err := postboxSessionStorage(ctx, remote.native)
	if err != nil {
		return archiveMediaSessionResult{}, err
	}
	client := telegram.NewClient(telegramMacAPIID, telegramMacAPIHash, telegram.Options{
		DC: remote.native.DCID, SessionStorage: storage, NoUpdates: true, AllowCDN: true,
		DialTimeout: 15 * time.Second,
		Resolver:    telegramnetwork.Resolver(httpClient),
		ReconnectionBackoff: func() backoff.BackOff {
			return postboxConnectionBackoff()
		},
		OnSelfError: func(_ context.Context, err error) error { return err },
		Middlewares: []telegram.Middleware{newTelegramFloodWaitPolicy(progress)},
		Device: telegram.DeviceConfig{
			DeviceModel: "Mac", SystemVersion: "macOS", AppVersion: "11.15",
			SystemLangCode: "en-US", LangPack: "macos", LangCode: "en",
		},
	})
	var result archiveMediaSessionResult
	err = client.Run(ctx, func(ctx context.Context) error {
		if _, err := client.Self(ctx); err != nil {
			return err
		}
		var err error
		result, err = downloadArchiveMediaFromDialogs(ctx, tg.NewClient(client), remote.native.AccountID, multiAccount, candidates, mediaTempDir, progress)
		return err
	})
	return result, err
}

func downloadArchiveMediaFromDialogs(ctx context.Context, raw *tg.Client, accountID string, multiAccount bool, candidates []store.Message, mediaTempDir string, progress ProgressReporter) (archiveMediaSessionResult, error) {
	candidatesByChat := make(map[string][]store.Message)
	for _, candidate := range candidates {
		candidatesByChat[candidate.ChatJID] = append(candidatesByChat[candidate.ChatJID], candidate)
	}
	var result archiveMediaSessionResult
	for _, folderID := range postboxHistoryFolderIDs {
		err := query.GetDialogs(raw).FolderID(folderID).BatchSize(postboxHistoryBatchSize).ForEach(ctx, func(ctx context.Context, elem dialogs.Elem) error {
			if elem.Deleted() {
				return nil
			}
			rawChatID, ok := postboxpkg.TelegramPeerToPostboxID(elem.Dialog.GetPeer())
			if !ok {
				return nil
			}
			chatID := postboxpkg.PeerStoreID(accountID, rawChatID, multiAccount)
			for _, candidate := range candidatesByChat[chatID] {
				if err := ctx.Err(); err != nil {
					return err
				}
				downloadArchiveMediaCandidate(ctx, raw, elem, accountID, candidate, mediaTempDir, progress, &result)
			}
			delete(candidatesByChat, chatID)
			return nil
		})
		if err != nil {
			return result, err
		}
	}
	return result, ctx.Err()
}

func downloadArchiveMediaCandidate(
	ctx context.Context,
	raw *tg.Client,
	elem dialogs.Elem,
	accountID string,
	candidate store.Message,
	mediaTempDir string,
	progress ProgressReporter,
	result *archiveMediaSessionResult,
) {
	messageID, ok := archiveCloudMessageID(candidate.MessageID)
	if !ok {
		return
	}
	result.attempted++
	getCtx, cancel := context.WithTimeout(ctx, postboxRemoteMessageTimeout)
	remoteMessage, getErr := getRemoteMessageForPeer(getCtx, raw, elem.Peer, elem.Dialog.GetPeer(), messageID)
	cancel()
	if getErr != nil {
		if errors.Is(getCtx.Err(), context.DeadlineExceeded) {
			result.timeouts++
		} else {
			result.errors++
		}
		return
	}
	if remoteMessage == nil {
		return
	}
	path, size, reason := downloadTelegramMessageMedia(ctx, raw, querymessages.Elem{Msg: remoteMessage}, mediaTempDir, fmt.Sprintf("%s:%d", accountID, candidate.SourcePK))
	if path == "" {
		switch reason {
		case "timeout":
			result.timeouts++
		case "error":
			result.errors++
		default:
			// The candidate remains unresolved and is counted after
			// every usable native account has been attempted.
		}
		return
	}
	candidate.MediaPath = path
	candidate.MediaSize = size
	result.messages = append(result.messages, candidate)
	result.downloaded++
	if progress != nil && result.downloaded%25 == 0 {
		_ = progress.Report(int64(result.downloaded), "downloading Telegram attachments")
	}
}

func getRemoteMessageForPeer(ctx context.Context, raw *tg.Client, inputPeer tg.InputPeerClass, expectedPeer tg.PeerClass, messageID int) (tg.NotEmptyMessage, error) {
	if messageID <= 0 {
		return nil, errors.New("invalid Telegram cloud message id")
	}
	expectedPeerID, _ := telegramPeerID(expectedPeer)
	var firstErr error
	if channel, ok := inputPeer.(*tg.InputPeerChannel); ok {
		response, err := raw.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: &tg.InputChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash},
			ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: messageID}},
		})
		if err == nil {
			if found, ok := postboxFindRemoteMessage(response, expectedPeerID, messageID); ok {
				return found, nil
			}
		} else {
			firstErr = err
		}
	}
	response, err := raw.MessagesGetMessages(ctx, []tg.InputMessageClass{&tg.InputMessageID{ID: messageID}})
	if err == nil {
		if found, ok := postboxFindRemoteMessage(response, expectedPeerID, messageID); ok {
			return found, nil
		}
	} else if firstErr == nil {
		firstErr = err
	}
	if inputPeer != nil {
		response, err = raw.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: inputPeer, OffsetID: messageID + 1, Limit: 1})
		if err == nil {
			if found, ok := postboxFindRemoteMessage(response, expectedPeerID, messageID); ok {
				return found, nil
			}
		} else if firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, nil
}

func PersistArchivedTelegramMedia(ctx context.Context, st *store.Store, result ArchiveMediaResult, backfillErr error) (int64, store.ImportStats, error) {
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

func ArchivedTelegramMediaCandidates(ctx context.Context, st *store.Store, chatID string, excludeSourcePKs map[int64]struct{}) ([]store.Message, error) {
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
