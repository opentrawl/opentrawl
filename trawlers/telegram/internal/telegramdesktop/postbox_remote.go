package telegramdesktop

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	tdcrypto "github.com/gotd/td/crypto"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	querymessages "github.com/gotd/td/telegram/query/messages"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	postboxpkg "github.com/opentrawl/opentrawl/trawlers/telegram/internal/telegramdesktop/postbox"
)

const (
	telegramMacAPIID   = 9
	telegramMacAPIHash = "3975f648bb682ee889f35483bc618d1c" // gitleaks:allow
)

const PostboxSessionRejectedRemedy = "Telegram rejected the media session borrowed from Telegram for macOS (AUTH_KEY_UNREGISTERED). Open Telegram for macOS, let it finish syncing, then run trawl telegram sync --fetch-media again."

type PostboxSessionRejectedError struct {
	Err error
}

func (e PostboxSessionRejectedError) Error() string {
	return "Telegram rejected the media session borrowed from Telegram for macOS (AUTH_KEY_UNREGISTERED) after refreshing it"
}

func (e PostboxSessionRejectedError) Unwrap() error {
	return e.Err
}

type PostboxSessionRefreshError struct {
	Err error
}

func (e PostboxSessionRefreshError) Error() string {
	return "Telegram rejected the media session borrowed from Telegram for macOS (AUTH_KEY_UNREGISTERED), and refreshing it from Telegram for macOS failed"
}

func (e PostboxSessionRefreshError) Unwrap() error {
	return e.Err
}

func IsPostboxSessionRejected(err error) bool {
	var rejected PostboxSessionRejectedError
	if errors.As(err, &rejected) {
		return true
	}
	var refreshFailed PostboxSessionRefreshError
	return errors.As(err, &refreshFailed)
}

type postboxRemoteMediaDownloader func(context.Context, *postboxpkg.NativeSession, []postboxpkg.MessageRecord, []int, string, ProgressReporter) (postboxRemoteMediaStats, bool, error)

type postboxRemoteSession struct {
	source postboxpkg.Source
	native *postboxpkg.NativeSession
}

func downloadPostboxRemoteMedia(ctx context.Context, messages []postboxpkg.MessageRecord, sources []postboxpkg.Source, mediaTempDir string, downloader postboxRemoteMediaDownloader, progress ProgressReporter) (postboxRemoteMediaStats, error) {
	sharePostboxDuplicateMedia(messages)
	sharePostboxResourceMedia(messages)
	candidates := postboxRemoteMediaCandidateIndexes(messages)
	stats := postboxRemoteMediaStats{
		Candidates: len(candidates),
		Missing:    postboxRemoteMediaMissingCount(postboxRemoteMediaCandidates(messages)),
	}
	if len(candidates) == 0 || strings.TrimSpace(mediaTempDir) == "" {
		return stats, nil
	}
	sessions := postboxNativeSessions(sources)
	if len(sessions) == 0 {
		clearPostboxPlaceholderMedia(messages)
		stats.Unavailable = len(candidates)
		stats.Missing = postboxRemoteMediaMissingCount(postboxRemoteMediaCandidates(messages))
		return stats, nil
	}
	byAccount := make(map[string][]int)
	for _, idx := range candidates {
		byAccount[messages[idx].AccountID] = append(byAccount[messages[idx].AccountID], idx)
	}
	for accountID, indexes := range byAccount {
		ordered := preferredPostboxSessions(accountID, sessions)
		handled := false
		for _, remoteSession := range ordered {
			result, ok, err := downloadPostboxRemoteMediaForSessionWithRefresh(ctx, remoteSession, messages, indexes, mediaTempDir, downloader, progress)
			if IsPostboxSessionRejected(err) {
				return stats, err
			}
			if err != nil || !ok {
				continue
			}
			addPostboxRemoteMediaStats(&stats, result)
			handled = true
			break
		}
		if !handled {
			stats.Unavailable += len(indexes)
		}
		sharePostboxDuplicateMedia(messages)
		sharePostboxResourceMedia(messages)
	}
	clearPostboxPlaceholderMedia(messages)
	stats.Missing = postboxRemoteMediaMissingCount(postboxRemoteMediaCandidates(messages))
	return stats, nil
}

func postboxRemoteMediaCandidateIndexes(messages []postboxpkg.MessageRecord) []int {
	var indexes []int
	for i, msg := range messages {
		if msg.MediaPath != "" || msg.MediaType == "" {
			continue
		}
		if !postboxHasRemoteMediaIdentity(msg) || postboxCloudMediaKey(msg) == nil {
			continue
		}
		indexes = append(indexes, i)
	}
	return indexes
}

