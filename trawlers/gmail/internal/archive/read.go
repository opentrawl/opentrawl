package archive

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlkit/whomatch"
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
	if err := db.QueryRowContext(ctx, `select count(*) from messages where is_unread = 1`).Scan(&status.Unread); err != nil {
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
	whoValue := whomatch.Normalize(opts.Who)
	hasFilters := opts.After != nil || opts.Before != nil || whoValue != ""
	if query == "" && !hasFilters {
		return SearchResult{}, fmt.Errorf("search query is required")
	}
	whoFilter, err := s.resolveSearchWho(ctx, whoValue)
	if err != nil {
		return SearchResult{}, err
	}
	from, where, args, order := searchQueryParts(opts, query, whoFilter)
	// limit 0 means everything for internal callers; a positive limit caps the
	// rows and marks the result truncated.
	limit := opts.Limit
	if limit < 0 {
		limit = 0
	}
	boundedTotals := opts.BoundedTotals && limit > 0
	total := int64(0)
	if !boundedTotals {
		var err error
		total, err = s.countSearch(ctx, from, where, args)
		if err != nil {
			return SearchResult{}, err
		}
	}
	ownerEmails, err := s.OwnerEmails(ctx)
	if err != nil {
		return SearchResult{}, err
	}
	matchColumns := `'' as subject_match, '' as body_match`
	if query != "" {
		matchColumns = `
snippet(messages_fts, 1, char(57344), char(57345), '…', 32) as subject_match,
snippet(messages_fts, 2, char(57344), char(57345), '…', 32) as body_match`
	}
	querySQL := `
select m.id, m.time, m.from_name, m.from_address, m.subject, m.body, m.is_unread,
       ` + matchColumns + `
` + from + `
` + where + `
` + order
	queryArgs := args
	if limit > 0 {
		queryLimit := limit
		if boundedTotals {
			queryLimit++
		}
		querySQL += "\nlimit ?"
		queryArgs = append(queryArgs, queryLimit)
	}
	rows, err := s.store.DB().QueryContext(ctx, querySQL, queryArgs...)
	if err != nil {
		return SearchResult{}, fmt.Errorf("search messages: %w", err)
	}
	result := SearchResult{Query: query, WhoResolved: whoFilter.resolved, WhoQuery: whoFilter.query, TotalMatches: total, Truncated: limit > 0 && total > int64(limit)}
	for rows.Next() {
		var id, when, fromName, fromAddress, subject, body, subjectMatch, bodyMatch string
		var unread bool
		if err := rows.Scan(&id, &when, &fromName, &fromAddress, &subject, &body, &unread, &subjectMatch, &bodyMatch); err != nil {
			return SearchResult{}, err
		}
		ref := RefPrefix + id
		result.Results = append(result.Results, SearchHit{
			Ref:     ref,
			Time:    when,
			Who:     displaySender(fromName, fromAddress, ownerEmails),
			Where:   subject,
			Snippet: plainSnippet(query, subject, body),
			Unread:  unread,
			Matches: searchMatches(subjectMatch, bodyMatch),
		})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return SearchResult{}, err
	}
	if err := rows.Close(); err != nil {
		return SearchResult{}, err
	}
	if boundedTotals {
		if len(result.Results) > limit {
			result.Results = result.Results[:limit]
			result.TotalMatches = int64(limit + 1)
			result.TotalIsLowerBound = true
			result.Truncated = true
		} else {
			result.TotalMatches = int64(len(result.Results))
			result.Truncated = false
		}
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
select id, thread_id, time, from_name, from_address, to_address, cc_address, subject, body, labels_json, is_unread
from messages
where id = ?
`, id).Scan(&out.ID, &out.ThreadID, &out.Time, &out.Headers.FromName, &out.Headers.FromAddress, &out.Headers.ToAddress, &out.Headers.CcAddress, &out.Headers.Subject, &out.Body, &labels, &out.Unread)
	if err == sql.ErrNoRows {
		return OpenResult{}, fmt.Errorf("message not found: %s", ref)
	}
	if err != nil {
		return OpenResult{}, err
	}
	out.Ref = RefPrefix + out.ID
	out.Labels = parseLabels(labels)
	out.Attachments, err = s.messageAttachments(ctx, out.ID)
	if err != nil {
		return OpenResult{}, err
	}
	return out, nil
}

func (s *Store) messageAttachments(ctx context.Context, id string) ([]Attachment, error) {
	rows, err := s.store.DB().QueryContext(ctx, `
select filename, mime_type, size_bytes
from attachments
where message_id = ?
order by id
`, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []Attachment
	for rows.Next() {
		var attachment Attachment
		if err := rows.Scan(&attachment.Filename, &attachment.MIMEType, &attachment.Size); err != nil {
			return nil, err
		}
		out = append(out, attachment)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Store) countSearch(ctx context.Context, from string, where string, args []any) (int64, error) {
	var total int64
	err := s.store.DB().QueryRowContext(ctx, `
select count(*)
`+from+`
`+where, args...).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("count search messages: %w", err)
	}
	return total, nil
}

func searchQueryParts(opts SearchOptions, query string, who searchWhoFilter) (string, string, []any, string) {
	from := "from messages m"
	order := "order by m.time_unix desc"
	var clauses []string
	var args []any
	if strings.TrimSpace(query) != "" {
		from = "from messages_fts join messages m on m.id = messages_fts.id"
		clauses = append(clauses, "messages_fts match ?")
		args = append(args, ftsQuery(query))
		order = "order by rank, m.time_unix desc"
	}
	if opts.After != nil {
		clauses = append(clauses, "m.time_unix >= ?")
		args = append(args, opts.After.Unix())
	}
	if opts.Before != nil {
		clauses = append(clauses, "m.time_unix <= ?")
		args = append(args, opts.Before.Unix())
	}
	if who.enabled {
		if len(who.participantKeys) == 0 {
			clauses = append(clauses, "0 = 1")
		} else {
			clauses = append(clauses, `exists (
  select 1
  from message_participants mp
  where mp.message_id = m.id
    and mp.participant_key in (`+placeholders(len(who.participantKeys))+`)
)`)
			for _, key := range who.participantKeys {
				args = append(args, key)
			}
		}
	}
	if len(clauses) == 0 {
		return from, "", args, order
	}
	return from, "where " + strings.Join(clauses, " and "), args, order
}

func placeholders(count int) string {
	values := make([]string, count)
	for i := range values {
		values[i] = "?"
	}
	return strings.Join(values, ",")
}
