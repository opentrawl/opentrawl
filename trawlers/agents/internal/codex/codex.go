// Package codex reads Codex threads: an index in ~/.codex/state_5.sqlite and
// one JSON-lines rollout per thread under ~/.codex/sessions.
package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/agents/internal/agentconversation"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

// CodexThreadIndexEntry is one row of Codex's own thread index.
type CodexThreadIndexEntry struct {
	CodexThreadIdentifier string
	RolloutFilePath       string
	ThreadTitle           string
	WorkingFolderPath     string
	// ThreadStartedBy is Codex's source: vscode for the Codex app, exec for
	// codex exec, or a sub-agent description.
	ThreadStartedBy string
	// ThreadSourceKind is user for a thread a person started in the app.
	ThreadSourceKind string
	ThreadOriginator string
}

// CodexThreadWasDrivenThroughT3Code reports whether T3 Code ran the thread.
// T3 Code's own store holds the same turns and records who wrote each one.
func (entry CodexThreadIndexEntry) CodexThreadWasDrivenThroughT3Code() bool {
	return entry.ThreadOriginator == "T3 Code"
}

// CodexThreadIsAnApprovalReview reports a thread Codex starts by itself to
// review an approval request. Nobody holds a conversation in it.
func (entry CodexThreadIndexEntry) CodexThreadIsAnApprovalReview() bool {
	return entry.ThreadSourceKind == "guardian_review" || strings.Contains(entry.ThreadStartedBy, `"guardian"`)
}

func (entry CodexThreadIndexEntry) userTurnsWereTypedByAPerson() bool {
	return entry.ThreadStartedBy == "vscode" && (entry.ThreadSourceKind == "user" || entry.ThreadSourceKind == "")
}

