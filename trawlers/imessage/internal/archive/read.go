package archive

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"github.com/openclaw/crawlkit/state"
)

var (
	ErrChatNotFound    = errors.New("chat not found")
	ErrMessageNotFound = errors.New("message not found")
)

func (s *Store) Status(ctx context.Context) (Status, error) {
	status := Status{ArchivePath: s.path, ArchiveBytes: fileSize(s.path)}
	if !s.schemaOutdated {
		marker, err := s.syncMarkers(ctx)
		if err != nil {
			return Status{}, err
		}
		status.LastSyncAt = marker[stateLastSyncAt]
		status.SourcePath = marker[stateSourcePath]
		status.SourceModifiedAt = marker[stateSourceModifiedAt]
		if sourceBytes := marker[stateSourceBytes]; sourceBytes != "" {
			status.SourceBytes, _ = strconv.ParseInt(sourceBytes, 10, 64)
		}
	}
	db := s.store.DB()
	var err error
	if status.Handles, err = countTable(ctx, db, "handles"); err != nil {
		return Status{}, err
	}
	hasContactMappings, err := tableExists(ctx, db, "contact_mappings")
	if err != nil {
		return Status{}, err
	}
	if hasContactMappings {
		if status.NamedContacts, err = countTable(ctx, db, "contact_mappings"); err != nil {
			return Status{}, err
		}
	}
	if status.Chats, err = countTable(ctx, db, "chats"); err != nil {
		return Status{}, err
	}
	if status.Participants, err = countTable(ctx, db, "chat_participants"); err != nil {
		return Status{}, err
	}
	if status.ChatMessages, err = countTable(ctx, db, "chat_messages"); err != nil {
		return Status{}, err
	}
	if status.Messages, err = countTable(ctx, db, "messages"); err != nil {
		return Status{}, err
	}
	_ = db.QueryRowContext(ctx, earliestMessageDateSQL).Scan(&status.EarliestMessageDate)
	_ = db.QueryRowContext(ctx, latestMessageDateSQL).Scan(&status.LatestMessageDate)
	return status, nil
}

func (s *Store) CountChats(ctx context.Context) (int64, error) {
	return countTable(ctx, s.store.DB(), "chats")
}

