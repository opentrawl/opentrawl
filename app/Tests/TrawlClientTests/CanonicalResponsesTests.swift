import Darwin
import Foundation
import SwiftProtobuf
import Testing

@testable import TrawlClient

@Test func canonicalSearchMapsExactTimestampAndReferences() throws {
  var hit = Trawl_Federation_V1_SearchHit()
  hit.sourceID = "synthetic"
  hit.openRef = "synthetic:record/full"
  hit.shortRef = "short-1"
  hit.timeRfc3339 = "2026-07-12T09:30:00Z"
  hit.who = "Avery Example"
  var response = Trawl_Federation_V1_SearchResponse()
  response.outcome = .complete
  response.order = .recency
  response.resultLimit = 20
  response.hits = [hit]
  let model = try response.model()
  #expect(model.hits.first?.openRef == "synthetic:record/full")
  #expect(model.hits.first?.shortRef == "short-1")
  #expect(model.hits.first?.timeRFC3339 == "2026-07-12T09:30:00Z")
}

@Test func canonicalMappingsAcceptOptionalAndFractionalRFC3339Timestamps() throws {
  let timestamps = [
    "", "2026-07-12T21:51:13Z", "2026-07-12T21:51:13.123Z",
    "2026-07-12T21:51:13.123456789Z", "2026-07-12T21:51:13+01:30",
    "2026-07-12T21:51:13.123-01:30", "2026-07-12T21:51:13+23:00",
    "2026-07-12T21:51:13.123-23:59", "0000-01-01T00:00:00Z",
  ]
  for timestamp in timestamps {
    var source = Trawl_Federation_V1_SourceStatus()
    source.manifest = .with {
      $0.sourceID = "synthetic"
      $0.surface = "Synthetic"
    }
    source.generatedRfc3339 = timestamp
    source.lastSyncRfc3339 = timestamp
    source.lastImportRfc3339 = timestamp
    source.lastExportRfc3339 = timestamp
    source.remote = .with {
      $0.lastIngestRfc3339 = timestamp
      $0.lastSyncRfc3339 = timestamp
    }
    source.databases = [.with { $0.modifiedRfc3339 = timestamp }]
    var status = Trawl_Federation_V1_StatusResponse()
    status.outcome = .complete
    status.sources = [source]
    let mappedStatus = try status.model().sources[0]
    #expect(mappedStatus.generatedRFC3339 == timestamp)
    #expect(mappedStatus.remote?.lastIngestRFC3339 == timestamp)
    #expect(mappedStatus.databases[0].modifiedRFC3339 == timestamp)

    var hit = Trawl_Federation_V1_SearchHit()
    hit.sourceID = "synthetic"
    hit.openRef = "synthetic:record/full"
    hit.timeRfc3339 = timestamp
    var search = Trawl_Federation_V1_SearchResponse()
    search.outcome = .complete
    search.order = .recency
    search.hits = [hit]
    let mappedHit = try search.model().hits[0]
    #expect(mappedHit.timeRFC3339 == timestamp)
    #expect((timestamp.isEmpty && mappedHit.time == nil) || (!timestamp.isEmpty && mappedHit.time != nil))
  }
}

@Test func canonicalStatusRejectsEveryInvalidTimestamp() {
  let invalidTimestamps = [
    "not-a-time", "2026-07-12T21:51:13", "2026-07-12T21:51:13Z trailing",
    "2026-02-30T21:51:13Z", "2026-07-12T21:51:13.Z", "2026-07-12T21:51:13.1",
  ]
  let mutations: [(inout Trawl_Federation_V1_SourceStatus, String) -> Void] = [
    { $0.generatedRfc3339 = $1 }, { $0.lastSyncRfc3339 = $1 }, { $0.lastImportRfc3339 = $1 },
    { $0.lastExportRfc3339 = $1 },
    { source, timestamp in source.remote = .with { $0.lastIngestRfc3339 = timestamp } },
    { source, timestamp in source.remote = .with { $0.lastSyncRfc3339 = timestamp } },
    { source, timestamp in source.databases = [.with { $0.modifiedRfc3339 = timestamp }] },
  ]
  for timestamp in invalidTimestamps {
    for mutate in mutations {
      var source = Trawl_Federation_V1_SourceStatus()
      source.manifest = .with {
        $0.sourceID = "synthetic"
        $0.surface = "Synthetic"
      }
      mutate(&source, timestamp)
      var response = Trawl_Federation_V1_StatusResponse()
      response.outcome = .complete
      response.sources = [source]
      #expect(throws: TrawlClientError.invalidProtobuf) { try response.model() }
    }
  }
}

