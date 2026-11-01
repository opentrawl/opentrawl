// Package claudecode reads Claude Code transcripts: one JSON-lines file per
// session in ~/.claude/projects/<folder>/.
package claudecode

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/opentrawl/opentrawl/trawlers/agents/internal/agentconversation"
)

// ClaudeCodeTranscriptFilePaths lists the main transcript of every session.
// Sub-agent transcripts sit in folders beside them and are left out.
func ClaudeCodeTranscriptFilePaths(claudeCodeProjectsFolderPath string) ([]string, error) {
	return filepath.Glob(filepath.Join(claudeCodeProjectsFolderPath, "*", "*.jsonl"))
}

type claudeCodeTranscriptEntry struct {
	Type             string                 `json:"type"`
	UUID             string                 `json:"uuid"`
	Timestamp        time.Time              `json:"timestamp"`
	Entrypoint       string                 `json:"entrypoint"`
	WorkingFolder    string                 `json:"cwd"`
	IsMeta           bool                   `json:"isMeta"`
	IsSidechain      bool                   `json:"isSidechain"`
	IsCompactSummary bool                   `json:"isCompactSummary"`
	Origin           *struct{ Kind string } `json:"origin"`
	Message          struct {
		ID      string          `json:"id"`
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	Attachment struct {
		Type      string                 `json:"type"`
		Prompt    json.RawMessage        `json:"prompt"`
		Origin    *struct{ Kind string } `json:"origin"`
		Timestamp time.Time              `json:"timestamp"`
	} `json:"attachment"`
	CustomTitle string `json:"customTitle"`
	AITitle     string `json:"aiTitle"`
	Summary     string `json:"summary"`
}

type claudeCodeContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

var systemReminderBlock = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)

// A slash command the person typed, such as /compact with instructions, is
// stored as markup around the command and its arguments.
var slashCommandMarkup = regexp.MustCompile(`(?s)^<command-name>(.*?)</command-name>.*?(?:<command-args>(.*?)</command-args>)?\s*$`)

// Text that arrives as a user entry but that no person typed: harness
// notices, command output and compaction summaries.
var textPrefixesThatAreNotTurns = []string{
	"<task-notification>",
	"<local-command-stdout>",
	"<local-command-stderr>",
	"<local-command-caveat>",
	"<side-sessions-event>",
	"[Request interrupted by user",
	"This session is being continued from a previous conversation",
}

// ClaudeCodeSessionWasDrivenThroughT3Code reports whether T3 Code ran the
// session. T3 Code's own store holds the same turns and records who wrote
// each one, so the Agents trawler reads those sessions from T3 Code.
func ClaudeCodeSessionWasDrivenThroughT3Code(conversationEntrypoint string) bool {
	return conversationEntrypoint == "sdk-ts"
}

