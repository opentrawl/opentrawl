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

func TestIngestBackupMessageShardSplitsRecipientListParticipants(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "gogcrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	raw := strings.Join([]string{
		"From: Sender Example <sender@example.com>",
		"To: \"'first@example.com'\" <first@example.com>, \"Second, Example\" <second@example.com>, 'third@example.com' <third@example.com>",
		"Subject: Recipient split",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"Hello.",
		"",
	}, "\r\n")
	row := backupMessageRowJSON("mrecipients", "trecipients", []byte(raw))
	shard := BackupShard{Path: "data/gmail/account/messages/part-000001.jsonl.gz.age", Hash: "hash1", Kind: BackupShardMessages}
	if _, err := st.IngestBackupShard(ctx, shard, []byte(row)); err != nil {
		t.Fatal(err)
	}
	rows, err := st.store.DB().QueryContext(ctx, `
select name, address, display_name
from message_participants
where message_id = ? and role = 'to'
order by address
`, "mrecipients")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type gotParticipant struct {
		name    string
		address string
		display string
	}
	var got []gotParticipant
	for rows.Next() {
		var participant gotParticipant
		if err := rows.Scan(&participant.name, &participant.address, &participant.display); err != nil {
			t.Fatal(err)
		}
		got = append(got, participant)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []gotParticipant{
		{address: "first@example.com", display: "first@example.com"},
		{name: "Second, Example", address: "second@example.com", display: "Second, Example"},
		{address: "third@example.com", display: "third@example.com"},
	}
	if len(got) != len(want) {
		t.Fatalf("participants = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("participant %d = %#v, want %#v", i, got[i], want[i])
		}
	}
	open, err := st.OpenMessage(ctx, RefPrefix+"mrecipients")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(open.Headers.ToAddress, "'first@example.com'") || strings.Contains(open.Headers.ToAddress, "'third@example.com'") {
		t.Fatalf("to header kept redundant quoted names: %q", open.Headers.ToAddress)
	}
}

func TestIngestBackupMessageShardDecodesLegacyCharsetsWithoutReplacement(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "gogcrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	declared1252 := strings.Join([]string{
		"From: Shop <shop@example.com>",
		"To: Bob Example <bob@example.com>",
		"Subject: =?windows-1252?Q?Get_a_=A3120_=93Apple=94_Gift_Card?=",
		"Content-Type: text/plain; charset=windows-1252",
		"Content-Transfer-Encoding: quoted-printable",
		"",
		"Get a =A3120 Apple Gift Card in =93July=94.",
		"",
	}, "\r\n")
	undeclared8Bit := []byte("From: Legacy <legacy@example.com>\r\nTo: Bob Example <bob@example.com>\r\nSubject: Legacy ")
	undeclared8Bit = append(undeclared8Bit, 0x93)
	undeclared8Bit = append(undeclared8Bit, []byte("quote")...)
	undeclared8Bit = append(undeclared8Bit, 0x94)
	undeclared8Bit = append(undeclared8Bit, []byte("\r\nContent-Type: text/plain\r\nContent-Transfer-Encoding: 8bit\r\n\r\nLegacy ")...)
	undeclared8Bit = append(undeclared8Bit, 0x93)
	undeclared8Bit = append(undeclared8Bit, []byte("quote")...)
	undeclared8Bit = append(undeclared8Bit, 0x94)
	undeclared8Bit = append(undeclared8Bit, []byte(" costs ")...)
	undeclared8Bit = append(undeclared8Bit, 0xa3)
	undeclared8Bit = append(undeclared8Bit, []byte("120.\r\n")...)
	row := backupMessageRowJSON("m1252", "t1252", []byte(declared1252)) +
		backupMessageRowJSON("m8bit", "t8bit", undeclared8Bit)
	shard := BackupShard{Path: "data/gmail/account/messages/part-000001.jsonl.gz.age", Hash: "hash1", Kind: BackupShardMessages}
	if _, err := st.IngestBackupShard(ctx, shard, []byte(row)); err != nil {
		t.Fatal(err)
	}
	open1252, err := st.OpenMessage(ctx, RefPrefix+"m1252")
	if err != nil {
		t.Fatal(err)
	}
	if open1252.Headers.Subject != "Get a \u00a3120 \u201cApple\u201d Gift Card" {
		t.Fatalf("subject = %q", open1252.Headers.Subject)
	}
	if open1252.Body != "Get a \u00a3120 Apple Gift Card in \u201cJuly\u201d." {
		t.Fatalf("body = %q", open1252.Body)
	}
	requireNoReplacementChar(t, "declared subject", open1252.Headers.Subject)
	requireNoReplacementChar(t, "declared body", open1252.Body)
	search, err := st.Search(ctx, SearchOptions{Query: "Gift", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(search.Results) != 1 {
		t.Fatalf("search results = %#v", search.Results)
	}
	requireNoReplacementChar(t, "search snippet", search.Results[0].Snippet)
	if !strings.Contains(search.Results[0].Snippet, "\u00a3120") {
		t.Fatalf("search snippet = %q", search.Results[0].Snippet)
	}
	open8Bit, err := st.OpenMessage(ctx, RefPrefix+"m8bit")
	if err != nil {
		t.Fatal(err)
	}
	if open8Bit.Headers.Subject != "Legacy \u201cquote\u201d" {
		t.Fatalf("undeclared subject = %q", open8Bit.Headers.Subject)
	}
	if open8Bit.Body != "Legacy \u201cquote\u201d costs \u00a3120." {
		t.Fatalf("undeclared body = %q", open8Bit.Body)
	}
	requireNoReplacementChar(t, "undeclared subject", open8Bit.Headers.Subject)
	requireNoReplacementChar(t, "undeclared body", open8Bit.Body)
}

func TestIngestBackupMessageShardDecodesResidualQuotedPrintableText(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "gogcrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	raw := strings.Join([]string{
		"From: Shop <shop@example.com>",
		"To: Bob Example <bob@example.com>",
		"Subject: Sender bug",
		"MIME-Version: 1.0",
		`Content-Type: multipart/alternative; boundary="alt"`,
		"",
		"--alt",
		"Content-Type: text/plain; charset=US-ASCII",
		"Content-Transfer-Encoding: 7bit",
		"",
		"Open web-vie=",
		"w?a=3DXJC92e",
		"City: M=C3=BCnchen=E2=80=8B",
		"--alt",
		"Content-Type: text/html; charset=utf-8",
		"Content-Transfer-Encoding: quoted-printable",
		"",
		"<html><body>Open web-view</body></html>",
		"--alt--",
		"",
	}, "\r\n")
	row := backupMessageRowJSON("mresidualqp", "tresidualqp", []byte(raw))
	shard := BackupShard{Path: "data/gmail/account/messages/part-000001.jsonl.gz.age", Hash: "hash1", Kind: BackupShardMessages}
	if _, err := st.IngestBackupShard(ctx, shard, []byte(row)); err != nil {
		t.Fatal(err)
	}
	open, err := st.OpenMessage(ctx, RefPrefix+"mresidualqp")
	if err != nil {
		t.Fatal(err)
	}
	want := "Open web-view?a=XJC92e City: M\u00fcnchen\u200b"
	if open.Body != want {
		t.Fatalf("body = %q, want %q", open.Body, want)
	}
}

