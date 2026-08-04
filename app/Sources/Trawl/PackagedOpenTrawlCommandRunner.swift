import Foundation

struct OpenTrawlCommandTextOutput: Sendable, Equatable {
  let text: String
  let exitCode: Int32

  var succeeded: Bool { exitCode == 0 }
}

protocol OpenTrawlCommandRunning: Sendable {
  func runTrawl(arguments: [String]) async -> OpenTrawlCommandTextOutput
  func stopRunningTrawl() async
}

actor PackagedOpenTrawlCommandRunner: OpenTrawlCommandRunning {
  let helperURL: URL
  let outputColumnCount: Int

  private var activeProcess: Process?

  init(helperURL: URL, outputColumnCount: Int) {
    self.helperURL = helperURL
    self.outputColumnCount = outputColumnCount
  }

  func runTrawl(arguments: [String]) async -> OpenTrawlCommandTextOutput {
    guard FileManager.default.isExecutableFile(atPath: helperURL.path) else {
      return OpenTrawlCommandTextOutput(
        text: OperationalCopy.CommandDemo.helperUnavailableOutput,
        exitCode: -1
      )
    }

    let commandOutput = Pipe()
    let process = Process()
    process.executableURL = helperURL
    process.currentDirectoryURL = helperURL.deletingLastPathComponent()
    process.arguments = arguments
    var commandEnvironment = ProcessInfo.processInfo.environment
    commandEnvironment["COLUMNS"] = String(outputColumnCount)
    process.environment = commandEnvironment
    process.standardOutput = commandOutput
    process.standardError = commandOutput

    do {
      try process.run()
      activeProcess = process
    } catch {
      return OpenTrawlCommandTextOutput(
        text: OperationalCopy.CommandDemo.commandFailedOutput,
        exitCode: -1
      )
    }

    let outputReader = Task.detached {
      commandOutput.fileHandleForReading.readDataToEndOfFile()
    }
    await withTaskCancellationHandler {
      while process.isRunning {
        try? await Task.sleep(for: .milliseconds(20))
        await Task.yield()
      }
    } onCancel: {
      if process.isRunning {
        process.terminate()
      }
    }
    let data = await outputReader.value
    if activeProcess === process {
      activeProcess = nil
    }
    return OpenTrawlCommandTextOutput(
      text: String(decoding: data, as: UTF8.self),
      exitCode: process.terminationStatus
    )
  }

  func stopRunningTrawl() async {
    guard let process = activeProcess else { return }
    if process.isRunning {
      process.terminate()
    }
    while activeProcess === process {
      if process.isRunning {
        process.terminate()
      }
      try? await Task.sleep(for: .milliseconds(20))
      await Task.yield()
    }
  }
}
