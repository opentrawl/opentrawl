package archive_test

import (
	"context"
	"database/sql"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
)

// realArchivePath is this machine's live notes archive. The tests in this file
// copy it (read-only source) into a temp dir and open the copy, which triggers
// the TRAWL-210 re-projection migration. They skip cleanly when absent.
func realArchivePath(t *testing.T) string {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir: %v", err)
	}
	path := filepath.Join(home, ".opentrawl", "notes", "notes.db")
	if _, err := os.Stat(path); err != nil {
		t.Skipf("real notes archive not present at %s", path)
	}
	return path
}

func copyArchive(t *testing.T, src string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "notes.db")
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return dst
}

// latestText returns the most recently observed version's projected text for a
// note, plus its status and reason.
func latestText(ctx context.Context, t *testing.T, db *sql.DB, noteID string) (text, status, reason string) {
	t.Helper()
	err := db.QueryRowContext(ctx, `
select text, text_status, unsupported_reason
from note_versions
where note_id = ?
order by source_modified_at desc, latest_observed_at desc
limit 1`, noteID).Scan(&text, &status, &reason)
	if err != nil {
		t.Fatalf("note %s: %v", noteID, err)
	}
	return text, status, reason
}

func TestReprojectRealArchiveDecodesTargets(t *testing.T) {
	ctx := context.Background()
	path := copyArchive(t, realArchivePath(t))
	st, err := archive.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	targets := []struct{ id, label string }{
		{"70B536CC-A316-439A-BDFC-F6E454174A9A", "Meals (checklist)"},
		{"E606E03F-938F-4F8C-AE06-0D06BE5B88D8", "Shopping (checklist)"},
		{"544B9AEA-2085-4F82-85CB-FE5A84A4D855", "Read meters (checklist + table)"},
		{"556F36DD-580B-412A-8232-3E832EDE4C4E", "Poker buy ins (table)"},
		{"52AAB069-C87C-4930-B7C3-2D3601310B2D", "Bread baking notes (table)"},
		{"05B25E6E-D314-43BE-A667-474DA01A9234", "garden inspiration (image)"},
		{"152E0268-FA6B-4EF9-A8E8-E1348FE70797", "PubQuiz2 (10 images)"},
		{"06C3B2AA-BA44-4A1D-8122-03AF80D5A13B", "House note for bids (calculation)"},
		{"9CF5B559-D7C0-4BB9-B6B4-76095E27A21C", "Vegetable kingdom (gallery)"},
		{"2EA270A0-03B0-41FE-A11D-DB7201813E8D", "Together to do (emoji)"},
	}
	for _, tc := range targets {
		text, status, reason := latestText(ctx, t, st.DB(), tc.id)
		t.Logf("\n===== %s [%s] status=%s reason=%q =====\n%s\n", tc.id, tc.label, status, reason, text)
	}
}

// TestReprojectRealArchiveLegacyRowsStayUnsupported confirms the 4 known legacy
// binary-plist bodies remain unsupported after migration, now with a precise
// reason instead of the old generic "not gzip or zlib" wording.
func TestReprojectRealArchiveLegacyRowsStayUnsupported(t *testing.T) {
	ctx := context.Background()
	path := copyArchive(t, realArchivePath(t))
	st, err := archive.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()

	legacy := []string{
		"267EE50F-0555-4818-A0EE-E0452C9C3F02",
		"38C424D5-52C1-4FDF-A83F-ABAD889CD041",
		"9B8AA1F5-E6A5-4592-BC1E-5533D80385FC",
		"A1A15777-5D7D-494E-A40A-B215951529D8",
	}
	for _, id := range legacy {
		_, status, reason := latestText(ctx, t, st.DB(), id)
		t.Logf("%s status=%s reason=%q", id, status, reason)
		if status != "unsupported" {
			t.Fatalf("%s status = %q, want unsupported", id, status)
		}
		if !strings.Contains(reason, "legacy binary-plist") {
			t.Fatalf("%s reason = %q, want mention of legacy binary-plist", id, reason)
		}
	}

	var total, unsupported int
	if err := st.DB().QueryRowContext(ctx, `select count(*), coalesce(sum(case when text_status='unsupported' then 1 else 0 end),0) from note_versions`).Scan(&total, &unsupported); err != nil {
		t.Fatal(err)
	}
	t.Logf("note_versions total=%d unsupported=%d", total, unsupported)
	if unsupported != 4 {
		t.Fatalf("unsupported rows = %d, want 4", unsupported)
	}
}
