import Foundation
import Testing

@testable import TrawlClient
@testable import TrawlCore

private struct SearchClient: TrawlClient {
  let response: SearchResponse
  func status() async throws -> StatusResponse { fatalError() }
  func requestPhotos() async throws -> StatusResponse { fatalError() }
  func sync() async throws -> SyncResponse { fatalError() }
  func search(_: String, source _: String?) async throws -> SearchResponse { response }
  func open(sourceID _: String, ref _: String, anchorID _: String) async throws -> OpenResponse {
    fatalError()
  }
}

@MainActor
@Test func partialSearchKeepsUsefulCanonicalHits() async throws {
  var hit = Trawl_Federation_V1_SearchHit()
  hit.sourceID = "synthetic"
  hit.openRef = "synthetic:record/full"
  hit.shortRef = "short-1"
  hit.timeRfc3339 = "2026-07-12T09:30:00Z"
  hit.anchorID = "match"
  hit.summary = .with { $0.title = "Synthetic record" }
  hit.evidence = [
    .with {
      $0.label = "Matching text"
      $0.text = .with {
        $0.runs = [
          .with {
            $0.text = "Synthetic"
            $0.matched = true
          }
        ]
      }
    }
  ]
  var failure = Trawl_Federation_V1_SourceFailure()
  failure.sourceID = "slow"
  failure.surface = "Slow source"
  failure.code = .timeout
  failure.message = "Synthetic timeout"
  var response = Trawl_Federation_V1_SearchResponse()
  response.outcome = .partial
  response.order = .recency
  response.resultLimit = 20
  response.hits = [hit]
  response.failures = [failure]
  let model = SearchModel(client: SearchClient(response: try response.model()), debounce: .zero)
  await model.search("synthetic", source: nil)
  #expect(model.phase == .partial)
  #expect(model.results.map(\.openRef) == ["synthetic:record/full"])
  #expect(model.failures.map(\.code) == [.timeout])
  #expect(model.hasTimeoutFailure)
}

@MainActor
@Test func timeoutGuidanceUsesTypedFailuresAndResponseSurfaceFallback() async throws {
  var hit = Trawl_Federation_V1_SearchHit()
  hit.sourceID = "response-only"
  hit.openRef = "response-only:record/1"
  hit.anchorID = "match"
  hit.summary = .with { $0.title = "Synthetic record" }
  hit.evidence = [
    .with {
      $0.label = "Matching text"
      $0.text = .with {
        $0.runs = [
          .with {
            $0.text = "Synthetic"
            $0.matched = true
          }
        ]
      }
    }
  ]
  var source = Trawl_Federation_V1_SearchSourceResult()
  source.sourceID = "response-only"
  source.displayName = "Response source"
  source.hits = [hit]
  var timeout = Trawl_Federation_V1_SourceFailure()
  timeout.sourceID = "slow"
  timeout.surface = "Slow source"
  timeout.code = .timeout
  timeout.message = "Wait expired."
  timeout.remedy = "trawl doctor slow"
  var permission = Trawl_Federation_V1_SourceFailure()
  permission.sourceID = "private"
  permission.surface = "Private source"
  permission.code = .permission
  permission.message = "Access denied."
  permission.remedy = "trawl doctor private"
  var response = Trawl_Federation_V1_SearchResponse()
  response.outcome = .partial
  response.order = .recency
  response.resultLimit = 20
  response.sources = [source]
  response.hits = [hit]
  response.failures = [timeout, permission]
  let model = SearchModel(client: SearchClient(response: try response.model()), debounce: .zero)
  await model.search("mixed", source: nil)
  #expect(model.hasTimeoutFailure)
  #expect(model.failureGuidance == "Slow source: Wait expired. 1 more source failed.")
  #expect(model.failureGuidance?.contains("trawl doctor") == false)
  #expect(model.sourceDisplayName(for: "response-only", resolvedName: nil) == "Response source")
  #expect(
    model.sourceDisplayName(for: "missing", resolvedName: nil)
      == SearchSourceResolver.unavailableDisplayName)
}

