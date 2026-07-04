package cli

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha512"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/telecrawl/internal/store"
	"github.com/openclaw/telecrawl/internal/telegramdesktop"
)

func TestMetadataAdvertisesContactExport(t *testing.T) {
	manifest := controlManifest()
	command, ok := manifest.Commands["contact-export"]
	if !ok {
		t.Fatalf("commands = %#v", manifest.Commands)
	}
	if command.Mutates || !command.JSON {
		t.Fatalf("contact-export command = %#v", command)
	}
	want := []string{"telecrawl", "--json", "contacts", "export"}
	if !slices.Equal(command.Argv, want) {
		t.Fatalf("argv = %#v, want %#v", command.Argv, want)
	}
	openCommand, ok := manifest.Commands["open"]
	if !ok {
		t.Fatalf("commands = %#v, want open", manifest.Commands)
	}
	if openCommand.Mutates || !openCommand.JSON {
		t.Fatalf("open command = %#v", openCommand)
	}
	if !slices.Contains(manifest.Capabilities, "open") {
		t.Fatalf("capabilities = %#v, want open", manifest.Capabilities)
	}
	if !slices.Contains(manifest.Capabilities, "who") {
		t.Fatalf("capabilities = %#v, want who", manifest.Capabilities)
	}
	if !slices.Contains(manifest.Capabilities, "verbose_logs") {
		t.Fatalf("capabilities = %#v, want verbose_logs", manifest.Capabilities)
	}
	if manifest.Paths.DefaultLogs != defaultLogDir() {
		t.Fatalf("default logs = %q, want %q", manifest.Paths.DefaultLogs, defaultLogDir())
	}
}

func TestVerboseLogsWriteFileAndStreamToStderr(t *testing.T) {
	clearTestLog(t)
	logPath := filepath.Join(defaultLogDir(), telecrawlLogFileName)

	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"metadata"}, &stdout, &stderr); err != nil {
		t.Fatalf("metadata error = %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("metadata without -v wrote stderr:\n%s", stderr.String())
	}
	if _, err := os.Stat(logPath); err != nil {
		t.Fatalf("log file missing at %s: %v", logPath, err)
	}
	logText := readTestLog(t)
	for _, want := range []string{"metadata start:", "metadata finish: outcome=success"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("log missing %q:\n%s", want, logText)
		}
	}

	stdout.Reset()
	stderr.Reset()
	if err := Run(context.Background(), []string{"-v", "metadata"}, &stdout, &stderr); err != nil {
		t.Fatalf("metadata -v error = %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "metadata start:") || !strings.Contains(stderr.String(), "metadata finish: outcome=success") {
		t.Fatalf("-v stderr missing log lines:\n%s", stderr.String())
	}
	if strings.Contains(stderr.String(), "DEBUG") {
		t.Fatalf("-v streamed debug line:\n%s", stderr.String())
	}
}

func TestImportVerboseLogsPhaseTimings(t *testing.T) {
	clearTestLog(t)
	source := makePostboxFixture(t)
	db := filepath.Join(t.TempDir(), "telecrawl.db")

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"-vv", "--db", db, "--json", "import", "--path", source, "--dialogs-limit", "1", "--messages-limit", "1"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("import -vv error = %v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
	}
	logText := readTestLog(t)
	for _, want := range []string{
		"sync_done: messages=1",
		"chats=1",
		"media_messages=1",
		"sync_phase: source=telegram",
		"import_ms=",
		"write_ms=",
	} {
		if !strings.Contains(logText, want) {
			t.Fatalf("import log missing %q:\n%s", want, logText)
		}
	}
	if !strings.Contains(stderr.String(), "sync_done: messages=1") || !strings.Contains(stderr.String(), "sync_phase: source=telegram") {
		t.Fatalf("-vv stderr missing sync log lines:\n%s", stderr.String())
	}
}

