import Foundation
import Observation
import TrawlClient

public struct SearchStateInput: Sendable, Equatable {
  public let query: String
  public let registeredTrawler: RegisteredTrawlerIdentity?
  public let earliestMatchingArchiveRecordTimeInclusive: Date?
  public let latestMatchingArchiveRecordTimeInclusive: Date?
  public let limit: UInt32

  public init(
    query: String,
    registeredTrawler: RegisteredTrawlerIdentity?,
    earliestMatchingArchiveRecordTimeInclusive: Date? = nil,
    latestMatchingArchiveRecordTimeInclusive: Date? = nil,
    limit: UInt32
  ) {
    self.query = query
    self.registeredTrawler = registeredTrawler
    self.earliestMatchingArchiveRecordTimeInclusive = earliestMatchingArchiveRecordTimeInclusive
    self.latestMatchingArchiveRecordTimeInclusive = latestMatchingArchiveRecordTimeInclusive
    self.limit = limit
  }

  public var hasSearchCriteria: Bool {
    !query.isEmpty
      || registeredTrawler != nil
      || earliestMatchingArchiveRecordTimeInclusive != nil
      || latestMatchingArchiveRecordTimeInclusive != nil
  }
}

public enum SearchStateEvent: Sendable, Equatable {
  case loading(SearchStateInput)
  case response(SearchStateInput, FederatedTrawlerSearchOperation)
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

  public func displayName(for registeredTrawler: RegisteredTrawlerIdentity) -> String? {
    statuses.first(where: { $0.id == registeredTrawler })?
      .registeredTrawlerManifest.registeredTrawlerDisplayName
  }

