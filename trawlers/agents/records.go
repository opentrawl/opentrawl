package agents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/opentrawl/opentrawl/trawlers/agents/internal/agentconversation"
	"github.com/opentrawl/opentrawl/trawlkit"
	ckflags "github.com/opentrawl/opentrawl/trawlkit/flags"
	"github.com/opentrawl/opentrawl/trawlkit/openrecord"
	conversation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/conversation"
	message "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/message"
	open "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/open"
	person "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/person"
	presentation "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/presentation"
	search "github.com/opentrawl/opentrawl/trawlkit/proto/trawl/search"
	"github.com/opentrawl/opentrawl/trawlkit/render"
	"github.com/opentrawl/opentrawl/trawlkit/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	personUsingTheHarnessDisplayName = "me"
	anotherAgentOrProgramDisplayName = "agent or program"
	// Turns shown on each side of an opened turn.
	openedTurnContextTurnCountOnEachSide = 5
)

type agentMessageListOptions struct {
	onlyMessagesYouSent bool
	matchingWords       string
	after               string
	before              string
}

func turnAuthorDisplayName(turn archivedAgentConversationTurn, agentDisplayName string) string {
	switch turn.turnAuthor {
	case agentconversation.AgentConversationTurnAuthorPersonUsingTheHarness:
		return personUsingTheHarnessDisplayName
	case agentconversation.AgentConversationTurnAuthorAgentInConversation:
		return agentDisplayName
	}
	return anotherAgentOrProgramDisplayName
}

func projectMessageRecord(turn archivedAgentConversationTurn, conversation archivedAgentConversation) *message.MessageRecord {
	messageRecord := &message.MessageRecord{
		MessageTime: &presentation.ArchiveRecordAssociatedTimeForDisplay{
			ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime{ExactTime: timestamppb.New(turn.turnTime)},
		},
		CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(turn.turnReference),
		PeopleRelatedToMessage: []*person.PersonRelatedToArchiveRecord{{
			PersonDisplayName:         turnAuthorDisplayName(turn, conversation.agentDisplayName),
			PersonRoleInArchiveRecord: person.PersonRoleInArchiveRecord_PERSON_ROLE_IN_ARCHIVE_RECORD_SENDER,
		}},
		MessageText:             turn.turnText,
		ConversationDisplayName: conversation.conversationTitle,
	}
	if len(turn.attachedFileNames) > 0 {
		messageRecord.MessageMedia = &message.MessageMedia{
			MessageMediaContentKind: message.MessageMediaContentKind_MESSAGE_MEDIA_CONTENT_KIND_FILE,
			MessageMediaTitle:       strings.Join(turn.attachedFileNames, ", "),
		}
	}
	return messageRecord
}

func conversationLinkInSourceApp(archived archivedAgentConversation) *conversation.ConversationLinkInSourceApp {
	if archived.linkThatOpensConversation == "" {
		return nil
	}
	return &conversation.ConversationLinkInSourceApp{
		SourceAppDisplayName:         string(archived.agentHarness),
		ConversationLinkForSourceApp: archived.linkThatOpensConversation,
	}
}

func (*App) Conversations(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, query trawlkit.ConversationQuery) (*conversation.ConversationListResponse, error) {
	if query.Unread {
		return nil, trawlkit.ErrConversationsNoReadState
	}
	if query.PersonConversationFilterResolvedAcrossTrawlerArchives != nil {
		return &conversation.ConversationListResponse{}, nil
	}
	limitClause := ""
	if !query.All && query.Limit > 0 {
		limitClause = fmt.Sprintf(" LIMIT %d", query.Limit+1)
	}
	rows, err := req.OpenedTrawlerArchiveStore.DB().QueryContext(ctx,
		`SELECT `+archivedAgentConversationColumns+` FROM agent_conversations ORDER BY most_recent_turn_time DESC`+limitClause)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	response := &conversation.ConversationListResponse{}
	for rows.Next() {
		archived, err := scanArchivedAgentConversation(rows)
		if err != nil {
			return nil, err
		}
		response.ConversationRecordsNewestFirst = append(response.ConversationRecordsNewestFirst, projectConversationRecord(archived))
	}
	if !query.All && query.Limit > 0 && len(response.ConversationRecordsNewestFirst) > query.Limit {
		response.ConversationRecordsNewestFirst = response.ConversationRecordsNewestFirst[:query.Limit]
		response.MoreConversationRecordsExist = true
	}
	return response, rows.Err()
}

