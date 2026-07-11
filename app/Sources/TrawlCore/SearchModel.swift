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
  case opening(String)
  case openResponse(String, OpenResponse)
  case openFailed(String, String)
}

public enum SearchPhase: Sendable, Equatable {
  case idle
  case loading
  case complete
  case partial
  case failed(String)
  case timedOut
}

public enum SearchOpenPhase: Sendable, Equatable {
  case idle
  case loading
  case output
  case failed(String)
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
  public private(set) var resultLimit: UInt32 = 0
  public private(set) var isTruncated = false
  public private(set) var openPhase: SearchOpenPhase = .idle
  public private(set) var openResult: OpenResponse?

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
    phase = .idle
  }

  /// Clears state owned by a query or scope before SwiftUI schedules the next search task.
  public func invalidateForInputChange() {
    generation &+= 1
    openGeneration &+= 1
    results = []
    failures = []
    resultLimit = 0
    isTruncated = false
    phase = .idle
    openPhase = .idle
    openResult = nil
  }

  public func search(_ rawQuery: String, source: String?) async {
    generation &+= 1
    openGeneration &+= 1
    let token = generation
    let query = rawQuery.trimmingCharacters(in: .whitespacesAndNewlines)
    guard !query.isEmpty else {
      results = []
      failures = []
      resultLimit = 0
      isTruncated = false
      phase = .idle
      openPhase = .idle
      openResult = nil
      return
    }

    results = []
    failures = []
    resultLimit = 0
    isTruncated = false
    phase = .loading
    openPhase = .idle
    openResult = nil
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
      resultLimit = response.resultLimit
      isTruncated = response.truncated
      switch response.outcome {
      case .complete:
        phase = .complete
      case .partial:
        phase = .partial
      case .failed:
        phase = .failed(failureGuidance ?? "No source returned search results.")
      }
    } catch is CancellationError {
      return
    } catch is SearchWaitExpired {
      guard token == generation else { return }
      observe(.timedOut(input))
      results = []
      phase = .timedOut
    } catch TrawlClientError.timedOut {
      guard token == generation else { return }
      observe(.timedOut(input))
      results = []
      failures = []
      phase = .timedOut
    } catch TrawlClientError.cancelled {
      return
    } catch {
      guard token == generation else { return }
      observe(.searchFailed(input, error.localizedDescription))
      results = []
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
      let response = try await client.open(hit.id)
      observe(.openResponse(hit.id, response))
      try Task.checkCancellation()
      guard token == openGeneration else { return }
      openResult = response
      switch response.outcome {
      case .complete, .partial:
        openPhase = .output
      case .failed:
        openPhase = .failed(response.failure?.message ?? "OpenTrawl could not open this result.")
      }
    } catch is CancellationError {
      return
    } catch TrawlClientError.cancelled {
      return
    } catch {
      guard token == openGeneration else { return }
      observe(.openFailed(hit.id, error.localizedDescription))
      openPhase = .failed(error.localizedDescription)
    }
  }

  public var failureGuidance: String? {
    guard let first = failures.first else { return nil }
    let source = first.sourceName.isEmpty ? "A source" : first.sourceName
    let remedy = first.remedy.isEmpty ? "" : " \(first.remedy)"
    let more = failures.count > 1 ? " \(failures.count - 1) more source failures." : ""
    return "\(source): \(first.message)\(remedy)\(more)"
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

  private func invalidateInput() {
    selectedResultID = nil
    model.invalidateForInputChange()
  }
}
