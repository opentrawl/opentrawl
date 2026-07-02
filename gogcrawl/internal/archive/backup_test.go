package archive

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIngestBackupMessageShardParsesRawRFC822(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "gogcrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	internalDateMS := int64(1783000000123)
	raw := strings.Join([]string{
		"From: Alice Example <alice@example.com>",
		"To: Bob Example <bob@example.com>, Carol Example <carol@example.com>",
		"Cc: Dana Example <dana@example.com>",
		"Subject: Project sync",
		"MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="b"`,
		"",
		"--b",
		"Content-Type: text/plain; charset=utf-8",
		"Content-Transfer-Encoding: quoted-printable",
		"",
		"Hello   Bob.",
		"Project=20sync body.",
		"--b",
		`Content-Type: text/plain; name="notes.txt"`,
		`Content-Disposition: attachment; filename="notes.txt"`,
		"Content-Transfer-Encoding: base64",
		"",
		"YXR0YWNobWVudCBib2R5",
		"--b--",
		"",
	}, "\r\n")
	row := `{"id":"m1","threadId":"t1","historyId":"h1","internalDate":1783000000123,"labelIds":["INBOX"],"sizeEstimate":100,"raw":"` +
		base64.RawURLEncoding.EncodeToString([]byte(raw)) + "\"}\n"
	shard := BackupShard{Path: "data/gmail/account/messages/part-000001.jsonl.gz.age", Hash: "hash1", Kind: BackupShardMessages}
	result, err := st.IngestBackupShard(ctx, shard, []byte(row))
	if err != nil {
		t.Fatal(err)
	}
	if result.Seen != 1 || result.Inserted != 1 {
		t.Fatalf("ingest result = %#v", result)
	}
	open, err := st.OpenMessage(ctx, RefPrefix+"m1")
	if err != nil {
		t.Fatal(err)
	}
	if open.Time != time.UnixMilli(internalDateMS).Local().Format(time.RFC3339) {
		t.Fatalf("time = %q", open.Time)
	}
	if open.Headers.FromName != "Alice Example" || open.Headers.FromAddress != "alice@example.com" {
		t.Fatalf("from = %#v", open.Headers)
	}
	if !strings.Contains(open.Headers.ToAddress, "bob@example.com") || !strings.Contains(open.Headers.ToAddress, "carol@example.com") {
		t.Fatalf("to = %q", open.Headers.ToAddress)
	}
	if !strings.Contains(open.Headers.CcAddress, "dana@example.com") {
		t.Fatalf("cc = %q", open.Headers.CcAddress)
	}
	if open.Body != "Hello Bob. Project sync body." {
		t.Fatalf("body = %q", open.Body)
	}
	if len(open.Attachments) != 1 {
		t.Fatalf("attachments = %#v", open.Attachments)
	}
	attachment := open.Attachments[0]
	if attachment.Filename != "notes.txt" || attachment.MIMEType != "text/plain" || attachment.Size != int64(len("attachment body")) {
		t.Fatalf("attachment = %#v", attachment)
	}
}

func TestIngestBackupLabelShardStoresLabels(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "gogcrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	shard := BackupShard{Path: "data/gmail/account/labels.jsonl.gz.age", Hash: "hash1", Kind: BackupShardLabels}
	result, err := st.IngestBackupShard(ctx, shard, []byte("{\"id\":\"INBOX\",\"name\":\"Inbox\",\"type\":\"system\"}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Labels != 1 {
		t.Fatalf("labels = %d", result.Labels)
	}
	var name string
	if err := st.store.DB().QueryRowContext(ctx, `select name from gmail_labels where id = 'INBOX'`).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Inbox" {
		t.Fatalf("label name = %q", name)
	}
}

func TestPendingBackupShardsTracksHashes(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "gogcrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	shard := BackupShard{Path: "data/gmail/account/messages/part-000001.jsonl.gz.age", Hash: "hash1", Kind: BackupShardMessages}
	if pending, err := st.PendingBackupShards(ctx, []BackupShard{shard}); err != nil || len(pending) != 1 {
		t.Fatalf("initial pending = %#v, %v", pending, err)
	}
	if _, err := st.IngestBackupShard(ctx, shard, nil); err != nil {
		t.Fatal(err)
	}
	if pending, err := st.PendingBackupShards(ctx, []BackupShard{shard}); err != nil || len(pending) != 0 {
		t.Fatalf("same hash pending = %#v, %v", pending, err)
	}
	shard.Hash = "hash2"
	if pending, err := st.PendingBackupShards(ctx, []BackupShard{shard}); err != nil || len(pending) != 1 {
		t.Fatalf("changed hash pending = %#v, %v", pending, err)
	}
}

func TestLoadBackupManifestFindsDataAndCheckpointShards(t *testing.T) {
	repo := t.TempDir()
	manifest := `{
  "services": {
    "gmail": {
      "shards": [
        {"path":"data/gmail/account/labels.jsonl.gz.age","plaintext_sha256":"labels-hash","rows":2},
        {"path":"data/gmail/account/messages/part-000001.jsonl.gz.age","plaintext_sha256":"messages-hash","rows":10},
        {"path":"checkpoints/gmail/account/messages/part-000002.jsonl.gz.age","plaintext_sha256":"checkpoint-hash","rows":3}
      ]
    }
  }
}`
	if err := os.WriteFile(filepath.Join(repo, "manifest.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	shards, err := LoadBackupManifest(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(shards) != 3 {
		t.Fatalf("shards = %#v", shards)
	}
	if shards[0].Kind != BackupShardLabels || shards[1].Kind != BackupShardMessages || shards[2].Kind != BackupShardMessages {
		t.Fatalf("shards = %#v", shards)
	}
}