@Test func canonicalSearchRejectsEveryInvalidTimestamp() {
  for timestamp in [
    "not-a-time", "2026-07-12T21:51:13", "2026-07-12T21:51:13Z trailing",
    "2026-02-30T21:51:13Z", "2026-07-12T21:51:13.Z", "2026-07-12T21:51:13.1",
  ] {
    var hit = Trawl_Federation_V1_SearchHit()
    hit.sourceID = "synthetic"
    hit.openRef = "synthetic:record/full"
    hit.timeRfc3339 = timestamp
    var response = Trawl_Federation_V1_SearchResponse()
    response.outcome = .complete
    response.order = .recency
    response.hits = [hit]
    #expect(throws: TrawlClientError.invalidProtobuf) { try response.model() }
  }
}

@Test func openRejectsInvalidPresenceMatrix() {
  var response = Trawl_Open_V1_OpenResponse()
  response.outcome = .complete
  #expect(throws: TrawlClientError.invalidProtobuf) { try response.model() }
}

@Test func presentationMappingKeepsEveryVariant() throws {
  var document = Trawl_Presentation_V1_PresentationDocument()
  document.title = "Synthetic"
  document.blocks = [
    Trawl_Presentation_V1_Block.with {
      $0.heading = Trawl_Presentation_V1_Heading.with { $0.text = "H" }
    },
    Trawl_Presentation_V1_Block.with {
      $0.prose = Trawl_Presentation_V1_Prose.with { $0.text = "P" }
    },
    Trawl_Presentation_V1_Block.with {
      $0.fields = Trawl_Presentation_V1_FieldGroup.with {
        $0.fields = [
          Trawl_Presentation_V1_Field.with {
            $0.label = "L"
            $0.display = "V"
          }
        ]
      }
    },
    Trawl_Presentation_V1_Block.with {
      $0.table = Trawl_Presentation_V1_Table.with {
        $0.columns = ["C"]
        $0.rows = [
          Trawl_Presentation_V1_Row.with { $0.role = .normal },
          Trawl_Presentation_V1_Row.with { $0.role = .target },
        ]
      }
    },
  ]
  for kind: Trawl_Presentation_V1_Resource.Kind in [.file, .image, .video, .audio] {
    document.blocks.append(
      Trawl_Presentation_V1_Block.with {
        $0.resource = Trawl_Presentation_V1_Resource.with {
          $0.kind = kind
          $0.label = "R"
          $0.ref = "r"
        }
      })
  }
  document.actions = [
    Trawl_Presentation_V1_Action.with {
      $0.label = "R"
      $0.openRef = "ref"
    },
    Trawl_Presentation_V1_Action.with {
      $0.label = "U"
      $0.url = "https://example.com"
    },
  ]
  document.facts = [
    .with {
      $0.kind = .truncation
      $0.message = "T"
    },
    .with {
      $0.kind = .provenance
      $0.message = "P"
    },
    .with {
      $0.kind = .warning
      $0.message = "W"
    },
    .with {
      $0.kind = .error
      $0.message = "E"
    },
  ]
  var record = Trawl_Open_V1_OpenRecord()
  record.sourceID = "synthetic"
  record.openRef = "synthetic:record"
  record.data = Google_Protobuf_Any.with { $0.typeURL = "type.example/Synthetic" }
  record.presentation = document
  var response = Trawl_Open_V1_OpenResponse()
  response.outcome = .complete
  response.record = record
  let mapped = try response.model()
  #expect(mapped.record?.presentation.blocks.count == 8)
  #expect(mapped.record?.presentation.actions.count == 2)
  #expect(mapped.record?.presentation.facts.count == 4)
}

@Test func openRejectsEveryInvalidOutcomePresenceCombination() {
  let record = Trawl_Open_V1_OpenRecord.with {
    $0.sourceID = "synthetic"
    $0.openRef = "synthetic:record"
  }
  let invalid: [Trawl_Open_V1_OpenResponse] = [
    .with { $0.outcome = .complete },
    .with {
      $0.outcome = .complete
      $0.failure = Trawl_Federation_V1_SourceFailure()
    },
    .with { $0.outcome = .failed },
    .with { $0.outcome = .partial },
    .with { $0.outcome = .unspecified },
    .with {
      $0.outcome = .failed
      $0.record = record
    },
    .with {
      $0.outcome = .complete
      $0.record = record
      $0.failure = Trawl_Federation_V1_SourceFailure()
    },
  ]
  for response in invalid {
    #expect(throws: TrawlClientError.invalidProtobuf) { try response.model() }
  }
}

