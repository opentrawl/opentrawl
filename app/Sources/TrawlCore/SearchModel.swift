import Foundation
import Observation
import TrawlClient

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
  case output(String)
  case failed(String)
}

@MainActor
@Observable
public final class SearchModel {
  public static let defaultWaitSeconds = 10

  private let client: any TrawlClient
  private let debounce: Duration
  private let waitLimit: Duration
  private var generation: UInt64 = 0

  public private(set) var phase: SearchPhase = .idle
  public private(set) var results: [SearchHit] = []
  public private(set) var openPhase: SearchOpenPhase = .idle

  public init(
    client: any TrawlClient,
    debounce: Duration = .milliseconds(300),
    waitLimit: Duration = .seconds(SearchModel.defaultWaitSeconds)
  ) {
    self.client = client
    self.debounce = debounce
    self.waitLimit = waitLimit
  }

  public func reset() {
    generation &+= 1
    results = []
    phase = .idle
    openPhase = .idle
  }

  public func search(_ rawQuery: String, source: String?) async {
    generation &+= 1
    let token = generation
    let query = rawQuery.trimmingCharacters(in: .whitespacesAndNewlines)
    guard !query.isEmpty else {
      results = []
      phase = .idle
      openPhase = .idle
      return
    }

    results = []
    phase = .loading
    openPhase = .idle

    do {
      try await Task.sleep(for: debounce)
      guard token == generation else { return }
      let response = try await searchWithinLimit(query, source: source)
      try Task.checkCancellation()
      guard token == generation else { return }

      results = response.hits
      switch response.completion {
      case .complete:
        phase = .complete
      case .partial:
        phase = .partial
      case .failed:
        phase = .failed("No source returned search results.")
      }
    } catch is CancellationError {
      return
    } catch is SearchWaitExpired {
      guard token == generation else { return }
      results = []
      phase = .timedOut
    } catch {
      guard token == generation else { return }
      results = []
      phase = .failed(error.localizedDescription)
    }
  }

  public func open(_ hit: SearchHit) async {
    guard results.contains(where: { $0.id == hit.id }) else { return }
    let token = generation
    openPhase = .loading
    do {
      let output = try await client.open(hit.id)
      try Task.checkCancellation()
      guard token == generation else { return }
      openPhase = .output(output)
    } catch is CancellationError {
      return
    } catch {
      guard token == generation else { return }
      openPhase = .failed(error.localizedDescription)
    }
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
