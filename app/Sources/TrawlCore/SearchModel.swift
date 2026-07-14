import Foundation
import Observation
import TrawlClient

public struct SearchStateInput: Sendable, Equatable {
  public let query: String
  public let sourceID: String?
  public let limit: UInt32

  public init(query: String, sourceID: String?, limit: UInt32) {
    self.query = query
    self.sourceID = sourceID
    self.limit = limit
  }
}

public enum SearchStateEvent: Sendable, Equatable {
  case loading(SearchStateInput)
  case response(SearchStateInput, SearchResponse)
  case timedOut(SearchStateInput)
  case searchFailed(SearchStateInput, String)
  case opening(SearchHitID)
  case openResponse(SearchHitID, OpenResponse)
  case openFailed(SearchHitID, String)
}

@MainActor
@Observable
public final class SearchSourceResolver {
  public static let unavailableDisplayName = "Source name unavailable"

  public private(set) var statuses: [SourceStatus]

  public init(statuses: [SourceStatus], scopedStatus: SourceStatus? = nil) {
    self.statuses = Self.includingScopedStatus(scopedStatus, in: statuses)
  }

  public func replace(with statuses: [SourceStatus], scopedStatus: SourceStatus? = nil) {
    self.statuses = Self.includingScopedStatus(scopedStatus, in: statuses)
  }

  public func displayName(for sourceID: String) -> String? {
    statuses.first(where: { $0.id == sourceID })?.manifest.displayName
  }

  public func displayNameOrUnavailable(for sourceID: String) -> String {
    displayName(for: sourceID) ?? Self.unavailableDisplayName
  }

  private static func includingScopedStatus(
    _ scopedStatus: SourceStatus?,
    in statuses: [SourceStatus]
  ) -> [SourceStatus] {
    guard let scopedStatus, !statuses.contains(where: { $0.id == scopedStatus.id }) else {
      return statuses
    }
    return statuses + [scopedStatus]
  }
}

public enum SearchPhase: Sendable, Equatable {
  case idle
  case loading
  case complete
  case partial
  case skipped
  case failed(String)
  case timedOut
}

public enum SearchOpenPhase: Sendable, Equatable {
  case idle
  case loading
  case output
  case failed(String)
  case timedOut(String)
}

@MainActor
@Observable
public final class SearchModel {
  public static let defaultWaitSeconds = 10

  private let client: any TrawlClient
  private let debounce: Duration
  private let waitLimit: Duration
  private let observe: @Sendable (SearchStateEvent) -> Void
  private var generation: UInt64 = 0
  private var openGeneration: UInt64 = 0

  public private(set) var phase: SearchPhase = .idle
  public private(set) var results: [SearchHit] = []
  public private(set) var failures: [SourceFailure] = []
  public private(set) var skippedSources: [SkippedSource] = []
  public private(set) var sourceResults: [SearchSourceResult] = []
  public private(set) var order: SearchOrder = .recency
  public private(set) var sourceSurfaces: [String: String] = [:]
  public private(set) var resultLimit: UInt32 = 0
  public private(set) var isTruncated = false
  public private(set) var openPhase: SearchOpenPhase = .idle
  public private(set) var openResult: OpenResponse?
  public private(set) var committedInput: SearchStateInput?
  public private(set) var timedOutLocally = false

  public init(
    client: any TrawlClient,
    debounce: Duration = .milliseconds(300),
    waitLimit: Duration = .seconds(SearchModel.defaultWaitSeconds),
    observe: @escaping @Sendable (SearchStateEvent) -> Void = { _ in }
  ) {
    self.client = client
    self.debounce = debounce
    self.waitLimit = waitLimit
    self.observe = observe
  }

  public func reset() {
    invalidateForInputChange()
    clearCommittedSearch()
    phase = .idle
  }

  /// Cancels work for an edited query without disturbing the page the person is reading.
  public func invalidateForInputChange() {
    generation &+= 1
  }

  public func search(_ rawQuery: String, source: String?) async {
    generation &+= 1
    openGeneration &+= 1
    let token = generation
    let query = rawQuery.trimmingCharacters(in: .whitespacesAndNewlines)
    guard !query.isEmpty else {
      clearCommittedSearch()
      return
    }

    timedOutLocally = false
    phase = .loading
    let input = SearchStateInput(
      query: query,
      sourceID: source,
      limit: SearchResponse.maximumResults
    )
    observe(.loading(input))

    do {
      try await Task.sleep(for: debounce)
      guard token == generation else { return }
      let response = try await searchWithinLimit(query, source: source)
      observe(.response(input, response))
      try Task.checkCancellation()
      guard token == generation else { return }

      results = response.hits
      failures = response.failures
      skippedSources = response.skippedSources
      sourceResults = response.sources
      sourceSurfaces = Dictionary(
        uniqueKeysWithValues: response.sources.map { ($0.sourceID, $0.displayName) })
      order = response.order
      resultLimit = response.resultLimit
      isTruncated = response.truncated
      committedInput = input
      switch response.outcome {
      case .complete:
        phase = .complete
      case .partial:
        phase =
          response.hits.isEmpty && response.failures.isEmpty && !response.skippedSources.isEmpty
          ? .skipped : .partial
      case .failed:
        timedOutLocally = false
        phase =
          response.hits.isEmpty && !response.failures.isEmpty
            && response.failures.allSatisfy({ $0.code == .timeout })
          ? .timedOut : .failed(failureGuidance ?? "No source returned search results.")
      }
    } catch is CancellationError {
      return
    } catch is SearchWaitExpired {
      guard token == generation else { return }
      observe(.timedOut(input))
      timedOutLocally = true
      phase = .timedOut
    } catch TrawlClientError.timedOut {
      guard token == generation else { return }
      observe(.timedOut(input))
      timedOutLocally = true
      phase = .timedOut
    } catch TrawlClientError.cancelled {
      return
    } catch {
      guard token == generation else { return }
      observe(.searchFailed(input, error.localizedDescription))
      phase = .failed(error.localizedDescription)
    }
  }

