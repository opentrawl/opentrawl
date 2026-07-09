package notes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/notes/internal/notesdb"
	"github.com/opentrawl/opentrawl/trawlers/notes/internal/notestime"
	"github.com/opentrawl/opentrawl/trawlers/notes/internal/projection"
	"github.com/opentrawl/opentrawl/trawlers/notes/internal/wal"
	"github.com/opentrawl/opentrawl/trawlkit"
	cklog "github.com/opentrawl/opentrawl/trawlkit/log"
	"github.com/opentrawl/opentrawl/trawlkit/render"
)

type stateSpec struct {
	offset      int64
	source      string
	detail      string
	description string
}

func (c *Crawler) Sync(ctx context.Context, req *trawlkit.Request) (*trawlkit.SyncReport, error) {
	sourcePath := strings.TrimSpace(c.syncStorePath)
	source := "live"
	label := strings.TrimSpace(c.syncLabel)
	if label == "" {
		label = "current"
	}
	stats, err := c.syncSource(ctx, req, sourcePath, source, label, true)
	if err != nil {
		return nil, err
	}
	report := &trawlkit.SyncReport{
		Added:    int64(stats.NewVersions),
		Updated:  int64(stats.Observations),
		Warnings: stats.SkipWarnings,
	}
	if stats.AttachmentsMissing > 0 {
		report.Warnings = append(report.Warnings, missingAttachmentsWarning(stats.AttachmentsMissing))
	}
	return report, nil
}

func missingAttachmentsWarning(count int) string {
	if count == 1 {
		return "1 referenced attachment file is missing on disk"
	}
	return fmt.Sprintf("%d referenced attachment files are missing on disk", count)
}

func (c *Crawler) runSyncStore(ctx context.Context, req *trawlkit.Request) error {
	if len(req.Args) != 1 {
		return usageError("sync-store needs one NoteStore.sqlite path")
	}
	label := strings.TrimSpace(c.storeLabel)
	if label == "" {
		return usageError("sync-store requires --label")
	}
	stats, err := c.syncSource(ctx, req, req.Args[0], "historical_store", label, false)
	if err != nil {
		return err
	}
	if req.Format == "json" {
		return writeJSON(req.Out, stats)
	}
	if _, err = fmt.Fprintf(req.Out, "Sync complete\n\nVersions added: %d\nObservations stored: %d\nAttachments copied: %d\nAttachments missing: %d\nSource: %s\n",
		stats.NewVersions, stats.Observations, stats.AttachmentsCopied, stats.AttachmentsMissing, label); err != nil {
		return err
	}
	for _, warning := range stats.SkipWarnings {
		if _, err = fmt.Fprintln(req.Out, warning); err != nil {
			return err
		}
	}
	return nil
}

func (c *Crawler) syncSource(ctx context.Context, req *trawlkit.Request, sourcePath, source, label string, replaceNotes bool) (archive.SyncStats, error) {
	start := time.Now().UTC()
	sourcePath = strings.TrimSpace(sourcePath)
	snap, err := notesdb.SnapshotPath(ctx, sourcePath)
	if err != nil {
		return archive.SyncStats{}, sourceErr(err)
	}
	defer func() { _ = snap.Close() }()
	st, err := archive.Use(ctx, req.Store, req.Paths.Archive)
	if err != nil {
		return archive.SyncStats{}, err
	}
	stats, err := syncSnapshot(ctx, req, st, snap, source, label, replaceNotes, start)
	if err != nil {
		return archive.SyncStats{}, err
	}
	if req.Log != nil {
		_ = req.Log.Info("sync_complete", strings.Join([]string{
			"source=" + logValue(source),
			"label=" + logValue(label),
			"notes=" + strconv.Itoa(stats.Notes),
			"versions_added=" + strconv.Itoa(stats.NewVersions),
			"observations=" + strconv.Itoa(stats.Observations),
		}, " "))
	}
	return stats, nil
}

