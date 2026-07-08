package notes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/openclaw/crawlkit"
	cklog "github.com/openclaw/crawlkit/log"
	"github.com/opentrawl/opentrawl/trawlers/notes/internal/archive"
	"github.com/opentrawl/opentrawl/trawlers/notes/internal/notesdb"
	"github.com/opentrawl/opentrawl/trawlers/notes/internal/notestime"
	"github.com/opentrawl/opentrawl/trawlers/notes/internal/wal"
)

type stateSpec struct {
	offset      int64
	source      string
	detail      string
	description string
}

func (c *Crawler) Sync(ctx context.Context, req *crawlkit.Request) (*crawlkit.SyncReport, error) {
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
	return &crawlkit.SyncReport{Added: int64(stats.NewVersions), Updated: int64(stats.Observations)}, nil
}

func (c *Crawler) runSyncStore(ctx context.Context, req *crawlkit.Request) error {
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
	_, err = fmt.Fprintf(req.Out, "Sync complete\n\nVersions added: %d\nObservations stored: %d\nSource: %s\n", stats.NewVersions, stats.Observations, label)
	return err
}

func (c *Crawler) syncSource(ctx context.Context, req *crawlkit.Request, sourcePath, source, label string, replaceNotes bool) (archive.SyncStats, error) {
	start := time.Now().UTC()
	sourcePath = strings.TrimSpace(sourcePath)
	snap, err := notesdb.SnapshotPath(sourcePath)
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

func syncSnapshot(ctx context.Context, req *crawlkit.Request, st *archive.Store, snap notesdb.Snapshot, source, detail string, replaceNotes bool, start time.Time) (archive.SyncStats, error) {
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
	for i, spec := range specs {
		if req.Progress != nil {
			req.Progress(crawlkit.Progress{Phase: "source", Done: int64(i), Total: int64(len(specs)), Message: "reading Notes store state"})
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
		stateBodies := []notesdb.Body{}
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
	coverage := archive.CoverageForSync(coverageCounts(bodies), notestime.Format(start))
	bodies = dedupeBodyObservations(bodies)
	notes := archiveNotes(final.Notes)
	state := syncState(snap.SourcePath, source, detail, len(walData), len(walOffsets), start)
	stats, err := st.ApplySync(ctx, archive.SyncBatch{
		Notes:        notes,
		Bodies:       bodies,
		SyncState:    state,
		Coverage:     coverage,
		LastSeenAt:   notestime.Format(start),
		ReplaceNotes: replaceNotes,
	})
	if err != nil {
		return archive.SyncStats{}, err
	}
	stats.Notes = len(notes)
	stats.BodyReads = bodyReads
	stats.WALBytes = int64(len(walData))
	stats.WALCommits = len(walOffsets)
	stats.SourcePath = archive.SourcePathHint(snap.SourcePath)
	if req.Progress != nil {
		req.Progress(crawlkit.Progress{Phase: "archive", Done: int64(len(bodies)), Total: int64(len(bodies)), Message: "wrote Notes archive"})
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

func coverageCounts(bodies []archive.BodyInsert) map[string]archive.CoverageCount {
	candidates := map[string]int64{}
	assigned := map[string]map[string]bool{}
	for _, body := range bodies {
		sourceClass := coverageSourceClass(body)
		if sourceClass == "" {
			continue
		}
		candidates[sourceClass]++
		if assigned[sourceClass] == nil {
			assigned[sourceClass] = map[string]bool{}
		}
		assigned[sourceClass][body.NoteID+"\x00"+body.ZDataSHA256] = true
	}
	out := map[string]archive.CoverageCount{}
	for sourceClass, candidateCount := range candidates {
		out[sourceClass] = archive.CoverageCount{
			Candidates: candidateCount,
			Assigned:   int64(len(assigned[sourceClass])),
		}
	}
	return out
}

func coverageSourceClass(body archive.BodyInsert) string {
	if body.Source == "wal_prefix" || body.SourceSequence > 0 {
		return "wal_replay"
	}
	switch body.Source {
	case "live":
		return "live_store"
	case "historical_store":
		return "old_store_copies"
	default:
		return ""
	}
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