func postboxNativeSessions(sources []postboxpkg.Source) map[string]postboxRemoteSession {
	sessions := make(map[string]postboxRemoteSession)
	for _, source := range sources {
		nativeSession, err := postboxpkg.NativeSessionForSource(source)
		if err == nil && nativeSession != nil {
			sessions[nativeSession.AccountID] = postboxRemoteSession{
				source: source,
				native: nativeSession,
			}
		}
	}
	return sessions
}

func preferredPostboxSessions(accountID string, sessions map[string]postboxRemoteSession) []postboxRemoteSession {
	var ordered []postboxRemoteSession
	if preferred, ok := sessions[accountID]; ok {
		ordered = append(ordered, preferred)
	}
	keys := make([]string, 0, len(sessions))
	for id := range sessions {
		if id != accountID {
			keys = append(keys, id)
		}
	}
	sort.Strings(keys)
	for _, id := range keys {
		ordered = append(ordered, sessions[id])
	}
	return ordered
}

func downloadPostboxRemoteMediaForSessionWithRefresh(ctx context.Context, remoteSession postboxRemoteSession, messages []postboxpkg.MessageRecord, indexes []int, mediaTempDir string, downloader postboxRemoteMediaDownloader, progress ProgressReporter) (postboxRemoteMediaStats, bool, error) {
	result, ok, err := downloader(ctx, remoteSession.native, messages, indexes, mediaTempDir, progress)
	if !isPostboxAuthKeyUnregistered(err) {
		return result, ok, err
	}
	if progress != nil {
		_ = progress.Report(0, "refreshing Telegram media session")
	}
	refreshed, refreshErr := refreshPostboxRemoteSession(remoteSession.source)
	if refreshErr != nil {
		return postboxRemoteMediaStats{}, false, PostboxSessionRefreshError{Err: refreshErr}
	}
	result, ok, err = downloader(ctx, refreshed.native, messages, indexes, mediaTempDir, progress)
	if isPostboxAuthKeyUnregistered(err) {
		return postboxRemoteMediaStats{}, false, PostboxSessionRejectedError{Err: err}
	}
	return result, ok, err
}

func refreshPostboxRemoteSession(source postboxpkg.Source) (*postboxRemoteSession, error) {
	nativeSession, err := postboxpkg.NativeSessionForSource(source)
	if err != nil {
		return nil, fmt.Errorf("read Telegram for macOS media session: %w", err)
	}
	if nativeSession == nil {
		return nil, errors.New("Telegram for macOS media session was not found")
	}
	return &postboxRemoteSession{source: source, native: nativeSession}, nil
}

func downloadPostboxRemoteMediaForSession(ctx context.Context, nativeSession *postboxpkg.NativeSession, messages []postboxpkg.MessageRecord, indexes []int, mediaTempDir string, progress ProgressReporter) (postboxRemoteMediaStats, bool, error) {
	storage, err := postboxSessionStorage(ctx, nativeSession)
	if err != nil {
		return postboxRemoteMediaStats{}, false, err
	}
	client := telegram.NewClient(telegramMacAPIID, telegramMacAPIHash, telegram.Options{
		DC:             nativeSession.DCID,
		SessionStorage: storage,
		NoUpdates:      true,
		AllowCDN:       true,
		Middlewares:    []telegram.Middleware{newTelegramFloodWaitPolicy(progress)},
		Device: telegram.DeviceConfig{
			DeviceModel:    "Mac",
			SystemVersion:  "macOS",
			AppVersion:     "11.15",
			SystemLangCode: "en-US",
			LangPack:       "macos",
			LangCode:       "en",
		},
	})
	var stats postboxRemoteMediaStats
	err = client.Run(ctx, func(ctx context.Context) error {
		if _, err := client.Self(ctx); err != nil {
			return err
		}
		raw := tg.NewClient(client)
		for _, idx := range indexes {
			msg := &messages[idx]
			if postboxCloudMediaKey(*msg) == nil {
				stats.Unavailable++
				continue
			}
			stats.Attempted++
			// Reserve the retry budget so a valid flood wait cannot consume the RPC deadline.
			getCtx, cancel := context.WithTimeout(ctx, postboxRemoteMessageTimeout)
			remoteMessage, err := getPostboxRemoteMessage(getCtx, raw, *msg)
			cancel()
			if err != nil {
				if errors.Is(getCtx.Err(), context.DeadlineExceeded) {
					stats.Timeouts++
				} else {
					stats.Errors++
				}
				continue
			}
			if remoteMessage == nil {
				stats.Unavailable++
				continue
			}
			path, size, reason := downloadTelegramMessageMedia(ctx, raw, querymessages.Elem{Msg: remoteMessage}, mediaTempDir, fmt.Sprintf("%s:%d", nativeSession.AccountID, msg.SourcePK))
			if path != "" {
				msg.MediaPath = path
				msg.MediaSize = size
				stats.Downloaded++
				continue
			}
			switch reason {
			case "timeout":
				stats.Timeouts++
			case "error":
				stats.Errors++
			default:
				stats.Unavailable++
			}
		}
		return nil
	})
	if err != nil {
		return postboxRemoteMediaStats{}, false, err
	}
	return stats, true, nil
}

