package telegramdesktop

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tdcrypto "github.com/gotd/td/crypto"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/downloader"
	querymessages "github.com/gotd/td/telegram/query/messages"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"
	postboxpkg "github.com/opentrawl/opentrawl/trawlers/telegram/internal/telegramdesktop/postbox"
)

const (
	telegramMacAPIID   = 9
	telegramMacAPIHash = "3975f648bb682ee889f35483bc618d1c" // gitleaks:allow
)

type postboxRemoteMediaDownloader func(context.Context, *postboxpkg.NativeSession, []postboxpkg.MessageRecord, []int, string, ProgressReporter) (postboxRemoteMediaStats, bool, error)

type postboxRemoteSession struct {
	source postboxpkg.Source
	native *postboxpkg.NativeSession
}

func downloadPostboxRemoteMedia(ctx context.Context, messages []postboxpkg.MessageRecord, sources []postboxpkg.Source, mediaTempDir string, downloader postboxRemoteMediaDownloader, progress ProgressReporter) postboxRemoteMediaStats {
	sharePostboxDuplicateMedia(messages)
	sharePostboxResourceMedia(messages)
	candidates := postboxRemoteMediaCandidateIndexes(messages)
	stats := postboxRemoteMediaStats{
		Candidates: len(candidates),
		Missing:    postboxRemoteMediaMissingCount(postboxRemoteMediaCandidates(messages)),
	}
	if len(candidates) == 0 || strings.TrimSpace(mediaTempDir) == "" {
		return stats
	}
	sessions := postboxNativeSessions(sources)
	if len(sessions) == 0 {
		clearPostboxPlaceholderMedia(messages)
		stats.Unavailable = len(candidates)
		stats.Missing = postboxRemoteMediaMissingCount(postboxRemoteMediaCandidates(messages))
		return stats
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
			if err != nil || !ok {
				reportPostboxRemoteMediaUnavailable(progress, err)
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
	return stats
}

func reportPostboxRemoteMediaUnavailable(progress ProgressReporter, err error) {
	if progress == nil || err == nil {
		return
	}
	message := "Telegram cloud media is unavailable; local messages will still sync"
	if isPostboxAuthKeyUnregistered(err) {
		message = "Telegram rejected the media session (AUTH_KEY_UNREGISTERED); local messages will still sync"
	}
	_ = progress.Report(0, message)
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
		return postboxRemoteMediaStats{}, false, refreshErr
	}
	result, ok, err = downloader(ctx, refreshed.native, messages, indexes, mediaTempDir, progress)
	return result, ok, err
}

func refreshPostboxRemoteSession(source postboxpkg.Source) (*postboxRemoteSession, error) {
	nativeSession, err := postboxpkg.NativeSessionForSource(source)
	if err != nil {
		return nil, fmt.Errorf("read Telegram for macOS media session: %w", err)
	}
	if nativeSession == nil {
		return nil, errors.New("media session from Telegram for macOS was not found")
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
	namespace, rawID, ok := postboxpkg.PostboxPeerParts(msg.RawChatID)
	if !ok {
		return nil, errors.New("invalid Postbox peer id")
	}
	var expected tg.PeerClass
	switch namespace {
	case 0:
		expected = &tg.PeerUser{UserID: rawID}
	case 1:
		expected = &tg.PeerChat{ChatID: rawID}
	case 2:
		expected = &tg.PeerChannel{ChannelID: rawID}
	default:
		return nil, errors.New("unsupported Postbox peer id")
	}
	return getRemoteMessageForPeer(ctx, raw, postboxInputPeer(msg), expected, key.MessageID)
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
		if currentPeerID, ok := telegramPeerID(message.GetPeerID()); ok && currentPeerID != peerID {
			continue
		}
		return message, true
	}
	return nil, false
}

func telegramPeerID(peer tg.PeerClass) (int64, bool) {
	switch value := peer.(type) {
	case *tg.PeerUser:
		return value.UserID, value.UserID != 0
	case *tg.PeerChat:
		return -value.ChatID, value.ChatID != 0
	case *tg.PeerChannel:
		return -1000000000000 - value.ChannelID, value.ChannelID != 0
	default:
		return 0, false
	}
}

func downloadTelegramMessageMedia(ctx context.Context, raw *tg.Client, elem querymessages.Elem, mediaTempDir, key string) (string, int64, string) {
	if strings.TrimSpace(mediaTempDir) == "" {
		return "", 0, "unavailable"
	}
	file, ok := telegramMessageFile(elem)
	if !ok {
		return "", 0, "unavailable"
	}
	hash := sha256.Sum256([]byte(key))
	messageDir := filepath.Join(mediaTempDir, fmt.Sprintf("%x", hash[:]))
	if err := os.MkdirAll(messageDir, 0o700); err != nil {
		return "", 0, "error"
	}
	name := filepath.Base(strings.TrimSpace(file.Name))
	if name == "." || name == string(filepath.Separator) || name == "" {
		name = "media"
	}
	outputPath := filepath.Join(messageDir, name)
	downloadCtx, cancel := context.WithTimeout(ctx, telegramMediaDownloadTimeout)
	defer cancel()
	if _, err := downloader.NewDownloader().WithAllowCDN(true).Download(raw, file.Location).ToPath(downloadCtx, outputPath); err != nil {
		if errors.Is(downloadCtx.Err(), context.DeadlineExceeded) {
			return "", 0, "timeout"
		}
		return "", 0, "error"
	}
	info, err := os.Stat(outputPath)
	if err != nil || info.Size() <= 0 {
		return "", 0, "unavailable"
	}
	return outputPath, info.Size(), ""
}

func telegramMessageFile(elem querymessages.Elem) (querymessages.File, bool) {
	if file, ok := elem.File(); ok {
		return file, true
	}
	msg, ok := elem.Msg.(*tg.Message)
	if !ok {
		return querymessages.File{}, false
	}
	media, ok := msg.Media.(*tg.MessageMediaWebPage)
	if !ok {
		return querymessages.File{}, false
	}
	page, ok := media.Webpage.(*tg.WebPage)
	if !ok {
		return querymessages.File{}, false
	}
	if rawDocument, ok := page.GetDocument(); ok {
		if doc, ok := rawDocument.AsNotEmpty(); ok {
			return querymessages.File{
				Name:     firstNonEmpty(telegramDocumentFilename(doc), telegramDocumentAudioTitle(doc), fmt.Sprintf("doc%d", doc.ID)),
				MIMEType: doc.MimeType,
				Location: doc.AsInputDocumentFileLocation(""),
			}, true
		}
	}
	if rawPhoto, ok := page.GetPhoto(); ok {
		if photo, ok := rawPhoto.AsNotEmpty(); ok {
			thumbSize := telegramLargestPhotoThumbSize(photo)
			if thumbSize == "" {
				return querymessages.File{}, false
			}
			return querymessages.File{
				Name:     fmt.Sprintf("photo%d_%s.jpg", photo.ID, time.Unix(int64(photo.Date), 0).Format("2006-01-02_15-04-05")),
				MIMEType: "image/jpeg",
				Location: &tg.InputPhotoFileLocation{ID: photo.ID, AccessHash: photo.AccessHash, FileReference: photo.FileReference, ThumbSize: thumbSize},
			}, true
		}
	}
	return querymessages.File{}, false
}

func telegramDocumentFilename(doc *tg.Document) string {
	for _, attr := range doc.Attributes {
		if filename, ok := attr.(*tg.DocumentAttributeFilename); ok {
			return filename.FileName
		}
	}
	return ""
}

func telegramDocumentAudioTitle(doc *tg.Document) string {
	for _, attr := range doc.Attributes {
		if audio, ok := attr.(*tg.DocumentAttributeAudio); ok {
			title, _ := audio.GetTitle()
			return title
		}
	}
	return ""
}

func telegramLargestPhotoThumbSize(photo *tg.Photo) string {
	var bestType string
	var bestArea int
	for _, rawSize := range photo.Sizes {
		size, ok := rawSize.(interface {
			GetW() int
			GetH() int
			GetType() string
		})
		if !ok {
			continue
		}
		if area := size.GetW() * size.GetH(); area > bestArea {
			bestArea = area
			bestType = size.GetType()
		}
	}
	return bestType
}

func addPostboxRemoteMediaStats(left *postboxRemoteMediaStats, right postboxRemoteMediaStats) {
	left.Attempted += right.Attempted
	left.Downloaded += right.Downloaded
	left.Unavailable += right.Unavailable
	left.Timeouts += right.Timeouts
	left.Errors += right.Errors
}