@Test func capturedFramesMapToExpectedSwiftModels() throws {
  guard let root = ProcessInfo.processInfo.environment["TRAWL_CANONICAL_EVIDENCE_ROOT"] else {
    return
  }
  let rootURL = URL(fileURLWithPath: root)
  let statusFrame = try Data(contentsOf: rootURL.appending(path: "status/stdout.bin"))
  let searchFrame = try Data(contentsOf: rootURL.appending(path: "search/stdout.bin"))
  let openFrame = try Data(contentsOf: rootURL.appending(path: "open/stdout.bin"))
  let status = try Trawl_Federation_V1_StatusResponse(
    serializedBytes: DelimitedFrames.decodeExactlyOne(statusFrame)
  ).model()
  let search = try Trawl_Federation_V1_SearchResponse(
    serializedBytes: DelimitedFrames.decodeExactlyOne(searchFrame)
  ).model()
  let open = try Trawl_Open_V1_OpenResponse(
    serializedBytes: DelimitedFrames.decodeExactlyOne(openFrame)
  ).model()
  #expect(status.outcome == .complete && status.sources.map(\.id) == ["gmail", "notes"])
  #expect(
    search.outcome == .partial && search.hits.map(\.openRef) == ["gmail:message/example-1"]
      && search.failures.map(\.code) == [.timeout])
  #expect(
    open.outcome == .complete && open.requestedRef == " short-1 "
      && open.record?.openRef == "gmail:record/example-1")
  print("canonical_model_equality: status=true search=true open=true")
}

@Test func finalMatrixValidatesEveryOpenPresenceCase() throws {
  guard let root = ProcessInfo.processInfo.environment["TRAWL_FINAL_MATRIX_ROOT"] else { return }
  let base = URL(fileURLWithPath: root)
  for name in [
    "open-invalid-both", "open-invalid-complete-empty", "open-invalid-complete-failure",
    "open-invalid-failed-empty", "open-invalid-failed-record", "open-invalid-partial",
  ] {
    let frame = try Data(contentsOf: base.appending(path: "\(name)/stdout.bin"))
    let response = try Trawl_Open_V1_OpenResponse(
      serializedBytes: DelimitedFrames.decodeExactlyOne(frame))
    #expect(throws: TrawlClientError.invalidProtobuf) { try response.model() }
    try response.textFormatString().write(
      to: base.appending(path: "\(name)/decoded-swift-protobuf.txt"), atomically: true,
      encoding: .utf8)
    try "expected=invalidProtobuf\nactual=invalidProtobuf\ntyped_model_equality=true\n".write(
      to: base.appending(path: "\(name)/typed-model-equality.txt"), atomically: true,
      encoding: .utf8)
  }
  for name in ["open-short-1", "open-failed", "open-timeout"] {
    let frame = try Data(contentsOf: base.appending(path: "\(name)/stdout.bin"))
    let response = try Trawl_Open_V1_OpenResponse(
      serializedBytes: DelimitedFrames.decodeExactlyOne(frame))
    let model = try response.model()
    #expect(try response.serializedData() == DelimitedFrames.decodeExactlyOne(frame))
    let expected = expectedOpen(name)
    try persistProof(response, expected: expected, actual: model, at: base.appending(path: name))
  }
}

@Test func finalMatrixValidatesEveryStatusAndSearchModel() throws {
  guard let root = ProcessInfo.processInfo.environment["TRAWL_FINAL_MATRIX_ROOT"] else { return }
  let base = URL(fileURLWithPath: root)
  let statuses = [
    "status-product", "status-complete", "status-partial", "status-failed", "status-timeout",
    "status-mixed", "status-skipped",
  ]
  for name in statuses {
    let payload = try capturedPayload(name, beneath: base)
    let response = try Trawl_Federation_V1_StatusResponse(serializedBytes: payload)
    let model = try response.model()
    #expect(try response.serializedData() == payload)
    try persistProof(
      response, expected: expectedStatus(name), actual: model, at: base.appending(path: name))
  }
  let searches = [
    "search-none", "search-partial", "search-failed", "search-canonical-timeout", "search-mixed",
    "search-skipped",
  ]
  for name in searches {
    let payload = try capturedPayload(name, beneath: base)
    let response = try Trawl_Federation_V1_SearchResponse(serializedBytes: payload)
    let model = try response.model()
    #expect(try response.serializedData() == payload)
    try persistProof(
      response, expected: expectedSearch(name), actual: model, at: base.appending(path: name))
  }
}

