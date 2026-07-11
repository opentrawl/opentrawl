import Darwin
import Foundation
import SwiftProtobuf
import TrawlClient

private func write<Message>(_ message: Message) throws where Message: SwiftProtobuf.Message {
  FileHandle.standardOutput.write(try DelimitedFrames.encode(message))
}

private func count(_ id: String, _ display: String) -> Trawl_App_V1_Count {
  var value = Trawl_App_V1_Count()
  value.id = id
  value.display = display
  return value
}

private func source(
  _ id: String,
  _ name: String,
  _ state: String,
  _ summary: String,
  _ display: String,
  _ synced: String,
  _ archiveBytes: Int64
) -> Trawl_App_V1_SourceStatus {
  var value = Trawl_App_V1_SourceStatus()
  value.appID = id
  value.surface = name
  value.state = state
  value.summary = summary
  value.counts = [count("items", display)]
  value.lastSyncedDisplay = synced
  value.archiveBytes = archiveBytes
  return value
}

private func hit(
  _ reference: String,
  _ sourceID: String,
  _ title: String,
  _ snippet: String,
  _ when: String
) -> Trawl_App_V1_SearchHit {
  var value = Trawl_App_V1_SearchHit()
  value.openRef = reference
  value.appID = sourceID
  value.title = title
  value.snippet = snippet
  value.whenDisplay = when
  return value
}

private func failure(
  _ sourceID: String,
  _ surface: String,
  _ message: String,
  _ remedy: String
) -> Trawl_App_V1_SourceFailure {
  var value = Trawl_App_V1_SourceFailure()
  value.appID = sourceID
  value.surface = surface
  value.code = .permission
  value.message = message
  value.remedy = remedy
  return value
}

private let sources = [
  source("imessage", "Messages", "ok", "Recently synced.", "27 messages", "1h ago", 620_000),
  source("whatsapp", "WhatsApp", "ok", "Recently synced.", "31 messages", "45m ago", 710_000),
  source("telegram", "Telegram", "ok", "Recently synced.", "24 messages", "2h ago", 520_000),
  source("gmail", "Gmail", "ok", "Recently synced.", "42 messages", "just now", 840_000),
  source("calendar", "Calendar", "ok", "Recently synced.", "12 events", "30m ago", 180_000),
  source("contacts", "Contacts", "ok", "Recently synced.", "8 people", "1d ago", 140_000),
  source("photos", "Photos", "stale", "Sync recommended.", "16 photos", "2d ago", 1_900_000),
  source("twitter", "X", "ok", "Recently synced.", "18 posts", "12m ago", 360_000),
  source("notes", "Notes", "ok", "Recently synced.", "9 notes", "3h ago", 210_000),
]

private let hits = [
  hit("gmail:message:example-1", "gmail", "Avery Example", "Project Lantern is ready for a final review.", "10 Jul"),
  hit("imessage:message:example-2", "imessage", "+15550001111", "The synthetic pickup moved to Friday at 14:00.", "9 Jul"),
  hit("notes:note:example-3", "notes", "Packing list", "Passport, charger and the example train ticket.", "8 Jul"),
]

private func status() throws {
  var response = Trawl_App_V1_StatusResponse()
  response.outcome = .complete
  response.sources = sources
  try write(response)
}

private func syntheticSync() throws {
  var response = Trawl_App_V1_SyncResponse()
  response.outcome = .complete
  response.sources = sources.map { source in
    var result = Trawl_App_V1_SyncSourceResult()
    result.appID = source.appID
    result.surface = source.surface
    result.outcome = .complete
    return result
  }
  try write(response)
}

private func search(_ arguments: [String]) throws {
  let query = arguments.last?.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() ?? ""
  let sourceID: String?
  if let index = arguments.firstIndex(of: "--source"), arguments.indices.contains(index + 1) {
    sourceID = arguments[index + 1]
  } else {
    sourceID = nil
  }

  if query == "timeout" {
    Thread.sleep(forTimeInterval: 30)
  }

  var response = Trawl_App_V1_SearchResponse()
  response.resultLimit = 20
  switch query {
  case "failed":
    response.outcome = .failed
    response.failures = [failure("calendar", "Calendar", "Synthetic calendar search failed.", "Check calendar access.")]
  case "none":
    response.outcome = .complete
  default:
    response.hits = hits.filter { sourceID == nil || $0.appID == sourceID }
    if query == "partial" {
      response.outcome = .partial
      response.failures = [failure("calendar", "Calendar", "Synthetic calendar search failed.", "Check calendar access.")]
    } else {
      response.outcome = .complete
    }
  }
  try write(response)
}

private func open(_ reference: String) throws {
  var response = Trawl_App_V1_OpenResponse()
  response.openRef = reference
  if reference == "failed" {
    response.outcome = .failed
    response.failure = failure("notes", "Notes", "Synthetic result could not be opened.", "Try the search again.")
    try write(response)
    return
  }

  response.outcome = .complete
  if reference.hasPrefix("imessage:") {
    response.appID = "imessage"
    response.output = Data(
      "Source: Messages\nFrom: +15550001111\nDate: 9 July 2026\n\nThe synthetic pickup moved to Friday at 14:00.\n".utf8
    )
  } else if reference.hasPrefix("notes:") {
    response.appID = "notes"
    response.output = Data(
      "Source: Notes\nTitle: Packing list\n\nPassport, charger and the example train ticket.\n".utf8
    )
  } else {
    response.appID = "gmail"
    response.output = Data(
      "Source: Gmail\nFrom: Avery Example <avery@example.com>\nSubject: Project Lantern\nDate: 10 July 2026\n\nProject Lantern is ready for a final review.\n".utf8
    )
  }
  try write(response)
}

private func run(_ arguments: [String]) throws {
  if arguments == ["__app", "status"] { return try status() }
  if arguments == ["__app", "sync"] { return try syntheticSync() }
  if arguments.starts(with: ["__app", "search"]) { return try search(Array(arguments.dropFirst(2))) }
  if arguments.starts(with: ["__app", "open"]), arguments.count == 3 {
    return try open(arguments[2])
  }
  FileHandle.standardError.write(Data("unsupported synthetic command\n".utf8))
  exit(2)
}

do {
  try run(Array(CommandLine.arguments.dropFirst()))
} catch {
  FileHandle.standardError.write(Data("synthetic helper failed: \(error)\n".utf8))
  exit(2)
}
