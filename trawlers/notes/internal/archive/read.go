package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/openclaw/crawlkit/store"
	"github.com/opentrawl/opentrawl/trawlers/notes/internal/notestime"
)

func (s *Store) Status(ctx context.Context) (Status, error) {
	out := Status{ArchivePath: s.path, ArchiveBytes: fileSize(s.path)}
	version, err := s.store.SchemaVersion(ctx)
	if err != nil {
		return Status{}, err
	}
	out.SchemaVersion = version
	db := s.store.DB()
	if out.Notes, err = countTable(ctx, db, "notes"); err != nil {
		return Status{}, err
	}
	if out.Versions, err = countTable(ctx, db, "note_versions"); err != nil {
		return Status{}, err
	}
	if out.DecodedVersions, err = countWhere(ctx, db, "note_versions", "text_status = 'decoded'"); err != nil {
		return Status{}, err
	}
	if out.Observations, err = countTable(ctx, db, "version_observations"); err != nil {
		return Status{}, err
	}
	out.Coverage, err = s.Coverage(ctx)
	if err != nil {
		return Status{}, err
	}
	state, err := s.SyncState(ctx)
	if err != nil {
		return Status{}, err
	}
	out.LastSyncAt = state["last_sync_at"]
	out.SourceModifiedAt = state["source_modified_at"]
	out.LastSourcePathHint = state["source_path_hint"]
	return out, nil
}