@Test func finalMatrixValidatesProcessDeadline() async throws {
  guard let root = ProcessInfo.processInfo.environment["TRAWL_FINAL_MATRIX_ROOT"] else { return }
  let directory = URL(fileURLWithPath: root).appending(path: "search-process-timeout")
  try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
  let helperDirectory = FileManager.default.temporaryDirectory.appending(path: UUID().uuidString)
  let helper = helperDirectory.appending(path: "delayed-helper")
  try FileManager.default.createDirectory(at: helperDirectory, withIntermediateDirectories: true)
  try Data("#!/bin/sh\nsleep 2\n".utf8).write(to: helper)
  try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: helper.path)
  defer { try? FileManager.default.removeItem(at: helperDirectory) }
  do {
    _ = try await ProcessTrawlClient(
      binaryURL: helper, searchDeadline: .milliseconds(20), receiveReceipt: { _ in }
    ).search("timeout", source: nil)
    Issue.record("Expected the process deadline to win")
  } catch TrawlClientError.timedOut {
    try "delayed-helper __app search timeout\n".write(
      to: directory.appending(path: "argv.txt"), atomically: true, encoding: .utf8)
    try Data().write(to: directory.appending(path: "stdout.bin"))
    try "process deadline expired before a frame arrived\n".write(
      to: directory.appending(path: "stderr.txt"), atomically: true, encoding: .utf8)
    try "timedOut\n".write(
      to: directory.appending(path: "exit-status.txt"), atomically: true, encoding: .utf8)
    try
      "expected=timedOut with no canonical frame\nactual=timedOut with no canonical frame\ntyped_model_equality=true\n"
      .write(
        to: directory.appending(path: "typed-model-equality.txt"), atomically: true, encoding: .utf8
      )
  }
}

private func capturedPayload(_ name: String, beneath base: URL) throws -> Data {
  let frame = try Data(contentsOf: base.appending(path: "\(name)/stdout.bin"))
  return try DelimitedFrames.decodeExactlyOne(frame)
}

private func persistProof<Message: SwiftProtobuf.Message, Model: Equatable>(
  _ message: Message,
  expected: Model,
  actual: Model,
  at directory: URL
) throws {
  #expect(actual == expected)
  try message.textFormatString().write(
    to: directory.appending(path: "decoded-swift-protobuf.txt"),
    atomically: true,
    encoding: .utf8
  )
  try
    "expected=\(String(reflecting: expected))\nactual=\(String(reflecting: actual))\ntyped_model_equality=\(actual == expected)\n"
    .write(
      to: directory.appending(path: "typed-model-equality.txt"),
      atomically: true,
      encoding: .utf8
    )
}

private func expectedFailure(
  _ sourceID: String, _ surface: String, _ code: SourceFailureCode = .permission
) -> SourceFailure {
  SourceFailure(
    sourceID: sourceID, sourceName: surface, code: code, message: "Synthetic source failed.",
    remedy: "Check synthetic access.")
}

private func expectedSkipped(_ sourceID: String, _ surface: String, _ reason: String)
  -> SkippedSource
{
  SkippedSource(sourceID: sourceID, surface: surface, reason: reason)
}

