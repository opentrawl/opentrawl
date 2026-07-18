package telegramdesktop

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gotd/td/telegram/query/dialogs"
	querymessages "github.com/gotd/td/telegram/query/messages"
	"github.com/gotd/td/tg"
	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	postboxpkg "github.com/opentrawl/opentrawl/trawlers/telegram/internal/telegramdesktop/postbox"
)

func TestPostboxConnectionBackoffIsBounded(t *testing.T) {
	t.Parallel()
	b := postboxConnectionBackoff()
	if got, want := b.MaxElapsedTime, 30*time.Second; got != want {
		t.Fatalf("max elapsed time = %v, want %v", got, want)
	}
	if got, want := b.MaxInterval, 3*time.Second; got != want {
		t.Fatalf("max interval = %v, want %v", got, want)
	}
}

func TestCloudHistoryCannotCompleteAfterCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := (&postboxHistoryLoader{}).download(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("download error = %v, want context canceled", err)
	}
}

func TestCloudHistoryEnumeratesMainAndArchivedFolders(t *testing.T) {
	t.Parallel()
	var folders []int
	loader := postboxHistoryLoader{visitDialogs: func(_ context.Context, folderID int, _ func(context.Context, dialogs.Elem) error) error {
		folders = append(folders, folderID)
		return nil
	}}
	if err := loader.download(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(folders) != 2 || folders[0] != 0 || folders[1] != 1 {
		t.Fatalf("visited folders = %v, want main [0] then archive [1]", folders)
	}
}

func TestCloudHistoryFindsBasicGroupBehindSupergroupMigration(t *testing.T) {
	t.Parallel()
	full := &tg.ChannelFull{ID: 200}
	full.SetMigratedFromChatID(100)
	result := &tg.MessagesChatFull{
		FullChat: full,
		Chats:    []tg.ChatClass{&tg.Chat{ID: 100, Title: "Example group"}},
	}

	id, info, ok := migratedGroupHistory(result, &tg.User{ID: 1})
	if !ok || id != 100 || info.kind != "group" || info.name != "Example group" {
		t.Fatalf("migration = id:%d info:%+v ok:%v", id, info, ok)
	}
}

func TestCloudHistoryUsesPostboxMessageIdentity(t *testing.T) {
	t.Parallel()
	peer := &tg.PeerChannel{ChannelID: 42}
	rawChatID, ok := postboxpkg.TelegramPeerToPostboxID(peer)
	if !ok {
		t.Fatal("channel did not map to Postbox")
	}
	loader := postboxHistoryLoader{
		self:      &tg.User{ID: 99, FirstName: "Morgan"},
		accountID: "appstore/account-example",
	}
	chatID := postboxpkg.PeerStoreID(loader.accountID, rawChatID, false)
	message := loader.convertMessage(querymessages.Elem{Msg: &tg.Message{
		ID: 7, PeerID: peer, FromID: &tg.PeerUser{UserID: 8}, Date: 1_788_000_000, Message: "A synthetic old message",
	}}, rawChatID, chatID, cloudPeerDetails{kind: "channel", name: "Example channel"}, map[string]store.Contact{})

	wantPK := postboxpkg.SourcePK(loader.accountID, rawChatID, 0, 7, false)
	if message.SourcePK != wantPK || message.MessageID != "0:7" || message.ChatJID != chatID {
		t.Fatalf("message identity = %#v, want source_pk=%d chat=%s", message, wantPK, chatID)
	}
}

func TestCloudHistoryAdvancesResumeOffsetOnlyAfterArchiveCommit(t *testing.T) {
	t.Parallel()
	commitErr := errors.New("archive write failed")
	loader := postboxHistoryLoader{
		resumeOffsets: map[string]int{"account:chat": 100},
		opts: PostboxHistoryOptions{DialogBatch: func(string, int, bool, ImportResult) error {
			return commitErr
		}},
	}
	if err := loader.flushDialogBatch("account:chat", 50, false, "chat", cloudPeerDetails{}, nil, nil); !errors.Is(err, commitErr) {
		t.Fatalf("flush error = %v, want %v", err, commitErr)
	}
	if got := loader.resumeOffsets["account:chat"]; got != 100 {
		t.Fatalf("resume offset = %d after failed write, want last committed 100", got)
	}

	loader.opts.DialogBatch = func(string, int, bool, ImportResult) error { return nil }
	if err := loader.flushDialogBatch("account:chat", 50, false, "chat", cloudPeerDetails{}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got := loader.resumeOffsets["account:chat"]; got != 50 {
		t.Fatalf("resume offset = %d after committed write, want 50", got)
	}
}

func TestCloudHistoryTraversalIsPerDialog(t *testing.T) {
	t.Parallel()
	const completed = "account:complete"
	const discovered = "account:new"

	tests := []struct {
		name        string
		incremental bool
		checkpoint  string
		chatID      string
		download    bool
		stopAtKnown bool
		legacy      bool
	}{
		{name: "incomplete resume skips completed dialog", checkpoint: completed, download: false},
		{name: "incomplete resume traverses unfinished dialog", checkpoint: discovered, download: true},
		{name: "known dialog updates incrementally", incremental: true, checkpoint: completed, download: true, stopAtKnown: true},
		{name: "new dialog receives full traversal", incremental: true, checkpoint: discovered, chatID: "new-chat", download: true},
		{name: "legacy known chat migrates incrementally", incremental: true, legacy: true, checkpoint: discovered, chatID: "legacy-chat", download: true, stopAtKnown: true},
		{name: "chat first seen during legacy migration receives full traversal", incremental: true, legacy: true, checkpoint: discovered, chatID: "new-chat", download: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			loader := postboxHistoryLoader{opts: PostboxHistoryOptions{
				CompletedDialogs:       map[string]bool{completed: true},
				LegacyCompletedChatIDs: map[string]bool{"legacy-chat": test.legacy},
				Incremental:            test.incremental,
			}}
			download, stopAtKnown := loader.dialogTraversal(test.checkpoint, test.chatID)
			if download != test.download || stopAtKnown != test.stopAtKnown {
				t.Fatalf("traversal = download:%v incremental:%v, want download:%v incremental:%v", download, stopAtKnown, test.download, test.stopAtKnown)
			}
		})
	}
}

func TestMigratedHistoryDiscoverySkipsKnownIncrementalDialogs(t *testing.T) {
	t.Parallel()
	const completed = "account:complete"
	const discovered = "account:new"

	initial := postboxHistoryLoader{opts: PostboxHistoryOptions{CompletedDialogs: map[string]bool{completed: true}}}
	if !initial.shouldDiscoverMigratedHistory(completed) {
		t.Fatal("incomplete initial run must resume migrated-history discovery")
	}
	incremental := postboxHistoryLoader{opts: PostboxHistoryOptions{Incremental: true, CompletedDialogs: map[string]bool{completed: true}}}
	if incremental.shouldDiscoverMigratedHistory(completed) {
		t.Fatal("known incremental dialog triggered redundant migrated-history discovery")
	}
	if !incremental.shouldDiscoverMigratedHistory(discovered) {
		t.Fatal("new incremental dialog did not discover migrated history")
	}
	legacy := postboxHistoryLoader{opts: PostboxHistoryOptions{Incremental: true, LegacyCompletedChatIDs: map[string]bool{"legacy-chat": true}}}
	if !legacy.shouldDiscoverMigratedHistory(discovered) {
		t.Fatal("legacy checkpoint migration skipped migrated-history discovery")
	}
}

func TestIncrementalHistorySkipsRPCWhenDialogTopIsArchived(t *testing.T) {
	t.Parallel()
	const rawChatID int64 = 42
	const messageID = 99
	pk := postboxpkg.SourcePK("account-example", rawChatID, 0, messageID, false)
	loader := postboxHistoryLoader{
		accountID: "account-example",
		opts: PostboxHistoryOptions{MessageExists: func(_ context.Context, sourcePK int64) (bool, error) {
			return sourcePK == pk, nil
		}},
	}
	top := &tg.Message{ID: messageID}
	if archived, err := loader.dialogTopAlreadyArchived(context.Background(), rawChatID, top); err != nil || !archived {
		t.Fatal("archived dialog top did not suppress redundant history request")
	}
	if archived, err := loader.dialogTopAlreadyArchived(context.Background(), rawChatID, &tg.Message{ID: messageID + 1}); err != nil || archived {
		t.Fatal("unseen dialog top was treated as archived")
	}
	if archived, err := loader.dialogTopAlreadyArchived(context.Background(), rawChatID, nil); err != nil || archived {
		t.Fatal("missing dialog top was treated as archived")
	}
}

func TestCloudMessageFromMeUsesAuthorIdentity(t *testing.T) {
	t.Parallel()
	self := &tg.User{ID: 10}

	other := &tg.Message{Out: true}
	other.SetFromID(&tg.PeerUser{UserID: 20})
	other.SetFlags()
	if cloudMessageFromMe(other, self) {
		t.Fatal("another author's channel message was classified as from me")
	}
	mine := &tg.Message{Out: false}
	mine.SetFromID(&tg.PeerUser{UserID: self.ID})
	mine.SetFlags()
	if !cloudMessageFromMe(mine, self) {
		t.Fatal("self-authored message was classified as incoming")
	}
	withoutAuthor := &tg.Message{Out: true}
	withoutAuthor.SetFlags()
	if !cloudMessageFromMe(withoutAuthor, self) {
		t.Fatal("senderless outgoing message did not use Telegram's direction fallback")
	}
}
