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

  #expect(model.openPhase == .output("first line\nsecond line\n"))
  #expect(model.openResult?.output == Data("first line\nsecond line\n".utf8))
  #expect(model.results == [hit])
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
