package telegramdesktop

import (
	"context"
	"errors"
	"testing"

	querymessages "github.com/gotd/td/telegram/query/messages"
	"github.com/gotd/td/tg"
	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	postboxpkg "github.com/opentrawl/opentrawl/trawlers/telegram/internal/telegramdesktop/postbox"
)

func TestCloudHistoryCannotCompleteAfterCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := (&postboxHistoryLoader{}).download(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("download error = %v, want context canceled", err)
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
