// Package t3code reads T3 Code threads from its SQLite store,
// ~/.t3/userdata/statev2.sqlite. T3 Code drives Claude Code and Codex and
// records, for each message, whether a person or an agent wrote it.
package t3code

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/agents/internal/agentconversation"
	"github.com/opentrawl/opentrawl/trawlkit/store"
)

const t3CodeStateDatabaseFileName = "statev2.sqlite"

// T3CodeStateDatabasePath is the store inside T3 Code's user data folder.
func T3CodeStateDatabasePath(t3CodeUserDataFolderPath string) string {
	return filepath.Join(t3CodeUserDataFolderPath, t3CodeStateDatabaseFileName)
}

// T3CodeEnvironmentIdentifier names this machine's T3 Code server. T3 Code
// links to a thread through it.
func T3CodeEnvironmentIdentifier(t3CodeUserDataFolderPath string) (string, error) {
	identifier, err := os.ReadFile(filepath.Join(t3CodeUserDataFolderPath, "environment-id"))
	return strings.TrimSpace(string(identifier)), err
}

// In T3 Code a person can quote part of an agent's reply and comment on it.
// The app sends that as a markdown link whose address carries the quote and
// the comment.
var quotedReplyWithComment = regexp.MustCompile(`\[Assistant quote\]\((t3-citation://[^)\s]+)\)`)

func readableQuotedReplyWithComment(markdownLink string) string {
	address := quotedReplyWithComment.FindStringSubmatch(markdownLink)[1]
	parsedAddress, err := url.Parse(address)
	if err != nil {
		return markdownLink
	}
	query := parsedAddress.Query()
	var quotedLines []string
	for _, line := range strings.Split(query.Get("text"), "\n") {
		if strings.TrimSpace(line) != "" {
			quotedLines = append(quotedLines, "> "+line)
		}
	}
	// The person's comment comes first, so a list row that shows only the
	// start of a turn shows the person's words, not the agent's.
	return "\n\n" + strings.TrimSpace(query.Get("comment")) + "\n\nReplying to:\n" + strings.Join(quotedLines, "\n")
}

var threeOrMoreNewlines = regexp.MustCompile(`\n{3,}`)

// T3 Code sends this text to the agent when a person attaches files and writes nothing.
const textT3CodeSendsForAttachmentsWithoutWords = "[User attached one or more files without additional text. Respond using the conversation context and the attached files.]"

func t3CodeMessageTextForReading(text string) string {
	if strings.TrimSpace(text) == textT3CodeSendsForAttachmentsWithoutWords {
		return ""
	}
	text = quotedReplyWithComment.ReplaceAllStringFunc(text, readableQuotedReplyWithComment)
	return strings.TrimSpace(threeOrMoreNewlines.ReplaceAllString(text, "\n\n"))
}

type t3CodeMessagePayload struct {
	Text      string `json:"text"`
	CreatedBy string `json:"createdBy"`
	// CreationSource is web or mobile when a person typed the message in a
	// T3 Code window. T3 Code's server also creates messages it marks as the
	// user's, such as task notifications, scheduled prompts and resumes.
	CreationSource string `json:"creationSource"`
	Attachments    []struct {
		Name string `json:"name"`
	} `json:"attachments"`
}

