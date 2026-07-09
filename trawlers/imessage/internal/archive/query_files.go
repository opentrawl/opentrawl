package archive

import (
	_ "embed"
	"strings"
)

//go:embed queries/chats/summary.sql
var chatSummarySQL string

//go:embed queries/chats/participant_handles.sql
var participantHandlesSQL string

//go:embed queries/messages/list.sql
var messagesListSQL string

//go:embed queries/messages/count.sql
var countMessagesSQL string

//go:embed queries/open/message.sql
var openMessageSQL string

//go:embed queries/open/before.sql
var openBeforeSQL string

//go:embed queries/open/after.sql
var openAfterSQL string

//go:embed queries/search/list.sql
var searchListSQL string

//go:embed queries/search/count.sql
var countSearchSQL string

//go:embed queries/who/rows.sql
var whoRowsSQL string

//go:embed queries/who/owner_identifiers.sql
var ownerIdentifiersSQL string

//go:embed queries/who/stats_by_candidate.sql
var whoStatsByCandidateSQL string

//go:embed queries/status/latest_message_date.sql
var latestMessageDateSQL string

//go:embed queries/status/earliest_message_date.sql
var earliestMessageDateSQL string

//go:embed queries/sync/insert_handles.sql
var insertHandlesSQL string

//go:embed queries/sync/insert_contact_mapping.sql
var insertContactMappingSQL string

//go:embed queries/sync/insert_chats.sql
var insertChatsSQL string

//go:embed queries/sync/insert_chat_participants.sql
var insertChatParticipantsSQL string

//go:embed queries/sync/insert_chat_messages.sql
var insertChatMessagesSQL string

//go:embed queries/sync/insert_messages.sql
var insertMessagesSQL string

//go:embed queries/sync/insert_messages_fts.sql
var insertMessagesFTSSQL string

// unreadReceivedExpr counts a chat's unread received messages. It uses
// count(distinct ...) because the participant join multiplies each message
// row by the participant count; a plain sum would over-count. Unread means a
// received message (is_from_me = 0) the owner has not read (is_read = 0);
// messages the owner sent carry no "unread by me" meaning and are excluded.
const unreadReceivedExpr = `count(distinct case when m.is_from_me = 0 and coalesce(m.is_read, 0) = 0 then cm.message_rowid end)`

// chatSummaryQuery builds the chat summary read. unreadSelect is the SQL that
// fills the unread column: the count expression when the archive stores read
// state, or "null" when it does not, so a pre-migration archive reports a nil
// unread rather than a fake zero. having filters to unread chats when set.
func chatSummaryQuery(where, unreadSelect, having string) string {
	query := strings.Replace(chatSummarySQL, "{{UNREAD_SELECT}}", unreadSelect, 1)
	query = strings.Replace(query, "{{WHERE}}", where, 1)
	return strings.Replace(query, "{{HAVING}}", having, 1)
}

func messagesQuery(order, tie, limitClause string) string {
	query := strings.ReplaceAll(messagesListSQL, "{{ORDER}}", order)
	query = strings.ReplaceAll(query, "{{TIE}}", tie)
	return strings.Replace(query, "{{LIMIT}}", limitClause, 1)
}

const searchWhoWith = `with resolved_who(handle_rowid) as (
  select source_rowid
  from handles
  where source_rowid in ({{WHO_HANDLES}})
)`

const searchWhoWithEmpty = `with resolved_who(handle_rowid) as (
  select source_rowid
  from handles
  where 0
)`

const searchWhoFilter = `  and (
    {{FROM_ME_FILTER}}
    m.handle_rowid in (select handle_rowid from resolved_who)
    or exists (
      select 1
      from chat_messages who_cm
      join chat_participants who_cp on who_cp.chat_rowid = who_cm.chat_rowid
      where who_cm.message_rowid = m.source_rowid
        and who_cp.handle_rowid in (select handle_rowid from resolved_who)
    )
  )`

const searchTimeAfterFilter = `  and m.date >= ?`
const searchTimeBeforeFilter = `  and m.date <= ?`

