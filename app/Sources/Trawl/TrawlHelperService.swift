import Foundation
import Observation
import TrawlClient

struct LocalSemanticSearchSnapshot: Sendable, Equatable {
  let isAvailable: Bool
  let isEnabled: Bool
  let chunkSummary: String?
  let sizeSummary: String?
  let modelSummary: String?
  let helperSummary: String?
  let rawStatusLine: String?

  static let unavailable = LocalSemanticSearchSnapshot(
    isAvailable: false,
    isEnabled: false,
    chunkSummary: nil,
    sizeSummary: nil,
    modelSummary: nil,
    helperSummary: nil,
    rawStatusLine: nil
  )

  static func parse(from output: String) -> LocalSemanticSearchSnapshot {
    let lines = output
      .split(whereSeparator: \.isNewline)
      .map { $0.trimmingCharacters(in: .whitespaces) }
      .filter { !$0.isEmpty }
    guard let statusLine = lines.first(where: { $0.hasPrefix("Semantic search:") }) else {
      return .unavailable
    }
    let body = statusLine.dropFirst("Semantic search:".count).trimmingCharacters(in: .whitespaces)
    let segments = body.components(separatedBy: " · ").map {
      $0.trimmingCharacters(in: .whitespaces)
    }
    let isEnabled = segments.first?.lowercased() == "enabled"
    let chunkSummary = segments.first { $0.localizedCaseInsensitiveContains("chunk") }
    let sizeSummary = segments.first {
      $0.hasSuffix("MB") || $0.hasSuffix("GB") || $0.hasSuffix("KB") || $0.hasSuffix("B")
    }

    var modelSummary: String?
    var helperSummary: String?
    if let modelLine = lines.first(where: { $0.hasPrefix("Model:") }) {
      let modelSegments = modelLine.components(separatedBy: " · ").map {
        $0.trimmingCharacters(in: .whitespaces)
      }
      if let first = modelSegments.first {
        modelSummary = first.dropFirst("Model:".count).trimmingCharacters(in: .whitespaces)
      }
      if let helper = modelSegments.first(where: { $0.hasPrefix("Helper:") }) {
        helperSummary = helper.dropFirst("Helper:".count).trimmingCharacters(in: .whitespaces)
      }
    }

    return LocalSemanticSearchSnapshot(
      isAvailable: true,
      isEnabled: isEnabled,
      chunkSummary: chunkSummary,
      sizeSummary: sizeSummary,
      modelSummary: modelSummary,
      helperSummary: helperSummary,
      rawStatusLine: statusLine
    )
  }
}

struct ScopedAgentTokenRow: Sendable, Equatable, Identifiable {
  let tokenID: String
  let agentName: String
  let allowedApps: String
  let createdDate: String
  let isRevoked: Bool

  var id: String { tokenID }

  static func parseList(from output: String) -> [ScopedAgentTokenRow] {
    let lines = output.split(whereSeparator: \.isNewline).map(String.init)
    var rows: [ScopedAgentTokenRow] = []
    for line in lines {
      let trimmed = line.trimmingCharacters(in: .whitespaces)
      guard trimmed.hasPrefix("tok_") else { continue }
      let columns = splitAlignedColumns(trimmed)
      guard columns.count >= 3 else { continue }
      let tokenID = columns[0]
      let agentName = columns[1]
      let allowedApps = columns[2]
      let createdDate = columns.count >= 4 ? columns[3] : ""
      let statusText = columns.count >= 5 ? columns[4].lowercased() : ""
      let isRevoked = statusText.contains("revoked")
      rows.append(
        ScopedAgentTokenRow(
          tokenID: tokenID,
          agentName: agentName,
          allowedApps: allowedApps,
          createdDate: createdDate,
          isRevoked: isRevoked
        )
      )
    }
    return rows
  }
}

struct DetectedLocalAIAgentRow: Sendable, Equatable, Identifiable {
  let displayName: String
  let statusText: String
  let configPath: String?

  var id: String { "\(displayName):\(configPath ?? "")" }

  static func parseConnectPreview(from output: String) -> [DetectedLocalAIAgentRow] {
    let lines = output.split(whereSeparator: \.isNewline).map {
      $0.trimmingCharacters(in: .whitespaces)
    }
    var rows: [DetectedLocalAIAgentRow] = []
    var currentName: String?
    var currentStatus: String?
    var currentPath: String?

    func flush() {
      if let name = currentName {
        rows.append(
          DetectedLocalAIAgentRow(
            displayName: name,
            statusText: currentStatus ?? "Detected",
            configPath: currentPath
          )
        )
      }
      currentName = nil
      currentStatus = nil
      currentPath = nil
    }

    let knownAgents = [
      "Claude Code", "Claude Desktop", "Codex", "Cursor",
      "T3 Code", "OpenClaw", "Hermes Agent", "executor.sh",
    ]
    for line in lines {
      if let matched = knownAgents.first(where: {
        line == $0 || line.hasPrefix("\($0) ") || line.hasPrefix("\($0) ·")
      }) {
        flush()
        currentName = matched
        let remainder = line.dropFirst(matched.count)
          .trimmingCharacters(in: CharacterSet(charactersIn: " ·—:-"))
        if !remainder.isEmpty {
          currentStatus = remainder
        }
      } else if currentName != nil {
        if line.hasPrefix("status") {
          let value = line.dropFirst("status".count).trimmingCharacters(in: .whitespaces)
          if !value.isEmpty { currentStatus = value }
        } else if line.hasPrefix("file") || line.hasPrefix("path") || line.hasPrefix("config") {
          let parts = line.split(separator: " ", maxSplits: 1)
          if parts.count == 2 {
            currentPath = String(parts[1]).trimmingCharacters(in: .whitespaces)
          }
        }
      }
    }
    flush()
    return rows
  }
}

