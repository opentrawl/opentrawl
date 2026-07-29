import Foundation
import Observation
import TrawlClient

public struct SearchStateInput: Sendable, Equatable {
  public let query: String
  public let registeredTrawlerManifestIdentity: String?
  public let limit: UInt32

  public init(
    query: String,
    registeredTrawlerManifestIdentity: String?,
    limit: UInt32
  ) {
    self.query = query
    self.registeredTrawlerManifestIdentity = registeredTrawlerManifestIdentity
    self.limit = limit
  }
}

public enum SearchStateEvent: Sendable, Equatable {
  case loading(SearchStateInput)
  case response(SearchStateInput, SearchResponse)
  case timedOut(SearchStateInput)
  case searchFailed(SearchStateInput, String)
  case opening(SearchMatchIdentifier)
  case openResponse(SearchMatchIdentifier, OpenResponse)
  case openFailed(SearchMatchIdentifier, String)
}

@MainActor
@Observable
public final class SearchTrawlerResolver {
  public static let unavailableDisplayName = "Trawler name unavailable"

  public private(set) var statuses: [TrawlerStatus]

  public init(statuses: [TrawlerStatus], scopedStatus: TrawlerStatus? = nil) {
    self.statuses = Self.includingScopedStatus(scopedStatus, in: statuses)
  }

  public func replace(with statuses: [TrawlerStatus], scopedStatus: TrawlerStatus? = nil) {
    self.statuses = Self.includingScopedStatus(scopedStatus, in: statuses)
  }

  public func displayName(for registeredTrawlerManifestIdentity: String) -> String? {
    statuses.first(where: { $0.id == registeredTrawlerManifestIdentity })?
      .registeredTrawlerManifest.registeredTrawlerDisplayName
  }

  public func displayNameOrUnavailable(
    for registeredTrawlerManifestIdentity: String
  ) -> String {
    displayName(for: registeredTrawlerManifestIdentity) ?? Self.unavailableDisplayName
  }