func syncSnapshot(ctx context.Context, req *trawlkit.Request, st *archive.Store, snap notesdb.Snapshot, source, detail string, replaceNotes bool, start time.Time) (archive.SyncStats, error) {
	var progress *cklog.Progress
	if req.Log != nil {
		progress = req.Log.Progress(cklog.ProgressOptions{Event: "notes_sync", Unit: "states"})
	}
	walOffsets, walData, err := wal.CommitOffsetsFile(snap.Path + "-wal")
	if err != nil {
		return archive.SyncStats{}, err
	}
	specs := stateSpecs(source, detail, walOffsets, len(walData))
	prev := map[string]string{}
	noteTitles := map[string]string{}
	bodies := []archive.BodyInsert{}
	var final notesdb.FinalState
	var attachments []notesdb.Attachment
	var tableData []notesdb.TableData
	for i, spec := range specs {
		if req.Progress != nil {
			req.Progress(trawlkit.Progress{Phase: "source", Done: int64(i), Total: int64(len(specs)), Message: "reading Notes store state"})
		}
		reportLogProgress(progress, int64(i), "reading Notes store state")
		state, err := wal.Materialize(snap.Path, walData, spec.offset)
		if err != nil {
			return archive.SyncStats{}, err
		}
		db, err := notesdb.Open(ctx, state.Path)
		if err != nil {
			_ = state.Close()
			return archive.SyncStats{}, err
		}
		index, err := notesdb.ReadModificationIndex(ctx, db)
		if err != nil {
			_ = db.Close()
			_ = state.Close()
			return archive.SyncStats{}, err
		}
		changed := notesdb.ChangedSince(prev, index)
		if i == 0 {
			changed = allChanged(index)
		}
		var stateBodies []notesdb.Body
		if i == len(specs)-1 {
			final, err = notesdb.ReadFinalState(ctx, db)
			if err != nil {
				_ = db.Close()
				_ = state.Close()
				return archive.SyncStats{}, err
			}
			for _, note := range final.Notes {
				noteTitles[note.ID] = note.Title
			}
			stateBodies = final.Bodies
			// Attachments are not versioned the way note bodies are: they are
			// read once from the final state, alongside ReadFinalState, not
			// once per WAL-replay step.
			attachments, err = notesdb.ReadAttachments(ctx, db)
			if err != nil {
				_ = db.Close()
				_ = state.Close()
				return archive.SyncStats{}, err
			}
			tableData, err = readTableData(ctx, db, final.Bodies)
			if err != nil {
				_ = db.Close()
				_ = state.Close()
				return archive.SyncStats{}, err
			}
		} else {
			stateBodies, err = notesdb.ReadBodies(ctx, db, changed)
			if err != nil {
				_ = db.Close()
				_ = state.Close()
				return archive.SyncStats{}, err
			}
		}
		for _, body := range stateBodies {
			bodies = append(bodies, bodyInsert(body, noteTitles[body.NoteID], spec, start))
		}
		prev = index
		if err := db.Close(); err != nil {
			_ = state.Close()
			return archive.SyncStats{}, err
		}
		if err := state.Close(); err != nil {
			return archive.SyncStats{}, err
		}
	}
	backfillBodyTitles(bodies, noteTitles)
	bodyReads := len(bodies)
	bodies = dedupeBodyObservations(bodies)
	withBody := noteIDsWithBody(bodies)
	realNotes, skipped := splitBodilessNotes(final.Notes, withBody)
	notes := archiveNotes(realNotes)
	state := syncState(snap.SourcePath, source, detail, len(walData), len(walOffsets), start)
	groupContainerDir := filepath.Dir(snap.SourcePath)
	archiveBaseDir := filepath.Dir(req.Paths.Archive)
	attachmentInserts, err := resolveAttachments(attachments, groupContainerDir, archiveBaseDir)
	if err != nil {
		return archive.SyncStats{}, err
	}
	stats, err := st.ApplySync(ctx, archive.SyncBatch{
		Notes:        notes,
		Bodies:       bodies,
		Attachments:  attachmentInserts,
		TableData:    tableInserts(tableData, start),
		SyncState:    state,
		LastSeenAt:   notestime.Format(start),
		ReplaceNotes: replaceNotes,
	})
	if err != nil {
		return archive.SyncStats{}, err
	}
	stats.Notes = len(notes)
	stats.BodyReads = bodyReads
	stats.SkippedNoBody = len(skipped)
	stats.SkipWarnings = skipWarnings(skipped)
	if req.Log != nil && len(skipped) > 0 {
		_ = req.Log.Info("notes_skipped_no_body", strings.Join(skipWarnings(skipped), "; "))
	}
	stats.WALBytes = int64(len(walData))
	stats.WALCommits = len(walOffsets)
	stats.SourcePath = archive.SourcePathHint(snap.SourcePath)
	if req.Progress != nil {
		req.Progress(trawlkit.Progress{Phase: "archive", Done: int64(len(bodies)), Total: int64(len(bodies)), Message: "wrote Notes archive"})
	}
	reportLogProgress(progress, int64(len(specs)), "wrote Notes archive")
	return stats, nil
}