  public func displayNameOrUnavailable(
    for registeredTrawler: RegisteredTrawlerIdentity
  ) -> String {
    displayName(for: registeredTrawler) ?? Self.unavailableDisplayName
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
  case loaded(OpenResponse, OpenRecord)
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
  public private(set) var trawlerDisplayNamesByRegisteredTrawler:
    [RegisteredTrawlerIdentity: String] = [:]
  public private(set) var resultLimit: UInt32 = 0
  public private(set) var isTruncated = false
  public private(set) var isLoadingMore = false
  public private(set) var loadMoreFailureMessage: String?
  public private(set) var openPhase: SearchOpenPhase = .idle
  public private(set) var committedInput: SearchStateInput?
  public private(set) var timedOutLocally = false

  public var canLoadMore: Bool {
    isTruncated
      && !isLoadingMore
      && resultLimit < TrawlArchiveSearchRequest.maximumSupportedSearchMatchCount
  }

  public var totalMatchedRecordCount: UInt64 {
    trawlerSearchResults.reduce(UInt64(0)) { $0 + $1.totalSearchMatches }
  }

  public var totalMatchedCountIsLowerBound: Bool {
    trawlerSearchResults.contains(where: \.totalSearchMatchesIsLowerBound)
  }

  public init(
    client: any TrawlClient,
    debounce: Duration = .milliseconds(180),
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
    registeredTrawler: RegisteredTrawlerIdentity?,
    earliestMatchingArchiveRecordTimeInclusive: Date? = nil,
    latestMatchingArchiveRecordTimeInclusive: Date? = nil,
    allowEmptyBrowse: Bool = false
  ) async {
    generation &+= 1
    let token = generation
    let query = rawQuery.trimmingCharacters(in: .whitespacesAndNewlines)
    let input = SearchStateInput(
      query: query,
      registeredTrawler: registeredTrawler,
      earliestMatchingArchiveRecordTimeInclusive: earliestMatchingArchiveRecordTimeInclusive,
      latestMatchingArchiveRecordTimeInclusive: latestMatchingArchiveRecordTimeInclusive,
      limit: TrawlArchiveSearchRequest.defaultMaximumReturnedSearchMatchCount
    )
    let isUndatedContactsBrowse =
      query.isEmpty
      && earliestMatchingArchiveRecordTimeInclusive == nil
      && latestMatchingArchiveRecordTimeInclusive == nil
      && registeredTrawler?.registeredTrawlerIdentity == "contacts"
    guard (!isUndatedContactsBrowse && (input.hasSearchCriteria || allowEmptyBrowse)) else {
      clearCommittedSearch()
      return
    }

    let isEmptyBrowse =
      query.isEmpty
      && earliestMatchingArchiveRecordTimeInclusive == nil
      && latestMatchingArchiveRecordTimeInclusive == nil
    let effectiveEarliest =
      isEmptyBrowse
      ? Date(timeIntervalSince1970: 0)
      : earliestMatchingArchiveRecordTimeInclusive
    let effectiveLatest =
      isEmptyBrowse
      ? (registeredTrawler?.registeredTrawlerIdentity == "calendar" ? nil : Date())
      : latestMatchingArchiveRecordTimeInclusive

    timedOutLocally = false
    isLoadingMore = false
    loadMoreFailureMessage = nil
    phase = .loading
    observe(.loading(input))

    do {
      try await Task.sleep(for: debounce)
      guard token == generation else { return }
      let request = TrawlArchiveSearchRequest(
        searchQueryText: query,
        onlySearchThisRegisteredTrawler: registeredTrawler,
        earliestMatchingArchiveRecordTimeInclusive: effectiveEarliest,
        latestMatchingArchiveRecordTimeInclusive: effectiveLatest,
        maximumReturnedSearchMatchCount: input.limit
      )
      let response = try await searchWithinLimit(request)
      observe(.response(input, response))
      try Task.checkCancellation()
      guard token == generation else { return }
      applySearchResponse(response, for: input)
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

  /// Loads the next 50 results for the currently committed search while keeping
  /// already loaded rows and the active selection intact if loading fails.
  public func loadMore() async {
    guard canLoadMore, let baseInput = committedInput else { return }
    let nextLimit = min(
      TrawlArchiveSearchRequest.maximumSupportedSearchMatchCount,
      max(baseInput.limit, resultLimit) + TrawlArchiveSearchRequest.pageStepSize
    )
    guard nextLimit > resultLimit else { return }
    let token = generation
    let nextInput = SearchStateInput(
      query: baseInput.query,
      registeredTrawler: baseInput.registeredTrawler,
      earliestMatchingArchiveRecordTimeInclusive:
        baseInput.earliestMatchingArchiveRecordTimeInclusive,
      latestMatchingArchiveRecordTimeInclusive:
        baseInput.latestMatchingArchiveRecordTimeInclusive,
      limit: nextLimit
    )
    isLoadingMore = true
    loadMoreFailureMessage = nil
    defer {
      if token == generation {
        isLoadingMore = false
      }
    }
    do {
      let isEmptyBrowse =
        nextInput.query.isEmpty
        && nextInput.earliestMatchingArchiveRecordTimeInclusive == nil
        && nextInput.latestMatchingArchiveRecordTimeInclusive == nil
      let effectiveEarliest =
        isEmptyBrowse
        ? Date(timeIntervalSince1970: 0)
        : nextInput.earliestMatchingArchiveRecordTimeInclusive
      let effectiveLatest =
        isEmptyBrowse
        ? (nextInput.registeredTrawler?.registeredTrawlerIdentity == "calendar" ? nil : Date())
        : nextInput.latestMatchingArchiveRecordTimeInclusive
      let request = TrawlArchiveSearchRequest(
        searchQueryText: nextInput.query,
        onlySearchThisRegisteredTrawler: nextInput.registeredTrawler,
        earliestMatchingArchiveRecordTimeInclusive: effectiveEarliest,
        latestMatchingArchiveRecordTimeInclusive: effectiveLatest,
        maximumReturnedSearchMatchCount: nextInput.limit
      )
      let response = try await searchWithinLimit(request)
      try Task.checkCancellation()
      guard token == generation else { return }
      applySearchResponse(response, for: nextInput)
    } catch is CancellationError {
      return
    } catch TrawlClientError.cancelled {
      return
    } catch {
      guard token == generation else { return }
      loadMoreFailureMessage = "Could not load more results. Earlier results are still shown."
    }
  }

  private func applySearchResponse(
    _ response: FederatedTrawlerSearchOperation,
    for input: SearchStateInput
  ) {
    searchMatches = response.searchMatchesInDisplayOrder
    operationFailures = response.operationFailures
    trawlersSkippedFromOperation = response.trawlersSkippedFromOperation
    trawlerSearchResults = response.trawlerSearchResults
    trawlerDisplayNamesByRegisteredTrawler = Dictionary(
      uniqueKeysWithValues: response.trawlerSearchResults.map {
        ($0.registeredTrawler, $0.registeredTrawlerDisplayName)
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
        ? .timedOut : .failed(failureGuidance ?? "No app returned search results.")
    }
  }

  public func open(_ searchMatch: SearchMatch) async {
    guard searchMatches.contains(searchMatch) else { return }
    await openLink(searchMatch.trawlLink, anchor: searchMatch.recordAnchor, matchID: searchMatch.id)
  }

  public func open(link: GloballyRoutableTrawlLink, anchor: RecordAnchorIdentifier) async {
    let matchID = SearchMatchIdentifier(trawlLink: link, recordAnchor: anchor)
    await openLink(link, anchor: anchor, matchID: matchID)
  }

  private func openLink(
    _ link: GloballyRoutableTrawlLink,
    anchor: RecordAnchorIdentifier,
    matchID: SearchMatchIdentifier
  ) async {
    openGeneration &+= 1
    let token = openGeneration
    openPhase = .loading
    observe(.opening(matchID))
    do {
      let response = try await client.open(
        link: link,
        anchor: anchor)
      observe(.openResponse(matchID, response))
      try Task.checkCancellation()
      guard token == openGeneration else { return }
      switch response.outcome {
      case .complete:
        if let record = response.record {
          openPhase = .loaded(response, record)
        } else {
          openPhase = .failed("OpenTrawl could not open this result.")
        }
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
      observe(.openFailed(matchID, error.localizedDescription))
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
        ? (trawlerDisplayNamesByRegisteredTrawler[
          failure.failedTrawler
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
    for registeredTrawler: RegisteredTrawlerIdentity,
    resolvedName: String?
  ) -> String {
    resolvedName
      ?? trawlerDisplayNamesByRegisteredTrawler[registeredTrawler]
      ?? SearchTrawlerResolver.unavailableDisplayName
  }

  public func displayTitle(for searchMatch: SearchMatch) -> String {
    searchMatch.title
  }

  public func clearOpenResult() {
    openGeneration &+= 1
    openPhase = .idle
  }

  private func clearCommittedSearch() {
    searchMatches = []
    operationFailures = []
    trawlersSkippedFromOperation = []
    trawlerSearchResults = []
    trawlerDisplayNamesByRegisteredTrawler = [:]
    resultLimit = 0
    isTruncated = false
    committedInput = nil
    timedOutLocally = false
    phase = .idle
    openGeneration &+= 1
    openPhase = .idle
  }

  private func searchWithinLimit(
    _ request: TrawlArchiveSearchRequest
  ) async throws -> FederatedTrawlerSearchOperation {
    let client = client
    let waitLimit = waitLimit
    return try await withThrowingTaskGroup(of: FederatedTrawlerSearchOperation.self) { group in
      group.addTask {
        try await client.search(request)
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
  public private(set) var registeredTrawler: RegisteredTrawlerIdentity?
  public private(set) var earliestMatchingArchiveRecordTimeInclusive: Date?
  public private(set) var latestMatchingArchiveRecordTimeInclusive: Date?
  public var selectedSearchMatchIdentifier: SearchMatch.ID?

  public var hasActiveDateRange: Bool {
    earliestMatchingArchiveRecordTimeInclusive != nil
      || latestMatchingArchiveRecordTimeInclusive != nil
  }

  public var hasSearchCriteria: Bool {
    !query.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
      || registeredTrawler != nil
      || hasActiveDateRange
  }

  public init(model: SearchModel, registeredTrawler: RegisteredTrawlerIdentity?) {
    self.model = model
    self.registeredTrawler = registeredTrawler
  }

  public func changeScope(to registeredTrawler: RegisteredTrawlerIdentity?) {
    guard registeredTrawler != self.registeredTrawler else { return }
    self.registeredTrawler = registeredTrawler
    selectedSearchMatchIdentifier = nil
    model.clearOpenResult()
    invalidateInput()
  }

  public func changeDateRange(
    earliestInclusive: Date?,
    latestInclusive: Date?
  ) {
    guard
      earliestInclusive != earliestMatchingArchiveRecordTimeInclusive
        || latestInclusive != latestMatchingArchiveRecordTimeInclusive
    else {
      return
    }
    earliestMatchingArchiveRecordTimeInclusive = earliestInclusive
    latestMatchingArchiveRecordTimeInclusive = latestInclusive
    selectedSearchMatchIdentifier = nil
    model.clearOpenResult()
    invalidateInput()
  }

  public func applyScopeAndDateRange(
    registeredTrawler: RegisteredTrawlerIdentity?,
    earliestInclusive: Date?,
    latestInclusive: Date?
  ) {
    let changed =
      registeredTrawler != self.registeredTrawler
      || earliestInclusive != earliestMatchingArchiveRecordTimeInclusive
      || latestInclusive != latestMatchingArchiveRecordTimeInclusive
    guard changed else { return }
    self.registeredTrawler = registeredTrawler
    earliestMatchingArchiveRecordTimeInclusive = earliestInclusive
    latestMatchingArchiveRecordTimeInclusive = latestInclusive
    selectedSearchMatchIdentifier = nil
    model.clearOpenResult()
    invalidateInput()
  }

  public func clearDateRange() {
    changeDateRange(earliestInclusive: nil, latestInclusive: nil)
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