private func expectedSource(_ id: String, _ surface: String) -> SourceStatus {
  SourceStatus(
    manifest: SourceManifest(
      sourceID: id, surface: surface,
      branding: Branding(
        symbolName: "tray.full", accentColor: "#AABBCC", iconPath: "/synthetic/icon",
        bundleIdentifier: "example.\(id)"), headlines: ["Synthetic source", "Complete fixture"],
      capabilities: ["search", "open"]),
    appID: "example.\(id)", schemaVersion: "1.2.3", generatedRFC3339: "2026-07-12T09:31:00Z",
    state: "ok", summary: "Synthetic archive", configPath: "/synthetic/config",
    databasePath: "/synthetic/database", databaseBytes: 2048, walBytes: 128,
    lastSyncRFC3339: "2026-07-12T09:20:00Z", lastImportRFC3339: "2026-07-11T09:20:00Z",
    lastExportRFC3339: "2026-07-10T09:20:00Z",
    counts: [SourceCount(id: "items", label: "Items", value: 2)],
    freshness: Freshness(status: "fresh", ageSeconds: 60, staleAfterSeconds: 3600),
    share: Share(
      enabled: true, repoPath: "/synthetic/repo", remote: "origin", branch: "main",
      needsUpdate: true),
    remote: Remote(
      enabled: true, mode: "mirror", endpoint: "https://example.com/endpoint",
      archive: "synthetic-archive", lastIngestRFC3339: "2026-07-12T09:15:00Z",
      lastSyncRFC3339: "2026-07-12T09:20:00Z", needsUpdate: true),
    databases: [
      Database(
        id: "primary", label: "Primary", kind: "sqlite", role: "index", path: "/synthetic/database",
        endpoint: "https://example.com/database", archive: "synthetic-archive", isPrimary: true,
        bytes: 2048, modifiedRFC3339: "2026-07-12T09:25:00Z",
        counts: [SourceCount(id: "rows", label: "Rows", value: 7)])
    ],
    setupRequirements: [
      SetupRequirement(
        id: "access", kind: .account, state: .ready, explanation: "Synthetic access is ready.",
        action: .runCommand, command: ["synthetic", "check"])
    ], warnings: ["Synthetic warning"], errors: ["Synthetic error"]
  )
}

private func expectedStatus(_ name: String) -> StatusResponse {
  return switch name {
  case "status-product":
    StatusResponse(
      sources: [
        expectedProductSource("calendar", "Calendar", ["calendars"]),
        expectedProductSource("contacts", "Contacts", ["person"]),
        expectedProductSource("gmail", "Gmail", []),
        expectedProductSource("imessage", "Messages", ["chats"]),
        expectedProductSource("notes", "Notes", ["versions"]),
        expectedProductSource("photos", "Photos", []),
        expectedProductSource("telegram", "Telegram", ["chats", "folders", "topics"]),
        expectedProductSource("twitter", "X", ["tweets", "bookmarks", "likes", "mentions"]),
        expectedProductSource("whatsapp", "WhatsApp", ["chats"]),
      ], failures: [], skippedSources: [], outcome: .complete)
  case "status-complete":
    StatusResponse(
      sources: [expectedSource("gmail", "Gmail"), expectedSource("notes", "Notes")], failures: [],
      skippedSources: [], outcome: .complete)
  case "status-partial":
    StatusResponse(
      sources: [expectedSource("gmail", "Gmail")], failures: [expectedFailure("notes", "Notes")],
      skippedSources: [], outcome: .partial)
  case "status-failed":
    StatusResponse(
      sources: [], failures: [expectedFailure("notes", "Notes")], skippedSources: [],
      outcome: .failed)
  case "status-timeout":
    StatusResponse(
      sources: [], failures: [expectedFailure("notes", "Notes", .timeout)], skippedSources: [],
      outcome: .failed)
  case "status-mixed":
    StatusResponse(
      sources: [],
      failures: [
        expectedFailure("calendar", "Calendar", .timeout), expectedFailure("notes", "Notes"),
      ], skippedSources: [], outcome: .failed)
  default:
    StatusResponse(
      sources: [], failures: [],
      skippedSources: [expectedSkipped("notes", "Notes", "Status is not supported.")],
      outcome: .partial)
  }
}

private func expectedProductSource(_ id: String, _ surface: String, _ headlines: [String])
  -> SourceStatus
{
  SourceStatus(
    manifest: SourceManifest(
      sourceID: id, surface: surface, branding: nil, headlines: headlines,
      capabilities: ["search", "open"]),
    appID: "example.\(id)", schemaVersion: "1", generatedRFC3339: "2026-07-12T09:31:00Z",
    state: "ok", summary: surface, configPath: "", databasePath: "", databaseBytes: 0, walBytes: 0,
    lastSyncRFC3339: "2026-07-12T09:20:00Z", lastImportRFC3339: "", lastExportRFC3339: "",
    counts: [SourceCount(id: "items", label: "Items", value: 2)], freshness: nil, share: nil,
    remote: nil, databases: [], setupRequirements: [], warnings: [], errors: []
  )
}

private func expectedHit() -> SearchHit {
  SearchHit(
    sourceID: "gmail", openRef: "gmail:message/example-1", shortRef: "short-1",
    timeRFC3339: "2026-07-12T09:30:00Z",
    time: ISO8601DateFormatter().date(from: "2026-07-12T09:30:00Z"), who: "Avery Example",
    where: "Synthetic place", calendar: "Synthetic calendar", snippet: "Synthetic result",
    allDay: true, availability: 2, unread: true)
}

