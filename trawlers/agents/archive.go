package agents

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/agents/internal/agentconversation"
	"github.com/opentrawl/opentrawl/trawlkit"
)

// The archive only grows. A conversation the harness has dropped or moved
// away stays, with every turn the archive read.
const agentArchiveSchema = `
CREATE TABLE IF NOT EXISTS agent_conversations(
	conversation_reference TEXT PRIMARY KEY,
	agent_harness TEXT NOT NULL,
	agent_display_name TEXT NOT NULL,
	conversation_title TEXT NOT NULL,
	working_folder_path TEXT NOT NULL,
	link_that_opens_conversation TEXT NOT NULL,
	machine_name TEXT NOT NULL,
	most_recent_turn_time TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS agent_conversations_by_most_recent_turn_time ON agent_conversations(most_recent_turn_time);
CREATE TABLE IF NOT EXISTS agent_conversation_turns(
	turn_reference TEXT PRIMARY KEY,
	conversation_reference TEXT NOT NULL,
	turn_time TEXT NOT NULL,
	turn_author TEXT NOT NULL,
	turn_text TEXT NOT NULL,
	attached_file_names TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS agent_conversation_turns_by_conversation_and_time ON agent_conversation_turns(conversation_reference, turn_time);
CREATE INDEX IF NOT EXISTS agent_conversation_turns_by_author_and_time ON agent_conversation_turns(turn_author, turn_time);
CREATE VIRTUAL TABLE IF NOT EXISTS agent_conversation_turn_search USING fts5(turn_text, content='agent_conversation_turns', content_rowid='rowid');
CREATE TRIGGER IF NOT EXISTS agent_conversation_turn_search_after_insert AFTER INSERT ON agent_conversation_turns BEGIN
	INSERT INTO agent_conversation_turn_search(rowid, turn_text) VALUES (new.rowid, new.turn_text);
END;
CREATE TRIGGER IF NOT EXISTS agent_conversation_turn_search_after_update AFTER UPDATE OF turn_text ON agent_conversation_turns BEGIN
	INSERT INTO agent_conversation_turn_search(agent_conversation_turn_search, rowid, turn_text) VALUES ('delete', old.rowid, old.turn_text);
	INSERT INTO agent_conversation_turn_search(rowid, turn_text) VALUES (new.rowid, new.turn_text);
END;
CREATE TABLE IF NOT EXISTS agent_source_files_already_read(
	source_file_path TEXT PRIMARY KEY,
	byte_count INTEGER NOT NULL,
	modification_time TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS agent_archive_update_times(
	update_time_kind TEXT PRIMARY KEY,
	update_time TEXT NOT NULL);
`

const (
	updateTimeKindLastSuccessfullyCompletedArchiveUpdate = "last successfully completed archive update"
	updateTimeKindT3CodeStoreReadStarted                 = "t3 code store read started"
)

// archiveTimeText sorts in time order as text.
const archiveTimeTextLayout = "2006-01-02T15:04:05.000000000Z"

func archiveTimeText(value time.Time) string {
	return value.UTC().Format(archiveTimeTextLayout)
}

func timeFromArchiveTimeText(value string) time.Time {
	parsed, _ := time.Parse(archiveTimeTextLayout, value)
	return parsed
}

const attachedFileNamesSeparator = "\n"

// writeAgentConversation adds a conversation's turns and refreshes its
// details. A turn already archived under another conversation keeps it:
// Claude Code copies earlier turns into a resumed session's transcript, and
// the turn belongs to the conversation where it was first written.
func writeAgentConversation(
	ctx context.Context,
	transaction *sql.Tx,
	conversation agentconversation.AgentConversation,
	machineName string,
) (addedOrChangedTurnCount uint64, err error) {
	if len(conversation.TurnsInConversationOrder) == 0 {
		return 0, nil
	}
	if _, err := transaction.ExecContext(ctx, `INSERT INTO agent_conversations VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(conversation_reference) DO UPDATE SET
			agent_display_name = excluded.agent_display_name,
			conversation_title = excluded.conversation_title,
			working_folder_path = excluded.working_folder_path,
			link_that_opens_conversation = excluded.link_that_opens_conversation,
			most_recent_turn_time = MAX(most_recent_turn_time, excluded.most_recent_turn_time)`,
		conversation.CanonicalConversationReference,
		string(conversation.AgentHarness),
		conversation.AgentDisplayName,
		conversation.ConversationTitle,
		conversation.WorkingFolderPath,
		conversation.LinkThatOpensConversation,
		machineName,
		archiveTimeText(conversation.MostRecentTurnTime()),
	); err != nil {
		return 0, err
	}
	for _, turn := range conversation.TurnsInConversationOrder {
		writeResult, err := transaction.ExecContext(ctx, `INSERT INTO agent_conversation_turns VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(turn_reference) DO UPDATE SET
				turn_time = excluded.turn_time,
				turn_author = excluded.turn_author,
				turn_text = excluded.turn_text,
				attached_file_names = excluded.attached_file_names
			WHERE conversation_reference = excluded.conversation_reference
				AND (turn_text IS NOT excluded.turn_text OR turn_author IS NOT excluded.turn_author
					OR attached_file_names IS NOT excluded.attached_file_names)`,
			turn.CanonicalTurnReference,
			conversation.CanonicalConversationReference,
			archiveTimeText(turn.TurnTime),
			string(turn.TurnAuthor),
			turn.TurnText,
			strings.Join(turn.AttachedFileNames, attachedFileNamesSeparator),
		)
		if err != nil {
			return 0, err
		}
		if changedRowCount, _ := writeResult.RowsAffected(); changedRowCount > 0 {
			addedOrChangedTurnCount++
		}
	}
	return addedOrChangedTurnCount, nil
}