func (s *Store) Messages(ctx context.Context, chatID string, limit int, asc bool) ([]MessageRow, error) {
	if s.schemaOutdated {
		return nil, ErrSchemaOutdated
	}
	id, err := parseID(chatID, "chat")
	if err != nil {
		return nil, err
	}
	order := "desc"
	tie := "desc"
	if asc {
		order = "asc"
		tie = "asc"
	}
	limitClause := ""
	args := []any{id}
	if limit > 0 {
		limitClause = "limit ?"
		args = append(args, limit)
	}
	rows, err := s.store.DB().QueryContext(ctx, messagesQuery(order, tie, limitClause), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanMessages(rows)
}

func (s *Store) CountMessages(ctx context.Context, chatID string) (int64, error) {
	id, err := parseID(chatID, "chat")
	if err != nil {
		return 0, err
	}
	var count int64
	err = s.store.DB().QueryRowContext(ctx, countMessagesSQL, id).Scan(&count)
	return count, err
}

func (s *Store) OpenMessage(ctx context.Context, messageID string, contextLimit int) (MessageContext, error) {
	if s.schemaOutdated {
		return MessageContext{}, ErrSchemaOutdated
	}
	id, err := parseID(messageID, "message")
	if err != nil {
		return MessageContext{}, err
	}
	if contextLimit < 0 {
		contextLimit = 0
	}
	targetRows, err := s.messageRows(ctx, openMessageSQL, id)
	if err != nil {
		return MessageContext{}, err
	}
	if len(targetRows) == 0 {
		return MessageContext{}, ErrMessageNotFound
	}
	target := targetRows[0]
	out := MessageContext{Message: target.MessageRow}
	if target.ChatID == "" {
		return out, nil
	}
	chat, err := s.Chat(ctx, target.ChatID)
	if err != nil {
		return MessageContext{}, err
	}
	out.Chat = chat
	if contextLimit == 0 {
		return out, nil
	}
	chatID, err := parseID(target.ChatID, "chat")
	if err != nil {
		return MessageContext{}, err
	}
	before, err := s.messageRows(ctx, openBeforeSQL, chatID, target.rawDate, target.rawDate, id, contextLimit)
	if err != nil {
		return MessageContext{}, err
	}
	for i, j := 0, len(before)-1; i < j; i, j = i+1, j-1 {
		before[i], before[j] = before[j], before[i]
	}
	after, err := s.messageRows(ctx, openAfterSQL, chatID, target.rawDate, target.rawDate, id, contextLimit)
	if err != nil {
		return MessageContext{}, err
	}
	out.Before = plainMessages(before)
	out.After = plainMessages(after)
	return out, nil
}

func (s *Store) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	page, err := s.SearchPage(ctx, query, SearchOptions{Limit: limit})
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

func (s *Store) SearchPage(ctx context.Context, query string, options SearchOptions) (SearchPage, error) {
	if s.schemaOutdated {
		return SearchPage{}, ErrSchemaOutdated
	}
	query = strings.TrimSpace(query)
	if query == "" && !hasSearchFilter(options) {
		return SearchPage{}, errors.New("search query is required")
	}
	items, err := s.searchResults(ctx, query, options)
	if err != nil {
		return SearchPage{}, err
	}
	total, err := s.countSearch(ctx, query, options)
	if err != nil {
		return SearchPage{}, err
	}
	return SearchPage{Items: items, Total: total}, nil
}

func (s *Store) searchResults(ctx context.Context, query string, options SearchOptions) ([]SearchResult, error) {
	limitClause := ""
	args := searchArgs(query, options)
	if options.Limit > 0 {
		limitClause = "limit ?"
		args = append(args, options.Limit)
	}
	rows, err := s.store.DB().QueryContext(ctx, searchQuery(limitClause, query, options), args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []SearchResult{}
	for rows.Next() {
		var messageID, chatIDValue, handleID int64
		var participantCount int64
		var fromMe, hasAttachments int
		var senderHandle, senderDisplayName, chatDisplayName string
		var result SearchResult
		var rawDate int64
		if err := rows.Scan(&messageID, &result.GUID, &chatIDValue, &result.ChatTitle, &result.ChatKind, &result.ChatParticipantCount, &handleID, &senderHandle, &senderDisplayName, &rawDate, &result.Service, &fromMe, &hasAttachments, &result.Text, &chatDisplayName, &participantCount, &result.Snippet); err != nil {
			return nil, err
		}
		result.MessageID = strconv.FormatInt(messageID, 10)
		if chatIDValue != 0 {
			result.ChatID = strconv.FormatInt(chatIDValue, 10)
		}
		if handleID != 0 {
			result.HandleID = strconv.FormatInt(handleID, 10)
		}
		result.SenderHandle = senderHandle
		result.Time = FormatAppleDateTime(rawDate)
		result.FromMe = fromMe != 0
		result.HasAttachments = hasAttachments != 0
		result.SenderLabel = senderLabel(result.FromMe, senderDisplayName, senderHandle, chatDisplayName, participantCount)
		result.Snippet = contractSnippet(result.Text, query)
		out = append(out, result)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].ChatID == "" {
			continue
		}
		handles, err := participantHandles(ctx, s.store.DB(), out[i].ChatID)
		if err != nil {
			return nil, err
		}
		out[i].ChatParticipantHandles = handles
	}
	return out, nil
}

func (s *Store) CountSearch(ctx context.Context, query string) (int64, error) {
	return s.countSearch(ctx, query, SearchOptions{})
}

func (s *Store) countSearch(ctx context.Context, query string, options SearchOptions) (int64, error) {
	query = strings.TrimSpace(query)
	if query == "" && !hasSearchFilter(options) {
		return 0, errors.New("search query is required")
	}
	var count int64
	err := s.store.DB().QueryRowContext(ctx, countSearchQuery(query, options), searchArgs(query, options)...).Scan(&count)
	return count, err
}

func searchArgs(query string, options SearchOptions) []any {
	who := candidateSearchWho(options.Who)
	args := whoFilterArgs(who)
	if strings.TrimSpace(query) != "" {
		args = append(args, ftsQuery(query))
	}
	if options.HasAfter {
		args = append(args, options.After)
	}
	if options.HasBefore {
		args = append(args, options.Before)
	}
	return args
}

func hasSearchFilter(options SearchOptions) bool {
	return options.Who != nil || options.HasAfter || options.HasBefore
}

type messageScanRow struct {
	MessageRow
	rawDate int64
}

func (s *Store) messageRows(ctx context.Context, query string, args ...any) ([]messageScanRow, error) {
	rows, err := s.store.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanMessageRows(rows)
}

func plainMessages(rows []messageScanRow) []MessageRow {
	out := make([]MessageRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.MessageRow)
	}
	return out
}