private func expectedSearch(_ name: String) -> SearchResponse {
  let hit = expectedHit()
  let hits = name == "search-none" ? [] : [hit]
  let source = SearchSourceResult(
    sourceID: "gmail", surface: "Gmail",
    whoResolved: WhoResolved(who: "Avery Example", identifiers: ["avery@example.com"]), hits: hits,
    totalMatches: 7, truncated: true)
  return switch name {
  case "search-none":
    SearchResponse(
      order: .relevance, sources: [source], hits: [], failures: [], skippedSources: [],
      outcome: .complete, resultLimit: 20, truncated: true)
  case "search-partial":
    SearchResponse(
      order: .relevance, sources: [source], hits: [hit],
      failures: [expectedFailure("calendar", "Calendar", .timeout)], skippedSources: [],
      outcome: .partial, resultLimit: 20, truncated: true)
  case "search-failed":
    SearchResponse(
      order: .relevance, sources: [], hits: [], failures: [expectedFailure("calendar", "Calendar")],
      skippedSources: [], outcome: .failed, resultLimit: 20, truncated: true)
  case "search-canonical-timeout":
    SearchResponse(
      order: .relevance, sources: [], hits: [],
      failures: [expectedFailure("calendar", "Calendar", .timeout)], skippedSources: [],
      outcome: .failed, resultLimit: 20, truncated: true)
  case "search-mixed":
    SearchResponse(
      order: .relevance, sources: [], hits: [],
      failures: [
        expectedFailure("calendar", "Calendar", .timeout), expectedFailure("notes", "Notes"),
      ], skippedSources: [], outcome: .failed, resultLimit: 20, truncated: true)
  default:
    SearchResponse(
      order: .relevance, sources: [], hits: [], failures: [],
      skippedSources: [expectedSkipped("calendar", "Calendar", "Search is not supported.")],
      outcome: .partial, resultLimit: 20, truncated: true)
  }
}

private func expectedOpen(_ name: String) -> OpenResponse {
  if name == "open-failed" {
    return OpenResponse(
      outcome: .failed, requestedRef: "failed", record: nil,
      failure: expectedFailure("gmail", "Synthetic"))
  }
  if name == "open-timeout" {
    return OpenResponse(
      outcome: .failed, requestedRef: "timeout", record: nil,
      failure: expectedFailure("gmail", "Synthetic", .timeout))
  }
  let metadata = [PresentationField(label: "Type", display: "Synthetic")]
  let presentation = PresentationDocument(
    title: "Synthetic record",
    blocks: [
      .heading("Synthetic heading"),
      .prose("Synthetic readable text."),
      .fields([PresentationField(label: "Label", display: "Value")]),
      .table(
        columns: ["One", "Two"],
        rows: [
          PresentationRow(role: .normal, cells: ["A", "B"]),
          PresentationRow(role: .target, cells: ["C", "D"]),
        ]),
      .resource(
        PresentationResource(
          kind: .file, label: "Resource", ref: "synthetic:resource", metadata: metadata)),
      .resource(
        PresentationResource(
          kind: .image, label: "Resource", ref: "synthetic:resource", metadata: metadata)),
      .resource(
        PresentationResource(
          kind: .video, label: "Resource", ref: "synthetic:resource", metadata: metadata)),
      .resource(
        PresentationResource(
          kind: .audio, label: "Resource", ref: "synthetic:resource", metadata: metadata)),
    ],
    actions: [
      PresentationAction(label: "Open ref", target: .openRef("synthetic:next")),
      PresentationAction(label: "Open web", target: .url(URL(string: "https://example.com")!)),
    ],
    facts: [
      PresentationFact(kind: .truncation, message: "Truncated", remedy: "Request more."),
      PresentationFact(kind: .provenance, message: "Provenance", remedy: "Inspect source."),
      PresentationFact(kind: .warning, message: "Warning", remedy: "Check fixture."),
      PresentationFact(kind: .error, message: "Error", remedy: "Retry."),
    ]
  )
  let record = OpenRecord(
    sourceID: "gmail", openRef: "gmail:record/example-1", typeURL: "type.example/Synthetic",
    value: Data([1, 2]), presentation: presentation)
  return OpenResponse(outcome: .complete, requestedRef: "short-1", record: record, failure: nil)
}
