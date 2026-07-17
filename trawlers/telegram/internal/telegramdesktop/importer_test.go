package telegramdesktop

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha512"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/telegram/internal/store"
	postboxpkg "github.com/opentrawl/opentrawl/trawlers/telegram/internal/telegramdesktop/postbox"
)

func TestResolveImportSourceUsesDefaultPostbox(t *testing.T) {
	root, _, _ := makePostboxFixture(t)
	source := resolveImportSourcePath(context.Background(), "", root)
	if source.path != root || source.explicit || source.unavailable != nil {
		t.Fatalf("source = %+v, want Postbox path", source)
	}
}

func TestResolveImportSourceUsesExplicitPostbox(t *testing.T) {
	_, _, account := makePostboxFixture(t)
	source := resolveImportSourcePath(context.Background(), account, "unused-default")
	if source.path != account || !source.explicit || source.unavailable != nil {
		t.Fatalf("source = %+v, want explicit postbox path", source)
	}
}

func TestPostboxParserSanitizedFixture(t *testing.T) {
	root, _, _ := makePostboxFixture(t)
	result, err := Import(context.Background(), ImportOptions{
		Path: root,
	}, filepath.Join(t.TempDir(), "telecrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Chats) != 1 || result.Chats[0].Name != "Fixture Person" {
		t.Fatalf("chats = %#v", result.Chats)
	}
	if len(result.Contacts) != 1 || result.Contacts[0].FullName != "Fixture Person" {
		t.Fatalf("contacts = %#v", result.Contacts)
	}
	if len(result.Messages) != 1 || result.Messages[0].Text != "fixture hello" || result.Messages[0].MediaType != "photo_or_video" {
		t.Fatalf("messages = %#v", result.Messages)
	}
}

func TestGroupParticipantsFromMessagesUsesKnownGroupAuthors(t *testing.T) {
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	participants := groupParticipantsFromMessages(
		[]store.Chat{
			{JID: "100", Kind: "group", Name: "team room"},
			{JID: "200", Kind: "user", Name: "direct chat"},
		},
		[]store.Contact{{JID: "600", FullName: "Alice Example", FirstName: "Alice"}},
		[]store.Message{
			{SourcePK: 1, ChatJID: "100", SenderJID: "600", SenderName: "Alice", Timestamp: now},
			{SourcePK: 2, ChatJID: "100", SenderJID: "600", SenderName: "Alice", Timestamp: now.Add(time.Minute)},
			{SourcePK: 3, ChatJID: "100", SenderJID: "700", SenderName: "Bob Example", Timestamp: now.Add(2 * time.Minute)},
			{SourcePK: 4, ChatJID: "200", SenderJID: "800", SenderName: "Direct Person", Timestamp: now.Add(3 * time.Minute)},
		},
	)
	if len(participants) != 2 {
		t.Fatalf("participants = %#v, want 2 known group authors", participants)
	}
	if participants[0].GroupJID != "100" || participants[0].UserJID != "600" || participants[0].ContactName != "Alice Example" || !participants[0].IsActive {
		t.Fatalf("first participant = %#v", participants[0])
	}
	if participants[1].GroupJID != "100" || participants[1].UserJID != "700" || participants[1].ContactName != "Bob Example" || !participants[1].IsActive {
		t.Fatalf("second participant = %#v", participants[1])
	}
}

func TestCopyImportedMediaArchivesByContentHash(t *testing.T) {
	t.Parallel()
	source := filepath.Join(t.TempDir(), "source-media")
	if err := os.WriteFile(source, []byte("fixture media"), 0o600); err != nil {
		t.Fatal(err)
	}
	messages := []store.Message{
		{SourcePK: 1, MediaPath: source},
		{SourcePK: 2, MediaPath: source},
	}
	var stats store.ImportStats
	archiveDir := filepath.Join(t.TempDir(), "media")

	if err := copyImportedMedia(messages, archiveDir, &stats); err != nil {
		t.Fatal(err)
	}
	if messages[0].MediaPath == source {
		t.Fatal("media path still points at source cache")
	}
	if messages[1].MediaPath != messages[0].MediaPath {
		t.Fatalf("duplicate media archived to different paths: %q != %q", messages[1].MediaPath, messages[0].MediaPath)
	}
	if messages[0].MediaSize != int64(len("fixture media")) {
		t.Fatalf("media size = %d, want %d", messages[0].MediaSize, len("fixture media"))
	}
	if stats.MediaFiles != 1 || stats.MediaBytes != int64(len("fixture media")) {
		t.Fatalf("media stats = %+v", stats)
	}
	data, err := os.ReadFile(messages[0].MediaPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "fixture media" {
		t.Fatalf("archived media = %q", data)
	}
	if !strings.HasPrefix(messages[0].MediaPath, archiveDir+string(os.PathSeparator)) {
		t.Fatalf("media path %q is not under archive dir %q", messages[0].MediaPath, archiveDir)
	}
}

func TestCopyImportedContactAvatarsArchivesByContentHash(t *testing.T) {
	t.Parallel()
	source := filepath.Join(t.TempDir(), "source-avatar")
	if err := os.WriteFile(source, []byte("fixture avatar"), 0o600); err != nil {
		t.Fatal(err)
	}
	contacts := []store.Contact{
		{JID: "1", AvatarPath: source},
		{JID: "2", AvatarPath: source},
		{JID: "3", AvatarPath: filepath.Join(t.TempDir(), "missing-avatar")},
	}
	archiveDir := filepath.Join(t.TempDir(), "media")

	if err := copyImportedContactAvatars(contacts, archiveDir); err != nil {
		t.Fatal(err)
	}
	if contacts[0].AvatarPath == source {
		t.Fatal("avatar path still points at source cache")
	}
	if contacts[1].AvatarPath != contacts[0].AvatarPath {
		t.Fatalf("duplicate avatar archived to different paths: %q != %q", contacts[1].AvatarPath, contacts[0].AvatarPath)
	}
	if contacts[2].AvatarPath != "" {
		t.Fatalf("missing avatar path = %q, want cleared", contacts[2].AvatarPath)
	}
	data, err := os.ReadFile(contacts[0].AvatarPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "fixture avatar" {
		t.Fatalf("archived avatar = %q", data)
	}
	if !strings.HasPrefix(contacts[0].AvatarPath, archiveDir+string(os.PathSeparator)) {
		t.Fatalf("avatar path %q is not under archive dir %q", contacts[0].AvatarPath, archiveDir)
	}
}

func TestCopyImportedMediaKeepsExistingArchiveRef(t *testing.T) {
	t.Parallel()
	archiveDir := filepath.Join(t.TempDir(), "media")
	archivedPath := filepath.Join(archiveDir, "ab", "already-archived")
	if err := os.MkdirAll(filepath.Dir(archivedPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archivedPath, []byte("already archived"), 0o600); err != nil {
		t.Fatal(err)
	}
	messages := []store.Message{{SourcePK: 1, MediaPath: archivedPath}}
	var stats store.ImportStats

	if err := copyImportedMedia(messages, archiveDir, &stats); err != nil {
		t.Fatal(err)
	}
	if messages[0].MediaPath != archivedPath {
		t.Fatalf("media path = %q, want existing archive path %q", messages[0].MediaPath, archivedPath)
	}
	if messages[0].MediaSize != int64(len("already archived")) {
		t.Fatalf("media size = %d, want %d", messages[0].MediaSize, len("already archived"))
	}
	if stats.MediaFiles != 1 || stats.MediaBytes != int64(len("already archived")) {
		t.Fatalf("media stats = %+v", stats)
	}
}

func TestCopyImportedMediaSkipsMissingSourceCache(t *testing.T) {
	t.Parallel()
	messages := []store.Message{
		{SourcePK: 1, MediaPath: filepath.Join(t.TempDir(), "missing-cache-file"), MediaSize: 99},
	}
	var stats store.ImportStats

	if err := copyImportedMedia(messages, filepath.Join(t.TempDir(), "media"), &stats); err != nil {
		t.Fatal(err)
	}
	if messages[0].MediaPath != "" || messages[0].MediaSize != 0 {
		t.Fatalf("missing media ref = path %q size %d, want cleared", messages[0].MediaPath, messages[0].MediaSize)
	}
	if stats.MediaFiles != 0 || stats.MediaBytes != 0 {
		t.Fatalf("media stats = %+v, want zero", stats)
	}
}

func TestImportPassesExistingMediaRefsToPostboxImporter(t *testing.T) {
	t.Parallel()
	source, _, _ := makePostboxFixture(t)
	media := filepath.Join(t.TempDir(), "already-archived")
	if err := os.WriteFile(media, []byte("already archived"), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := Import(context.Background(), ImportOptions{
		Path:                    source,
		FetchMedia:              true,
		ExistingMediaSourcePath: source,
		ExistingMediaRefs: []ExistingMediaRef{{
			SourcePK:  postboxpkg.SourcePK("stable/account-123", 100, 0, 1, false),
			MediaType: "photo",
			MediaPath: media,
			MediaSize: int64(len("already archived")),
		}},
	}, filepath.Join(t.TempDir(), "telecrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 1 || result.Messages[0].MediaPath == "" {
		t.Fatalf("existing media ref was not restored: %#v", result.Messages)
	}
	if result.Stats.RemoteMediaMissing != 0 || result.Stats.RemoteMediaCandidates != 0 {
		t.Fatalf("remote stats = %+v", result.Stats)
	}
}

func TestImportDoesNotFetchMediaByDefault(t *testing.T) {
	t.Parallel()
	source, _, _ := makePostboxFixture(t)
	result, err := Import(context.Background(), ImportOptions{Path: source}, filepath.Join(t.TempDir(), "telecrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Stats.RemoteMediaCandidates != 0 || result.Stats.RemoteMediaMissing != 0 {
		t.Fatalf("postbox default import fetched media: %+v", result.Stats)
	}
}

func makePostboxFixture(t *testing.T) (root string, lane string, account string) {
	t.Helper()
	root = t.TempDir()
	lane = filepath.Join(root, "stable")
	account = filepath.Join(lane, "account-123")
	dbDir := filepath.Join(account, "postbox", "db")
	if err := os.MkdirAll(dbDir, 0o700); err != nil {
		t.Fatal(err)
	}
	keyAndSalt := make([]byte, 48)
	for i := range keyAndSalt {
		keyAndSalt[i] = byte(i)
	}
	if err := os.WriteFile(filepath.Join(lane, ".tempkeyEncrypted"), encryptedTempKeyFixture(t, []byte("no-matter-key"), keyAndSalt), 0o600); err != nil {
		t.Fatal(err)
	}
	fixtureDB := filepath.Join("postbox", "testdata", "sqlcipher_v4.db")
	if err := copyFile(filepath.Join(dbDir, "db_sqlite"), fixtureDB); err != nil {
		t.Fatal(err)
	}
	return root, lane, account
}

func copyFile(dst, src string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	_, err = io.Copy(out, in)
	return err
}

func encryptedTempKeyFixture(t *testing.T, passcode []byte, keyAndSalt []byte) []byte {
	t.Helper()
	plain := make([]byte, 64)
	copy(plain, keyAndSalt)
	binary.LittleEndian.PutUint32(plain[48:52], uint32(tempkeyMurmur3(keyAndSalt)))
	digest := sha512.Sum512(passcode)
	block, err := aes.NewCipher(digest[:32])
	if err != nil {
		t.Fatal(err)
	}
	out := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, digest[48:]).CryptBlocks(out, plain)
	return out
}

func tempkeyMurmur3(data []byte) int32 {
	const seed uint32 = 0xf7ca7fd2
	const c1 uint32 = 0xcc9e2d51
	const c2 uint32 = 0x1b873593
	length := len(data)
	h1 := seed
	roundedEnd := length & 0xfffffffc
	for i := 0; i < roundedEnd; i += 4 {
		k1 := uint32(data[i]) | uint32(data[i+1])<<8 | uint32(data[i+2])<<16 | uint32(data[i+3])<<24
		k1 *= c1
		k1 = (k1 << 15) | (k1 >> 17)
		k1 *= c2
		h1 ^= k1
		h1 = (h1 << 13) | (h1 >> 19)
		h1 = h1*5 + 0xe6546b64
	}
	var k1 uint32
	switch length & 3 {
	case 3:
		k1 ^= uint32(data[roundedEnd+2]) << 16
		fallthrough
	case 2:
		k1 ^= uint32(data[roundedEnd+1]) << 8
		fallthrough
	case 1:
		k1 ^= uint32(data[roundedEnd])
		k1 *= c1
		k1 = (k1 << 15) | (k1 >> 17)
		k1 *= c2
		h1 ^= k1
	}
	h1 ^= uint32(length)
	h1 ^= h1 >> 16
	h1 *= 0x85ebca6b
	h1 ^= h1 >> 13
	h1 *= 0xc2b2ae35
	h1 ^= h1 >> 16
	return int32(h1)
}