func projectConversationRecord(archived archivedAgentConversation) *conversation.ConversationRecord {
	return &conversation.ConversationRecord{
		CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(archived.conversationReference),
		ConversationDisplayName:  archived.conversationTitle,
		ConversationParticipantIdentitiesObservedByTrawlerArchive: []*conversation.ConversationParticipantIdentityObservedByTrawlerArchive{
			{PersonDisplayName: archived.agentDisplayName + " in " + string(archived.agentHarness)},
		},
		MostRecentConversationActivityTime: timestamppb.New(archived.mostRecentTurnTime),
		ConversationLinkInSourceApp:        conversationLinkInSourceApp(archived),
	}
}

func (app *App) ListMessages(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, query trawlkit.TrawlerMessageListQuery) (*message.MessageListResponse, error) {
	database := req.OpenedTrawlerArchiveStore.DB()
	var conditions []string
	var arguments []any
	from := `agent_conversation_turns`
	conversationReference := ""
	if local := query.OptionalLocalConversationShortReferenceForRestrictingMessagesToOneConversation; local != "" {
		references, err := req.ResolveShortReference(ctx, trawlkit.NewLocalTrawlerShortReference(local))
		if err != nil {
			return nil, err
		}
		conversationReference = trawlkit.CanonicalArchiveRecordReferenceText(references[0])
		conditions = append(conditions, `conversation_reference = ?`)
		arguments = append(arguments, conversationReference)
	}
	if after := strings.TrimSpace(app.messageListOptions.after); after != "" {
		afterTime, err := ckflags.Date(after)
		if err != nil {
			return nil, render.DateOptionNeedsTime("after")
		}
		conditions = append(conditions, `turn_time >= ?`)
		arguments = append(arguments, archiveTimeText(afterTime))
	}
	if before := strings.TrimSpace(app.messageListOptions.before); before != "" {
		beforeTime, err := ckflags.ParseDateOrTimeThroughEndOfEnteredPrecision(before)
		if err != nil {
			return nil, render.DateOptionNeedsTime("before")
		}
		conditions = append(conditions, `turn_time <= ?`)
		arguments = append(arguments, archiveTimeText(beforeTime))
	}
	if app.messageListOptions.onlyMessagesYouSent {
		conditions = append(conditions, `turn_author = ?`)
		arguments = append(arguments, string(agentconversation.AgentConversationTurnAuthorPersonUsingTheHarness))
	}
	if words := strings.TrimSpace(app.messageListOptions.matchingWords); words != "" {
		conditions = append(conditions, `rowid IN (SELECT rowid FROM agent_conversation_turn_search WHERE agent_conversation_turn_search MATCH ?)`)
		arguments = append(arguments, store.FTS5TokenQuery(words))
	}
	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}
	var total uint64
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+from+where, arguments...).Scan(&total); err != nil {
		return nil, err
	}
	turns, err := readArchivedAgentConversationTurns(ctx, database,
		`SELECT `+archivedAgentConversationTurnColumns+` FROM `+from+where+` ORDER BY turn_time DESC LIMIT ?`,
		append(arguments, query.MaximumReturnedMessageCount)...)
	if err != nil {
		return nil, err
	}
	response := &message.MessageListResponse{
		TotalMatchingMessageCount: total,
		MoreMatchingMessagesExist: total > uint64(len(turns)),
	}
	conversations := map[string]archivedAgentConversation{}
	for _, turn := range turns {
		archived, err := cachedArchivedAgentConversation(ctx, database, conversations, turn.conversationReference)
		if err != nil {
			return nil, err
		}
		messageRecord := projectMessageRecord(turn, archived)
		// A list shows the start of each message; with words to match, it
		// shows the passage that matched. Open shows the whole message.
		if words := strings.TrimSpace(app.messageListOptions.matchingWords); words != "" {
			var matchingPassage string
			err := database.QueryRowContext(ctx, `SELECT snippet(agent_conversation_turn_search, 0, char(1), '', '…', 32)
				FROM agent_conversation_turn_search
				WHERE agent_conversation_turn_search MATCH ?
					AND rowid = (SELECT rowid FROM agent_conversation_turns WHERE turn_reference = ?)`,
				store.FTS5TokenQuery(words), turn.turnReference).Scan(&matchingPassage)
			if err != nil {
				return nil, err
			}
			messageRecord.MessageText = passageStartingShortlyBeforeFirstMatch(matchingPassage)
		}
		response.MessageRecordsNewestFirst = append(response.MessageRecordsNewestFirst, messageRecord)
	}
	if conversationReference != "" {
		archived, err := cachedArchivedAgentConversation(ctx, database, conversations, conversationReference)
		if err != nil {
			return nil, err
		}
		response.ConversationDisplayNameForMessageRecordsRestrictedToOneConversation = archived.conversationTitle
	}
	return response, nil
}