  public func open(_ hit: SearchHit) async {
    guard results.contains(hit) else { return }
    openGeneration &+= 1
    let token = openGeneration
    openPhase = .loading
    openResult = nil
    observe(.opening(hit.id))
    do {
      let response = try await client.open(
        sourceID: hit.sourceID, ref: hit.openRef, anchorID: hit.anchorID)
      observe(.openResponse(hit.id, response))
      try Task.checkCancellation()
      guard token == openGeneration else { return }
      openResult = response
      switch response.outcome {
      case .complete:
        openPhase = .output
      case .partial:
        openPhase = .failed(TrawlClientError.invalidProtobuf.localizedDescription)
      case .failed:
        if response.failure?.code == .timeout {
          openPhase = .timedOut(response.failure?.message ?? "Opening this result timed out.")
        } else {
          openPhase = .failed(response.failure?.message ?? "OpenTrawl could not open this result.")
        }
      }
    } catch is CancellationError {
      return
    } catch TrawlClientError.cancelled {
      return
    } catch {
      guard token == openGeneration else { return }
      observe(.openFailed(hit.id, error.localizedDescription))
      if let clientError = error as? TrawlClientError, clientError == .timedOut {
        openPhase = .timedOut(error.localizedDescription)
      } else {
        openPhase = .failed(error.localizedDescription)
      }
    }
  }

  public var failureGuidance: String? {
    guard !failures.isEmpty else { return nil }
    return failures.map { failure in
      let source =
        failure.sourceName.isEmpty
          ? (sourceSurfaces[failure.sourceID] ?? "A source")
          : failure.sourceName
      return "\(source): \(failure.message)"
    }
    .joined(separator: " ")
  }

  public var hasTimeoutFailure: Bool {
    failures.contains(where: { $0.code == .timeout })
  }

  public func sourceDisplayName(for sourceID: String, resolvedName: String?) -> String {
    resolvedName ?? sourceSurfaces[sourceID] ?? SearchSourceResolver.unavailableDisplayName
  }

  public func displayTitle(for hit: SearchHit) -> String {
    hit.summary.title
  }

  public func clearOpenResult() {
    openGeneration &+= 1
    openPhase = .idle
    openResult = nil
  }

  private func clearCommittedSearch() {
    results = []
    failures = []
    skippedSources = []
    sourceResults = []
    sourceSurfaces = [:]
    resultLimit = 0
    isTruncated = false
    committedInput = nil
    timedOutLocally = false
    phase = .idle
    openGeneration &+= 1
    openPhase = .idle
    openResult = nil
  }

  private func searchWithinLimit(_ query: String, source: String?) async throws -> SearchResponse {
    let client = client
    let waitLimit = waitLimit
    return try await withThrowingTaskGroup(of: SearchResponse.self) { group in
      group.addTask {
        try await client.search(query, source: source)
      }
      group.addTask {
        try await Task.sleep(for: waitLimit)
        throw SearchWaitExpired()
      }
      defer { group.cancelAll() }
      guard let response = try await group.next() else {
        throw SearchWaitExpired()
      }
      return response
    }
  }
}

private struct SearchWaitExpired: Error {}

@MainActor
@Observable
public final class SearchInteraction {
  private let model: SearchModel

  public var query: String = "" {
    didSet {
      guard query != oldValue else { return }
      invalidateInput()
    }
  }
  public private(set) var sourceID: String?
  public var selectedResultID: SearchHit.ID?

  public init(model: SearchModel, sourceID: String?) {
    self.model = model
    self.sourceID = sourceID
  }

  public func changeScope(to sourceID: String?) {
    guard sourceID != self.sourceID else { return }
    self.sourceID = sourceID
    invalidateInput()
  }

  public func resultForReturn() -> SearchHit? {
    guard let selectedResultID else { return nil }
    return model.results.first(where: { $0.id == selectedResultID })
  }

  public func handleReturn() async {
    guard let hit = resultForReturn() else { return }
    await model.open(hit)
  }

  public func reconcileCommittedResults() {
    guard let selectedResultID, !model.results.contains(where: { $0.id == selectedResultID }) else { return }
    self.selectedResultID = nil
    model.clearOpenResult()
  }

  private func invalidateInput() {
    model.invalidateForInputChange()
  }
}