struct ExpandedConversationMessageLine: Sendable, Equatable, Identifiable {
  let id: String
  let timestampText: String
  let senderText: String
  let messageText: String
  let trawlLink: GloballyRoutableTrawlLink?

  static func parseMessagesOutput(_ output: String) -> [ExpandedConversationMessageLine] {
    let lines = output.split(whereSeparator: \.isNewline).map {
      $0.trimmingCharacters(in: .whitespaces)
    }
    var parsed: [ExpandedConversationMessageLine] = []
    for (index, line) in lines.enumerated() {
      guard !line.isEmpty else { continue }
      // Skip table header and hint lines.
      if line.lowercased().hasPrefix("when ")
        || line.lowercased().hasPrefix("time ")
        || line.lowercased().hasPrefix("next:")
        || line.lowercased().hasPrefix("help:")
        || line.lowercased().hasPrefix("showing ")
      {
        continue
      }
      let columns = splitAlignedColumns(line)
      guard columns.count >= 2 else { continue }
      let linkCandidate = columns.last.flatMap { token -> GloballyRoutableTrawlLink? in
        let link = GloballyRoutableTrawlLink(globallyRoutableTrawlLink: token)
        return parseGloballyRoutableTrawlLink(link) != nil ? link : nil
      }
      let contentColumns = linkCandidate != nil ? Array(columns.dropLast()) : columns
      guard !contentColumns.isEmpty else { continue }
      let timestamp = contentColumns[0]
      let sender: String
      let text: String
      if contentColumns.count >= 3 {
        sender = contentColumns[1]
        text = contentColumns.dropFirst(2).joined(separator: " · ")
      } else if contentColumns.count == 2 {
        sender = ""
        text = contentColumns[1]
      } else {
        continue
      }
      guard !text.isEmpty else { continue }
      parsed.append(
        ExpandedConversationMessageLine(
          id: linkCandidate?.globallyRoutableTrawlLink ?? "line-\(index)",
          timestampText: timestamp,
          senderText: sender,
          messageText: text,
          trawlLink: linkCandidate
        )
      )
    }
    return parsed
  }
}

struct AroundRelatedRecordRow: Sendable, Equatable, Identifiable {
  let id: String
  let whenText: String
  let howText: String
  let appDisplayName: String
  let summaryText: String
  let trawlLink: GloballyRoutableTrawlLink?

  static func parseAroundOutput(_ output: String) -> [AroundRelatedRecordRow] {
    let lines = output.split(whereSeparator: \.isNewline).map {
      $0.trimmingCharacters(in: .whitespaces)
    }
    var rows: [AroundRelatedRecordRow] = []
    for (index, line) in lines.enumerated() {
      guard !line.isEmpty else { continue }
      let lower = line.lowercased()
      if lower.hasPrefix("when ")
        || lower.hasPrefix("around ")
        || lower.hasPrefix("window ")
        || lower.hasPrefix("next:")
        || lower.hasPrefix("help:")
        || lower.hasPrefix("showing ")
      {
        continue
      }
      let columns = splitAlignedColumns(line)
      guard columns.count >= 3 else { continue }
      let linkCandidate = columns.last.flatMap { token -> GloballyRoutableTrawlLink? in
        let link = GloballyRoutableTrawlLink(globallyRoutableTrawlLink: token)
        return parseGloballyRoutableTrawlLink(link) != nil ? link : nil
      }
      guard linkCandidate != nil else { continue }
      let payload = Array(columns.dropLast())
      guard payload.count >= 3 else { continue }
      let whenText = payload[0]
      let howText = payload.count >= 4 ? payload[1] : ""
      let appName = payload.count >= 4 ? payload[2] : payload[1]
      let summary = payload.count >= 4
        ? payload.dropFirst(3).joined(separator: " · ")
        : payload.dropFirst(2).joined(separator: " · ")
      rows.append(
        AroundRelatedRecordRow(
          id: linkCandidate?.globallyRoutableTrawlLink ?? "around-\(index)",
          whenText: whenText,
          howText: howText,
          appDisplayName: appName,
          summaryText: summary,
          trawlLink: linkCandidate
        )
      )
    }
    return rows
  }
}

@MainActor
@Observable
final class TrawlHelperController {
  private let runner: any OpenTrawlCommandRunning