// assignShortReferencesToArchivedRecordsWithoutOne gives every conversation
// and turn a link. Assignment reads every existing link, so it runs once for
// each update, not once for each conversation.
func assignShortReferencesToArchivedRecordsWithoutOne(ctx context.Context, transaction *sql.Tx) error {
	rows, err := transaction.QueryContext(ctx, `SELECT conversation_reference FROM agent_conversations
		UNION ALL SELECT turn_reference FROM agent_conversation_turns`)
	if err != nil {
		return err
	}
	var candidates []trawlkit.ShortReferenceAssignmentCandidate
	for rows.Next() {
		var reference string
		if err := rows.Scan(&reference); err != nil {
			_ = rows.Close()
			return err
		}
		candidates = append(candidates, shortReferenceCandidate(reference))
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	return trawlkit.AssignShortReferencesForObservedArchiveRecordsUsingCallerOwnedSQLTransaction(ctx, transaction, candidates)
}

func shortReferenceCandidate(canonicalReference string) trawlkit.ShortReferenceAssignmentCandidate {
	reference := trawlkit.NewCanonicalArchiveRecordReference(canonicalReference)
	return trawlkit.ShortReferenceAssignmentCandidate{
		StableRecordReferenceUsedForShortReferenceAssignment:       reference,
		CurrentRecordReferenceReturnedWhenShortReferenceIsResolved: reference,
	}
}

type archivedAgentConversation struct {
	conversationReference     string
	agentHarness              agentconversation.AgentHarness
	agentDisplayName          string
	conversationTitle         string
	workingFolderPath         string
	linkThatOpensConversation string
	machineName               string
	mostRecentTurnTime        time.Time
}

type archivedAgentConversationTurn struct {
	turnReference         string
	conversationReference string
	turnTime              time.Time
	turnAuthor            agentconversation.AgentConversationTurnAuthor
	turnText              string
	attachedFileNames     []string
	// searchMatchMarkedText is set only on a search result.
	searchMatchMarkedText string
}

const archivedAgentConversationColumns = `conversation_reference, agent_harness, agent_display_name, conversation_title,
	working_folder_path, link_that_opens_conversation, machine_name, most_recent_turn_time`

func scanArchivedAgentConversation(row interface{ Scan(...any) error }) (archivedAgentConversation, error) {
	var conversation archivedAgentConversation
	var harness, mostRecentTurnTime string
	err := row.Scan(&conversation.conversationReference, &harness, &conversation.agentDisplayName, &conversation.conversationTitle,
		&conversation.workingFolderPath, &conversation.linkThatOpensConversation, &conversation.machineName, &mostRecentTurnTime)
	conversation.agentHarness = agentconversation.AgentHarness(harness)
	conversation.mostRecentTurnTime = timeFromArchiveTimeText(mostRecentTurnTime)
	return conversation, err
}

const archivedAgentConversationTurnColumns = `turn_reference, conversation_reference, turn_time, turn_author, turn_text, attached_file_names`

func scanArchivedAgentConversationTurn(row interface{ Scan(...any) error }, extraDestinations ...any) (archivedAgentConversationTurn, error) {
	var turn archivedAgentConversationTurn
	var turnTime, author, attachedFileNames string
	destinations := append([]any{&turn.turnReference, &turn.conversationReference, &turnTime, &author, &turn.turnText, &attachedFileNames}, extraDestinations...)
	err := row.Scan(destinations...)
	turn.turnTime = timeFromArchiveTimeText(turnTime)
	turn.turnAuthor = agentconversation.AgentConversationTurnAuthor(author)
	if attachedFileNames != "" {
		turn.attachedFileNames = strings.Split(attachedFileNames, attachedFileNamesSeparator)
	}
	return turn, err
}

func readArchivedAgentConversation(ctx context.Context, database *sql.DB, conversationReference string) (archivedAgentConversation, error) {
	return scanArchivedAgentConversation(database.QueryRowContext(ctx,
		`SELECT `+archivedAgentConversationColumns+` FROM agent_conversations WHERE conversation_reference = ?`, conversationReference))
}

func readArchivedAgentConversationTurns(ctx context.Context, database *sql.DB, query string, arguments ...any) ([]archivedAgentConversationTurn, error) {
	rows, err := database.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var turns []archivedAgentConversationTurn
	for rows.Next() {
		turn, err := scanArchivedAgentConversationTurn(rows)
		if err != nil {
			return nil, err
		}
		turns = append(turns, turn)
	}
	return turns, rows.Err()
}