@MainActor
@Test func skippedOnlySearchIsNotNoMatches() async throws {
  var skipped = Trawl_Federation_V1_SkippedSource()
  skipped.sourceID = "synthetic"
  skipped.surface = "Synthetic"
  skipped.reason = "Search is not supported."
  var response = Trawl_Federation_V1_SearchResponse()
  response.outcome = .partial
  response.order = .recency
  response.resultLimit = 20
  response.skippedSources = [skipped]
  let model = SearchModel(client: SearchClient(response: try response.model()), debounce: .zero)
  await model.search("synthetic", source: nil)
  #expect(model.phase == .skipped)
  #expect(model.skippedSources.map(\.sourceID) == ["synthetic"])
  #expect(model.skippedSources.map(\.surface) == ["Synthetic"])
  #expect(model.skippedSources.map(\.reason) == ["Search is not supported."])
}

@MainActor
@Test func staleSearchReplyCannotReplaceTheNewestQuery() async {
  let old = canonicalHit("old")
  let new = canonicalHit("new")
  let client = ScriptedSearchClient { query, _ in
    if query == "old" {
      await uncancellableDelay(.milliseconds(80))
      return canonicalSearch([old])
    }
    return canonicalSearch([new])
  }
  let model = SearchModel(client: client, debounce: .zero)
  let oldTask = Task { await model.search("old", source: "gmail") }
  await uncancellableDelay(.milliseconds(5))
  await model.search("new", source: "gmail")
  await oldTask.value
  #expect(model.results == [new])
}

@MainActor
@Test func queryAndScopeChangesInvalidateOldSelection() async {
  let hit = canonicalHit("selected")
  let model = SearchModel(
    client: ScriptedSearchClient { _, _ in canonicalSearch([hit]) }, debounce: .zero)
  let interaction = SearchInteraction(model: model, sourceID: "gmail")
  await model.search("old", source: "gmail")
  interaction.selectedResultID = hit.id
  interaction.query = "new"
  #expect(interaction.resultForReturn() == nil)
  #expect(model.results.isEmpty)
  interaction.changeScope(to: "notes")
  #expect(interaction.selectedResultID == nil)
}

@MainActor
@Test func selectingForKeyboardNavigationDoesNotOpenTheResult() async {
  let hit = canonicalHit("keyboard")
  let model = SearchModel(
    client: ScriptedSearchClient { _, _ in canonicalSearch([hit]) }, debounce: .zero)
  let interaction = SearchInteraction(model: model, sourceID: nil)

  await model.search("synthetic", source: nil)
  interaction.selectedResultID = hit.id

  #expect(interaction.resultForReturn() == hit)
  #expect(model.openPhase == .idle)
  #expect(model.openResult == nil)
}

@MainActor
@Test func returnHandlerOpensTheKeyboardSelectedResultAndKeepsTheSearchState() async {
  let hit = canonicalHit("return")
  let opened = OpenResponse(
    outcome: .complete,
    requestedRef: hit.openRef,
    requestedAnchorID: hit.anchorID,
    record: OpenRecord(
      sourceID: hit.sourceID,
      openRef: hit.openRef,
      typeURL: "type.example/Synthetic",
      value: Data([1]),
      presentation: canonicalPresentation()
    ),
    failure: nil
  )
  let model = SearchModel(
    client: ScriptedSearchClient(
      search: { _, _ in canonicalSearch([hit]) },
      open: { _, _, _ in opened }
    ),
    debounce: .zero
  )
  let interaction = SearchInteraction(model: model, sourceID: "gmail")

  interaction.query = "synthetic"
  await model.search(interaction.query, source: interaction.sourceID)
  interaction.selectedResultID = hit.id
  await interaction.handleReturn()

  #expect(interaction.query == "synthetic")
  #expect(interaction.sourceID == "gmail")
  #expect(model.results == [hit])
  #expect(model.openPhase == .output)
  #expect(model.openResult == opened)
}