func TestIngestBackupMessageShardKeepsLiteralQuotedPrintableText(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "gogcrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	raw := strings.Join([]string{
		"From: Shop <shop@example.com>",
		"To: Bob Example <bob@example.com>",
		"Subject: Literal token",
		"Content-Type: text/plain; charset=utf-8",
		"Content-Transfer-Encoding: 7bit",
		"",
		"Literal token =3D stays.",
		"",
	}, "\r\n")
	row := backupMessageRowJSON("mliteralqp", "tliteralqp", []byte(raw))
	shard := BackupShard{Path: "data/gmail/account/messages/part-000001.jsonl.gz.age", Hash: "hash1", Kind: BackupShardMessages}
	if _, err := st.IngestBackupShard(ctx, shard, []byte(row)); err != nil {
		t.Fatal(err)
	}
	open, err := st.OpenMessage(ctx, RefPrefix+"mliteralqp")
	if err != nil {
		t.Fatal(err)
	}
	want := "Literal token =3D stays."
	if open.Body != want {
		t.Fatalf("body = %q, want %q", open.Body, want)
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

func TestPendingBackupShardsReingestsLegacyMessageHashes(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "gogcrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	shard := BackupShard{Path: "data/gmail/account/messages/part-000001.jsonl.gz.age", Hash: "hash1", Kind: BackupShardMessages}
	_, err = st.store.DB().ExecContext(ctx, `
insert into ingested_shards(path, hash, kind, rows, ingested_at)
values (?, ?, ?, ?, ?)
`, shard.Path, shard.Hash, string(shard.Kind), 1, time.Now().Format(time.RFC3339))
	if err != nil {
		t.Fatal(err)
	}
	if pending, err := st.PendingBackupShards(ctx, []BackupShard{shard}); err != nil || len(pending) != 1 {
		t.Fatalf("legacy hash pending = %#v, %v", pending, err)
	}
	if _, err := st.IngestBackupShard(ctx, shard, nil); err != nil {
		t.Fatal(err)
	}
	if pending, err := st.PendingBackupShards(ctx, []BackupShard{shard}); err != nil || len(pending) != 0 {
		t.Fatalf("versioned hash pending = %#v, %v", pending, err)
	}
}

func TestPendingBackupShardsReingestsV4MessageHashes(t *testing.T) {
	ctx := context.Background()
	st, err := Open(ctx, filepath.Join(t.TempDir(), "gogcrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	shard := BackupShard{Path: "data/gmail/account/messages/part-000001.jsonl.gz.age", Hash: "hash1", Kind: BackupShardMessages}
	_, err = st.store.DB().ExecContext(ctx, `
insert into ingested_shards(path, hash, kind, rows, ingested_at)
values (?, ?, ?, ?, ?)
`, shard.Path, "mail-decode-v4:"+shard.Hash, string(shard.Kind), 1, time.Now().Format(time.RFC3339))
	if err != nil {
		t.Fatal(err)
	}
	if pending, err := st.PendingBackupShards(ctx, []BackupShard{shard}); err != nil || len(pending) != 1 {
		t.Fatalf("v4 hash pending = %#v, %v", pending, err)
	}
	if _, err := st.IngestBackupShard(ctx, shard, nil); err != nil {
		t.Fatal(err)
	}
	if pending, err := st.PendingBackupShards(ctx, []BackupShard{shard}); err != nil || len(pending) != 0 {
		t.Fatalf("v5 hash pending = %#v, %v", pending, err)
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

func backupMessageRowJSON(id, threadID string, raw []byte) string {
	return `{"id":"` + id + `","threadId":"` + threadID + `","historyId":"h1","internalDate":1783000000123,"labelIds":["INBOX"],"sizeEstimate":100,"raw":"` +
		base64.RawURLEncoding.EncodeToString(raw) + "\"}\n"
}

func requireNoReplacementChar(t *testing.T, label, value string) {
	t.Helper()
	if strings.ContainsRune(value, '\uFFFD') {
		t.Fatalf("%s contains U+FFFD: %q", label, value)
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