// ReadClaudeCodeConversation reads one transcript file. It also returns the
// entrypoint that started the session, such as cli, claude-desktop or sdk-ts.
func ReadClaudeCodeConversation(transcriptFilePath string) (agentconversation.AgentConversation, string, error) {
	sessionIdentifier := strings.TrimSuffix(filepath.Base(transcriptFilePath), ".jsonl")
	conversation := agentconversation.AgentConversation{
		CanonicalConversationReference: "agents:claude-code-session/" + sessionIdentifier,
		AgentHarness:                   agentconversation.AgentHarnessClaudeCode,
		AgentDisplayName:               "Claude",
	}
	transcriptFile, err := os.Open(transcriptFilePath)
	if err != nil {
		return conversation, "", err
	}
	defer func() { _ = transcriptFile.Close() }()

	conversationEntrypoint := ""
	var title, firstPersonTurnText string
	assistantTurnIndexByMessageIdentifier := map[string]int{}
	// Claude Code sometimes writes the same entry twice.
	entryIdentifiersAlreadyRead := map[string]bool{}
	scanner := bufio.NewScanner(transcriptFile)
	scanner.Buffer(make([]byte, 1024*1024), 256*1024*1024)
	for scanner.Scan() {
		lineBytes := scanner.Bytes()
		// Most lines are tool output and request records; only these hold turns or titles.
		if !bytes.Contains(lineBytes, []byte(`"type":"user"`)) &&
			!bytes.Contains(lineBytes, []byte(`"type":"assistant"`)) &&
			!bytes.Contains(lineBytes, []byte(`"queued_command"`)) &&
			!bytes.Contains(lineBytes, []byte(`"customTitle"`)) &&
			!bytes.Contains(lineBytes, []byte(`"aiTitle"`)) &&
			!bytes.Contains(lineBytes, []byte(`"type":"summary"`)) {
			continue
		}
		var entry claudeCodeTranscriptEntry
		if err := json.Unmarshal(lineBytes, &entry); err != nil {
			// A line still being written at the end of a live transcript.
			continue
		}
		if entry.UUID != "" {
			if entryIdentifiersAlreadyRead[entry.UUID] {
				continue
			}
			entryIdentifiersAlreadyRead[entry.UUID] = true
		}
		if conversationEntrypoint == "" && entry.Entrypoint != "" {
			conversationEntrypoint = entry.Entrypoint
		}
		if conversation.WorkingFolderPath == "" && entry.WorkingFolder != "" {
			conversation.WorkingFolderPath = entry.WorkingFolder
		}
		switch entry.Type {
		case "custom-title":
			title = entry.CustomTitle
		case "ai-title":
			if title == "" {
				title = entry.AITitle
			}
		case "summary":
			if title == "" {
				title = entry.Summary
			}
		case "attachment":
			// A message typed while the agent is working is kept as a queued command.
			if entry.Attachment.Type != "queued_command" || entry.IsSidechain {
				continue
			}
			text := turnTextWithoutHarnessNotices(textFromClaudeCodeContent(entry.Attachment.Prompt))
			if text == "" {
				continue
			}
			turnTime := entry.Attachment.Timestamp
			if turnTime.IsZero() {
				turnTime = entry.Timestamp
			}
			author := userTurnAuthor(entry.Attachment.Origin, conversationEntrypoint)
			conversation.TurnsInConversationOrder = append(conversation.TurnsInConversationOrder, agentconversation.AgentConversationTurn{
				CanonicalTurnReference: "agents:claude-code-entry/" + entry.UUID,
				TurnTime:               turnTime,
				TurnAuthor:             author,
				TurnText:               text,
			})
			if author == agentconversation.AgentConversationTurnAuthorPersonUsingTheHarness && firstPersonTurnText == "" {
				firstPersonTurnText = text
			}
		case "user":
			if entry.IsMeta || entry.IsSidechain || entry.IsCompactSummary || entry.UUID == "" {
				continue
			}
			if entry.Origin != nil && entry.Origin.Kind == "task-notification" {
				continue
			}
			text := turnTextWithoutHarnessNotices(textFromClaudeCodeContent(entry.Message.Content))
			if text == "" {
				continue
			}
			author := userTurnAuthor(entry.Origin, conversationEntrypoint)
			conversation.TurnsInConversationOrder = append(conversation.TurnsInConversationOrder, agentconversation.AgentConversationTurn{
				CanonicalTurnReference: "agents:claude-code-entry/" + entry.UUID,
				TurnTime:               entry.Timestamp,
				TurnAuthor:             author,
				TurnText:               text,
			})
			if author == agentconversation.AgentConversationTurnAuthorPersonUsingTheHarness && firstPersonTurnText == "" {
				firstPersonTurnText = text
			}
		case "assistant":
			if entry.IsSidechain || entry.UUID == "" {
				continue
			}
			text := strings.TrimSpace(textFromClaudeCodeContent(entry.Message.Content))
			if text == "" {
				continue
			}
			// One reply can be stored as several entries, one per content block.
			if turnIndex, seen := assistantTurnIndexByMessageIdentifier[entry.Message.ID]; seen && entry.Message.ID != "" {
				conversation.TurnsInConversationOrder[turnIndex].TurnText += "\n\n" + text
				continue
			}
			assistantTurnIndexByMessageIdentifier[entry.Message.ID] = len(conversation.TurnsInConversationOrder)
			conversation.TurnsInConversationOrder = append(conversation.TurnsInConversationOrder, agentconversation.AgentConversationTurn{
				CanonicalTurnReference: "agents:claude-code-entry/" + entry.UUID,
				TurnTime:               entry.Timestamp,
				TurnAuthor:             agentconversation.AgentConversationTurnAuthorAgentInConversation,
				TurnText:               text,
			})
		}
	}
	if err := scanner.Err(); err != nil {
		return conversation, conversationEntrypoint, fmt.Errorf("read %s: %w", transcriptFilePath, err)
	}
	conversation.ConversationTitle = strings.TrimSpace(title)
	if conversation.ConversationTitle == "" {
		conversation.ConversationTitle = agentconversation.TitleFromFirstLineOfText(firstPersonTurnText)
	}
	return conversation, conversationEntrypoint, nil
}

// userTurnAuthor reads who wrote a user entry. Claude Code records another
// session's message as a peer, and a session started with claude -p as sdk-cli.
func userTurnAuthor(origin *struct{ Kind string }, conversationEntrypoint string) agentconversation.AgentConversationTurnAuthor {
	if origin != nil && origin.Kind != "" && origin.Kind != "human" {
		return agentconversation.AgentConversationTurnAuthorAnotherAgentOrProgram
	}
	if conversationEntrypoint == "sdk-cli" {
		return agentconversation.AgentConversationTurnAuthorAnotherAgentOrProgram
	}
	return agentconversation.AgentConversationTurnAuthorPersonUsingTheHarness
}

func textFromClaudeCodeContent(content json.RawMessage) string {
	if len(content) == 0 {
		return ""
	}
	var plainText string
	if json.Unmarshal(content, &plainText) == nil {
		return plainText
	}
	var blocks []claudeCodeContentBlock
	if json.Unmarshal(content, &blocks) != nil {
		return ""
	}
	var texts []string
	for _, block := range blocks {
		if block.Type == "text" && strings.TrimSpace(block.Text) != "" {
			texts = append(texts, block.Text)
		}
	}
	return strings.Join(texts, "\n")
}

func turnTextWithoutHarnessNotices(text string) string {
	text = strings.TrimSpace(systemReminderBlock.ReplaceAllString(text, ""))
	if slashCommand := slashCommandMarkup.FindStringSubmatch(text); slashCommand != nil {
		text = strings.TrimSpace(slashCommand[1] + " " + strings.TrimSpace(slashCommand[2]))
	}
	for _, prefix := range textPrefixesThatAreNotTurns {
		if strings.HasPrefix(text, prefix) {
			return ""
		}
	}
	return text
}
