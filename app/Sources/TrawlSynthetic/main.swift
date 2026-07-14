import Darwin
import Foundation
import SwiftProtobuf
import TrawlClient

private func write<Message>(_ message: Message) throws where Message: SwiftProtobuf.Message {
  FileHandle.standardOutput.write(try DelimitedFrames.encode(message))
}
private func count(_ id: String = "items", _ label: String = "Items", _ value: Int64 = 2)
  -> Trawl_Federation_V1_Count
{
  .with {
    $0.id = id
    $0.label = label
    $0.value = value
  }
}
private func manifest(_ id: String, _ surface: String) -> Trawl_Federation_V1_SourceManifest {
  .with {
    $0.sourceID = id
    $0.displayName = surface
    $0.branding = .with {
      $0.symbolName = "tray.full"
      $0.accentColor = "#AABBCC"
      $0.iconPath = "/synthetic/icon"
      $0.bundleIdentifier = "example.\(id)"
    }
    $0.headlines = ["Synthetic source", "Complete fixture"]
    $0.capabilities = ["search", "open"]
  }
}
private func source(_ id: String, _ surface: String) -> Trawl_Federation_V1_SourceStatus {
  .with {
    $0.manifest = manifest(id, surface)
    $0.appID = "example.\(id)"
    $0.schemaVersion = "1.2.3"
    $0.generatedRfc3339 = "2026-07-12T09:31:00Z"
    $0.state = "ok"
    $0.summary = "Synthetic archive"
    $0.configPath = "/synthetic/config"
    $0.databasePath = "/synthetic/database"
    $0.databaseBytes = 2048
    $0.walBytes = 128
    $0.lastSyncRfc3339 = "2026-07-12T09:20:00Z"
    $0.lastImportRfc3339 = "2026-07-11T09:20:00Z"
    $0.lastExportRfc3339 = "2026-07-10T09:20:00Z"
    $0.counts = [count()]
    $0.freshness = .with {
      $0.status = "fresh"
      $0.ageSeconds = 60
      $0.staleAfterSeconds = 3600
    }
    $0.share = .with {
      $0.enabled = true
      $0.repoPath = "/synthetic/repo"
      $0.remote = "origin"
      $0.branch = "main"
      $0.needsUpdate = true
    }
    $0.remote = .with {
      $0.enabled = true
      $0.mode = "mirror"
      $0.endpoint = "https://example.com/endpoint"
      $0.archive = "synthetic-archive"
      $0.lastIngestRfc3339 = "2026-07-12T09:15:00Z"
      $0.lastSyncRfc3339 = "2026-07-12T09:20:00Z"
      $0.needsUpdate = true
    }
    $0.databases = [
      .with {
        $0.id = "primary"
        $0.label = "Primary"
        $0.kind = "sqlite"
        $0.role = "index"
        $0.path = "/synthetic/database"
        $0.endpoint = "https://example.com/database"
        $0.archive = "synthetic-archive"
        $0.isPrimary = true
        $0.bytes = 2048
        $0.modifiedRfc3339 = "2026-07-12T09:25:00Z"
        $0.counts = [count("rows", "Rows", 7)]
      }
    ]
    $0.setupRequirements = [
      .with {
        $0.id = "access"
        $0.kind = .account
        $0.state = .ready
        $0.explanation = "Synthetic access is ready."
        $0.action = .runCommand
        $0.command = ["synthetic", "check"]
      }
    ]
    $0.warnings = ["Synthetic warning"]
    $0.errors = ["Synthetic error"]
  }
}
private func productSource(_ id: String, _ surface: String, _ headlines: [String])
  -> Trawl_Federation_V1_SourceStatus
{
  .with {
    $0.manifest = .with {
      $0.sourceID = id
      $0.displayName = surface
      $0.headlines = headlines
      $0.capabilities = ["search", "open"]
    }
    $0.appID = "example.\(id)"
    $0.schemaVersion = "1"
    $0.generatedRfc3339 = "2026-07-12T09:31:00Z"
    $0.state = "ok"
    $0.summary = surface
    $0.lastSyncRfc3339 = "2026-07-12T09:20:00Z"
    $0.counts = [count()]
  }
}
private func productSources() -> [Trawl_Federation_V1_SourceStatus] {
  [
    productSource("calendar", "Calendar", ["events", "calendars"]),
    productSource("contacts", "Contacts", ["people"]),
    productSource("gmail", "Gmail", ["emails"]),
    productSource("imessage", "Messages", ["chats"]),
    productSource("notes", "Notes", ["notes", "folders", "versions"]),
    productSource("photos", "Photos", ["photos"]),
    productSource("telegram", "Telegram", ["chats", "folders", "topics"]),
    productSource("twitter", "Twitter (X)", ["tweets", "bookmarks", "likes", "mentions"]),
    productSource("whatsapp", "WhatsApp", ["chats", "groups"]),
  ]
}
private func hit(_ sourceID: String, _ ref: String, _ who: String) -> Trawl_Federation_V1_SearchHit
{
  .with {
    $0.sourceID = sourceID
    $0.openRef = ref
    $0.shortRef = "short-1"
    $0.timeRfc3339 = "2026-07-12T09:30:00Z"
    $0.anchorID = "match"
    $0.summary = .with {
      $0.title = "Synthetic place"
      $0.subtitle = who
    }
    $0.evidence = [
      .with {
        $0.label = "Message body"
        $0.text = .with {
          $0.runs = [
            .with {
              $0.text = "Synthetic result"
              $0.matched = true
            }
          ]
        }
      }
    ]
    $0.allDay = true
    $0.availability = 2
    $0.unread = true
  }
}
private func failure(
  _ sourceID: String, _ surface: String, _ code: Trawl_Federation_V1_FailureCode = .permission
) -> Trawl_Federation_V1_SourceFailure {
  var value = Trawl_Federation_V1_SourceFailure()
  value.sourceID = sourceID
  value.surface = surface
  value.code = code
  value.message = "Synthetic source failed."
  value.remedy = "Check synthetic access."
  return value
}
private func status() throws {
  var response = Trawl_Federation_V1_StatusResponse()
  switch ProcessInfo.processInfo.environment["TRAWL_SYNTHETIC_STATUS"] ?? "complete" {
  case "product":
    response.outcome = .complete
    response.sources = productSources()
  case "partial":
    response.outcome = .partial
    response.sources = [source("gmail", "Gmail")]
    response.failures = [failure("notes", "Notes")]
  case "failed", "timeout":
    response.outcome = .failed
    response.failures = [
      failure(
        "notes", "Notes",
        ProcessInfo.processInfo.environment["TRAWL_SYNTHETIC_STATUS"] == "timeout"
          ? .timeout : .permission)
    ]
  case "mixed":
    response.outcome = .failed
    response.failures = [
      failure("calendar", "Calendar", .timeout), failure("notes", "Notes", .permission),
    ]
  case "skipped":
    response.outcome = .partial
    response.skippedSources = [
      Trawl_Federation_V1_SkippedSource.with {
        $0.sourceID = "notes"
        $0.surface = "Notes"
        $0.reason = "Status is not supported."
      }
    ]
  default:
    response.outcome = .complete
    response.sources = [source("gmail", "Gmail"), source("notes", "Notes")]
  }
  try write(response)
}
private func syntheticSync() throws {
  var response = Trawl_App_V1_SyncResponse()
  response.outcome = .complete
  response.sources = productSources().map { source in
    .with {
      $0.appID = source.manifest.sourceID
      $0.surface = source.manifest.displayName
      $0.outcome = .complete
    }
  }
  try write(response)
}
private func search(_ arguments: [String]) throws {
  let query = arguments.last ?? ""
  if query == "frame-missing" { return }
  if query == "frame-truncated" {
    FileHandle.standardOutput.write(Data([1, 0, 0]))
    return
  }
  if query == "frame-oversized" {
    FileHandle.standardOutput.write(Data([1, 0, 0, 1]))
    return
  }
  if query == "frame-invalid-protobuf" {
    FileHandle.standardOutput.write(Data([1, 0, 0, 0, 255]))
    return
  }
  if query == "frame-extra" {
    FileHandle.standardOutput.write(Data([2, 0, 0, 0, 8, 1, 0]))
    return
  }
  if query == "process-nonzero" { exit(7) }
  if query == "process-hang" {
    Thread.sleep(forTimeInterval: 30)
    return
  }
  var response = Trawl_Federation_V1_SearchResponse()
  response.order = .relevance
  response.resultLimit = 20
  response.truncated = true
  if query == "complete-frame-hang" {
    response.outcome = .complete
    try write(response)
    Thread.sleep(forTimeInterval: 30)
    return
  }
  if query == "timeout" { Thread.sleep(forTimeInterval: 30) }
  if query == "failed" || query == "canonical-timeout" || query == "mixed" {
    response.outcome = .failed
    response.failures =
      query == "mixed"
      ? [failure("calendar", "Calendar", .timeout), failure("notes", "Notes", .permission)]
      : [failure("calendar", "Calendar", query == "canonical-timeout" ? .timeout : .permission)]
  } else if query == "skipped" {
    response.outcome = .partial
    response.skippedSources = [
      Trawl_Federation_V1_SkippedSource.with {
        $0.sourceID = "calendar"
        $0.surface = "Calendar"
        $0.reason = "Search is not supported."
      }
    ]
  } else {
    let value = hit("gmail", "gmail:message/example-1", "Avery Example")
    response.outcome = query == "partial" ? .partial : .complete
    response.hits = query == "none" ? [] : [value]
    response.sources = [
      Trawl_Federation_V1_SearchSourceResult.with {
        $0.sourceID = "gmail"
        $0.displayName = "Gmail"
        $0.whoResolved = .with {
          $0.who = "Avery Example"
          $0.identifiers = ["avery@example.com"]
        }
        $0.hits = response.hits
        $0.totalMatches = 7
        $0.truncated = true
      }
    ]
    if query == "partial" { response.failures = [failure("calendar", "Calendar", .timeout)] }
  }
  try write(response)
}
private func open(_ arguments: [String]) throws {
  guard arguments.count == 3 else { exit(2) }
  let sourceID = arguments[0]
  let requestedRef = arguments[1]
  let requestedAnchorID = arguments[2]
  var response = Trawl_Open_V1_OpenResponse()
  response.requestedRef = requestedRef
  response.requestedAnchorID = requestedAnchorID
  if requestedRef == "failed" {
    response.outcome = .failed
    response.failure = failure(sourceID, "Synthetic")
    try write(response)
    return
  }
  if requestedRef == "timeout" {
    response.outcome = .failed
    response.failure = failure(sourceID, "Synthetic", .timeout)
    try write(response)
    return
  }
  if requestedRef == "invalid-complete-empty" {
    response.outcome = .complete
    try write(response)
    return
  }
  if requestedRef == "invalid-complete-failure" {
    response.outcome = .complete
    response.failure = failure(sourceID, "Synthetic")
    try write(response)
    return
  }
  if requestedRef == "invalid-failed-empty" {
    response.outcome = .failed
    try write(response)
    return
  }
  if requestedRef == "invalid-partial" {
    response.outcome = .partial
    try write(response)
    return
  }
  var document = Trawl_Presentation_V1_PresentationDocument()
  document.title = "Synthetic record"
  document.primaryAnchorID = requestedAnchorID
  document.blocks = [
    Trawl_Presentation_V1_Block.with {
      $0.anchorID = requestedAnchorID
      $0.heading = Trawl_Presentation_V1_Heading.with { $0.text = "Synthetic heading" }
    },
    Trawl_Presentation_V1_Block.with {
      $0.prose = Trawl_Presentation_V1_Prose.with { $0.text = "Synthetic readable text." }
    },
    Trawl_Presentation_V1_Block.with {
      $0.fields = Trawl_Presentation_V1_FieldGroup.with {
        $0.fields = [
          Trawl_Presentation_V1_Field.with {
            $0.label = "Label"
            $0.display = "Value"
          }
        ]
      }
    },
    Trawl_Presentation_V1_Block.with {
      $0.table = Trawl_Presentation_V1_Table.with {
        $0.columns = ["One", "Two"]
        $0.rows = [
          Trawl_Presentation_V1_Row.with {
            $0.role = .normal
            $0.cells = [
              Trawl_Presentation_V1_Cell.with { $0.display = "A" },
              Trawl_Presentation_V1_Cell.with { $0.display = "B" },
            ]
          },
          Trawl_Presentation_V1_Row.with {
            $0.role = .target
            $0.cells = [
              Trawl_Presentation_V1_Cell.with { $0.display = "C" },
              Trawl_Presentation_V1_Cell.with { $0.display = "D" },
            ]
          },
        ]
      }
    },
  ]
  for kind: Trawl_Presentation_V1_Resource.Kind in [.file, .image, .video, .audio] {
    document.blocks.append(
      Trawl_Presentation_V1_Block.with {
        $0.resource = Trawl_Presentation_V1_Resource.with {
          $0.kind = kind
          $0.label = "Resource"
          $0.ref = "\(sourceID):resource/example-1"
          $0.metadata = [
            .with {
              $0.label = "Type"
              $0.display = "Synthetic"
            }
          ]
        }
      })
  }
  document.actions = [
    Trawl_Presentation_V1_Action.with {
      $0.label = "Open ref"
      $0.openRef = "\(sourceID):record/next"
    },
    Trawl_Presentation_V1_Action.with {
      $0.label = "Open web"
      $0.url = "https://example.com"
    },
  ]
  document.facts = [
    Trawl_Presentation_V1_Fact.with {
      $0.kind = .truncation
      $0.message = "Truncated"
      $0.remedy = "Request more."
    },
    Trawl_Presentation_V1_Fact.with {
      $0.kind = .provenance
      $0.message = "Provenance"
      $0.remedy = "Inspect source."
    },
    Trawl_Presentation_V1_Fact.with {
      $0.kind = .warning
      $0.message = "Warning"
      $0.remedy = "Check fixture."
    },
    Trawl_Presentation_V1_Fact.with {
      $0.kind = .error
      $0.message = "Error"
      $0.remedy = "Retry."
    },
  ]
  var record = Trawl_Open_V1_OpenRecord()
  record.sourceID = sourceID
  record.openRef = "\(sourceID):record/example-1"
  record.data = Google_Protobuf_Any.with {
    $0.typeURL = "type.example/Synthetic"
    $0.value = Data([1, 2])
  }
  record.presentation = document
  if requestedRef == "invalid-both" { response.failure = failure(sourceID, "Synthetic") }
  response.outcome = requestedRef == "invalid-failed-record" ? .failed : .complete
  response.record = record
  try write(response)
}
private func resource(_ arguments: [String]) throws {
  guard arguments.count == 3, let maxBytes = UInt32(arguments[2]) else { exit(2) }
  let sourceID = arguments[0]
  let ref = arguments[1]
  let data = Data("synthetic resource bytes".utf8)
  guard sourceID == "photos", ref == "photos:resource/example-1", data.count <= Int(maxBytes)
  else { exit(2) }
  try write(
    Trawl_Presentation_V1_ResourceResponse.with {
      $0.resourceRef = ref
      $0.contentType = "image/jpeg"
      $0.data = data
    })
}
private func evidence(_ arguments: [String]) throws {
  guard arguments.count == 2 else { exit(2) }
  let frame = try Data(contentsOf: URL(fileURLWithPath: arguments[1]))
  let payload = try DelimitedFrames.decodeExactlyOne(frame)
  switch arguments[0] {
  case "status":
    let message = try Trawl_Federation_V1_StatusResponse(serializedBytes: payload)
    guard try message.serializedData() == payload else { exit(1) }
    print(message.textFormatString())
    print("deterministic_payload_equality: true")
  case "search":
    let message = try Trawl_Federation_V1_SearchResponse(serializedBytes: payload)
    guard try message.serializedData() == payload else { exit(1) }
    print(message.textFormatString())
    print("deterministic_payload_equality: true")
  case "open":
    let message = try Trawl_Open_V1_OpenResponse(serializedBytes: payload)
    guard try message.serializedData() == payload else { exit(1) }
    print(message.textFormatString())
    print("deterministic_payload_equality: true")
  default: exit(2)
  }
}
do {
  let arguments = Array(CommandLine.arguments.dropFirst())
  if arguments == ["__app", "status"] {
    try status()
  } else if arguments == ["__app", "request-photos"] {
    try status()
  } else if arguments == ["__app", "sync"] {
    try syntheticSync()
  } else if arguments.starts(with: ["__app", "search"]) {
    try search(Array(arguments.dropFirst(2)))
  } else if arguments.starts(with: ["__app", "open"]) {
    try open(Array(arguments.dropFirst(2)))
  } else if arguments.starts(with: ["__app", "resource"]) {
    try resource(Array(arguments.dropFirst(2)))
  } else if arguments.starts(with: ["--evidence"]) {
    try evidence(Array(arguments.dropFirst()))
  } else {
    exit(2)
  }
} catch {
  FileHandle.standardError.write(Data("synthetic helper failed: \(error)\n".utf8))
  exit(2)
}
