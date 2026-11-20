package cli

import (
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/output"
)

// RunCommandLine runs the CLI or its wire worker with the process argument vector.
func RunCommandLine(arguments []string) int {
	commandArguments := arguments[1:]
	if len(commandArguments) > 0 && commandArguments[0] == trawlkit.HiddenWireSubcommand {
		return ExecuteTrawlerWire(commandArguments)
	}
	stdout, stderr := output.ProcessOutputStreams()
	return ExecuteWithTrawlInvocationDisplay(commandArguments, stdout, stderr, stableTrawlInvocationDisplay(arguments[0]))
}

func stableTrawlInvocationDisplay(argumentZero string) string {
	if argumentZero == "./trawl" {
		return "./trawl"
	}
	return "trawl"
}
