package render

import (
	"io"
	"strings"
)

// Output for scoped agent access tokens (trawl token), the MCP server
// (trawl mcp) and local agent setup (trawl connect).

// FieldValueRow is one labelled fact in a field and value table.
type FieldValueRow struct {
	FieldLabel string
	FieldValue string
}

// AgentAccessTokenRowPresentation is one token in trawl token list.
type AgentAccessTokenRowPresentation struct {
	AgentAccessTokenIdentifier string
	AgentName                  string
	AllowedSourcesText         string
	CreatedDate                string
	AgentAccessTokenRevoked    bool
}

// AgentConnectionConfigurationPresentation is one agent's MCP or skill
// configuration from trawl connect: previewed as an exact diff, or written.
type AgentConnectionConfigurationPresentation struct {
	AgentDisplayName                   string
	AgentDetectedLocally               bool
	AgentConnectionConfigurationPath   string
	AgentConnectionConfigurationWasSet bool
	AgentConnectionStatusSentence      string
	AgentConnectionTokenIdentifier     string
	AgentConnectionInstructionSentence string
	AgentConnectionDiffLines           []string
}

// AgentConnectionSetupPresentation is the result of trawl connect.
type AgentConnectionSetupPresentation struct {
	AgentConnectionSummarySentence string
	AgentConnectionFacts           []FieldValueRow
	AgentConnectionConfigurations  []AgentConnectionConfigurationPresentation
	NextTrawlCommands              [][]string
}

// AgentMCPServerListeningPresentation is what trawl mcp --http shows when it starts.
type AgentMCPServerListeningPresentation struct {
	AgentMCPServerListeningSentence string
	AgentMCPServerFacts             []FieldValueRow
	NextTrawlCommands               [][]string
}

// AgentAccessOperationFailed words a token, mcp or connect operation failure.
func AgentAccessOperationFailed(reason string) CommandFailure {
	return archiveOrOperationFailed(ensureTrailingPeriod(strings.TrimSpace(reason)))
}

// AgentAccessCommandIsWrong words a bad token, mcp or connect command line.
func AgentAccessCommandIsWrong(reason string) CommandFailure {
	return commandIsWrong(ensureTrailingPeriod(strings.TrimSpace(reason)))
}

// AgentAccessSourceNotAllowed words a scoped agent request for a source its
// token does not permit.
func AgentAccessSourceNotAllowed(trawlerName string, allowedTrawlerNames []string) CommandFailure {
	sentence := "This access token is not allowed to read " + strings.TrimSpace(trawlerName) + "."
	if len(allowedTrawlerNames) > 0 {
		sentence += " Allowed sources: " + strings.Join(allowedTrawlerNames, ", ") + "."
	}
	return archiveOrOperationFailed(sentence)
}

// WriteAgentAccessTokenCreated shows a new token once. Only its hash is kept.
func (output TrawlCommandOutput) WriteAgentAccessTokenCreated(token AgentAccessTokenRowPresentation, bearerTokenText string) error {
	writer := output.standardOutput
	if err := writeSentenceAndFieldValueRows(writer, "Created access token for "+token.AgentName+". Copy it now: it is not shown again.", []FieldValueRow{
		{FieldLabel: "token", FieldValue: bearerTokenText},
		{FieldLabel: "id", FieldValue: token.AgentAccessTokenIdentifier},
		{FieldLabel: "sources", FieldValue: token.AllowedSourcesText},
	}); err != nil {
		return err
	}
	return writeNextCommands(writer, []NextCommand{nextCommandWithLabel(nextCommandLabelHelp, "token", "list")})
}

// WriteAgentAccessTokenList renders every token, newest first.
func (output TrawlCommandOutput) WriteAgentAccessTokenList(tokens []AgentAccessTokenRowPresentation) error {
	writer := output.standardOutput
	if len(tokens) == 0 {
		return writeLine(writer, "No access tokens. Create one with trawl token create AGENT or trawl connect --write.")
	}
	rows := make([][]string, 0, len(tokens))
	for _, token := range tokens {
		status := "active"
		if token.AgentAccessTokenRevoked {
			status = "revoked"
		}
		rows = append(rows, []string{token.AgentAccessTokenIdentifier, token.AgentName, status, token.CreatedDate, token.AllowedSourcesText})
	}
	return writeTable(writer, []TableColumn{
		{Header: "id", MinimumWidth: 8, NeverTruncateCellValues: true},
		{Header: "agent", MinimumWidth: 5, NeverTruncateCellValues: true},
		{Header: "status", MinimumWidth: 6, NeverTruncateCellValues: true},
		{Header: "created", MinimumWidth: 10, NeverTruncateCellValues: true},
		{Header: "sources", Wrap: true},
	}, rows, 4)
}