func scanMessages(rows *sql.Rows) ([]MessageRow, error) {
	scanned, err := scanMessageRows(rows)
	if err != nil {
		return nil, err
	}
	return plainMessages(scanned), nil
}

func scanMessageRows(rows *sql.Rows) ([]messageScanRow, error) {
	out := []messageScanRow{}
	for rows.Next() {
		var row messageScanRow
		var messageID, chatID, handleID int64
		var participantCount int64
		var fromMe, hasAttachments int
		var senderDisplayName, chatDisplayName string
		var rawDate int64
		if err := rows.Scan(&messageID, &row.GUID, &chatID, &handleID, &row.SenderHandle, &senderDisplayName, &rawDate, &row.Service, &fromMe, &row.Text, &hasAttachments, &chatDisplayName, &participantCount); err != nil {
			return nil, err
		}
		row.MessageID = strconv.FormatInt(messageID, 10)
		if chatID != 0 {
			row.ChatID = strconv.FormatInt(chatID, 10)
		}
		if handleID != 0 {
			row.HandleID = strconv.FormatInt(handleID, 10)
		}
		row.rawDate = rawDate
		row.Time = FormatAppleDateTime(row.rawDate)
		row.FromMe = fromMe != 0
		row.HasAttachments = hasAttachments != 0
		row.SenderLabel = senderLabel(row.FromMe, senderDisplayName, row.SenderHandle, chatDisplayName, participantCount)
		out = append(out, row)
	}
	return out, rows.Err()
}

func senderLabel(fromMe bool, displayName, handle, chatDisplayName string, participantCount int64) string {
	if fromMe {
		return "me"
	}
	if display := strings.TrimSpace(displayName); display != "" {
		return display
	}
	if participantCount <= 1 {
		if display := strings.TrimSpace(chatDisplayName); display != "" {
			return display
		}
	}
	if handle = strings.TrimSpace(handle); handle != "" {
		return handle
	}
	return "them"
}

// syncMarkers reads the scalar sync markers from the one crawlkit state.Store.
func (s *Store) syncMarkers(ctx context.Context) (map[string]string, error) {
	syncState := state.New(s.store.DB())
	out := map[string]string{}
	for _, id := range []string{stateLastSyncAt, stateSourcePath, stateSourceBytes, stateSourceModifiedAt} {
		rec, ok, err := getStateAnySource(ctx, syncState, syncEntityType, id)
		if err != nil {
			return nil, err
		}
		if ok {
			out[id] = rec.Value
		}
	}
	return out, nil
}

func getStateAnySource(ctx context.Context, syncState *state.Store, entityType, entityID string) (state.Record, bool, error) {
	for _, source := range []string{syncSource, legacySyncSource} {
		rec, ok, err := syncState.Get(ctx, source, entityType, entityID)
		if err != nil || ok {
			return rec, ok, err
		}
	}
	return state.Record{}, false, nil
}
