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

//go:embed queries/search/who_matched.sql
var searchWhoMatchedSQL string

//go:embed queries/status/latest_message_date.sql
var latestMessageDateSQL string

//go:embed queries/status/earliest_message_date.sql
var earliestMessageDateSQL string

//go:embed queries/status/sync_state.sql
var syncStateSQL string

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

//go:embed queries/sync/upsert_sync_state.sql
var upsertSyncStateSQL string

func chatSummaryQuery(where string) string {
	return strings.Replace(chatSummarySQL, "{{WHERE}}", where, 1)
}

func messagesQuery(order, tie, limitClause string) string {
	query := strings.ReplaceAll(messagesListSQL, "{{ORDER}}", order)
	query = strings.ReplaceAll(query, "{{TIE}}", tie)
	return strings.Replace(query, "{{LIMIT}}", limitClause, 1)
}

const searchWhoWith = `with matched_who(handle_rowid) as (
  select source_rowid
  from handles
  where source_rowid in ({{WHO_HANDLES}})
)`

const searchWhoWithEmpty = `with matched_who(handle_rowid) as (
  select source_rowid
  from handles
  where 0
)`

const searchWhoFilter = `  and (
    m.handle_rowid in (select handle_rowid from matched_who)
    or exists (
      select 1
      from chat_messages who_cm
      join chat_participants who_cp on who_cp.chat_rowid = who_cm.chat_rowid
      where who_cm.message_rowid = m.source_rowid
        and who_cp.handle_rowid in (select handle_rowid from matched_who)
    )
  )`

func searchQuery(limitClause string, whoHandleCount int, includeWho bool) string {
	query := strings.Replace(searchListSQL, "{{WITH}}", searchWithClause(whoHandleCount, includeWho), 1)
	query = strings.Replace(query, "{{WHO_FILTER}}", searchFilterClause(includeWho), 1)
	return strings.Replace(query, "{{LIMIT}}", limitClause, 1)
}

func countSearchQuery(whoHandleCount int, includeWho bool) string {
	query := strings.Replace(countSearchSQL, "{{WITH}}", searchWithClause(whoHandleCount, includeWho), 1)
	return strings.Replace(query, "{{WHO_FILTER}}", searchFilterClause(includeWho), 1)
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

func searchFilterClause(includeWho bool) string {
	if includeWho {
		return searchWhoFilter
	}
	return ""
}

func placeholders(count int) string {
	if count <= 0 {
		return ""
	}
	return strings.TrimRight(strings.Repeat("?,", count), ",")
}
