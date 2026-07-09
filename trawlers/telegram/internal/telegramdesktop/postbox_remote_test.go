package telegramdesktop

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gotd/td/tgerr"
	postboxpkg "github.com/opentrawl/opentrawl/trawlers/telegram/internal/telegramdesktop/postbox"
)

type postboxTreeState map[string]postboxTreeEntry

type postboxTreeEntry struct {
	mode os.FileMode
	size int64
	sum  [32]byte
}

func TestPostboxRemoteMediaRejectedSessionRefreshesThenSucceeds(t *testing.T) {
	root, lane, account := makePostboxFixture(t)
	authKey := postboxSessionTestAuthKey(7)
	writePostboxSharedData(t, lane, account, authKey)
	sources := mustPostboxSources(t, root)

	before := readPostboxTreeState(t, root)
	calls := 0
	downloader := func(ctx context.Context, nativeSession *postboxpkg.NativeSession, messages []postboxpkg.MessageRecord, indexes []int, mediaTempDir string, progress ProgressReporter) (postboxRemoteMediaStats, bool, error) {
		calls++
		if !bytes.Equal(nativeSession.AuthKey, authKey) {
			t.Fatal("live Telegram for macOS session was not read")
		}
		switch calls {
		case 1:
			return postboxRemoteMediaStats{}, false, tgerr.New(401, "AUTH_KEY_UNREGISTERED")
		case 2:
			messages[indexes[0]].MediaPath = filepath.Join(mediaTempDir, "downloaded.bin")
			return postboxRemoteMediaStats{Attempted: 1, Downloaded: 1}, true, nil
		default:
			t.Fatalf("unexpected downloader call %d", calls)
			return postboxRemoteMediaStats{}, false, nil
		}
	}

	stats, err := downloadPostboxRemoteMedia(context.Background(), postboxSessionTestMessages(sources[0].AccountID), sources, t.TempDir(), downloader, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || stats.Downloaded != 1 || stats.Missing != 0 {
		t.Fatalf("remote media = calls:%d stats:%+v", calls, stats)
	}
	assertPostboxTreeUnchanged(t, before, root)
}

func TestPostboxRemoteMediaDoubleRejectionFailsAfterRefresh(t *testing.T) {
	root, lane, account := makePostboxFixture(t)
	authKey := postboxSessionTestAuthKey(31)
	writePostboxSharedData(t, lane, account, authKey)
	sources := mustPostboxSources(t, root)

	before := readPostboxTreeState(t, root)
	calls := 0
	downloader := func(ctx context.Context, nativeSession *postboxpkg.NativeSession, messages []postboxpkg.MessageRecord, indexes []int, mediaTempDir string, progress ProgressReporter) (postboxRemoteMediaStats, bool, error) {
		calls++
		return postboxRemoteMediaStats{}, false, tgerr.New(401, "AUTH_KEY_UNREGISTERED")
	}

	_, err := downloadPostboxRemoteMedia(context.Background(), postboxSessionTestMessages(sources[0].AccountID), sources, t.TempDir(), downloader, nil)
	if !IsPostboxSessionRejected(err) {
		t.Fatalf("error = %v, want Telegram session rejection", err)
	}
	if err.Error() != "Telegram rejected the media session borrowed from Telegram for macOS (AUTH_KEY_UNREGISTERED) after refreshing it" {
		t.Fatalf("error = %q", err.Error())
	}
	if calls != 2 {
		t.Fatalf("downloader calls = %d, want 2", calls)
	}
	assertPostboxTreeUnchanged(t, before, root)
}

func TestPostboxRemoteMediaRefreshReadFailureCarriesCause(t *testing.T) {
	root, lane, account := makePostboxFixture(t)
	authKey := postboxSessionTestAuthKey(47)
	sharedData := writePostboxSharedData(t, lane, account, authKey)
	sharedPath := filepath.Join(lane, "accounts-shared-data")
	sources := mustPostboxSources(t, root)

	before := readPostboxTreeState(t, root)
	calls := 0
	downloader := func(ctx context.Context, nativeSession *postboxpkg.NativeSession, messages []postboxpkg.MessageRecord, indexes []int, mediaTempDir string, progress ProgressReporter) (postboxRemoteMediaStats, bool, error) {
		calls++
		if calls != 1 {
			t.Fatalf("unexpected downloader call %d", calls)
		}
		if err := os.Remove(sharedPath); err != nil {
			t.Fatal(err)
		}
		return postboxRemoteMediaStats{}, false, tgerr.New(401, "AUTH_KEY_UNREGISTERED")
	}

	_, err := downloadPostboxRemoteMedia(context.Background(), postboxSessionTestMessages(sources[0].AccountID), sources, t.TempDir(), downloader, nil)
	if writeErr := os.WriteFile(sharedPath, sharedData, 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	var refreshErr PostboxSessionRefreshError
	if !errors.As(err, &refreshErr) {
		t.Fatalf("error = %T %v, want refresh failure", err, err)
	}
	if !strings.Contains(err.Error(), "refreshing it from Telegram for macOS failed") {
		t.Fatalf("error = %q, want refresh-failed message", err.Error())
	}
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("error = %T %v, want wrapped file read failure", err, err)
	}
	if calls != 1 {
		t.Fatalf("downloader calls = %d, want 1", calls)
	}
	assertPostboxTreeUnchanged(t, before, root)
}

func mustPostboxSources(t *testing.T, root string) []postboxpkg.Source {
	t.Helper()
	sources, err := postboxpkg.DiscoverSources(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 {
		t.Fatalf("sources = %#v, want one", sources)
	}
	return sources
}

func writePostboxSharedData(t *testing.T, lane, account string, authKey []byte) []byte {
	t.Helper()
	accountRecordID, err := postboxpkg.AccountDirRecordID(filepath.Base(account))
	if err != nil {
		t.Fatal(err)
	}
	shared := map[string]any{
		"accounts": []any{map[string]any{
			"id":        strconv.FormatInt(accountRecordID, 10),
			"primaryId": "2",
			"datacenters": []any{
				"2",
				map[string]any{"masterKey": map[string]any{"data": base64.StdEncoding.EncodeToString(authKey)}},
			},
		}},
	}
	data, err := json.Marshal(shared)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lane, "accounts-shared-data"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	return data
}

func postboxSessionTestAuthKey(seed byte) []byte {
	authKey := make([]byte, 256)
	for i := range authKey {
		authKey[i] = byte(int(seed)+i) ^ byte(i/3)
	}
	return authKey
}

func postboxSessionTestMessages(accountID string) []postboxpkg.MessageRecord {
	return []postboxpkg.MessageRecord{{
		AccountID:          accountID,
		RawChatID:          100,
		SourcePK:           1,
		ChatID:             "100",
		MessageID:          "0:1",
		Timestamp:          "2026-01-02T03:04:05Z",
		MediaType:          "photo",
		ReferencedMediaIDs: []postboxpkg.MediaRef{{Namespace: 0, ID: 123456789}},
	}}
}

func readPostboxTreeState(t *testing.T, root string) postboxTreeState {
	t.Helper()
	state := postboxTreeState{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		item := postboxTreeEntry{mode: info.Mode().Perm(), size: info.Size()}
		if !entry.IsDir() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			item.sum = sha256.Sum256(data)
		}
		state[rel] = item
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func assertPostboxTreeUnchanged(t *testing.T, before postboxTreeState, root string) {
	t.Helper()
	after := readPostboxTreeState(t, root)
	if err := diffPostboxTreeState(before, after); err != nil {
		t.Fatal(err)
	}
}

func diffPostboxTreeState(before, after postboxTreeState) error {
	for name, want := range before {
		got, ok := after[name]
		if !ok {
			return fmt.Errorf("missing source entry %s", name)
		}
		if got != want {
			return fmt.Errorf("source entry changed: %s", name)
		}
	}
	for name := range after {
		if _, ok := before[name]; !ok {
			return fmt.Errorf("new source entry: %s", name)
		}
	}
	return nil
}
