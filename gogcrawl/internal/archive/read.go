package archive

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func (s *Store) Status(ctx context.Context) (Status, error) {
	db := s.store.DB()
	status := Status{ArchivePath: s.path, ArchiveBytes: fileSize(s.path)}
	markers, err := s.SyncMarkers(ctx)
	if err != nil {
		return Status{}, err
	}
	if markers.HasCompleted {
		status.LastSyncAt = formatArchiveTime(markers.LastCompletedAt)
	}
	if status.Messages, err = countTable(ctx, db, "messages"); err != nil {
		return Status{}, err
	}
	if err := db.QueryRowContext(ctx, `select count(distinct from_address) from messages where trim(from_address) <> ''`).Scan(&status.Senders); err != nil {
		return Status{}, err
	}
	var oldest sql.NullInt64
	if err := db.QueryRowContext(ctx, `select min(time_unix) from messages`).Scan(&oldest); err != nil {
		return Status{}, err
	}
	if oldest.Valid {
		status.Since = int64(time.Unix(oldest.Int64, 0).Local().Year())
	}
	return status, nil
}

func (s *Store) Search(ctx context.Context, opts SearchOptions) (SearchResult, error) {
	query := strings.TrimSpace(opts.Query)
	if query == "" {
		return SearchResult{}, fmt.Errorf("search query is required")
	}
	where, args := searchWhere(opts, ftsQuery(query))
	total, err := s.countSearch(ctx, where, args)
	if err != nil {
		return SearchResult{}, err
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.store.DB().QueryContext(ctx, `
select m.id, m.time, m.from_name, m.from_address, m.subject,
       snippet(messages_fts, -1, '[', ']', '...', 12)
from messages_fts
join messages m on m.id = messages_fts.id
`+where+`
order by rank, m.time_unix desc
limit ?
`, append(args, limit)...)
	if err != nil {
		return SearchResult{}, fmt.Errorf("search messages: %w", err)
	}
	defer func() { _ = rows.Close() }()
	result := SearchResult{Query: query, TotalMatches: total, Truncated: total > int64(limit)}
	for rows.Next() {
		var id, when, fromName, fromAddress, subject, snippet string
		if err := rows.Scan(&id, &when, &fromName, &fromAddress, &subject, &snippet); err != nil {
			return SearchResult{}, err
		}
		result.Results = append(result.Results, SearchHit{
			Ref:     RefPrefix + id,
			Time:    when,
			Who:     displaySender(fromName, fromAddress),
			Where:   subject,
			Snippet: strings.TrimSpace(snippet),
		})
	}
	if err := rows.Err(); err != nil {
		return SearchResult{}, err
	}
	if result.Results == nil {
		result.Results = []SearchHit{}
	}
	return result, nil
}

func (s *Store) OpenMessage(ctx context.Context, ref string) (OpenResult, error) {
	id, err := parseRef(ref)
	if err != nil {
		return OpenResult{}, err
	}
	var out OpenResult
	var labels string
	err = s.store.DB().QueryRowContext(ctx, `
select id, thread_id, time, from_name, from_address, to_address, subject, body, labels_json
from messages
where id = ?
`, id).Scan(&out.ID, &out.ThreadID, &out.Time, &out.Headers.FromName, &out.Headers.FromAddress, &out.Headers.ToAddress, &out.Headers.Subject, &out.Body, &labels)
	if err == sql.ErrNoRows {
		return OpenResult{}, fmt.Errorf("message not found: %s", ref)
	}
	if err != nil {
		return OpenResult{}, err
	}
	out.Ref = RefPrefix + out.ID
	out.Labels = parseLabels(labels)
	return out, nil
}

func (s *Store) countSearch(ctx context.Context, where string, args []any) (int64, error) {
	var total int64
	err := s.store.DB().QueryRowContext(ctx, `
select count(*)
from messages_fts
join messages m on m.id = messages_fts.id
`+where, args...).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("count search messages: %w", err)
	}
	return total, nil
}

func searchWhere(opts SearchOptions, fts string) (string, []any) {
	clauses := []string{"where messages_fts match ?"}
	args := []any{fts}
	if opts.After != nil {
		clauses = append(clauses, "m.time_unix >= ?")
		args = append(args, opts.After.Unix())
	}
	if opts.Before != nil {
		clauses = append(clauses, "m.time_unix <= ?")
		args = append(args, opts.Before.Unix())
	}
	return strings.Join(clauses, " and "), args
}
