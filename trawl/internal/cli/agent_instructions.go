package cli

import (
	_ "embed"
	"fmt"
	"io"
)

//go:embed agent-instructions/SKILL.md
var searchOpenTrawlArchivesAgentInstructions string

type AgentInstructionsCmd struct{}

func (c *AgentInstructionsCmd) Run(runtime *Runtime) error {
	if searchOpenTrawlArchivesAgentInstructions == "" {
		return fmt.Errorf("OpenTrawl agent instructions are unavailable")
	}
	_, err := io.WriteString(runtime.stdout, searchOpenTrawlArchivesAgentInstructions)
	return err
}