// WriteAgentAccessTokenRevoked confirms a revoked token.
func (output TrawlCommandOutput) WriteAgentAccessTokenRevoked(tokenIdentifier string) error {
	sentence := "Revoked access token " + strings.TrimSpace(tokenIdentifier) + ". Agents using it are refused from now on."
	return writeLines(output.standardOutput, Wrap(sentence, OutputWidth(output.standardOutput)))
}

// WriteAgentConnectionSetup renders trawl connect. Diff lines are shown
// verbatim without wrapping so the user can inspect the exact change.
func (output TrawlCommandOutput) WriteAgentConnectionSetup(setup AgentConnectionSetupPresentation) error {
	writer := output.standardOutput
	if err := writeSentenceAndFieldValueRows(writer, setup.AgentConnectionSummarySentence, setup.AgentConnectionFacts); err != nil {
		return err
	}
	for _, configuration := range setup.AgentConnectionConfigurations {
		if err := writeLine(writer, ""); err != nil {
			return err
		}
		heading := configuration.AgentDisplayName
		if status := strings.TrimSpace(configuration.AgentConnectionStatusSentence); status != "" {
			heading += " (" + status + ")"
		}
		if path := strings.TrimSpace(configuration.AgentConnectionConfigurationPath); path != "" {
			if configuration.AgentConnectionConfigurationWasSet {
				heading += ": wrote " + path + "."
			} else {
				heading += ": " + path
			}
		} else if instruction := strings.TrimSpace(configuration.AgentConnectionInstructionSentence); instruction != "" {
			heading += ": " + instruction
		}
		if err := writeLines(writer, Wrap(heading, OutputWidth(writer))); err != nil {
			return err
		}
		if tokenID := strings.TrimSpace(configuration.AgentConnectionTokenIdentifier); tokenID != "" {
			if err := writeLines(writer, Wrap("Token id: "+tokenID, OutputWidth(writer))); err != nil {
				return err
			}
		}
		if path := strings.TrimSpace(configuration.AgentConnectionConfigurationPath); path != "" {
			if instruction := strings.TrimSpace(configuration.AgentConnectionInstructionSentence); instruction != "" {
				if err := writeLines(writer, Wrap(instruction, OutputWidth(writer))); err != nil {
					return err
				}
			}
		}
		if len(configuration.AgentConnectionDiffLines) > 0 {
			if err := writeLines(writer, configuration.AgentConnectionDiffLines); err != nil {
				return err
			}
		}
	}
	nextCommands := make([]NextCommand, 0, len(setup.NextTrawlCommands))
	for _, arguments := range setup.NextTrawlCommands {
		nextCommands = append(nextCommands, nextCommandWithLabel(nextCommandLabelTryAgain, arguments...))
	}
	return writeNextCommands(writer, nextCommands)
}

// WriteAgentMCPServerListening renders the start of trawl mcp --http.
func (output TrawlCommandOutput) WriteAgentMCPServerListening(listening AgentMCPServerListeningPresentation) error {
	writer := output.standardOutput
	if err := writeSentenceAndFieldValueRows(writer, listening.AgentMCPServerListeningSentence, listening.AgentMCPServerFacts); err != nil {
		return err
	}
	nextCommands := make([]NextCommand, 0, len(listening.NextTrawlCommands))
	for _, arguments := range listening.NextTrawlCommands {
		nextCommands = append(nextCommands, nextCommandWithLabel(nextCommandLabelHelp, arguments...))
	}
	return writeNextCommands(writer, nextCommands)
}

func writeSentenceAndFieldValueRows(writer io.Writer, sentence string, facts []FieldValueRow) error {
	if err := writeLines(writer, Wrap(strings.TrimSpace(sentence), OutputWidth(writer))); err != nil {
		return err
	}
	if len(facts) == 0 {
		return nil
	}
	rows := make([][]string, 0, len(facts))
	for _, fact := range facts {
		rows = append(rows, []string{fact.FieldLabel, fact.FieldValue})
	}
	return writeTable(writer, []TableColumn{
		{Header: "field", MinimumWidth: 8, NeverTruncateCellValues: true},
		{Header: "value", Wrap: true, NeverTruncateCellValues: true},
	}, rows, 1)
}
