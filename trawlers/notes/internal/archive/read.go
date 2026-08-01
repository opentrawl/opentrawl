package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/notes/internal/notestime"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

var (
	ErrNoteNotFound  = errors.New("note not found")
	ErrNoteAmbiguous = errors.New("note lookup is ambiguous")
)

func (s *Store) Status(ctx context.Context) (Status, error) {
	out := Status{ArchivePath: s.path, ArchiveBytes: fileSize(s.path)}
	db := s.store.DB()
	var err error
	// Notes counts the same population list and search browse: real notes
	// with a recovered body, leaving out Recently Deleted. Counting every row
	// in the notes table here disagreed with what list actually showed —
	// Recently Deleted notes inflated this number but never appeared browsing.
	where, args := browseWhere("")
	if err = db.QueryRowContext(ctx, `select count(*) from notes n `+where, args...).Scan(&out.Notes); err != nil {
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
	state, err := s.UpdateState(ctx)
	if err != nil {
		return Status{}, err
	}
	out.LastUpdateAt = state["last_update_at"]
	out.SourceModifiedAt = state["source_modified_at"]
	out.LastSourcePathHint = state["source_path_hint"]
	return out, nil
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
		return Note{}, ErrNoteAmbiguous
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

type SearchOptions struct {
	Limit  int
	After  time.Time
	Before time.Time
}

// Search returns one hit per matching note, not one per matching version. The
// full-text index holds a row per note version, so a note whose word appears in
// several recovered versions matches several times; Search collapses those to
// the best-ranked version of each note and hands back a note-level ref, so a
// reader browses notes, not version history. Recently Deleted notes are left
// out here as they are everywhere a reader browses.
func (s *Store) Search(ctx context.Context, query string, options SearchOptions) ([]SearchResult, int64, error) {
	query = strings.TrimSpace(query)
	ftsQuery := store.FTS5TokenQuery(query)
	if ftsQuery == "" {
		if query == "" && (!options.After.IsZero() || !options.Before.IsZero()) {
			return s.searchNotesMatchingDateFilters(ctx, options)
		}
		return nil, 0, errors.New("search query has no searchable terms")
	}
	where, args := searchWhere(ftsQuery, options.After, options.Before)
	var total int64
	if err := s.store.DB().QueryRowContext(ctx, `
select count(distinct notes_fts.note_id)
from notes_fts
join note_versions v on v.note_id = notes_fts.note_id and v.zdata_sha256 = notes_fts.zdata_sha256
left join notes n on n.note_id = notes_fts.note_id
`+where, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.store.DB().QueryContext(ctx, `
select notes_fts.note_id,
       coalesce(n.title, ''), coalesce(n.folder, ''),
       v.source_modified_at, v.first_observed_at, v.text,
       `+store.FTS5MarkedSearchResultSnippetSQLExpression("notes_fts", 2)+`,
       `+store.FTS5MarkedSearchResultSnippetSQLExpression("notes_fts", 3)+`
from notes_fts
join note_versions v on v.note_id = notes_fts.note_id and v.zdata_sha256 = notes_fts.zdata_sha256
left join notes n on n.note_id = notes_fts.note_id
`+where+`
order by rank, coalesce(nullif(v.source_modified_at, ''), v.first_observed_at) desc`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	results := []SearchResult{}
	seen := map[string]bool{}
	for rows.Next() {
		var noteID, title, folder, modified, observed, text, titleMatch, bodyMatch string
		if err := rows.Scan(&noteID, &title, &folder, &modified, &observed, &text, &titleMatch, &bodyMatch); err != nil {
			return nil, 0, err
		}
		if seen[noteID] {
			continue
		}
		seen[noteID] = true
		when := modified
		if when == "" {
			when = observed
		}
		results = append(results, SearchResult{
			Ref:     RefForNote(noteID),
			Time:    when,
			Title:   title,
			Folder:  folder,
			Snippet: store.FTS5Snippet(text, query),
			NoteID:  noteID,
			Matches: noteSearchMatches(titleMatch, bodyMatch),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	// Rank ordering above picks which version represents each note; the
	// reader-facing order is newest first, exactly as the header promises.
	sort.SliceStable(results, func(i, j int) bool {
		return contractTime(results[i].Time).After(contractTime(results[j].Time))
	})
	if options.Limit > 0 && len(results) > options.Limit {
		results = results[:options.Limit]
	}
	return results, total, nil
}

func (s *Store) searchNotesMatchingDateFilters(ctx context.Context, options SearchOptions) ([]SearchResult, int64, error) {
	noteListItemsNewestFirst, err := s.ListNotes(ctx, "", 0)
	if err != nil {
		return nil, 0, err
	}
	searchResultsNewestFirst := []SearchResult{}
	var totalDateFilteredSearchMatches int64
	for _, noteListItem := range noteListItemsNewestFirst {
		noteAssociatedTime := contractTime(noteListItem.ModifiedAt)
		if !options.After.IsZero() && noteAssociatedTime.Before(options.After) {
			continue
		}
		if !options.Before.IsZero() && noteAssociatedTime.After(options.Before) {
			continue
		}
		totalDateFilteredSearchMatches++
		if options.Limit > 0 && len(searchResultsNewestFirst) >= options.Limit {
			continue
		}
		searchResultsNewestFirst = append(searchResultsNewestFirst, SearchResult{
			Ref:    noteListItem.Ref,
			Time:   noteListItem.ModifiedAt,
			Title:  noteListItem.Title,
			Folder: noteListItem.Folder,
			NoteID: noteListItem.NoteID,
		})
	}
	return searchResultsNewestFirst, totalDateFilteredSearchMatches, nil
}

func noteSearchMatches(title, body string) []SearchMatch {
	values := []struct {
		field string
		value string
	}{{field: "title", value: title}, {field: "body", value: body}}
	matches := make([]SearchMatch, 0, len(values))
	for _, value := range values {
		if runs := store.ParseFTS5MarkedText(value.value); len(runs) > 0 {
			matches = append(matches, SearchMatch{Field: value.field, Runs: runs})
		}
	}
	return matches
}

// contractTime parses an archive timestamp for ordering; unparseable values
// sort last rather than failing a read.
func contractTime(value string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if t, err := time.Parse(layout, strings.TrimSpace(value)); err == nil {
			return t
		}
	}
	return time.Time{}
}

func searchWhere(ftsQuery string, after, before time.Time) (string, []any) {
	parts := []string{
		"notes_fts match ?",
		"coalesce(n.folder, '') <> '" + recentlyDeletedFolder + "'",
	}
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

func (s *Store) UpdateState(ctx context.Context) (map[string]string, error) {
	rows, err := s.store.DB().QueryContext(ctx, "select key, value from update_state order by key")
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
		return Note{}, ErrNoteNotFound
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