@MainActor
@Test func queryStateSurvivesSearchOutcomeTransitions() async {
  let hit = canonicalHit("typing")
  let model = SearchModel(
    client: ScriptedSearchClient { query, _ in
      query == "partial"
        ? SearchResponse(
          order: .recency,
          sources: [],
          hits: [hit],
          failures: [],
          skippedSources: [],
          outcome: .partial,
          resultLimit: 20,
          truncated: false
        )
        : canonicalSearch([])
    },
    debounce: .zero
  )
  let interaction = SearchInteraction(model: model, sourceID: nil)

  interaction.query = "p"
  await model.search(interaction.query, source: interaction.sourceID)
  interaction.query = "partial"
  await model.search(interaction.query, source: interaction.sourceID)

  #expect(interaction.query == "partial")
  #expect(model.phase == .partial)
  #expect(model.results == [hit])
}

@MainActor
@Test func searchFieldStateRetainsItsIdentityAndRequestsFocusForAQueryTransition() async {
  let field = SearchFieldState()
  let identity = field.identity
  let model = SearchModel(
    client: ScriptedSearchClient { _, _ in canonicalSearch([]) }, debounce: .zero)
  let interaction = SearchInteraction(model: model, sourceID: nil)

  interaction.query = "p"
  field.requestFocus()
  await model.search(interaction.query, source: interaction.sourceID)
  interaction.query = "partial"

  #expect(field.identity == identity)
  #expect(field.focusRequest == 1)
  #expect(interaction.query == "partial")
  #expect(model.phase == .idle)
}

@MainActor
@Test func openingCanonicalRecordKeepsTheSearchWorkspace() async {
  let hit = canonicalHit("open")
  let expected = OpenResponse(
    outcome: .complete,
    requestedRef: hit.openRef,
    requestedAnchorID: hit.anchorID,
    record: OpenRecord(
      sourceID: hit.sourceID,
      openRef: hit.openRef,
      typeURL: "type.example/Synthetic",
      value: Data([1, 2]),
      presentation: canonicalPresentation()
    ),
    failure: nil
  )
  let client = ScriptedSearchClient(
    search: { _, _ in canonicalSearch([hit]) },
    open: { sourceID, ref, anchorID in
      #expect(sourceID == hit.sourceID)
      #expect(ref == hit.openRef)
      #expect(anchorID == hit.anchorID)
      return expected
    })
  let model = SearchModel(client: client, debounce: .zero)
  await model.search("synthetic", source: nil)
  await model.open(hit)
  #expect(model.results == [hit])
  #expect(model.openPhase == .output)
  #expect(model.openResult == expected)
}

@MainActor
@Test func openingFailureKeepsTheSearchWorkspace() async {
  let hit = canonicalHit("missing")
  let failure = SourceFailure(
    sourceID: "gmail", sourceName: "Gmail", code: .notFound,
    message: "Synthetic result is unavailable.", remedy: "Search again.")
  let response = OpenResponse(
    outcome: .failed, requestedRef: hit.openRef, requestedAnchorID: hit.anchorID, record: nil,
    failure: failure)
  let model = SearchModel(
    client: ScriptedSearchClient(
      search: { _, _ in canonicalSearch([hit]) }, open: { _, _, _ in response }), debounce: .zero)
  await model.search("synthetic", source: nil)
  await model.open(hit)
  #expect(model.results == [hit])
  #expect(model.openResult == response)
  #expect(model.openPhase == .failed(failure.message))
}

