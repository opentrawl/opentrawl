package main

import (
	_ "embed"
	"fmt"
	"io"
	"os"

	trawloutput "github.com/opentrawl/opentrawl/trawlkit/output"
)

//go:embed SKILL.md
var shippedAgentInstructions string

func main() {
	stdout, stderr := trawloutput.StandardWriters()
	if err := run(os.Args[1:], stdout, stderr); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		os.Exit(1)
	}
}

func run(arguments []string, stdout io.Writer, stderr io.Writer) error {
	if len(arguments) == 0 {
		return trawloutput.HumanFacingErrorMessage("usage: trawl <search|open|agent-instructions|experiment> ...")
	}
	switch arguments[0] {
	case "search":
		return runNaturalLanguageSearch(arguments[1:], stdout)
	case "open":
		return runBaseTrawl(arguments, stdout, stderr)
	case "agent-instructions":
		return writeAgentInstructions(arguments[1:], stdout)
	case "experiment":
		return runExperimentCommand(arguments[1:], stdout, stderr)
	case "--help", "-h":
		if err := runBaseTrawl(arguments, stdout, stderr); err != nil {
			return err
		}
		_, err := fmt.Fprintln(stdout, "\nSearch experiment agent guidance:\n  trawl agent-instructions")
		return err
	default:
		return runBaseTrawl(arguments, stdout, stderr)
	}
}

func writeAgentInstructions(arguments []string, output io.Writer) error {
	if len(arguments) != 0 {
		return fmt.Errorf("usage: trawl agent-instructions")
	}
	_, err := io.WriteString(output, shippedAgentInstructions)
	return err
}

func runExperimentCommand(arguments []string, output io.Writer, progressOutput io.Writer) error {
	if len(arguments) < 2 {
		return trawloutput.HumanFacingErrorMessage("usage: trawl experiment <corpus|index> build ...")
	}
	switch arguments[0] + " " + arguments[1] {
	case "corpus build":
		return buildSearchResearchCorpus(arguments[2:], output, progressOutput)
	case "index build":
		return buildSearchResearchEmbeddingIndex(arguments[2:], output, progressOutput)
	default:
		return fmt.Errorf("unknown experiment command %q", arguments[0]+" "+arguments[1])
	}
}