func stateSpecs(source, detail string, commits []int64, walBytes int) []stateSpec {
	out := []stateSpec{{offset: 0, source: source, detail: detail, description: "base"}}
	for i, offset := range commits {
		item := stateSpec{offset: offset, source: source, detail: detail, description: "wal-commit-" + strconv.Itoa(i+1)}
		if source == "live" {
			item.source = "wal_prefix"
			item.detail = item.description
		}
		out = append(out, item)
	}
	if walBytes > 0 {
		out = append(out, stateSpec{offset: int64(walBytes), source: source, detail: detail, description: "full-wal"})
	}
	return out
}

func bodyInsert(body notesdb.Body, title string, spec stateSpec, observed time.Time) archive.BodyInsert {
	detail := strings.TrimSpace(spec.detail)
	if detail == "" {
		detail = spec.description
	}
	return archive.BodyInsert{
		NoteID:           body.NoteID,
		ZDataSHA256:      archive.SHA256(body.ZData),
		ZData:            body.ZData,
		Source:           spec.source,
		SourceDetail:     detail,
		SourceSequence:   sequenceFromDescription(spec.description),
		SourceModifiedAt: body.SourceModifiedAt,
		ObservedAt:       notestime.Format(observed),
		Title:            title,
	}
}

func backfillBodyTitles(bodies []archive.BodyInsert, titles map[string]string) {
	for i := range bodies {
		if strings.TrimSpace(bodies[i].Title) == "" {
			bodies[i].Title = titles[bodies[i].NoteID]
		}
	}
}

func dedupeBodyObservations(bodies []archive.BodyInsert) []archive.BodyInsert {
	out := make([]archive.BodyInsert, 0, len(bodies))
	seen := map[string]int{}
	for _, body := range bodies {
		key := body.NoteID + "\x00" + body.ZDataSHA256
		if index, ok := seen[key]; ok {
			out[index] = body
			continue
		}
		seen[key] = len(out)
		out = append(out, body)
	}
	return out
}

// readTableData captures the companion table CRDT blob for every table
// embedded in the final-state bodies. Bodies that fail to decode (e.g. legacy
// binary-plist bodies) are skipped; they carry no decodable table references.
func readTableData(ctx context.Context, db *sql.DB, bodies []notesdb.Body) ([]notesdb.TableData, error) {
	seen := map[string]bool{}
	uuids := []string{}
	for _, body := range bodies {
		ids, err := projection.TableAttachmentUUIDs(body.ZData)
		if err != nil {
			continue
		}
		for _, id := range ids {
			if !seen[id] {
				seen[id] = true
				uuids = append(uuids, id)
			}
		}
	}
	return notesdb.ReadTableData(ctx, db, uuids)
}

func tableInserts(data []notesdb.TableData, observed time.Time) []archive.TableDataInsert {
	out := make([]archive.TableDataInsert, 0, len(data))
	for _, td := range data {
		out = append(out, archive.TableDataInsert{
			AttachmentUUID: td.AttachmentUUID,
			ZData:          td.ZData,
			ObservedAt:     notestime.Format(observed),
		})
	}
	return out
}

