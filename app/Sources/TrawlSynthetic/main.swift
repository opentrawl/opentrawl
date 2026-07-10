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

private func status() throws -> Int32 {
  let values = [
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
  for value in values { try write(value) }
  return 0
}

private func search(_ arguments: [String]) throws -> Int32 {
  let query = arguments.last?.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() ?? ""
  var sourceID: String?
  if let index = arguments.firstIndex(of: "--source"), arguments.indices.contains(index + 1) {
    sourceID = arguments[index + 1]
  }

  switch query {
  case "timeout":
    Thread.sleep(forTimeInterval: 30)
    return 0
  case "failed":
    FileHandle.standardError.write(Data("synthetic total failure\n".utf8))
    return 1
  case "none":
    return 0
  default:
    break
  }

  let values = [
    hit("gmail:message:example-1", "gmail", "Avery Example", "Project Lantern is ready for a final review.", "10 Jul"),
    hit("imessage:message:example-2", "imessage", "+15550001111", "The synthetic pickup moved to Friday at 14:00.", "9 Jul"),
    hit("notes:note:example-3", "notes", "Packing list", "Passport, charger and the example train ticket.", "8 Jul"),
  ]
  for value in values where sourceID == nil || value.appID == sourceID { try write(value) }
  if query == "partial" {
    FileHandle.standardError.write(Data("one synthetic source failed\n".utf8))
    return 3
  }
  return 0
}

private func open(_ reference: String) -> Int32 {
  let output: String
  if reference.hasPrefix("imessage:") {
    output = """
      Source: Messages
      From: +15550001111
      Date: 9 July 2026

      The synthetic pickup moved to Friday at 14:00.
      """
  } else if reference.hasPrefix("notes:") {
    output = """
      Source: Notes
      Title: Packing list

      Passport, charger and the example train ticket.
      """
  } else {
    output = """
      Source: Gmail
      From: Avery Example <avery@example.com>
      Subject: Project Lantern
      Date: 10 July 2026

      Project Lantern is ready for a final review.
      """
  }
  FileHandle.standardOutput.write(Data((output + "\n").utf8))
  return 0
}

private func run(_ arguments: [String]) throws -> Int32 {
  if arguments.starts(with: ["__app", "status"]) { return try status() }
  if arguments.starts(with: ["__app", "sync"]) { return 0 }
  if arguments.starts(with: ["__app", "search"]) { return try search(Array(arguments.dropFirst(2))) }
  if arguments.first == "open", arguments.count == 2 { return open(arguments[1]) }
  FileHandle.standardError.write(Data("unsupported synthetic command\n".utf8))
  return 2
}

do {
  exit(try run(Array(CommandLine.arguments.dropFirst())))
} catch {
  FileHandle.standardError.write(Data("synthetic helper failed: \(error)\n".utf8))
  exit(2)
}