  private static func includingScopedStatus(
    _ scopedStatus: TrawlerStatus?,
    in statuses: [TrawlerStatus]
  ) -> [TrawlerStatus] {
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
  public private(set) var searchMatches: [SearchMatch] = []
  public private(set) var operationFailures: [TrawlerOperationFailure] = []
  public private(set) var trawlersSkippedFromOperation: [TrawlerSkippedFromOperation] = []
  public private(set) var trawlerSearchResults: [TrawlerSearchResult] = []
  public private(set) var trawlerDisplayNamesByManifestIdentity: [String: String] = [:]
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

  public func search(
    _ rawQuery: String,
    registeredTrawlerManifestIdentity: String?
  ) async {
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
      registeredTrawlerManifestIdentity: registeredTrawlerManifestIdentity,
      limit: SearchResponse.maximumResults
    )
    observe(.loading(input))

    do {
      try await Task.sleep(for: debounce)
      guard token == generation else { return }
      let response = try await searchWithinLimit(
        query,
        registeredTrawlerManifestIdentity: registeredTrawlerManifestIdentity)
      observe(.response(input, response))
      try Task.checkCancellation()
      guard token == generation else { return }

      searchMatches = response.searchMatchesInDisplayOrder
      operationFailures = response.operationFailures
      trawlersSkippedFromOperation = response.trawlersSkippedFromOperation
      trawlerSearchResults = response.trawlerSearchResults
      trawlerDisplayNamesByManifestIdentity = Dictionary(
        uniqueKeysWithValues: response.trawlerSearchResults.map {
          ($0.registeredTrawlerManifestIdentity, $0.registeredTrawlerDisplayName)
        })
      resultLimit = response.resultLimit
      isTruncated = response.moreSearchMatchesExist
      committedInput = input
      switch response.outcome {
      case .complete:
        phase = .complete
      case .partial:
        phase =
          response.searchMatchesInDisplayOrder.isEmpty
            && response.operationFailures.isEmpty
            && !response.trawlersSkippedFromOperation.isEmpty
          ? .skipped : .partial
      case .failed:
        timedOutLocally = false
        phase =
          response.searchMatchesInDisplayOrder.isEmpty
            && !response.operationFailures.isEmpty
            && response.operationFailures.allSatisfy({ $0.failureCode == .timeout })
          ? .timedOut : .failed(failureGuidance ?? "No trawler returned search results.")
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

  public func open(_ searchMatch: SearchMatch) async {
    guard searchMatches.contains(searchMatch) else { return }
    openGeneration &+= 1
    let token = openGeneration
    openPhase = .loading
    openResult = nil
    observe(.opening(searchMatch.id))
    do {
      let response = try await client.open(
        link: searchMatch.globallyRoutableTrawlLink,
        anchorIdentifier: searchMatch.matchingRecordAnchorIdentifier)
      observe(.openResponse(searchMatch.id, response))
      try Task.checkCancellation()
      guard token == openGeneration else { return }
      openResult = response
      switch response.outcome {
      case .complete:
        openPhase = .output
      case .partial:
        openPhase = .failed(TrawlClientError.invalidProtobuf.localizedDescription)
      case .failed:
        if response.failure?.failureCode == .timeout {
          openPhase = .timedOut(
            response.failure?.failureMessage ?? "Opening this result timed out.")
        } else {
          openPhase = .failed(
            response.failure?.failureMessage ?? "OpenTrawl could not open this result.")
        }
      }
    } catch is CancellationError {
      return
    } catch TrawlClientError.cancelled {
      return
    } catch {
      guard token == openGeneration else { return }
      observe(.openFailed(searchMatch.id, error.localizedDescription))
      if let clientError = error as? TrawlClientError, clientError == .timedOut {
        openPhase = .timedOut(error.localizedDescription)
      } else {
        openPhase = .failed(error.localizedDescription)
      }
    }
  }

  public var failureGuidance: String? {
    guard !operationFailures.isEmpty else { return nil }
    return operationFailures.map { failure in
      let trawlerDisplayName =
        failure.registeredTrawlerDisplayName.isEmpty
        ? (trawlerDisplayNamesByManifestIdentity[
          failure.registeredTrawlerManifestIdentity
        ] ?? "A trawler")
        : failure.registeredTrawlerDisplayName
      return "\(trawlerDisplayName): \(failure.failureMessage)"
    }
    .joined(separator: " ")
  }

  public var hasTimeoutFailure: Bool {
    operationFailures.contains(where: { $0.failureCode == .timeout })
  }

  public func trawlerDisplayName(
    for registeredTrawlerManifestIdentity: String,
    resolvedName: String?
  ) -> String {
    resolvedName
      ?? trawlerDisplayNamesByManifestIdentity[registeredTrawlerManifestIdentity]
      ?? SearchTrawlerResolver.unavailableDisplayName
  }

  public func displayTitle(for searchMatch: SearchMatch) -> String {
    searchMatch.title
  }

  public func clearOpenResult() {
    openGeneration &+= 1
    openPhase = .idle
    openResult = nil
  }

  private func clearCommittedSearch() {
    searchMatches = []
    operationFailures = []
    trawlersSkippedFromOperation = []
    trawlerSearchResults = []
    trawlerDisplayNamesByManifestIdentity = [:]
    resultLimit = 0
    isTruncated = false
    committedInput = nil
    timedOutLocally = false
    phase = .idle
    openGeneration &+= 1
    openPhase = .idle
    openResult = nil
  }

  private func searchWithinLimit(
    _ query: String,
    registeredTrawlerManifestIdentity: String?
  ) async throws -> SearchResponse {
    let client = client
    let waitLimit = waitLimit
    return try await withThrowingTaskGroup(of: SearchResponse.self) { group in
      group.addTask {
        try await client.search(
          query,
          registeredTrawlerManifestIdentity: registeredTrawlerManifestIdentity)
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
  public private(set) var registeredTrawlerManifestIdentity: String?
  public var selectedSearchMatchIdentifier: SearchMatch.ID?

  public init(model: SearchModel, registeredTrawlerManifestIdentity: String?) {
    self.model = model
    self.registeredTrawlerManifestIdentity = registeredTrawlerManifestIdentity
  }

  public func changeScope(to registeredTrawlerManifestIdentity: String?) {
    guard
      registeredTrawlerManifestIdentity != self.registeredTrawlerManifestIdentity
    else {
      return
    }
    self.registeredTrawlerManifestIdentity = registeredTrawlerManifestIdentity
    invalidateInput()
  }

  public func resultForReturn() -> SearchMatch? {
    guard let selectedSearchMatchIdentifier else { return nil }
    return model.searchMatches.first {
      $0.id == selectedSearchMatchIdentifier
    }
  }

  public func handleReturn() async {
    guard let searchMatch = resultForReturn() else { return }
    await model.open(searchMatch)
  }

  public func reconcileCommittedResults() {
    guard
      let selectedSearchMatchIdentifier,
      !model.searchMatches.contains(where: {
        $0.id == selectedSearchMatchIdentifier
      })
    else {
      return
    }
    self.selectedSearchMatchIdentifier = nil
    model.clearOpenResult()
  }

  private func invalidateInput() {
    model.invalidateForInputChange()
  }
}
