import Foundation
import PermissionGuide
import Testing
import TrawlClient

@testable import TrawlCore

@MainActor
@Test func partialSearchRetainsUsefulResults() async {
  let hit = SearchHit(
    id: "gmail:message:example-1",
    sourceID: "gmail",
    title: "Example sender",
    snippet: "Synthetic result",
    whenDisplay: "10 Jul"
  )
  let client = ScriptedClient(search: { _, _ in
    SearchResponse(
      hits: [hit],
      failures: [
        SourceFailure(
          sourceID: "calendar",
          sourceName: "Calendar",
          code: .permission,
          message: "Allow calendar access.",
          remedy: "Open System Settings."
        )
      ],
      outcome: .partial,
      resultLimit: 20,
      truncated: true
    )
  })
  let model = SearchModel(client: client, debounce: .zero, waitLimit: .seconds(1))

  await model.search("synthetic", source: nil)

  #expect(model.results == [hit])
  #expect(model.phase == .partial)
  #expect(model.failures.map(\.sourceID) == ["calendar"])
  #expect(model.resultLimit == 20)
  #expect(model.isTruncated)
}

@MainActor
@Test func searchPassesTheCompleteTypedQueryScopeAndResponse() async {
  let response = SearchResponse(
    hits: [
      SearchHit(
        id: "gmail:message:example-2",
        sourceID: "gmail",
        title: "Synthetic sender",
        snippet: "Synthetic snippet",
        whenDisplay: "11 Jul"
      )
    ],
    failures: [],
    outcome: .complete,
    resultLimit: 20,
    truncated: false
  )
  let receipt = SearchReceipt()
  let client = ScriptedClient(search: { query, source in
    await receipt.recordSearch(query: query, source: source)
    return response
  })
  let model = SearchModel(client: client, debounce: .zero, waitLimit: .seconds(1))

  await model.search("project lantern", source: "gmail")

  let searches = await receipt.searches
  #expect(searches == [SearchReceipt.Search(query: "project lantern", source: "gmail")])
  #expect(model.results == response.hits)
  #expect(model.failures == response.failures)
  #expect(model.resultLimit == response.resultLimit)
  #expect(model.isTruncated == response.truncated)
  #expect(model.phase == .complete)
}

@MainActor
@Test func newerQueryCannotBeOverwrittenByAStaleReply() async {
  let client = ScriptedClient(search: { query, _ in
    await ignoreCancellation(for: query == "old" ? .milliseconds(150) : .milliseconds(15))
    return SearchResponse(
      hits: [
        SearchHit(
          id: query,
          sourceID: "gmail",
          title: query,
          snippet: "Synthetic",
          whenDisplay: ""
        )
      ],
      outcome: .complete,
      resultLimit: 20,
      truncated: false
    )
  })
  let model = SearchModel(client: client, debounce: .zero, waitLimit: .seconds(1))

  let old = Task { await model.search("old", source: nil) }
  await ignoreCancellation(for: .milliseconds(10))
  let new = Task { await model.search("new", source: nil) }
  await old.value
  await new.value

  #expect(model.results.map(\.id) == ["new"])
  #expect(model.phase == .complete)
}

@MainActor
@Test func searchDistinguishesEmptyFailureAndTimeout() async {
  let empty = SearchModel(
    client: ScriptedClient(search: { _, _ in
      SearchResponse(hits: [], outcome: .complete, resultLimit: 20, truncated: false)
    }),
    debounce: .zero,
    waitLimit: .seconds(1)
  )
  await empty.search("none", source: nil)
  #expect(empty.phase == .complete)
  #expect(empty.results.isEmpty)

  let failed = SearchModel(
    client: ScriptedClient(search: { _, _ in
      SearchResponse(hits: [], outcome: .failed, resultLimit: 20, truncated: false)
    }),
    debounce: .zero,
    waitLimit: .seconds(1)
  )
  await failed.search("none", source: nil)
  #expect(failed.phase == .failed("No source returned search results."))

  let timedOut = SearchModel(
    client: ScriptedClient(search: { _, _ in
      await ignoreCancellation(for: .milliseconds(100))
      return SearchResponse(hits: [], outcome: .complete, resultLimit: 20, truncated: false)
    }),
    debounce: .zero,
    waitLimit: .milliseconds(10)
  )
  await timedOut.search("slow", source: nil)
  #expect(timedOut.phase == .timedOut)
}