const searchFTSJoin = `join messages_fts on messages_fts.source_rowid = m.source_rowid`
const searchFTSFilter = `  and messages_fts match ?`
const searchFTSOrder = `rank, cm.chat_rowid`
const searchNewestOrder = `m.date desc, m.source_rowid desc`

func searchQuery(limitClause string, searchText string, options SearchOptions) string {
	who := candidateSearchWho(options.Who)
	sqlText := strings.Replace(searchListSQL, "{{WITH}}", searchWithClause(len(who.handleRowIDs), who.enabled), 1)
	sqlText = strings.Replace(sqlText, "{{FTS_JOIN}}", searchFTSJoinClause(searchText), 1)
	sqlText = strings.Replace(sqlText, "{{FTS_FILTER}}", searchFTSFilterClause(searchText), 1)
	sqlText = strings.Replace(sqlText, "{{WHO_FILTER}}", searchFilterClause(who), 1)
	sqlText = strings.Replace(sqlText, "{{TIME_FILTER}}", searchTimeFilterClause(options), 1)
	sqlText = strings.Replace(sqlText, "{{ORDER}}", searchOrderClause(searchText), 1)
	return strings.Replace(sqlText, "{{LIMIT}}", limitClause, 1)
}

func countSearchQuery(searchText string, options SearchOptions) string {
	who := candidateSearchWho(options.Who)
	sqlText := strings.Replace(countSearchSQL, "{{WITH}}", searchWithClause(len(who.handleRowIDs), who.enabled), 1)
	sqlText = strings.Replace(sqlText, "{{FTS_JOIN}}", searchFTSJoinClause(searchText), 1)
	sqlText = strings.Replace(sqlText, "{{FTS_FILTER}}", searchFTSFilterClause(searchText), 1)
	sqlText = strings.Replace(sqlText, "{{WHO_FILTER}}", searchFilterClause(who), 1)
	return strings.Replace(sqlText, "{{TIME_FILTER}}", searchTimeFilterClause(options), 1)
}

func whoStatsByCandidateQuery(handleRows, ownerRows int) string {
	sqlText := strings.Replace(whoStatsByCandidateSQL, "{{HANDLE_ROWS}}", valuesRows(handleRows, 2), 1)
	return strings.Replace(sqlText, "{{OWNER_ROWS}}", valuesRows(ownerRows, 1), 1)
}

func searchFTSJoinClause(query string) string {
	if strings.TrimSpace(query) == "" {
		return ""
	}
	return searchFTSJoin
}

func searchFTSFilterClause(query string) string {
	if strings.TrimSpace(query) == "" {
		return ""
	}
	return searchFTSFilter
}

func searchOrderClause(query string) string {
	if strings.TrimSpace(query) == "" {
		return searchNewestOrder
	}
	return searchFTSOrder
}

func searchWithClause(whoHandleCount int, includeWho bool) string {
	if !includeWho {
		return ""
	}
	if whoHandleCount == 0 {
		return searchWhoWithEmpty
	}
	return strings.Replace(searchWhoWith, "{{WHO_HANDLES}}", placeholders(whoHandleCount), 1)
}

func searchFilterClause(who searchWhoMatch) string {
	if !who.enabled {
		return ""
	}
	fromMeFilter := ""
	if who.includeFromMe {
		fromMeFilter = "m.is_from_me = 1 or"
	}
	return strings.Replace(searchWhoFilter, "{{FROM_ME_FILTER}}", fromMeFilter, 1)
}

func searchTimeFilterClause(options SearchOptions) string {
	var parts []string
	if options.HasAfter {
		parts = append(parts, searchTimeAfterFilter)
	}
	if options.HasBefore {
		parts = append(parts, searchTimeBeforeFilter)
	}
	return strings.Join(parts, "\n")
}

func placeholders(count int) string {
	if count <= 0 {
		return ""
	}
	return strings.TrimRight(strings.Repeat("?,", count), ",")
}

func valuesRows(count, columns int) string {
	if count <= 0 {
		return "  select " + strings.TrimRight(strings.Repeat("0,", columns), ",") + " where 0"
	}
	row := "(" + placeholders(columns) + ")"
	return "  values " + strings.TrimRight(strings.Repeat(row+",", count), ",")
}