func (s *Store) Coverage(ctx context.Context) ([]Coverage, error) {
	rows, err := s.store.DB().QueryContext(ctx, `
select source_class, status, zdata_candidates, assigned_note_versions,
       unassigned_candidates, failure_reason, next_source, inspected_at
from coverage
order by source_class`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Coverage{}
	for rows.Next() {
		var item Coverage
		if err := rows.Scan(&item.SourceClass, &item.Status, &item.Candidates, &item.AssignedVersions,
			&item.UnassignedCandidates, &item.FailureReason, &item.NextSource, &item.InspectedAt); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) ResolveNote(ctx context.Context, value string) (Note, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return Note{}, errors.New("note identifier, ref or title prefix is required")
	}
	if id, ok := NoteIDFromRef(value); ok {
		value = id
	}
	if id, _, ok := VersionFromRef(value); ok {
		value = id
	}
	titlePrefix := escapeLike(value) + "%"
	rows, err := s.store.DB().QueryContext(ctx, `
select n.note_id, n.title, n.folder, n.created_at, n.modified_at, n.last_seen_at, count(v.zdata_sha256)
from notes n
left join note_versions v on v.note_id = n.note_id
where n.note_id = ?
   or lower(n.title) like lower(?) escape '\'
group by n.note_id, n.title, n.folder, n.created_at, n.modified_at, n.last_seen_at
order by case when n.note_id = ? then 0 else 1 end, n.title collate nocase`, value, titlePrefix, value)
	if err != nil {
		return Note{}, err
	}
	defer func() { _ = rows.Close() }()
	matches := []Note{}
	for rows.Next() {
		var note Note
		if err := rows.Scan(&note.ID, &note.Title, &note.Folder, &note.CreatedAt,
			&note.ModifiedAt, &note.LastSeenAt, &note.VersionCount); err != nil {
			return Note{}, err
		}
		matches = append(matches, note)
	}
	if err := rows.Err(); err != nil {
		return Note{}, err
	}
	if len(matches) == 0 {
		return s.resolveDeletedNote(ctx, value)
	}
	if len(matches) > 1 && matches[0].ID != value {
		return Note{}, fmt.Errorf("note reference %q is ambiguous (%d matches)", value, len(matches))
	}
	return matches[0], nil
}

func (s *Store) Versions(ctx context.Context, noteID string) ([]Version, error) {
	rows, err := s.store.DB().QueryContext(ctx, `
select v.note_id, v.zdata_sha256, substr(v.zdata_sha256, 1, 12), v.zdata_bytes,
       v.text_status, v.unsupported_reason, v.source_modified_at,
       v.first_observed_at, v.latest_observed_at,
       coalesce(o.source, ''), coalesce(o.source_detail, ''), coalesce(o.source_sequence, 0)
from note_versions v
left join version_observations o on o.observation_id = (
  select observation_id
  from version_observations
  where note_id = v.note_id and zdata_sha256 = v.zdata_sha256
  order by source_modified_at desc, observed_at desc, source_sequence desc, observation_id desc
  limit 1
)
where v.note_id = ?
order by coalesce(nullif(v.source_modified_at, ''), v.first_observed_at) desc,
         v.first_observed_at desc,
         v.zdata_sha256`, noteID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Version{}
	for rows.Next() {
		var item Version
		if err := rows.Scan(&item.NoteID, &item.SHA256, &item.ShortSHA, &item.ZDataBytes,
			&item.TextStatus, &item.Unsupported, &item.SourceModifiedAt,
			&item.FirstObservedAt, &item.LatestObservedAt,
			&item.Source, &item.SourceDetail, &item.SourceSequence); err != nil {
			return nil, err
		}
		item.Ref = RefForVersion(item.NoteID, item.SHA256)
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) VersionBody(ctx context.Context, noteID, shaPrefix string) (VersionBody, error) {
	shaPrefix = strings.TrimSpace(shaPrefix)
	query := `
select v.note_id, v.zdata_sha256, substr(v.zdata_sha256, 1, 12), v.zdata_bytes,
       v.text_status, v.unsupported_reason, v.source_modified_at,
       v.first_observed_at, v.latest_observed_at, v.text, v.zdata,
       coalesce(n.title, ''), coalesce(n.folder, ''),
       coalesce(o.source, ''), coalesce(o.source_detail, ''), coalesce(o.source_sequence, 0)
from note_versions v
left join notes n on n.note_id = v.note_id
left join version_observations o on o.observation_id = (
  select observation_id
  from version_observations
  where note_id = v.note_id and zdata_sha256 = v.zdata_sha256
  order by source_modified_at desc, observed_at desc, source_sequence desc, observation_id desc
  limit 1
)
where v.note_id = ?`
	args := []any{noteID}
	if shaPrefix != "" {
		query += " and v.zdata_sha256 like ? escape '\\'"
		args = append(args, escapeLike(shaPrefix)+"%")
	}
	query += `
order by coalesce(nullif(v.source_modified_at, ''), v.first_observed_at) desc,
         v.first_observed_at desc,
         v.zdata_sha256`
	rows, err := s.store.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return VersionBody{}, err
	}
	defer func() { _ = rows.Close() }()
	matches := []VersionBody{}
	for rows.Next() {
		var body VersionBody
		if err := rows.Scan(&body.NoteID, &body.SHA256, &body.ShortSHA, &body.ZDataBytes,
			&body.TextStatus, &body.Unsupported, &body.SourceModifiedAt,
			&body.FirstObservedAt, &body.LatestObservedAt, &body.Text, &body.ZData,
			&body.Title, &body.Folder, &body.Source, &body.SourceDetail, &body.SourceSequence); err != nil {
			return VersionBody{}, err
		}
		body.Ref = RefForVersion(body.NoteID, body.SHA256)
		matches = append(matches, body)
	}
	if err := rows.Err(); err != nil {
		return VersionBody{}, err
	}
	if len(matches) == 0 {
		return VersionBody{}, errors.New("no matching body version")
	}
	if shaPrefix != "" && len(matches) > 1 {
		return VersionBody{}, fmt.Errorf("version prefix %q is ambiguous (%d matches)", shaPrefix, len(matches))
	}
	return matches[0], nil
}

func (s *Store) AtTime(ctx context.Context, note Note, requested time.Time) (AtTimeResult, error) {
	requestedAt := notestime.Format(requested)
	out := AtTimeResult{RequestedTime: requestedAt, Note: note}
	rows, err := s.store.DB().QueryContext(ctx, `
select zdata_sha256
from note_versions
where note_id = ?
  and source_modified_at <> ''
  and source_modified_at <= ?
order by source_modified_at desc, first_observed_at desc, zdata_sha256
limit 2`, note.ID, requestedAt)
	if err != nil {
		return out, err
	}
	hashes := []string{}
	for rows.Next() {
		var sha string
		if err := rows.Scan(&sha); err != nil {
			_ = rows.Close()
			return out, err
		}
		hashes = append(hashes, sha)
	}
	if err := rows.Close(); err != nil {
		return out, err
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	if len(hashes) == 0 {
		out.Match = "none_before_time"
		out.Gap = "No recovered ZDATA state exists at or before the requested time. An older copied store, uncheckpointed WAL, APFS snapshot or Time Machine copy could fill this gap."
		return out, nil
	}
	body, err := s.VersionBody(ctx, note.ID, hashes[0])
	if err != nil {
		return out, err
	}
	match := "latest_modified_before"
	if body.SourceModifiedAt == requestedAt {
		match = "exact_modified_time"
	}
	out.Match = match
	out.Version = &body
	return out, nil
}

type SearchOptions struct {
	Limit  int
	After  time.Time
	Before time.Time
}

func (s *Store) Search(ctx context.Context, query string, options SearchOptions) ([]SearchResult, int64, error) {
	query = strings.TrimSpace(query)
	ftsQuery := store.FTS5TokenQuery(query)
	if ftsQuery == "" {
		return nil, 0, errors.New("search query has no searchable terms")
	}
	where, args := searchWhere(ftsQuery, options.After, options.Before)
	var total int64
	if err := s.store.DB().QueryRowContext(ctx, `
select count(*)
from notes_fts
join note_versions v on v.note_id = notes_fts.note_id and v.zdata_sha256 = notes_fts.zdata_sha256
`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	limitArg := options.Limit
	if limitArg <= 0 {
		limitArg = -1
	}
	queryArgs := append(args, limitArg)
	rows, err := s.store.DB().QueryContext(ctx, `
select notes_fts.note_id, notes_fts.zdata_sha256, substr(notes_fts.zdata_sha256, 1, 12),
       coalesce(n.title, ''), coalesce(n.folder, ''),
       v.source_modified_at, v.first_observed_at, v.text
from notes_fts
join note_versions v on v.note_id = notes_fts.note_id and v.zdata_sha256 = notes_fts.zdata_sha256
left join notes n on n.note_id = notes_fts.note_id
`+where+`
order by rank, coalesce(nullif(v.source_modified_at, ''), v.first_observed_at) desc
limit ?`, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	results := []SearchResult{}
	for rows.Next() {
		var noteID, sha, short, title, folder, modified, observed, text string
		if err := rows.Scan(&noteID, &sha, &short, &title, &folder, &modified, &observed, &text); err != nil {
			return nil, 0, err
		}
		when := modified
		if when == "" {
			when = observed
		}
		results = append(results, SearchResult{
			Ref:      RefForVersion(noteID, sha),
			Time:     when,
			Title:    title,
			Folder:   folder,
			Snippet:  store.FTS5Snippet(text, query),
			NoteID:   noteID,
			ShortSHA: short,
		})
	}
	return results, total, rows.Err()
}

func searchWhere(ftsQuery string, after, before time.Time) (string, []any) {
	parts := []string{"notes_fts match ?"}
	args := []any{ftsQuery}
	if !after.IsZero() {
		parts = append(parts, "coalesce(nullif(v.source_modified_at, ''), v.first_observed_at) >= ?")
		args = append(args, notestime.Format(after))
	}
	if !before.IsZero() {
		parts = append(parts, "coalesce(nullif(v.source_modified_at, ''), v.first_observed_at) <= ?")
		args = append(args, notestime.Format(before))
	}
	return "where " + strings.Join(parts, " and "), args
}

func (s *Store) SyncState(ctx context.Context) (map[string]string, error) {
	rows, err := s.store.DB().QueryContext(ctx, "select key, value from sync_state order by key")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, rows.Err()
}

func (s *Store) resolveDeletedNote(ctx context.Context, value string) (Note, error) {
	var note Note
	err := s.store.DB().QueryRowContext(ctx, `
select note_id, count(zdata_sha256)
from note_versions
where note_id = ?
group by note_id`, value).Scan(&note.ID, &note.VersionCount)
	if errors.Is(err, sql.ErrNoRows) {
		return Note{}, fmt.Errorf("no archived note matches %q", value)
	}
	if err != nil {
		return Note{}, err
	}
	note.Title = "(deleted note)"
	return note, nil
}

func countTable(ctx context.Context, db *sql.DB, table string) (int64, error) {
	var count int64
	err := db.QueryRowContext(ctx, `select count(*) from `+store.QuoteIdent(table)).Scan(&count)
	return count, err
}

func countWhere(ctx context.Context, db *sql.DB, table, where string) (int64, error) {
	var count int64
	err := db.QueryRowContext(ctx, `select count(*) from `+store.QuoteIdent(table)+` where `+where).Scan(&count)
	return count, err
}

func escapeLike(value string) string {
	var b strings.Builder
	for _, r := range value {
		if r == '\\' || r == '%' || r == '_' {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}