@MainActor
@Test func openingARowRetainsTheExactHelperOutput() async {
  let hit = SearchHit(
    id: "gmail:message:example-1",
    sourceID: "gmail",
    title: "Example sender",
    snippet: "Synthetic result",
    whenDisplay: ""
  )
  let client = ScriptedClient(
    search: { _, _ in
      SearchResponse(hits: [hit], outcome: .complete, resultLimit: 20, truncated: false)
    },
    open: { _ in
      OpenResponse(
        outcome: .complete,
        sourceID: "gmail",
        openRef: "gmail:message:example-1",
        output: Data("first line\nsecond line\n".utf8),
        failure: nil
      )
    }
  )
  let model = SearchModel(client: client, debounce: .zero, waitLimit: .seconds(1))
  await model.search("synthetic", source: nil)

  await model.open(hit)

  #expect(model.openPhase == .output)
  #expect(model.openResult?.output == Data("first line\nsecond line\n".utf8))
  #expect(model.results == [hit])
}

@MainActor
@Test func openingFailureDoesNotDiscardTheSearchWorkspace() async {
  let hit = SearchHit(
    id: "notes:example-1",
    sourceID: "notes",
    title: "Synthetic note",
    snippet: "Synthetic snippet",
    whenDisplay: "11 Jul"
  )
  let failure = SourceFailure(
    sourceID: "notes",
    sourceName: "Notes",
    code: .notFound,
    message: "The synthetic note is no longer available.",
    remedy: "Search again."
  )
  let receipt = SearchReceipt()
  let client = ScriptedClient(
    search: { _, _ in
      SearchResponse(hits: [hit], outcome: .complete, resultLimit: 20, truncated: false)
    },
    open: { ref in
      await receipt.recordOpen(ref: ref)
      return OpenResponse(
        outcome: .failed,
        sourceID: hit.sourceID,
        openRef: ref,
        output: Data(),
        failure: failure
      )
    }
  )
  let model = SearchModel(client: client, debounce: .zero, waitLimit: .seconds(1))

  await model.search("synthetic", source: "notes")
  await model.open(hit)

  let opens = await receipt.opens
  #expect(opens == [hit.id])
  #expect(model.results == [hit])
  #expect(model.openResult?.failure == failure)
  #expect(model.openPhase == .failed(failure.message))
}

@MainActor
@Test func returnImmediatelyAfterAQueryChangeCannotOpenTheOldResult() async {
  let oldHit = SearchHit(
    id: "messages:old",
    sourceID: "imessage",
    title: "Old synthetic result",
    snippet: "Synthetic",
    whenDisplay: "10 Jul"
  )
  let receipt = SearchReceipt()
  let client = ScriptedClient(
    search: { query, source in
      await receipt.recordSearch(query: query, source: source)
      return SearchResponse(hits: [oldHit], outcome: .complete, resultLimit: 20, truncated: false)
    },
    open: { ref in
      await receipt.recordOpen(ref: ref)
      return OpenResponse(outcome: .complete, sourceID: "imessage", openRef: ref, output: Data(), failure: nil)
    }
  )
  let model = SearchModel(client: client, debounce: .zero, waitLimit: .seconds(1))
  let interaction = SearchInteraction(model: model, sourceID: "imessage")

  await model.search("old", source: "imessage")
  interaction.selectedResultID = oldHit.id
  interaction.query = "new"
  if let submitted = interaction.resultForReturn() {
    await model.open(submitted)
  }

  let searches = await receipt.searches
  let opens = await receipt.opens
  #expect(searches == [SearchReceipt.Search(query: "old", source: "imessage")])
  #expect(opens.isEmpty)
  #expect(model.results.isEmpty)
  #expect(model.openPhase == .idle)
  #expect(model.openResult == nil)
}