// ReadT3CodeConversationsChangedSince reads every thread with activity after
// the time given. A zero time reads every thread.
func ReadT3CodeConversationsChangedSince(
	ctx context.Context,
	t3CodeUserDataFolderPath string,
	changedSince time.Time,
) ([]agentconversation.AgentConversation, error) {
	environmentIdentifier, err := T3CodeEnvironmentIdentifier(t3CodeUserDataFolderPath)
	if err != nil {
		return nil, err
	}
	t3CodeState, err := store.OpenForeignReadOnly(ctx, T3CodeStateDatabasePath(t3CodeUserDataFolderPath))
	if err != nil {
		return nil, err
	}
	defer func() { _ = t3CodeState.Close() }()
	database := t3CodeState.DB()

	threadRows, err := database.QueryContext(ctx, `SELECT t.thread_id, t.title, COALESCE(t.provider_instance_id, t.default_provider),
			COALESCE(json_extract(t.payload_json, '$.worktreePath'), p.workspace_root, '')
		FROM orchestration_v2_projection_threads t
		LEFT JOIN projection_projects p ON p.project_id = t.project_id
		WHERE t.updated_at > ?`, changedSince.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	var conversations []agentconversation.AgentConversation
	for threadRows.Next() {
		var threadIdentifier, title, agentProvider, workingFolderPath string
		if err := threadRows.Scan(&threadIdentifier, &title, &agentProvider, &workingFolderPath); err != nil {
			_ = threadRows.Close()
			return nil, err
		}
		conversations = append(conversations, agentconversation.AgentConversation{
			CanonicalConversationReference: "agents:t3-code-thread/" + threadIdentifier,
			AgentHarness:                   agentconversation.AgentHarnessT3Code,
			AgentDisplayName:               agentDisplayNameForT3CodeProvider(agentProvider),
			ConversationTitle:              strings.TrimSpace(title),
			WorkingFolderPath:              workingFolderPath,
			LinkThatOpensConversation:      "t3-thread://v2/" + environmentIdentifier + "/" + threadIdentifier,
		})
	}
	_ = threadRows.Close()
	if err := threadRows.Err(); err != nil {
		return nil, err
	}

	for conversationIndex := range conversations {
		conversation := &conversations[conversationIndex]
		threadIdentifier := strings.TrimPrefix(conversation.CanonicalConversationReference, "agents:t3-code-thread/")
		messageRows, err := database.QueryContext(ctx, `SELECT message_id, role, created_at, payload_json
			FROM orchestration_v2_projection_messages
			WHERE thread_id = ? AND streaming = 0 AND role IN ('user', 'assistant')
			ORDER BY created_at, message_id`, threadIdentifier)
		if err != nil {
			return nil, err
		}
		for messageRows.Next() {
			var messageIdentifier, role, createdAt, payloadJSON string
			if err := messageRows.Scan(&messageIdentifier, &role, &createdAt, &payloadJSON); err != nil {
				_ = messageRows.Close()
				return nil, err
			}
			var payload t3CodeMessagePayload
			if err := json.Unmarshal([]byte(payloadJSON), &payload); err != nil {
				_ = messageRows.Close()
				return nil, fmt.Errorf("t3 code message %s: %w", messageIdentifier, err)
			}
			turnTime, err := time.Parse(time.RFC3339Nano, createdAt)
			if err != nil {
				_ = messageRows.Close()
				return nil, fmt.Errorf("t3 code message %s time: %w", messageIdentifier, err)
			}
			turn := agentconversation.AgentConversationTurn{
				CanonicalTurnReference: "agents:t3-code-message/" + threadIdentifier + "/" + messageIdentifier,
				TurnTime:               turnTime,
				TurnAuthor:             agentconversation.AgentConversationTurnAuthorAgentInConversation,
				TurnText:               t3CodeMessageTextForReading(payload.Text),
			}
			if role == "user" {
				turn.TurnAuthor = agentconversation.AgentConversationTurnAuthorPersonUsingTheHarness
				if payload.CreatedBy != "user" || (payload.CreationSource != "web" && payload.CreationSource != "mobile") {
					turn.TurnAuthor = agentconversation.AgentConversationTurnAuthorAnotherAgentOrProgram
				}
			}
			for _, attachment := range payload.Attachments {
				if attachment.Name != "" {
					turn.AttachedFileNames = append(turn.AttachedFileNames, attachment.Name)
				}
			}
			if turn.TurnText == "" && len(turn.AttachedFileNames) == 0 {
				continue
			}
			conversation.TurnsInConversationOrder = append(conversation.TurnsInConversationOrder, turn)
		}
		_ = messageRows.Close()
		if err := messageRows.Err(); err != nil {
			return nil, err
		}
	}
	return conversations, nil
}

func agentDisplayNameForT3CodeProvider(agentProvider string) string {
	switch agentProvider {
	case "claudeAgent":
		return "Claude"
	case "codex":
		return "Codex"
	case "ollama":
		return "Ollama"
	}
	return "Agent"
}