// ReadCodexThreadIndex reads every thread Codex has indexed on this machine.
func ReadCodexThreadIndex(ctx context.Context, codexStateDatabasePath string) ([]CodexThreadIndexEntry, error) {
	codexState, err := store.OpenForeignReadOnly(ctx, codexStateDatabasePath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = codexState.Close() }()
	rows, err := codexState.DB().QueryContext(ctx, `SELECT id, rollout_path, COALESCE(NULLIF(name, ''), title), cwd, source,
		COALESCE(thread_source, ''), COALESCE(originator, '') FROM threads`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var entries []CodexThreadIndexEntry
	for rows.Next() {
		var entry CodexThreadIndexEntry
		if err := rows.Scan(&entry.CodexThreadIdentifier, &entry.RolloutFilePath, &entry.ThreadTitle, &entry.WorkingFolderPath,
			&entry.ThreadStartedBy, &entry.ThreadSourceKind, &entry.ThreadOriginator); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

type codexRolloutLine struct {
	Timestamp time.Time `json:"timestamp"`
	Type      string    `json:"type"`
	Payload   struct {
		Type string `json:"type"`
		// Message holds the text of an older rollout's user_message or agent_message event.
		Message string `json:"message"`
		Item    struct {
			Type    string `json:"type"`
			ID      string `json:"id"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"item"`
	} `json:"payload"`
}

// Codex passes its own notices and other programs' messages as user messages
// that start with a markup tag, such as <heartbeat> or <subagent_notification>.
var userMessageWrittenByAProgram = regexp.MustCompile(`^<[a-z_]+>`)

// ErrCodexRolloutFileIsMissing reports a thread whose rollout is no longer on
// this machine. Codex moves old rollouts away; the archive keeps what it read.
var ErrCodexRolloutFileIsMissing = errors.New("codex rollout file is missing")

// The Codex IDE extension puts the editor's state before the person's words,
// under a heading of its own.
const codexEditorContextHeading = "# Context from my IDE setup:"
const codexPersonRequestHeading = "## My request for Codex:"

func turnTextWithoutCodexEditorContext(text string) string {
	if !strings.HasPrefix(text, codexEditorContextHeading) {
		return text
	}
	if _, request, found := strings.Cut(text, codexPersonRequestHeading); found {
		return strings.TrimSpace(request)
	}
	return text
}

func (entry CodexThreadIndexEntry) codexTurnAuthor(writtenAsUser bool, text string) agentconversation.AgentConversationTurnAuthor {
	if !writtenAsUser {
		return agentconversation.AgentConversationTurnAuthorAgentInConversation
	}
	if entry.userTurnsWereTypedByAPerson() && !userMessageWrittenByAProgram.MatchString(text) {
		return agentconversation.AgentConversationTurnAuthorPersonUsingTheHarness
	}
	return agentconversation.AgentConversationTurnAuthorAnotherAgentOrProgram
}

// ReadCodexConversation reads one thread's rollout.
func ReadCodexConversation(entry CodexThreadIndexEntry) (agentconversation.AgentConversation, error) {
	conversation := agentconversation.AgentConversation{
		CanonicalConversationReference: "agents:codex-thread/" + entry.CodexThreadIdentifier,
		AgentHarness:                   agentconversation.AgentHarnessCodex,
		AgentDisplayName:               "Codex",
		ConversationTitle:              strings.TrimSpace(entry.ThreadTitle),
		WorkingFolderPath:              entry.WorkingFolderPath,
		LinkThatOpensConversation:      "codex://threads/" + entry.CodexThreadIdentifier,
	}
	rolloutFile, err := os.Open(entry.RolloutFilePath)
	if errors.Is(err, os.ErrNotExist) {
		return conversation, ErrCodexRolloutFileIsMissing
	}
	if err != nil {
		return conversation, err
	}
	defer func() { _ = rolloutFile.Close() }()
	// A resumed thread repeats earlier items.
	itemIdentifiersAlreadyRead := map[string]bool{}
	// Rollouts written before Codex recorded completed items hold each turn
	// as a user_message or agent_message event. A rollout uses one form.
	var olderFormTurns []agentconversation.AgentConversationTurn
	olderFormEventOrdinal := 0
	scanner := bufio.NewScanner(rolloutFile)
	scanner.Buffer(make([]byte, 1024*1024), 256*1024*1024)
	for scanner.Scan() {
		lineBytes := scanner.Bytes()
		// Most lines are tool calls and their output; only these hold turns.
		if !bytes.Contains(lineBytes, []byte(`"item_completed"`)) &&
			!bytes.Contains(lineBytes, []byte(`"user_message"`)) &&
			!bytes.Contains(lineBytes, []byte(`"agent_message"`)) {
			continue
		}
		var line codexRolloutLine
		if err := json.Unmarshal(lineBytes, &line); err != nil || line.Type != "event_msg" {
			continue
		}
		switch line.Payload.Type {
		case "user_message", "agent_message":
			olderFormEventOrdinal++
			text := turnTextWithoutCodexEditorContext(strings.TrimSpace(line.Payload.Message))
			if text == "" {
				continue
			}
			olderFormTurns = append(olderFormTurns, agentconversation.AgentConversationTurn{
				CanonicalTurnReference: fmt.Sprintf("agents:codex-event/%s/%d", entry.CodexThreadIdentifier, olderFormEventOrdinal),
				TurnTime:               line.Timestamp,
				TurnAuthor:             entry.codexTurnAuthor(line.Payload.Type == "user_message", text),
				TurnText:               text,
			})
		case "item_completed":
			item := line.Payload.Item
			if (item.Type != "UserMessage" && item.Type != "AgentMessage") || itemIdentifiersAlreadyRead[item.ID] {
				continue
			}
			itemIdentifiersAlreadyRead[item.ID] = true
			var texts []string
			for _, block := range item.Content {
				if strings.TrimSpace(block.Text) != "" {
					texts = append(texts, block.Text)
				}
			}
			text := turnTextWithoutCodexEditorContext(strings.TrimSpace(strings.Join(texts, "\n")))
			if text == "" {
				continue
			}
			conversation.TurnsInConversationOrder = append(conversation.TurnsInConversationOrder, agentconversation.AgentConversationTurn{
				CanonicalTurnReference: "agents:codex-item/" + entry.CodexThreadIdentifier + "/" + item.ID,
				TurnTime:               line.Timestamp,
				TurnAuthor:             entry.codexTurnAuthor(item.Type == "UserMessage", text),
				TurnText:               text,
			})
		}
	}
	if len(itemIdentifiersAlreadyRead) == 0 {
		conversation.TurnsInConversationOrder = olderFormTurns
	}
	if err := scanner.Err(); err != nil {
		return conversation, fmt.Errorf("read %s: %w", filepath.Base(entry.RolloutFilePath), err)
	}
	if conversation.ConversationTitle == "" {
		for _, turn := range conversation.TurnsInConversationOrder {
			if turn.TurnAuthor != agentconversation.AgentConversationTurnAuthorAgentInConversation {
				conversation.ConversationTitle = agentconversation.TitleFromFirstLineOfText(turn.TurnText)
				break
			}
		}
	}
	return conversation, nil
}