// The import/sync/wiretap aliases are one operation, so the completion event is
// always sync_done regardless of which alias the user typed.
func TestImportAliasesLogCanonicalSyncEvent(t *testing.T) {
	for _, alias := range []string{"import", "sync", "wiretap"} {
		t.Run(alias, func(t *testing.T) {
			clearTestLog(t)
			source := makePostboxFixture(t)
			db := filepath.Join(t.TempDir(), "telecrawl.db")

			var stdout, stderr bytes.Buffer
			if err := Run(context.Background(), []string{"--db", db, "--json", alias, "--path", source, "--dialogs-limit", "1", "--messages-limit", "1"}, &stdout, &stderr); err != nil {
				t.Fatalf("%s error = %v stderr=%s", alias, err, stderr.String())
			}
			logText := readTestLog(t)
			if !strings.Contains(logText, "sync_done: messages=1") {
				t.Fatalf("%s did not log canonical sync_done:\n%s", alias, logText)
			}
			for _, aliased := range []string{"import_done", "wiretap_done"} {
				if strings.Contains(logText, aliased) {
					t.Fatalf("%s leaked alias event %q:\n%s", alias, aliased, logText)
				}
			}
		})
	}
}

func TestStoreImportResultPreservesArchivedMediaOnReimport(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "telecrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	now := time.Unix(1_800_000_000, 0).UTC()
	archivedPath := filepath.Join(t.TempDir(), "media", "abc")
	if err := os.MkdirAll(filepath.Dir(archivedPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archivedPath, []byte("archived"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := telegramdesktop.ImportResult{
		Stats: store.ImportStats{SourcePath: "postbox", StartedAt: now, FinishedAt: now},
		Chats: []store.Chat{{JID: "100", Kind: "chat", Name: "saved media", LastMessageAt: now, MessageCount: 1}},
		Messages: []store.Message{{
			SourcePK:  9,
			ChatJID:   "100",
			ChatName:  "saved media",
			MessageID: "0:9",
			Timestamp: now,
			MediaType: "photo",
			MediaPath: archivedPath,
			MediaSize: 123,
		}},
	}
	if err := storeImportResult(ctx, st, &first, ""); err != nil {
		t.Fatal(err)
	}

	second := telegramdesktop.ImportResult{
		Stats: first.Stats,
		Chats: first.Chats,
		Messages: []store.Message{{
			SourcePK:  9,
			ChatJID:   "100",
			ChatName:  "saved media",
			MessageID: "0:9",
			Timestamp: now,
		}},
	}
	if err := storeImportResult(ctx, st, &second, ""); err != nil {
		t.Fatal(err)
	}
	if second.Stats.MediaMessages != 1 || second.Stats.MediaFiles != 1 || second.Stats.MediaBytes != 123 {
		t.Fatalf("refreshed stats = %+v, want preserved media stats", second.Stats)
	}

	messages, err := st.Messages(ctx, store.MessageFilter{HasMedia: true, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(messages))
	}
	if messages[0].MediaPath != archivedPath || messages[0].MediaSize != 123 {
		t.Fatalf("media ref = path %q size %d, want %q/123", messages[0].MediaPath, messages[0].MediaSize, archivedPath)
	}
	if messages[0].MediaType != "photo" {
		t.Fatalf("media type = %q, want preserved photo", messages[0].MediaType)
	}
	status, err := st.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if status.MediaMessages != 1 {
		t.Fatalf("media_messages = %d, want 1", status.MediaMessages)
	}

	otherSource := telegramdesktop.ImportResult{
		Stats: store.ImportStats{SourcePath: "other-postbox", StartedAt: now, FinishedAt: now},
		Chats: first.Chats,
		Messages: []store.Message{{
			SourcePK:  9,
			ChatJID:   "100",
			ChatName:  "saved media",
			MessageID: "0:9",
			Timestamp: now,
			MediaType: "photo",
		}},
	}
	if err := storeImportResult(ctx, st, &otherSource, ""); err != nil {
		t.Fatal(err)
	}
	messages, err = st.Messages(ctx, store.MessageFilter{HasMedia: true, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 {
		t.Fatalf("messages after source switch = %d, want 1", len(messages))
	}
	if messages[0].MediaPath != "" || messages[0].MediaSize != 0 {
		t.Fatalf("media ref crossed source boundary: path %q size %d", messages[0].MediaPath, messages[0].MediaSize)
	}
}

func TestPrintImportStatsIncludesMediaArchiveStats(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	now := time.Unix(1_800_000_000, 0).UTC()
	r := &runtime{stdout: &out}

	if err := r.print(store.ImportStats{
		SourcePath:    "postbox",
		DBPath:        "/tmp/telecrawl.db",
		Chats:         2,
		Messages:      3,
		MediaMessages: 2,
		MediaFiles:    1,
		MediaBytes:    1234,
		StartedAt:     now,
		FinishedAt:    now,
	}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"media_files: 1\n", "media_bytes: 1234\n"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "remote_media_downloads:") || strings.Contains(out.String(), "remote_media_missing:") {
		t.Fatalf("zero remote media stats should be omitted:\n%s", out.String())
	}
}

func TestPrintImportStatsIncludesRemoteMediaWhenUsed(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	now := time.Unix(1_800_000_000, 0).UTC()
	r := &runtime{stdout: &out}

	if err := r.print(store.ImportStats{
		SourcePath:             "postbox",
		DBPath:                 "/tmp/telecrawl.db",
		RemoteMediaCandidates:  4,
		RemoteMediaAttempted:   3,
		RemoteMediaDownloads:   2,
		RemoteMediaMissing:     1,
		RemoteMediaUnavailable: 1,
		RemoteMediaTimeouts:    0,
		RemoteMediaErrors:      0,
		StartedAt:              now,
		FinishedAt:             now,
	}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"remote_media_candidates: 4\n",
		"remote_media_attempted: 3\n",
		"remote_media_downloads: 2\n",
		"remote_media_missing: 1\n",
		"remote_media_unavailable: 1\n",
		"remote_media_timeouts: 0\n",
		"remote_media_errors: 0\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestPrintImportStatsIncludesRemoteMediaDiagnosticsWithoutDownloads(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	now := time.Unix(1_800_000_000, 0).UTC()
	r := &runtime{stdout: &out}

	if err := r.print(store.ImportStats{
		SourcePath:             "postbox",
		DBPath:                 "/tmp/telecrawl.db",
		RemoteMediaCandidates:  4,
		RemoteMediaAttempted:   4,
		RemoteMediaUnavailable: 4,
		StartedAt:              now,
		FinishedAt:             now,
	}); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"remote_media_candidates: 4\n",
		"remote_media_attempted: 4\n",
		"remote_media_downloads: 0\n",
		"remote_media_missing: 0\n",
		"remote_media_unavailable: 4\n",
	} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("output missing %q:\n%s", want, out.String())
		}
	}
}

func TestUsageDocumentsMediaFetchOptIn(t *testing.T) {
	t.Parallel()
	var out bytes.Buffer
	printCommandUsage(&out, []string{"import"})
	if !strings.Contains(out.String(), "--fetch-media") {
		t.Fatalf("import help should document media fetch opt-in:\n%s", out.String())
	}
}

func readTestLog(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(defaultLogDir(), telecrawlLogFileName))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func clearTestLog(t *testing.T) {
	t.Helper()
	path := filepath.Join(defaultLogDir(), telecrawlLogFileName)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func makePostboxFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	lane := filepath.Join(root, "stable")
	account := filepath.Join(lane, "account-123")
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
	fixtureDB := filepath.Join("..", "telegramdesktop", "postbox", "testdata", "sqlcipher_v4.db")
	if err := copyFile(filepath.Join(dbDir, "db_sqlite"), fixtureDB); err != nil {
		t.Fatal(err)
	}
	return root
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
	hash := seed
	i := 0
	for ; i+4 <= length; i += 4 {
		k := binary.LittleEndian.Uint32(data[i : i+4])
		k *= c1
		k = (k << 15) | (k >> 17)
		k *= c2
		hash ^= k
		hash = (hash << 13) | (hash >> 19)
		hash = hash*5 + 0xe6546b64
	}
	var k uint32
	switch length & 3 {
	case 3:
		k ^= uint32(data[i+2]) << 16
		fallthrough
	case 2:
		k ^= uint32(data[i+1]) << 8
		fallthrough
	case 1:
		k ^= uint32(data[i])
		k *= c1
		k = (k << 15) | (k >> 17)
		k *= c2
		hash ^= k
	}
	hash ^= uint32(length)
	hash ^= hash >> 16
	hash *= 0x85ebca6b
	hash ^= hash >> 13
	hash *= 0xc2b2ae35
	hash ^= hash >> 16
	return int32(hash)
}
