package telegramdesktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/gotd/td/tgerr"
	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	postboxpkg "github.com/opentrawl/opentrawl/trawlers/telegram/internal/telegramdesktop/postbox"
)

func TestArchiveMediaCandidatesRequireCloudMediaWithoutPath(t *testing.T) {
	t.Parallel()
	messages := []store.Message{
		{SourcePK: 1, MessageID: "0:10", MediaType: "photo"},
		{SourcePK: 2, MessageID: "0:11", MediaType: "photo", MediaPath: "/archive/already"},
		{SourcePK: 3, MessageID: "0:12"},
		{SourcePK: 4, MessageID: "1:13", MediaType: "photo"},
		{SourcePK: 5, MessageID: "bad", MediaType: "photo"},
	}
	got := archiveMediaCandidates(messages)
	if len(got) != 1 || got[0].SourcePK != 1 {
		t.Fatalf("candidates = %#v, want only missing cloud attachment", got)
	}
}

func TestArchiveMediaDialogMatchingKeepsAccountsSeparate(t *testing.T) {
	t.Parallel()
	rawChatID := int64(42)
	first := postboxpkg.PeerStoreID("stable/account-one", rawChatID, true)
	second := postboxpkg.PeerStoreID("stable/account-two", rawChatID, true)
	candidates := []store.Message{
		{SourcePK: 1, ChatJID: first, MessageID: "0:10", MediaType: "photo"},
		{SourcePK: 2, ChatJID: second, MessageID: "0:10", MediaType: "photo"},
	}
	got := archiveMediaCandidatesForDialog(candidates, "stable/account-one", rawChatID, true)
	if len(got) != 1 || got[0].SourcePK != 1 {
		t.Fatalf("account-scoped candidates = %#v, want first account only", got)
	}
}

func TestBackfillArchiveMediaCopiesOnlyDownloadedPaths(t *testing.T) {
	t.Parallel()
	temp := t.TempDir()
	dbPath := filepath.Join(temp, "telegram.db")
	staged := filepath.Join(temp, "staged.bin")
	if err := os.WriteFile(staged, []byte("synthetic attachment"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, lane, account := makePostboxFixture(t)
	writePostboxSharedData(t, lane, account, postboxSessionTestAuthKey(7))
	sources := mustPostboxSources(t, root)
	candidates := []store.Message{
		{SourcePK: 1, ChatJID: "42", MessageID: "0:10", MediaType: "photo", Text: "keep me"},
		{SourcePK: 2, ChatJID: "43", MessageID: "0:11", MediaType: "document", Text: "also keep me"},
	}
	download := func(_ context.Context, _ postboxRemoteSession, _ bool, messages []store.Message, _ string, _ ProgressReporter) (archiveMediaSessionResult, error) {
		resolved := messages[0]
		resolved.MediaPath = staged
		resolved.MediaSize = int64(len("synthetic attachment"))
		return archiveMediaSessionResult{messages: []store.Message{resolved}, attempted: 2, downloaded: 1}, nil
	}
	result, err := backfillArchiveMedia(context.Background(), sources, false, candidates, dbPath, temp, nil, download)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Updates) != 1 || result.Updates[0].SourcePK != 1 || result.Updates[0].MediaSize != int64(len("synthetic attachment")) {
		t.Fatalf("updates = %#v, want one downloaded attachment", result.Updates)
	}
	if result.Stats.RemoteMediaCandidates != 2 || result.Stats.RemoteMediaDownloads != 1 || result.Stats.RemoteMediaMissing != 1 {
		t.Fatalf("stats = %+v", result.Stats)
	}
	data, err := os.ReadFile(result.Updates[0].MediaPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "synthetic attachment" {
		t.Fatalf("archived attachment = %q", data)
	}
}

func TestBackfillArchiveMediaHonorsCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	download := func(context.Context, postboxRemoteSession, bool, []store.Message, string, ProgressReporter) (archiveMediaSessionResult, error) {
		called = true
		return archiveMediaSessionResult{}, nil
	}
	_, err := backfillArchiveMedia(ctx,
		[]postboxpkg.Source{{AccountID: "stable/account-example"}}, false,
		[]store.Message{{SourcePK: 1, MessageID: "0:10", MediaType: "photo"}},
		filepath.Join(t.TempDir(), "telegram.db"), t.TempDir(), nil, download,
	)
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("error=%v downloader_called=%v, want cancellation before download", err, called)
	}
}