@MainActor
@Test func uiStateProofReadsEverySearchAndOpenBoundaryBeforePresentation() async {
  let completeHit = SearchHit(
    id: "gmail:message:complete",
    sourceID: "gmail",
    title: "Complete synthetic result",
    snippet: "Synthetic",
    whenDisplay: "11 Jul"
  )
  let partialFailure = SourceFailure(
    sourceID: "calendar",
    sourceName: "Calendar",
    code: .permission,
    message: "Calendar access is unavailable.",
    remedy: "Allow Calendar access in System Settings."
  )
  let totalFailure = SourceFailure(
    sourceID: "notes",
    sourceName: "Notes",
    code: .unavailable,
    message: "The synthetic Notes archive is unavailable.",
    remedy: "Open Notes once, then search again."
  )
  let openFailure = SourceFailure(
    sourceID: "gmail",
    sourceName: "Gmail",
    code: .notFound,
    message: "The synthetic message is no longer available.",
    remedy: "Search again."
  )
  let noMatches = SearchResponse(
    hits: [], outcome: .complete, resultLimit: 20, truncated: false
  )
  let complete = SearchResponse(
    hits: [completeHit], outcome: .complete, resultLimit: 20, truncated: false
  )
  let partial = SearchResponse(
    hits: [completeHit],
    failures: [partialFailure],
    outcome: .partial,
    resultLimit: 20,
    truncated: true
  )
  let failed = SearchResponse(
    hits: [],
    failures: [totalFailure],
    outcome: .failed,
    resultLimit: 20,
    truncated: false
  )
  let openFailed = OpenResponse(
    outcome: .failed,
    sourceID: "gmail",
    openRef: completeHit.id,
    output: Data(),
    failure: openFailure
  )
  let events = SearchEventReceipt()
  let client = ScriptedClient(
    search: { query, _ in
      switch query {
      case "none": return noMatches
      case "complete", "new": return complete
      case "partial": return partial
      case "failed": return failed
      case "slow":
        await ignoreCancellation(for: .milliseconds(100))
        return noMatches
      case "old":
        await ignoreCancellation(for: .milliseconds(80))
        return SearchResponse(
          hits: [
            SearchHit(
              id: "gmail:message:old",
              sourceID: "gmail",
              title: "Old synthetic result",
              snippet: "Synthetic",
              whenDisplay: "11 Jul"
            )
          ],
          outcome: .complete,
          resultLimit: 20,
          truncated: false
        )
      default:
        return noMatches
      }
    },
    open: { _ in openFailed }
  )
  func makeModel(wait: Duration = .seconds(1)) -> SearchModel {
    SearchModel(
      client: client,
      debounce: .zero,
      waitLimit: wait,
      observe: events.record
    )
  }

  let noneModel = makeModel()
  await noneModel.search("none", source: nil)
  let completeModel = makeModel()
  await completeModel.search("complete", source: "gmail")
  await completeModel.open(completeHit)
  let partialModel = makeModel()
  await partialModel.search("partial", source: nil)
  let failedModel = makeModel()
  await failedModel.search("failed", source: "notes")
  let timeoutModel = makeModel(wait: .milliseconds(10))
  await timeoutModel.search("slow", source: nil)
  let staleModel = makeModel()
  let oldTask = Task { await staleModel.search("old", source: "gmail") }
  await ignoreCancellation(for: .milliseconds(5))
  let newTask = Task { await staleModel.search("new", source: "gmail") }
  await oldTask.value
  await newTask.value

  let captured = events.snapshot()
  let noMatchesInput = SearchStateInput(query: "none", sourceID: nil, limit: 20)
  let completeInput = SearchStateInput(query: "complete", sourceID: "gmail", limit: 20)
  let partialInput = SearchStateInput(query: "partial", sourceID: nil, limit: 20)
  let failedInput = SearchStateInput(query: "failed", sourceID: "notes", limit: 20)
  let timeoutInput = SearchStateInput(query: "slow", sourceID: nil, limit: 20)
  let oldInput = SearchStateInput(query: "old", sourceID: "gmail", limit: 20)
  let newInput = SearchStateInput(query: "new", sourceID: "gmail", limit: 20)

  #expect(captured.contains(.loading(noMatchesInput)))
  #expect(captured.contains(.response(noMatchesInput, noMatches)))
  #expect(captured.contains(.loading(completeInput)))
  #expect(captured.contains(.response(completeInput, complete)))
  #expect(captured.contains(.loading(partialInput)))
  #expect(captured.contains(.response(partialInput, partial)))
  #expect(captured.contains(.loading(failedInput)))
  #expect(captured.contains(.response(failedInput, failed)))
  #expect(captured.contains(.loading(timeoutInput)))
  #expect(captured.contains(.timedOut(timeoutInput)))
  #expect(captured.contains(.loading(oldInput)))
  #expect(captured.contains(where: { event in
    guard case .response(let input, let response) = event else { return false }
    return input == oldInput && response.hits.map(\.id) == ["gmail:message:old"]
  }))
  #expect(captured.contains(.loading(newInput)))
  #expect(captured.contains(.response(newInput, complete)))
  #expect(captured.contains(.opening(completeHit.id)))
  #expect(captured.contains(.openResponse(completeHit.id, openFailed)))
  #expect(staleModel.results == complete.hits)
  #expect(failedModel.phase == .failed(
    "Notes: The synthetic Notes archive is unavailable. Open Notes once, then search again."
  ))
  #expect(partialModel.failureGuidance ==
    "Calendar: Calendar access is unavailable. Allow Calendar access in System Settings."
  )
}

