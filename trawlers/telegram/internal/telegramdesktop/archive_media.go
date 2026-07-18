package telegramdesktop

import (
	"context"
	"errors"
	"fmt"
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

type archiveMediaSessionDownloader func(context.Context, postboxRemoteSession, bool, []store.Message, string, ProgressReporter) (archiveMediaSessionResult, error)

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
func BackfillArchiveMedia(ctx context.Context, sourcePath, dbPath string, messages []store.Message, progress ProgressReporter) (ArchiveMediaResult, error) {
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
	mediaTempDir, err := os.MkdirTemp("", "telecrawl-archive-media-*")
	if err != nil {
		return ArchiveMediaResult{}, err
	}
	defer func() { _ = os.RemoveAll(mediaTempDir) }()

	result, err = backfillArchiveMedia(ctx, sources, len(sources) > 1, candidates, dbPath, mediaTempDir, progress, downloadArchiveMediaSession)
	return result, err
}

func backfillArchiveMedia(ctx context.Context, sources []postboxpkg.Source, multiAccount bool, candidates []store.Message, dbPath, mediaTempDir string, progress ProgressReporter, download archiveMediaSessionDownloader) (ArchiveMediaResult, error) {
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
		sessionResult, err := downloadArchiveMediaSessionWithRefresh(ctx, remote, multiAccount, remaining, mediaTempDir, progress, download)
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
	result.Stats.RemoteMediaMissing = len(candidates) - len(result.Updates)
	return result, terminalErr
}

func downloadArchiveMediaSessionWithRefresh(ctx context.Context, remote postboxRemoteSession, multiAccount bool, candidates []store.Message, mediaTempDir string, progress ProgressReporter, download archiveMediaSessionDownloader) (archiveMediaSessionResult, error) {
	result, err := download(ctx, remote, multiAccount, candidates, mediaTempDir, progress)
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
	refreshedResult, err := download(ctx, *refreshed, multiAccount, remaining, mediaTempDir, progress)
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

func downloadArchiveMediaSession(ctx context.Context, remote postboxRemoteSession, multiAccount bool, candidates []store.Message, mediaTempDir string, progress ProgressReporter) (archiveMediaSessionResult, error) {
	storage, err := postboxSessionStorage(ctx, remote.native)
	if err != nil {
		return archiveMediaSessionResult{}, err
	}
	client := telegram.NewClient(telegramMacAPIID, telegramMacAPIHash, telegram.Options{
		DC: remote.native.DCID, SessionStorage: storage, NoUpdates: true, AllowCDN: true,
		DialTimeout: 15 * time.Second,
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
		raw := tg.NewClient(client)
		for _, folderID := range postboxHistoryFolderIDs {
			err := query.GetDialogs(raw).FolderID(folderID).BatchSize(postboxHistoryBatchSize).ForEach(ctx, func(ctx context.Context, elem dialogs.Elem) error {
				if err := ctx.Err(); err != nil {
					return err
				}
				if elem.Deleted() {
					return nil
				}
				rawChatID, ok := postboxpkg.TelegramPeerToPostboxID(elem.Dialog.GetPeer())
				if !ok {
					return nil
				}
				for _, candidate := range archiveMediaCandidatesForDialog(candidates, remote.native.AccountID, rawChatID, multiAccount) {
					messageID, ok := archiveCloudMessageID(candidate.MessageID)
					if !ok {
						continue
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
						continue
					}
					if remoteMessage == nil {
						continue
					}
					path, size, reason := downloadTelegramMessageMedia(ctx, raw, querymessages.Elem{Msg: remoteMessage}, mediaTempDir, fmt.Sprintf("%s:%d", remote.native.AccountID, candidate.SourcePK))
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
						continue
					}
					candidate.MediaPath = path
					candidate.MediaSize = size
					result.messages = append(result.messages, candidate)
					result.downloaded++
					if progress != nil && result.downloaded%25 == 0 {
						_ = progress.Report(int64(result.downloaded), "downloading Telegram attachments")
					}
				}
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return result, err
	}
	return result, nil
}

func archiveMediaCandidatesForDialog(candidates []store.Message, accountID string, rawChatID int64, multiAccount bool) []store.Message {
	chatID := postboxpkg.PeerStoreID(accountID, rawChatID, multiAccount)
	var result []store.Message
	for _, candidate := range candidates {
		if candidate.ChatJID == chatID {
			result = append(result, candidate)
		}
	}
	return result
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