func TestBackfillArchiveMediaPreservesCompletedDownloadOnCancellation(t *testing.T) {
	t.Parallel()
	temp := t.TempDir()
	dbPath := filepath.Join(temp, "telegram.db")
	staged := filepath.Join(temp, "staged.bin")
	if err := os.WriteFile(staged, []byte("completed before cancellation"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, lane, account := makePostboxFixture(t)
	writePostboxSharedData(t, lane, account, postboxSessionTestAuthKey(23))
	sources := mustPostboxSources(t, root)
	candidates := []store.Message{
		{SourcePK: 1, ChatJID: "42", MessageID: "0:10", MediaType: "photo"},
		{SourcePK: 2, ChatJID: "42", MessageID: "0:11", MediaType: "photo"},
	}
	download := func(_ context.Context, _ postboxRemoteSession, _ bool, messages []store.Message, _ string, _ ProgressReporter) (archiveMediaSessionResult, error) {
		resolved := messages[0]
		resolved.MediaPath = staged
		resolved.MediaSize = int64(len("completed before cancellation"))
		return archiveMediaSessionResult{
			messages: []store.Message{resolved}, attempted: 1, downloaded: 1,
		}, context.Canceled
	}

	result, err := backfillArchiveMedia(context.Background(), sources, false, candidates, dbPath, temp, nil, download)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want cancellation", err)
	}
	if len(result.Updates) != 1 || result.Updates[0].SourcePK != 1 {
		t.Fatalf("updates = %#v, want completed attachment", result.Updates)
	}
	data, readErr := os.ReadFile(result.Updates[0].MediaPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "completed before cancellation" {
		t.Fatalf("archived attachment = %q", data)
	}

	resumed := append([]store.Message(nil), candidates...)
	resumed[0].MediaPath = result.Updates[0].MediaPath
	resumed[0].MediaSize = result.Updates[0].MediaSize
	remaining := archiveMediaCandidates(resumed)
	if len(remaining) != 1 || remaining[0].SourcePK != 2 {
		t.Fatalf("resume candidates = %#v, want only unfinished attachment", remaining)
	}
}

func TestArchiveMediaRefreshesRejectedNativeSessionOnce(t *testing.T) {
	t.Parallel()
	root, lane, account := makePostboxFixture(t)
	writePostboxSharedData(t, lane, account, postboxSessionTestAuthKey(19))
	remote := orderedPostboxHistorySessions(mustPostboxSources(t, root))[0]
	calls := 0
	download := func(_ context.Context, _ postboxRemoteSession, _ bool, candidates []store.Message, _ string, _ ProgressReporter) (archiveMediaSessionResult, error) {
		calls++
		if calls == 1 {
			return archiveMediaSessionResult{messages: []store.Message{candidates[0]}, downloaded: 1}, tgerr.New(401, "AUTH_KEY_UNREGISTERED")
		}
		if len(candidates) != 1 || candidates[0].SourcePK != 2 {
			t.Fatalf("refresh candidates = %#v, want only unresolved attachment", candidates)
		}
		return archiveMediaSessionResult{messages: []store.Message{candidates[0]}, downloaded: 1}, nil
	}
	candidates := []store.Message{{SourcePK: 1}, {SourcePK: 2}}
	result, err := downloadArchiveMediaSessionWithRefresh(context.Background(), remote, false, candidates, t.TempDir(), nil, download)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || result.downloaded != 2 || len(result.messages) != 2 {
		t.Fatalf("calls=%d result=%+v, want one refresh then success", calls, result)
	}
}