  private(set) var semanticStatus: LocalSemanticSearchSnapshot = .unavailable
  private(set) var isUpdatingSemanticSearch = false
  private(set) var semanticErrorMessage: String?

  var semanticSearchEnabledToggle: Bool {
    get { semanticStatus.isEnabled }
    set {
      guard newValue != semanticStatus.isEnabled else { return }
      Task { await setSemanticSearchEnabled(newValue) }
    }
  }

  private(set) var scopedTokens: [ScopedAgentTokenRow] = []
  private(set) var detectedAgents: [DetectedLocalAIAgentRow] = []
  private(set) var isLoadingAgentAccess = false
  private(set) var agentAccessErrorMessage: String?

  init(
    helperURL: URL = TrawlRuntimeConfiguration().helperURL,
    runner: (any OpenTrawlCommandRunning)? = nil
  ) {
    self.runner = runner ?? PackagedOpenTrawlCommandRunner(
      helperURL: helperURL,
      outputColumnCount: 200
    )
  }

  func refreshSemanticStatus() async {
    let output = await runner.runTrawl(arguments: ["semantic", "status"])
    guard output.succeeded else {
      semanticStatus = .unavailable
      return
    }
    semanticStatus = LocalSemanticSearchSnapshot.parse(from: output.text)
    semanticErrorMessage = nil
  }

  func setSemanticSearchEnabled(_ enabled: Bool) async {
    guard !isUpdatingSemanticSearch else { return }
    isUpdatingSemanticSearch = true
    semanticErrorMessage = nil
    defer { isUpdatingSemanticSearch = false }

    let args = enabled ? ["semantic", "enable"] : ["semantic", "disable"]
    let output = await runner.runTrawl(arguments: args)
    if !output.succeeded {
      let detail = output.text.trimmingCharacters(in: .whitespacesAndNewlines)
      semanticErrorMessage = detail.isEmpty
        ? "Could not update local semantic search."
        : detail
    }
    await refreshSemanticStatus()
  }

  func refreshAgentAccess() async {
    isLoadingAgentAccess = true
    agentAccessErrorMessage = nil
    defer { isLoadingAgentAccess = false }

    async let tokenOutput = runner.runTrawl(arguments: ["token", "list"])
    async let connectOutput = runner.runTrawl(arguments: ["connect"])
    let (tokensResult, connectResult) = await (tokenOutput, connectOutput)

    if tokensResult.succeeded {
      scopedTokens = ScopedAgentTokenRow.parseList(from: tokensResult.text)
    } else {
      scopedTokens = []
    }

    if connectResult.succeeded {
      detectedAgents = DetectedLocalAIAgentRow.parseConnectPreview(from: connectResult.text)
    } else {
      detectedAgents = []
    }
  }

  func revokeToken(_ tokenID: String) async {
    let output = await runner.runTrawl(arguments: ["token", "revoke", tokenID])
    if !output.succeeded {
      agentAccessErrorMessage = "Could not revoke token \(tokenID)."
    }
    await refreshAgentAccess()
  }

  func loadConversationMessages(
    conversationLink: GloballyRoutableTrawlLink,
    before: Date? = nil,
    after: Date? = nil,
    limit: Int = 20
  ) async -> [ExpandedConversationMessageLine] {
    var args = [
      "messages",
      "--conversation", conversationLink.globallyRoutableTrawlLink,
      "--limit", String(limit),
    ]
    if let before {
      args += ["--before", before.ISO8601Format()]
    }
    if let after {
      args += ["--after", after.ISO8601Format()]
    }
    let output = await runner.runTrawl(arguments: args)
    guard output.succeeded else { return [] }
    return ExpandedConversationMessageLine.parseMessagesOutput(output.text)
  }

  func loadRelatedRecords(
    around link: GloballyRoutableTrawlLink,
    window: String = "3d",
    limit: Int = 15
  ) async -> [AroundRelatedRecordRow] {
    let output = await runner.runTrawl(arguments: [
      "around",
      link.globallyRoutableTrawlLink,
      "--window", window,
      "--limit", String(limit),
    ])
    guard output.succeeded else { return [] }
    return AroundRelatedRecordRow.parseAroundOutput(output.text)
  }
}

/// Splits a space-aligned CLI table row on runs of two or more spaces (or tabs).
private func splitAlignedColumns(_ line: String) -> [String] {
  var columns: [String] = []
  var current = ""
  var consecutiveSpaces = 0
  for character in line {
    if character == "\t" {
      let trimmed = current.trimmingCharacters(in: .whitespaces)
      if !trimmed.isEmpty { columns.append(trimmed) }
      current = ""
      consecutiveSpaces = 0
    } else if character == " " {
      consecutiveSpaces += 1
      current.append(character)
    } else {
      if consecutiveSpaces >= 2 {
        let trimmed = current.trimmingCharacters(in: .whitespaces)
        if !trimmed.isEmpty { columns.append(trimmed) }
        current = String(character)
      } else {
        current.append(character)
      }
      consecutiveSpaces = 0
    }
  }
  let trailing = current.trimmingCharacters(in: .whitespaces)
  if !trailing.isEmpty { columns.append(trailing) }
  return columns
}
