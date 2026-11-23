package cli

import (
	"github.com/opentrawl/opentrawl/trawlkit"
	"github.com/opentrawl/opentrawl/trawlkit/output"
)

// RunCommandLine runs the CLI, its wire worker, or a bundled multi-call plugin
// worker with the process argument vector.
func RunCommandLine(arguments []string) int {
	commandArguments := arguments[1:]
	if len(commandArguments) > 0 && commandArguments[0] == trawlkit.HiddenWireSubcommand {
		return ExecuteTrawlerWire(commandArguments)
	}
	if len(commandArguments) > 0 && commandArguments[0] == trawlkit.HiddenBundledPluginWorkerSubcommand {
		return ExecuteBundledPluginWorker(commandArguments[1:])
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
