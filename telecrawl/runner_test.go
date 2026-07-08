package telecrawl

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha512"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/openclaw/crawlkit"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == crawlkit.HiddenWireSubcommand {
		os.Exit(crawlkit.Run(os.Args[1:], []crawlkit.Crawler{New()}))
	}
	os.Exit(m.Run())
}

func TestSearchJSONUsesLocalOffsetTimestamp(t *testing.T) {
	stateRoot := stateRootForRun(t)
	writeSyntheticArchive(t, context.Background(), archivePathForRun(stateRoot))
	oldLocal := time.Local
	time.Local = time.FixedZone("fixture", 2*60*60)
	defer func() { time.Local = oldLocal }()

	code, stdout, stderr := runTelecrawl(t, "--json", "search", "launch")
	if code != 0 {
		t.Fatalf("search code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var payload struct {
		Results []struct {
			Time string `json:"time"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("search json = %s err=%v", stdout, err)
	}
	if len(payload.Results) != 1 {
		t.Fatalf("results = %#v", payload.Results)
	}
	if got, want := payload.Results[0].Time, "2026-07-02T16:00:00+02:00"; got != want {
		t.Fatalf("search time = %q, want %q", got, want)
	}
}

func TestRunSyncCreatesArchiveAtResolvedStateRoot(t *testing.T) {
	stateRoot := stateRootForRun(t)
	source := makePostboxSource(t)

	report := runSyncJSON(t, "--path", source)
	if report.Added != 1 || report.Updated != 0 || report.Removed != 0 {
		t.Fatalf("sync report = %+v, want added=1 updated=0 removed=0", report)
	}
	archivePath := archivePathForRun(stateRoot)
	if _, err := os.Stat(archivePath); err != nil {
		t.Fatalf("sync archive missing: err=%v path=%s", err, archivePath)
	}
	t.Logf("sync archive exists at resolved state root: path=%s", archivePath)
}

func TestSyncRepeatedFixtureReportsOnlyChangedContent(t *testing.T) {
	stateRoot := stateRootForRun(t)
	source := makePostboxSource(t)

	first := runSyncJSON(t, "--path", source)
	if first.Added != 1 || first.Updated != 0 || first.Removed != 0 {
		t.Fatalf("first sync report = %+v, want added=1 updated=0 removed=0", first)
	}
	t.Logf("first_sync_report added=%d updated=%d removed=%d", first.Added, first.Updated, first.Removed)
	second := runSyncJSON(t, "--path", source)
	if second.Added != 0 || second.Updated != 0 || second.Removed != 0 {
		t.Fatalf("second sync report = %+v, want added=0 updated=0 removed=0", second)
	}
	t.Logf("second_sync_report added=%d updated=%d removed=%d", second.Added, second.Updated, second.Removed)

	alterArchivedMessageText(t, archivePathForRun(stateRoot), "synthetic changed archive text")
	changed := runSyncJSON(t, "--path", source)
	if changed.Added != 0 || changed.Updated != 1 || changed.Removed != 0 {
		t.Fatalf("changed sync report = %+v, want added=0 updated=1 removed=0", changed)
	}
	t.Logf("changed_content_sync_report added=%d updated=%d removed=%d", changed.Added, changed.Updated, changed.Removed)
}

func TestSyncPreservedMediaRefsDoNotCountAsUpdated(t *testing.T) {
	stateRoot := stateRootForRun(t)
	source := makePostboxSource(t)
	first := runSyncJSON(t, "--path", source)
	if first.Added != 1 || first.Updated != 0 || first.Removed != 0 {
		t.Fatalf("first sync report = %+v, want added=1 updated=0 removed=0", first)
	}
	archivePath := archivePathForRun(stateRoot)
	mediaPath := filepath.Join(stateRoot, "telegram", "media", "preserved-fixture.bin")
	preserveArchivedMessageMedia(t, archivePath, mediaPath, "preserved fixture media")

	code, stdout, stderr := runTelecrawl(t, "sync", "--path", source)
	if code != 0 {
		t.Fatalf("second sync code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	assertSyncTextReport(t, stdout, 0, 0, 0)
	assertArchivedMessageMedia(t, archivePath, mediaPath, "preserved fixture media")
	t.Logf("second_sync_actual_report:\n%s", stdout)
}

func TestSyncPreservedMediaRefsWithChatFilterDoNotCountAsUpdated(t *testing.T) {
	stateRoot := stateRootForRun(t)
	source := makePostboxSource(t)
	first := runSyncJSON(t, "--path", source)
	if first.Added != 1 || first.Updated != 0 || first.Removed != 0 {
		t.Fatalf("first sync report = %+v, want added=1 updated=0 removed=0", first)
	}
	archivePath := archivePathForRun(stateRoot)
	mediaPath := filepath.Join(stateRoot, "telegram", "media", "preserved-fixture.bin")
	preserveArchivedMessageMedia(t, archivePath, mediaPath, "preserved fixture media")

	code, stdout, stderr := runTelecrawl(t, "sync", "--path", source, "--chat", "100")
	if code != 0 {
		t.Fatalf("chat-filtered second sync code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	assertSyncTextReport(t, stdout, 0, 0, 0)
	assertArchivedMessageMedia(t, archivePath, mediaPath, "preserved fixture media")
	t.Logf("chat_filtered_second_sync_actual_report:\n%s", stdout)
}

func TestSyncFlagsChatAndFetchMedia(t *testing.T) {
	source := makePostboxSource(t)
	stateRootForRun(t)
	chat := runSyncJSON(t, "--path", source, "--chat", "100")
	if chat.Added != 1 || chat.Updated != 0 || chat.Removed != 0 {
		t.Fatalf("--chat sync report = %+v, want one added message", chat)
	}
	t.Logf("chat_sync_report added=%d updated=%d removed=%d", chat.Added, chat.Updated, chat.Removed)

	stateRootForRun(t)
	code, stdout, stderr := runTelecrawl(t, "-v", "--json", "sync", "--path", source, "--fetch-media")
	if code != 0 {
		t.Fatalf("--fetch-media code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	var report syncJSONReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("--fetch-media json = %s err=%v", stdout, err)
	}
	if report.Added != 1 {
		t.Fatalf("--fetch-media report = %+v, want one added message", report)
	}
	t.Logf("fetch_media_sync_report added=%d updated=%d removed=%d", report.Added, report.Updated, report.Removed)
	for _, want := range []string{
		"remote_media_candidates=1",
		"remote_media_attempted=0",
		"remote_media_downloads=0",
		"remote_media_unavailable=1",
	} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("--fetch-media stderr missing %q:\n%s", want, stderr)
		}
	}
	t.Log("fetch_media_stderr_contains remote_media_candidates=1 remote_media_attempted=0 remote_media_downloads=0 remote_media_unavailable=1")
}

type syncJSONReport struct {
	Added   int64 `json:"added"`
	Updated int64 `json:"updated"`
	Removed int64 `json:"removed"`
}

func runSyncJSON(t *testing.T, args ...string) syncJSONReport {
	t.Helper()
	fullArgs := append([]string{"--json", "sync"}, args...)
	code, stdout, stderr := runTelecrawl(t, fullArgs...)
	if code != 0 {
		t.Fatalf("sync %v code=%d stdout=%s stderr=%s", args, code, stdout, stderr)
	}
	var report syncJSONReport
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("sync json = %s err=%v", stdout, err)
	}
	return report
}

func runTelecrawl(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	oldStdout, oldStderr := os.Stdout, os.Stderr
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = stdoutWriter
	os.Stderr = stderrWriter
	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	}()

	var stdout, stderr bytes.Buffer
	stdoutDone := copyPipe(&stdout, stdoutReader)
	stderrDone := copyPipe(&stderr, stderrReader)
	code := crawlkit.Run(args, []crawlkit.Crawler{New()})
	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()
	if err := <-stdoutDone; err != nil {
		t.Fatal(err)
	}
	if err := <-stderrDone; err != nil {
		t.Fatal(err)
	}
	return code, stdout.String(), stderr.String()
}

func stateRootForRun(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return filepath.Join(home, ".opentrawl")
}

func archivePathForRun(stateRoot string) string {
	return filepath.Join(stateRoot, "telegram", "telegram.db")
}

func copyPipe(dst *bytes.Buffer, src *os.File) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := io.Copy(dst, src)
		_ = src.Close()
		done <- err
	}()
	return done
}

func alterArchivedMessageText(t *testing.T, archivePath, text string) {
	t.Helper()
	db, err := sql.Open("sqlite3", archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	result, err := db.ExecContext(context.Background(), `update messages set text = ? where source_pk = (select source_pk from messages order by source_pk limit 1)`, text)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("altered archive messages = %d, want 1", rows)
	}
	t.Logf("altered_archive_message_text rows=%d", rows)
}

func preserveArchivedMessageMedia(t *testing.T, archivePath, mediaPath, mediaTitle string) {
	t.Helper()
	body := []byte("synthetic preserved media")
	if err := os.MkdirAll(filepath.Dir(mediaPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mediaPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite3", archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	result, err := db.ExecContext(context.Background(), `update messages set media_title = ?, media_path = ?, media_size = ? where source_pk = (select source_pk from messages order by source_pk limit 1)`, mediaTitle, mediaPath, len(body))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("altered archive media refs = %d, want 1", rows)
	}
	t.Logf("seeded_preserved_media_ref rows=%d path=%s size=%d", rows, mediaPath, len(body))
}

func assertArchivedMessageMedia(t *testing.T, archivePath, mediaPath, mediaTitle string) {
	t.Helper()
	db, err := sql.Open("sqlite3", archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	var gotTitle, gotPath string
	var gotSize int64
	if err := db.QueryRowContext(context.Background(), `select coalesce(media_title,''),coalesce(media_path,''),coalesce(media_size,0) from messages order by source_pk limit 1`).Scan(&gotTitle, &gotPath, &gotSize); err != nil {
		t.Fatal(err)
	}
	if gotTitle != mediaTitle || gotPath != mediaPath || gotSize != int64(len("synthetic preserved media")) {
		t.Fatalf("archived media refs = title %q path %q size %d, want title %q path %q size %d", gotTitle, gotPath, gotSize, mediaTitle, mediaPath, len("synthetic preserved media"))
	}
	t.Logf("archived_media_ref_preserved title=%q path=%s size=%d", gotTitle, gotPath, gotSize)
}

func assertSyncTextReport(t *testing.T, stdout string, added, updated, removed int64) {
	t.Helper()
	want := strings.Join([]string{
		"Sync complete",
		"Added: " + strconv.FormatInt(added, 10),
		"Updated: " + strconv.FormatInt(updated, 10),
		"Removed: " + strconv.FormatInt(removed, 10),
		"",
	}, "\n")
	if stdout != want {
		t.Fatalf("sync report text = %q, want %q", stdout, want)
	}
}

func makePostboxSource(t *testing.T) string {
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
	if err := copyFile(filepath.Join(dbDir, "db_sqlite"), filepath.Join("internal", "telegramdesktop", "postbox", "testdata", "sqlcipher_v4.db")); err != nil {
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
