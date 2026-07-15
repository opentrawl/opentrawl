package notes

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
	"github.com/opentrawl/opentrawl/trawlkit/output"
)

var attachmentFileContent = []byte("test image bytes")

// attachmentFixture builds a notes fixture with three attachment rows that
// exercise every supported status: a copied file (media file
// exists on disk), a missing file (ZMEDIA set, but no file anywhere under
// Accounts/*/Media/<media-uuid>/ -- the positive control that proves
// missing-detection actually fires), and a no-file row (ZMEDIA null,
// standing in for a gallery container or table row).
func attachmentFixture(t *testing.T) fixture {
	t.Helper()
	f := newFixture(t, false)
	insertAttachmentRows(t, f.db)
	writeAttachmentFile(t, f.dir, "synthetic-account", "synthetic-media-copied", "synthetic-generation", "photo.jpg", attachmentFileContent)
	return f
}

func insertAttachmentRows(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
insert into ZICCLOUDSYNCINGOBJECT (Z_PK, ZIDENTIFIER, ZFILENAME)
values
  (200, 'synthetic-media-copied', 'photo.jpg'),
  (201, 'synthetic-media-missing', 'ghost.jpg')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
insert into ZICCLOUDSYNCINGOBJECT (Z_PK, ZIDENTIFIER, ZTYPEUTI, ZNOTE, ZMEDIA)
values
  (300, 'synthetic-attachment-copied', 'public.jpeg', 10, 200),
  (301, 'synthetic-attachment-missing', 'public.jpeg', 10, 201),
  (302, 'synthetic-attachment-no-file', 'com.apple.notes.gallery', 10, null)`); err != nil {
		t.Fatal(err)
	}
}

func writeAttachmentFile(t *testing.T, groupContainerDir, account, mediaID, generation, filename string, content []byte) {
	t.Helper()
	dir := filepath.Join(groupContainerDir, "Accounts", account, "Media", mediaID, generation)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filename), content, 0o600); err != nil {
		t.Fatal(err)
	}
}

type attachmentRow struct {
	status      string
	archivePath string
	sourceBytes int64
}

func readAttachment(t *testing.T, db *sql.DB, attachmentID string) attachmentRow {
	t.Helper()
	var row attachmentRow
	err := db.QueryRow(`select status, archive_path, source_bytes from attachments where attachment_id = ?`, attachmentID).
		Scan(&row.status, &row.archivePath, &row.sourceBytes)
	if err != nil {
		t.Fatal(err)
	}
	return row
}

func countAttachments(t *testing.T, db *sql.DB) int {
	t.Helper()
	var count int
	if err := db.QueryRow(`select count(*) from attachments`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestSyncExtractsAttachmentFiles(t *testing.T) {
	f := attachmentFixture(t)
	defer f.close()
	archivePath := filepath.Join(t.TempDir(), "notes.db")
	c := New()
	c.syncStorePath = f.path()
	req := testRequest(t, archivePath, output.JSON, nil, true)
	report, err := c.Sync(context.Background(), req)
	if err != nil {
		closeStore(t, req)
		t.Fatal(err)
	}
	db := req.Store.DB()

	copiedRow := readAttachment(t, db, "synthetic-attachment-copied")
	if copiedRow.status != archive.AttachmentStatusCopied {
		t.Fatalf("copied status = %q, want %q", copiedRow.status, archive.AttachmentStatusCopied)
	}
	if copiedRow.archivePath == "" {
		t.Fatal("copied archive_path is empty")
	}
	copiedBytes, readErr := os.ReadFile(filepath.Join(filepath.Dir(archivePath), copiedRow.archivePath))
	if readErr != nil {
		closeStore(t, req)
		t.Fatal(readErr)
	}
	if string(copiedBytes) != string(attachmentFileContent) {
		t.Fatalf("copied file content = %q, want %q", copiedBytes, attachmentFileContent)
	}

	missingRow := readAttachment(t, db, "synthetic-attachment-missing")
	if missingRow.status != archive.AttachmentStatusMissing {
		t.Fatalf("missing status = %q, want %q", missingRow.status, archive.AttachmentStatusMissing)
	}
	if missingRow.archivePath != "" {
		t.Fatalf("missing archive_path = %q, want empty", missingRow.archivePath)
	}

	noFileRow := readAttachment(t, db, "synthetic-attachment-no-file")
	if noFileRow.status != archive.AttachmentStatusNoFile {
		t.Fatalf("no-file status = %q, want %q", noFileRow.status, archive.AttachmentStatusNoFile)
	}
	if noFileRow.archivePath != "" {
		t.Fatalf("no-file archive_path = %q, want empty", noFileRow.archivePath)
	}
	closeStore(t, req)

	if len(report.Warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly 1 (one missing attachment)", report.Warnings)
	}
	if want := "1 referenced attachment file is missing on disk"; report.Warnings[0] != want {
		t.Fatalf("warning = %q, want %q", report.Warnings[0], want)
	}
}

func TestSyncStoreReportsAttachmentCounts(t *testing.T) {
	f := attachmentFixture(t)
	defer f.close()
	c := New()
	c.storeLabel = "synthetic-store"

	var jsonBuf bytes.Buffer
	jsonReq := testRequest(t, filepath.Join(t.TempDir(), "notes.db"), output.JSON, &jsonBuf, true)
	jsonReq.Args = []string{f.path()}
	if err := c.runSyncStore(context.Background(), jsonReq); err != nil {
		closeStore(t, jsonReq)
		t.Fatal(err)
	}
	closeStore(t, jsonReq)

	var stats archive.SyncStats
	if err := json.Unmarshal(jsonBuf.Bytes(), &stats); err != nil {
		t.Fatal(err)
	}
	if stats.AttachmentsCopied != 1 {
		t.Fatalf("attachments copied = %d, want 1", stats.AttachmentsCopied)
	}
	if stats.AttachmentsMissing != 1 {
		t.Fatalf("attachments missing = %d, want 1", stats.AttachmentsMissing)
	}
	if stats.AttachmentsNoFile != 1 {
		t.Fatalf("attachments no_file = %d, want 1", stats.AttachmentsNoFile)
	}

	var textBuf bytes.Buffer
	textReq := testRequest(t, filepath.Join(t.TempDir(), "notes.db"), output.Text, &textBuf, true)
	textReq.Args = []string{f.path()}
	if err := c.runSyncStore(context.Background(), textReq); err != nil {
		closeStore(t, textReq)
		t.Fatal(err)
	}
	closeStore(t, textReq)
	text := textBuf.String()
	if !strings.Contains(text, "Attachments copied: 1") {
		t.Fatalf("text output = %q, want it to contain %q", text, "Attachments copied: 1")
	}
	if !strings.Contains(text, "Attachments missing: 1") {
		t.Fatalf("text output = %q, want it to contain %q", text, "Attachments missing: 1")
	}
}

func TestSyncAttachmentsDoNotDuplicateOnResync(t *testing.T) {
	f := attachmentFixture(t)
	defer f.close()
	archivePath := filepath.Join(t.TempDir(), "notes.db")
	c := New()
	c.syncStorePath = f.path()

	req := testRequest(t, archivePath, output.JSON, nil, true)
	if _, err := c.Sync(context.Background(), req); err != nil {
		closeStore(t, req)
		t.Fatal(err)
	}
	closeStore(t, req)

	req = testRequest(t, archivePath, output.JSON, nil, true)
	if _, err := c.Sync(context.Background(), req); err != nil {
		closeStore(t, req)
		t.Fatal(err)
	}
	count := countAttachments(t, req.Store.DB())
	closeStore(t, req)
	if count != 3 {
		t.Fatalf("attachments rows = %d, want 3", count)
	}
}

// A ZMEDIA reference whose target row is gone is corruption, so it must land
// in the warned-about missing count, never in the normal no-file bucket.
func TestSyncDanglingMediaReferenceIsMissing(t *testing.T) {
	f := attachmentFixture(t)
	defer f.close()
	if _, err := f.db.Exec(`
insert into ZICCLOUDSYNCINGOBJECT (Z_PK, ZIDENTIFIER, ZTYPEUTI, ZNOTE, ZMEDIA)
values (303, 'synthetic-attachment-dangling', 'public.jpeg', 10, 999)`); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "notes.db")
	c := New()
	c.syncStorePath = f.path()
	req := testRequest(t, archivePath, output.JSON, nil, true)
	report, err := c.Sync(context.Background(), req)
	if err != nil {
		closeStore(t, req)
		t.Fatal(err)
	}
	danglingRow := readAttachment(t, req.Store.DB(), "synthetic-attachment-dangling")
	closeStore(t, req)
	if danglingRow.status != archive.AttachmentStatusMissing {
		t.Fatalf("dangling status = %q, want %q", danglingRow.status, archive.AttachmentStatusMissing)
	}
	if want := "2 referenced attachment files are missing on disk"; len(report.Warnings) != 1 || report.Warnings[0] != want {
		t.Fatalf("warnings = %v, want exactly [%q]", report.Warnings, want)
	}
}
