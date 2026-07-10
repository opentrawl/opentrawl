@preconcurrency import Foundation
import OSLog
import SwiftProtobuf

public struct ProcessTrawlClient: TrawlClient {
  private static let logger = Logger(subsystem: "app.opentrawl.trawl", category: "helper")

  private let binaryURL: URL

  public init(binaryURL: URL = ProcessTrawlClient.embeddedBinary) {
    self.binaryURL = binaryURL
  }

  public static var embeddedBinary: URL {
    Bundle.main.bundleURL
      .appendingPathComponent("Contents/Helpers/trawl", isDirectory: false)
  }

  public func status() async throws -> StatusResponse {
    let result = try await run(arguments: ["__app", "status"])
    let messages: [Trawl_App_V1_SourceStatus] = try decodeMessages(result.stdout)
    return StatusResponse(
      sources: messages.map(\.model),
      completion: try completion(for: result)
    )
  }

  public func sync() async throws -> FanoutCompletion {
    let result = try await run(arguments: ["__app", "sync"])
    return try completion(for: result)
  }

  public func search(_ query: String, source: String?) async throws -> SearchResponse {
    var arguments = ["__app", "search"]
    if let source, !source.isEmpty {
      arguments += ["--source", source]
    }
    arguments.append(query)

    let result = try await run(arguments: arguments)
    let messages: [Trawl_App_V1_SearchHit] = try decodeMessages(result.stdout)
    return SearchResponse(
      hits: messages.map(\.model),
      completion: try completion(for: result)
    )
  }

  public func open(_ ref: String) async throws -> String {
    let result = try await run(arguments: ["open", ref])
    try checkProcess(result)
    guard result.exitCode == 0 else {
      throw TrawlClientError.processFailed(exitCode: result.exitCode)
    }
    return String(decoding: result.stdout, as: UTF8.self)
  }

  private func decodeMessages<Message: SwiftProtobuf.Message>(_ data: Data) throws -> [Message] {
    do {
      return try DelimitedFrames.decode(data).map { try Message(serializedBytes: $0) }
    } catch let error as TrawlClientError {
      throw error
    } catch {
      throw TrawlClientError.invalidMessage
    }
  }

  private func completion(for result: ProcessResult) throws -> FanoutCompletion {
    try checkProcess(result)
    return switch result.exitCode {
    case 0: .complete
    case 3: .partial
    case 1: .failed
    default: throw TrawlClientError.processFailed(exitCode: result.exitCode)
    }
  }

  private func checkProcess(_ result: ProcessResult) throws {
    if result.terminatedBySignal {
      throw TrawlClientError.processDied(signal: result.exitCode)
    }
    if !result.stderr.isEmpty {
      Self.logger.error("Helper diagnostic: \(result.stderr, privacy: .private)")
    }
  }

  private func run(arguments: [String]) async throws -> ProcessResult {
    guard FileManager.default.isExecutableFile(atPath: binaryURL.path) else {
      throw TrawlClientError.binaryMissing
    }
    try Task.checkCancellation()

    let invocation = ProcessInvocation(binaryURL: binaryURL, arguments: arguments)
    do {
      try invocation.process.run()
    } catch {
      throw TrawlClientError.launchFailed
    }

    return try await withTaskCancellationHandler {
      let stdoutTask = Task.detached {
        invocation.stdout.fileHandleForReading.readDataToEndOfFile()
      }
      let stderrTask = Task.detached {
        invocation.stderr.fileHandleForReading.readDataToEndOfFile()
      }
      let exitTask = Task.detached {
        invocation.process.waitUntilExit()
        return (
          invocation.process.terminationReason == .uncaughtSignal,
          invocation.process.terminationStatus
        )
      }

      let (terminatedBySignal, exitCode) = await exitTask.value
      let stdout = await stdoutTask.value
      let stderr = await stderrTask.value
      try Task.checkCancellation()
      return ProcessResult(
        stdout: stdout,
        stderr: String(decoding: stderr, as: UTF8.self),
        terminatedBySignal: terminatedBySignal,
        exitCode: exitCode
      )
    } onCancel: {
      invocation.terminate()
    }
  }
}

private struct ProcessResult: Sendable {
  let stdout: Data
  let stderr: String
  let terminatedBySignal: Bool
  let exitCode: Int32
}

private final class ProcessInvocation: @unchecked Sendable {
  let process = Process()
  let stdout = Pipe()
  let stderr = Pipe()

  init(binaryURL: URL, arguments: [String]) {
    process.executableURL = binaryURL
    process.arguments = arguments
    process.environment = ["HOME": NSHomeDirectory()]
    process.standardOutput = stdout
    process.standardError = stderr
  }

  func terminate() {
    if process.isRunning {
      process.terminate()
    }
  }
}
