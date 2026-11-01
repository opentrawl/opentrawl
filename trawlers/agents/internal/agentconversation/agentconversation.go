// Package agentconversation holds what every agent harness has in common: a
// conversation, and the turns people, agents and programs wrote in it. Each
// harness reader turns its own store into these values; the Agents trawler
// archives them.
package agentconversation

import (
	"strings"
	"time"
)

// AgentHarness is the program a conversation was held in.
type AgentHarness string

const (
	AgentHarnessClaudeCode AgentHarness = "Claude Code"
	AgentHarnessCodex      AgentHarness = "Codex"
	AgentHarnessT3Code     AgentHarness = "T3 Code"
)

// AgentConversationTurnAuthor says who wrote a turn, as far as the store can
// tell. A reader marks a turn as the person's only when the store shows a
// person typed it into the harness.
type AgentConversationTurnAuthor string

const (
	AgentConversationTurnAuthorPersonUsingTheHarness AgentConversationTurnAuthor = "person"
	AgentConversationTurnAuthorAgentInConversation   AgentConversationTurnAuthor = "agent"
	AgentConversationTurnAuthorAnotherAgentOrProgram AgentConversationTurnAuthor = "another agent or program"
)

type AgentConversationTurn struct {
	// CanonicalTurnReference uses the harness's own identifiers, so it is the
	// same on every machine that reads the store.
	CanonicalTurnReference string
	TurnTime               time.Time
	TurnAuthor             AgentConversationTurnAuthor
	// TurnText is exactly what the store holds, never shortened.
	TurnText string
	// AttachedFileNames names files the author attached, such as a pasted
	// screenshot. The files themselves stay in the harness's store.
	AttachedFileNames []string
}

type AgentConversation struct {
	CanonicalConversationReference string
	AgentHarness                   AgentHarness
	// AgentDisplayName names the agent that answers in this conversation, such
	// as Claude or Codex.
	AgentDisplayName          string
	ConversationTitle         string
	WorkingFolderPath         string
	LinkThatOpensConversation string
	TurnsInConversationOrder  []AgentConversationTurn
}

const maximumTitleRunesTakenFromFirstTurn = 80

// TitleFromFirstLineOfText names a conversation the harness did not title, by
// its first prompt, as the harnesses' own session lists do.
func TitleFromFirstLineOfText(text string) string {
	firstLine, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	firstLine = strings.Join(strings.Fields(firstLine), " ")
	runes := []rune(firstLine)
	if len(runes) > maximumTitleRunesTakenFromFirstTurn {
		return string(runes[:maximumTitleRunesTakenFromFirstTurn-1]) + "…"
	}
	return firstLine
}

// MostRecentTurnTime is zero for a conversation without turns.
func (conversation AgentConversation) MostRecentTurnTime() time.Time {
	var mostRecentTurnTime time.Time
	for _, turn := range conversation.TurnsInConversationOrder {
		if turn.TurnTime.After(mostRecentTurnTime) {
			mostRecentTurnTime = turn.TurnTime
		}
	}
	return mostRecentTurnTime
}
