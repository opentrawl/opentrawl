import Darwin
import Foundation
import TrawlCommandLine

let commandLineArguments = CommandLine.arguments
let firstCommandLineArgument = commandLineArguments.dropFirst().first

if firstCommandLineArgument == "--cli" {
  var commandLineArgumentVector = Array(
    UnsafeBufferPointer(start: CommandLine.unsafeArgv, count: Int(CommandLine.argc) + 1)
  )
  commandLineArgumentVector.remove(at: 1)
  exit(OpenTrawlRunCommandLine(CommandLine.argc - 1, &commandLineArgumentVector))
}

if URL(fileURLWithPath: commandLineArguments[0]).lastPathComponent == "trawl"
  || firstCommandLineArgument == "__trawlkit_wire"
  || firstCommandLineArgument == "__trawlkit_plugin_worker"
{
  exit(OpenTrawlRunCommandLine(CommandLine.argc, CommandLine.unsafeArgv))
}

TrawlApp.main()