func cachedArchivedAgentConversation(
	ctx context.Context,
	database *sql.DB,
	cache map[string]archivedAgentConversation,
	conversationReference string,
) (archivedAgentConversation, error) {
	if archived, cached := cache[conversationReference]; cached {
		return archived, nil
	}
	archived, err := readArchivedAgentConversation(ctx, database, conversationReference)
	if err != nil {
		return archived, err
	}
	cache[conversationReference] = archived
	return archived, nil
}

func (*App) Search(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, query trawlkit.Query) (*search.TrawlerSearchResponse, error) {
	response := &search.TrawlerSearchResponse{}
	words := strings.TrimSpace(query.Text)
	if words == "" || query.PersonFilter.ResolvedPersonFilter() != nil || query.PersonFilter.UnresolvedPersonFilterText() != "" {
		return response, nil
	}
	conditions := []string{`agent_conversation_turn_search MATCH ?`}
	arguments := []any{store.FTS5TokenQuery(words)}
	if !query.After.IsZero() {
		conditions = append(conditions, `t.turn_time >= ?`)
		arguments = append(arguments, archiveTimeText(query.After))
	}
	if !query.Before.IsZero() {
		conditions = append(conditions, `t.turn_time <= ?`)
		arguments = append(arguments, archiveTimeText(query.Before))
	}
	matching := ` FROM agent_conversation_turn_search JOIN agent_conversation_turns t ON t.rowid = agent_conversation_turn_search.rowid WHERE ` +
		strings.Join(conditions, " AND ")
	database := req.OpenedTrawlerArchiveStore.DB()
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*)`+matching, arguments...).Scan(&response.TotalSearchMatches); err != nil {
		return nil, err
	}
	limit := query.Limit
	if limit <= 0 {
		limit = -1
	}
	rows, err := database.QueryContext(ctx,
		`SELECT t.turn_reference, t.conversation_reference, t.turn_time, t.turn_author, t.turn_text, t.attached_file_names, `+
			store.FTS5MarkedSearchResultSnippetSQLExpression("agent_conversation_turn_search", 0)+
			matching+` ORDER BY t.turn_time DESC LIMIT ?`, append(arguments, limit)...)
	if err != nil {
		return nil, err
	}
	var turns []archivedAgentConversationTurn
	for rows.Next() {
		var markedText string
		turn, err := scanArchivedAgentConversationTurn(rows, &markedText)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		turn.searchMatchMarkedText = markedText
		turns = append(turns, turn)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	conversations := map[string]archivedAgentConversation{}
	for _, turn := range turns {
		archived, err := cachedArchivedAgentConversation(ctx, database, conversations, turn.conversationReference)
		if err != nil {
			return nil, err
		}
		presentation := &search.SearchMatchPresentation{
			MatchingRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay{
				ArchiveRecordAssociatedTime: &presentation.ArchiveRecordAssociatedTimeForDisplay_ExactTime{ExactTime: timestamppb.New(turn.turnTime)},
			},
			MatchingRecordKindDisplayName:          "message",
			PeopleRelatedToMatchingRecord:          projectMessageRecord(turn, archived).GetPeopleRelatedToMessage(),
			DigitalContainerNamesNearestToBroadest: nonEmptyTexts(archived.conversationTitle, string(archived.agentHarness)),
		}
		if field := trawlkit.NewSearchMatchTextFieldFromFTS5TextRuns("Message", store.ParseFTS5MarkedText(turn.searchMatchMarkedText)); field != nil {
			presentation.SearchMatchTextFieldsInDisplayOrder = []*search.SearchMatchTextField{field}
		}
		response.TrawlerSearchMatchesInDisplayOrder = append(response.TrawlerSearchMatchesInDisplayOrder, &search.TrawlerSearchMatch{
			CanonicalRecordReference: trawlkit.NewCanonicalArchiveRecordReference(turn.turnReference),
			RecordAnchor:             trawlkit.NewRecordAnchorIdentifier(trawlkit.MatchAnchorID),
			SearchMatchPresentation:  presentation,
		})
	}
	response.MoreSearchMatchesExist = response.TotalSearchMatches > uint64(len(turns))
	return response, nil
}

func nonEmptyTexts(texts ...string) []string {
	var kept []string
	for _, text := range texts {
		if strings.TrimSpace(text) != "" {
			kept = append(kept, text)
		}
	}
	return kept
}

func (app *App) OpenRecord(ctx context.Context, req *trawlkit.TrawlerCommandExecutionRequest, local *trawlkit.LocalTrawlerShortReference) (*open.OpenRecord, error) {
	references, err := req.ResolveShortReference(ctx, local)
	if err != nil {
		return nil, err
	}
	reference := trawlkit.CanonicalArchiveRecordReferenceText(references[0])
	database := req.OpenedTrawlerArchiveStore.DB()
	record := &open.OpenRecord{
		RecordTrawler:            app.RegisteredTrawlerDeclaration().RegisteredTrawler,
		CanonicalRecordReference: references[0],
	}
	if archived, err := readArchivedAgentConversation(ctx, database, reference); err == nil {
		record.TypedOpenedRecord = &open.OpenRecord_ConversationRecord{ConversationRecord: projectConversationRecord(archived)}
		return record, openrecord.Validate(record)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	openedTurns, err := readArchivedAgentConversationTurns(ctx, database,
		`SELECT `+archivedAgentConversationTurnColumns+` FROM agent_conversation_turns WHERE turn_reference = ?`, reference)
	if err != nil {
		return nil, err
	}
	if len(openedTurns) == 0 {
		return nil, fmt.Errorf("%s", render.NoArchiveRecordHasLink(render.ArchiveRecordKindNounMessage))
	}
	openedTurn := openedTurns[0]
	archived, err := readArchivedAgentConversation(ctx, database, openedTurn.conversationReference)
	if err != nil {
		return nil, err
	}
	turnsBefore, err := readArchivedAgentConversationTurns(ctx, database,
		`SELECT `+archivedAgentConversationTurnColumns+` FROM agent_conversation_turns
		WHERE conversation_reference = ? AND (turn_time, turn_reference) < (?, ?) ORDER BY turn_time DESC, turn_reference DESC LIMIT ?`,
		openedTurn.conversationReference, archiveTimeText(openedTurn.turnTime), openedTurn.turnReference, openedTurnContextTurnCountOnEachSide+1)
	if err != nil {
		return nil, err
	}
	turnsAfter, err := readArchivedAgentConversationTurns(ctx, database,
		`SELECT `+archivedAgentConversationTurnColumns+` FROM agent_conversation_turns
		WHERE conversation_reference = ? AND (turn_time, turn_reference) > (?, ?) ORDER BY turn_time, turn_reference LIMIT ?`,
		openedTurn.conversationReference, archiveTimeText(openedTurn.turnTime), openedTurn.turnReference, openedTurnContextTurnCountOnEachSide+1)
	if err != nil {
		return nil, err
	}
	earlierOmitted := len(turnsBefore) > openedTurnContextTurnCountOnEachSide
	laterOmitted := len(turnsAfter) > openedTurnContextTurnCountOnEachSide
	turnsBefore = turnsBefore[:min(len(turnsBefore), openedTurnContextTurnCountOnEachSide)]
	turnsAfter = turnsAfter[:min(len(turnsAfter), openedTurnContextTurnCountOnEachSide)]

	var contextNewestFirst []*message.MessageRecord
	for index := len(turnsAfter) - 1; index >= 0; index-- {
		contextNewestFirst = append(contextNewestFirst, projectMessageRecord(turnsAfter[index], archived))
	}
	contextNewestFirst = append(contextNewestFirst, projectMessageRecord(openedTurn, archived))
	for _, turn := range turnsBefore {
		contextNewestFirst = append(contextNewestFirst, projectMessageRecord(turn, archived))
	}
	record.TypedOpenedRecord = &open.OpenRecord_OpenedMessageRecordWithConversationContext{
		OpenedMessageRecordWithConversationContext: &message.OpenedMessageRecordWithConversationContext{
			ConversationDisplayName:                      archived.conversationTitle,
			ConversationContextMessageRecordsNewestFirst: contextNewestFirst,
			OpenedMessageRecordReference:                 references[0],
			OpenedMessageRecordAnchor:                    trawlkit.NewRecordAnchorIdentifier(trawlkit.MatchAnchorID),
			EarlierConversationContextMessagesOmitted:    earlierOmitted,
			LaterConversationContextMessagesOmitted:      laterOmitted,
			ConversationRecordReference:                  trawlkit.NewCanonicalArchiveRecordReference(archived.conversationReference),
			ConversationLinkInSourceApp:                  conversationLinkInSourceApp(archived),
		},
	}
	return record, openrecord.Validate(record)
}

// matchStartMarker is put before each matched word by SQLite's snippet().
const matchStartMarker = "\x01"

const wordsShownBeforeFirstMatch = 3

// passageStartingShortlyBeforeFirstMatch keeps the matched words inside the
// few lines a narrow list row shows.
func passageStartingShortlyBeforeFirstMatch(passage string) string {
	beforeMatch, fromMatch, found := strings.Cut(passage, matchStartMarker)
	if !found {
		return passage
	}
	wordsBeforeMatch := strings.Fields(beforeMatch)
	if len(wordsBeforeMatch) > wordsShownBeforeFirstMatch {
		beforeMatch = "…" + strings.Join(wordsBeforeMatch[len(wordsBeforeMatch)-wordsShownBeforeFirstMatch:], " ") + " "
	}
	return strings.ReplaceAll(beforeMatch+fromMatch, matchStartMarker, "")
}