@MainActor
@Test func searchReductionDistinguishesEveryCanonicalOutcome() async {
  let empty = SearchModel(
    client: ScriptedSearchClient { _, _ in canonicalSearch([]) }, debounce: .zero)
  await empty.search("none", source: nil)
  #expect(empty.phase == .complete)
  #expect(empty.results.isEmpty)

  let permission = SourceFailure(
    sourceID: "notes", sourceName: "Notes", code: .permission, message: "Allow Notes access.",
    remedy: "Open System Settings.")
  let timeout = SourceFailure(
    sourceID: "calendar", sourceName: "Calendar", code: .timeout, message: "Calendar timed out.",
    remedy: "Try again.")
  let failed = SearchModel(
    client: ScriptedSearchClient { _, _ in canonicalFailedSearch([permission]) }, debounce: .zero)
  await failed.search("failed", source: nil)
  #expect(failed.phase == .failed("Notes: Allow Notes access."))

  let allTimeout = SearchModel(
    client: ScriptedSearchClient { _, _ in canonicalFailedSearch([timeout]) }, debounce: .zero)
  await allTimeout.search("timeout", source: nil)
  #expect(allTimeout.phase == .timedOut)

  let mixed = SearchModel(
    client: ScriptedSearchClient { _, _ in canonicalFailedSearch([timeout, permission]) },
    debounce: .zero)
  await mixed.search("mixed", source: nil)
  #expect(
    mixed.phase == .failed("Calendar: Calendar timed out. 1 more source failed."))

  let processTimeout = SearchModel(
    client: ScriptedSearchClient { _, _ in throw TrawlClientError.timedOut }, debounce: .zero)
  await processTimeout.search("process-timeout", source: nil)
  #expect(processTimeout.phase == .timedOut)
}

private struct ScriptedSearchClient: TrawlClient {
  let searchAction: @Sendable (String, String?) async throws -> SearchResponse
  let openAction: @Sendable (String, String, String) async throws -> OpenResponse

  init(
    search: @escaping @Sendable (String, String?) async throws -> SearchResponse,
    open: @escaping @Sendable (String, String, String) async throws -> OpenResponse = {
      _, ref, anchorID in
      OpenResponse(
        outcome: .failed, requestedRef: ref, requestedAnchorID: anchorID, record: nil,
        failure: nil)
    }
  ) {
    searchAction = search
    openAction = open
  }

  func status() async throws -> StatusResponse { fatalError() }
  func requestPhotos() async throws -> StatusResponse { fatalError() }
  func sync() async throws -> SyncResponse { fatalError() }
  func search(_ query: String, source: String?) async throws -> SearchResponse {
    try await searchAction(query, source)
  }
  func open(sourceID: String, ref: String, anchorID: String) async throws -> OpenResponse {
    try await openAction(sourceID, ref, anchorID)
  }
}

private func canonicalHit(_ suffix: String) -> SearchHit {
  SearchHit(
    sourceID: "gmail", openRef: "gmail:message/\(suffix)", shortRef: "short-\(suffix)",
    timeRFC3339: "", time: nil, anchorID: "match",
    summary: ResultSummary(title: "Synthetic message", subtitle: "Avery Example"),
    evidence: [
      .text(label: "Message body", runs: [SearchTextRun(text: "Synthetic", matched: true)])
    ],
    allDay: false, availability: nil, unread: nil)
}

private func canonicalPresentation() -> PresentationDocument {
  PresentationDocument(
    title: "Synthetic", primaryAnchorID: "match",
    blocks: [.prose(anchorID: "match", text: "Synthetic matching passage")], actions: [],
    facts: [])
}

private func canonicalSearch(_ hits: [SearchHit]) -> SearchResponse {
  SearchResponse(
    order: .recency, sources: [], hits: hits, failures: [], skippedSources: [], outcome: .complete,
    resultLimit: 20, truncated: false)
}

private func canonicalFailedSearch(_ failures: [SourceFailure]) -> SearchResponse {
  SearchResponse(
    order: .recency, sources: [], hits: [], failures: failures, skippedSources: [],
    outcome: .failed, resultLimit: 20, truncated: false)
}

private func uncancellableDelay(_ duration: Duration) async {
  await withCheckedContinuation { continuation in
    DispatchQueue.global().asyncAfter(deadline: .now() + duration.timeInterval) {
      continuation.resume()
    }
  }
}

extension Duration {
  fileprivate var timeInterval: TimeInterval {
    let parts = components
    return TimeInterval(parts.seconds) + TimeInterval(parts.attoseconds) / 1_000_000_000_000_000_000
  }
}