private final class ScriptedClient: TrawlClient, @unchecked Sendable {
  let searchAction: @Sendable (String, String?) async throws -> SearchResponse
  let openAction: @Sendable (String) async throws -> OpenResponse

  init(
    search: @escaping @Sendable (String, String?) async throws -> SearchResponse,
    open: @escaping @Sendable (String) async throws -> OpenResponse = { ref in
      OpenResponse(outcome: .complete, sourceID: "", openRef: ref, output: Data(), failure: nil)
    }
  ) {
    searchAction = search
    openAction = open
  }

  func status() async throws -> StatusResponse {
    StatusResponse(sources: [], outcome: .complete)
  }

  func sync() async throws -> SyncResponse {
    SyncResponse(sources: [], failures: [], outcome: .complete)
  }

  func search(_ query: String, source: String?) async throws -> SearchResponse {
    try await searchAction(query, source)
  }

  func open(_ ref: String) async throws -> OpenResponse {
    try await openAction(ref)
  }
}

private actor SearchReceipt {
  struct Search: Sendable, Equatable {
    let query: String
    let source: String?
  }

  private(set) var searches: [Search] = []
  private(set) var opens: [String] = []

  func recordSearch(query: String, source: String?) {
    searches.append(Search(query: query, source: source))
  }

  func recordOpen(ref: String) {
    opens.append(ref)
  }
}

private final class SearchEventReceipt: @unchecked Sendable {
  private let lock = NSLock()
  private var events: [SearchStateEvent] = []

  func record(_ event: SearchStateEvent) {
    lock.withLock {
      events.append(event)
    }
  }

  func snapshot() -> [SearchStateEvent] {
    lock.withLock { events }
  }
}

private func ignoreCancellation(for duration: Duration) async {
  await withCheckedContinuation { continuation in
    DispatchQueue.global().asyncAfter(deadline: .now() + duration.timeInterval) {
      continuation.resume()
    }
  }
}

extension Duration {
  fileprivate var timeInterval: TimeInterval {
    let components = self.components
    return TimeInterval(components.seconds)
      + TimeInterval(components.attoseconds) / 1_000_000_000_000_000_000
  }
}