func archiveNotes(input []notesdb.Note) []archive.Note {
	out := make([]archive.Note, 0, len(input))
	for _, note := range input {
		out = append(out, archive.Note{
			ID:         note.ID,
			Title:      note.Title,
			Folder:     note.Folder,
			CreatedAt:  note.CreatedAt,
			ModifiedAt: note.ModifiedAt,
		})
	}
	return out
}

func noteIDsWithBody(bodies []archive.BodyInsert) map[string]bool {
	set := make(map[string]bool, len(bodies))
	for _, body := range bodies {
		set[body.NoteID] = true
	}
	return set
}

// splitBodilessNotes separates the notes worth archiving from the ones the
// source lists without any readable body. A body-less row is not a note we can
// show, so it is reported by reason rather than stored as an empty row
// (engineering rules 1.15).
func splitBodilessNotes(notes []notesdb.Note, withBody map[string]bool) (real, skipped []notesdb.Note) {
	for _, note := range notes {
		if withBody[note.ID] {
			real = append(real, note)
			continue
		}
		skipped = append(skipped, note)
	}
	return real, skipped
}

// skipWarnings names why each body-less note was left out, one line per reason.
// Apple leaves an empty placeholder for a note still downloading from iCloud and
// keeps an encrypted note's body locked until it is unlocked; anything else is a
// gap we cannot explain and flag for a closer look.
func skipWarnings(skipped []notesdb.Note) []string {
	if len(skipped) == 0 {
		return nil
	}
	var awaitingDownload, passwordProtected, unexplained int
	for _, note := range skipped {
		switch {
		case note.NeedsInitialFetch:
			awaitingDownload++
		case note.PasswordProtected:
			passwordProtected++
		default:
			unexplained++
		}
	}
	var out []string
	if awaitingDownload > 0 {
		out = append(out, fmt.Sprintf("Skipped %s notes still downloading from iCloud, with no body yet.", render.FormatInteger(int64(awaitingDownload))))
	}
	if passwordProtected > 0 {
		out = append(out, fmt.Sprintf("Skipped %s password-protected notes; unlock them in Notes, then sync again.", render.FormatInteger(int64(passwordProtected))))
	}
	if unexplained > 0 {
		out = append(out, fmt.Sprintf("Skipped %s notes with no body and no known reason; check the source store.", render.FormatInteger(int64(unexplained))))
	}
	return out
}

func syncState(sourcePath, source, label string, walBytes, walCommits int, syncedAt time.Time) map[string]string {
	return map[string]string{
		"last_sync_at":     notestime.Format(syncedAt),
		"source":           source,
		"source_label":     label,
		"source_path_hint": archive.SourcePathHint(sourcePath),
		"wal_bytes":        strconv.Itoa(walBytes),
		"wal_commits":      strconv.Itoa(walCommits),
	}
}

func allChanged(index map[string]string) map[string]bool {
	out := map[string]bool{}
	for id := range index {
		out[id] = true
	}
	return out
}

func sequenceFromDescription(value string) int {
	value = strings.TrimPrefix(value, "wal-commit-")
	n, _ := strconv.Atoi(value)
	return n
}

func sourceErr(err error) error {
	remedy := "grant Full Disk Access, then run trawl notes sync; or run trawl notes sync --store PATH"
	if errors.Is(err, notesdb.ErrMalformed) {
		remedy = "copy a complete NoteStore.sqlite and WAL pair, then run trawl notes sync --store PATH"
	}
	return commandErr("source_unreadable", "Apple Notes store could not be read", remedy, err)
}

func logValue(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if value == "" {
		return strconv.Quote("")
	}
	if strings.ContainsAny(value, " \t\r\n\"") {
		return strconv.Quote(value)
	}
	return value
}

func reportLogProgress(progress *cklog.Progress, done int64, message string) {
	if progress == nil {
		return
	}
	_ = progress.Report(done, message)
}

func writeJSON(w io.Writer, value any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(value)
}