func isPostboxAuthKeyUnregistered(err error) bool {
	return tgerr.Is(err, "AUTH_KEY_UNREGISTERED")
}

func postboxSessionStorage(ctx context.Context, nativeSession *postboxpkg.NativeSession) (*session.StorageMemory, error) {
	if nativeSession == nil || len(nativeSession.AuthKey) != 256 {
		return nil, errors.New("missing Postbox auth key")
	}
	var key tdcrypto.Key
	copy(key[:], nativeSession.AuthKey)
	keyID := key.ID()
	data := &session.Data{
		DC:        nativeSession.DCID,
		Addr:      nativeSession.Host,
		AuthKey:   append([]byte(nil), nativeSession.AuthKey...),
		AuthKeyID: keyID[:],
		Config: session.Config{
			ThisDC: nativeSession.DCID,
			DCOptions: []tg.DCOption{{
				ID:        nativeSession.DCID,
				IPAddress: nativeSession.Host,
				Port:      nativeSession.Port,
			}},
		},
	}
	storage := &session.StorageMemory{}
	if err := (&session.Loader{Storage: storage}).Save(ctx, data); err != nil {
		return nil, err
	}
	return storage, nil
}

func getPostboxRemoteMessage(ctx context.Context, raw *tg.Client, msg postboxpkg.MessageRecord) (tg.NotEmptyMessage, error) {
	key := postboxCloudMediaKey(msg)
	if key == nil || key.MessageID <= 0 {
		return nil, errors.New("invalid Postbox cloud message id")
	}
	messageID := key.MessageID
	inputPeer := postboxInputPeer(msg)
	var firstErr error
	if channel, ok := inputPeer.(*tg.InputPeerChannel); ok {
		result, err := raw.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: &tg.InputChannel{ChannelID: channel.ChannelID, AccessHash: channel.AccessHash},
			ID:      []tg.InputMessageClass{&tg.InputMessageID{ID: messageID}},
		})
		if err == nil {
			if found, ok := postboxFindRemoteMessage(result, key.PeerID, messageID); ok {
				return found, nil
			}
		} else {
			firstErr = err
		}
	}
	result, err := raw.MessagesGetMessages(ctx, []tg.InputMessageClass{&tg.InputMessageID{ID: messageID}})
	if err == nil {
		if found, ok := postboxFindRemoteMessage(result, key.PeerID, messageID); ok {
			return found, nil
		}
	} else if firstErr == nil {
		firstErr = err
	}
	if inputPeer != nil {
		result, err = raw.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{
			Peer:     inputPeer,
			OffsetID: messageID + 1,
			Limit:    1,
		})
		if err == nil {
			if found, ok := postboxFindRemoteMessage(result, key.PeerID, messageID); ok {
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

func postboxInputPeer(msg postboxpkg.MessageRecord) tg.InputPeerClass {
	namespace, rawID, ok := postboxpkg.PostboxPeerParts(msg.RawChatID)
	if !ok {
		return nil
	}
	switch namespace {
	case 0:
		if msg.AccessHash == 0 {
			return nil
		}
		return &tg.InputPeerUser{UserID: rawID, AccessHash: msg.AccessHash}
	case 1:
		return &tg.InputPeerChat{ChatID: rawID}
	case 2:
		if msg.AccessHash == 0 {
			return nil
		}
		return &tg.InputPeerChannel{ChannelID: rawID, AccessHash: msg.AccessHash}
	default:
		return nil
	}
}

func postboxFindRemoteMessage(result tg.MessagesMessagesClass, peerID int64, messageID int) (tg.NotEmptyMessage, bool) {
	if result == nil {
		return nil, false
	}
	modified, ok := result.AsModified()
	if !ok {
		return nil, false
	}
	for _, rawMessage := range modified.GetMessages() {
		message, ok := rawMessage.AsNotEmpty()
		if !ok || message.GetID() != messageID {
			continue
		}
		if currentPeerID, ok := tdataPeerID(message.GetPeerID(), 0); ok && currentPeerID != peerID {
			continue
		}
		return message, true
	}
	return nil, false
}

func addPostboxRemoteMediaStats(left *postboxRemoteMediaStats, right postboxRemoteMediaStats) {
	left.Attempted += right.Attempted
	left.Downloaded += right.Downloaded
	left.Unavailable += right.Unavailable
	left.Timeouts += right.Timeouts
	left.Errors += right.Errors
}
