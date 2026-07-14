import Testing

@testable import Trawl

@testable import TrawlClient
@testable import TrawlCore

@Test func searchWorkspaceShowsOnlyItsFieldUntilThereIsAQueryOutcome() {
  #expect(SearchWorkspaceMode.resolve(phase: .idle, resultCount: 0) == .field)
  #expect(SearchWorkspaceMode.resolve(phase: .loading, resultCount: 0) == .outcome)
  #expect(SearchWorkspaceMode.resolve(phase: .complete, resultCount: 0) == .outcome)
  #expect(SearchWorkspaceMode.resolve(phase: .failed("Synthetic failure."), resultCount: 0) == .outcome)
}

@Test func searchWorkspaceKeepsUsefulPartialResultsInTheResultsLayout() {
  #expect(SearchWorkspaceMode.resolve(phase: .complete, resultCount: 1) == .results)
  #expect(SearchWorkspaceMode.resolve(phase: .partial, resultCount: 1) == .results)
  #expect(SearchWorkspaceMode.resolve(phase: .partial, resultCount: 0) == .outcome)
}

@Test func searchWorkspaceKeepsTheResultsLayoutWhileAReplacementSearchRuns() {
  #expect(SearchWorkspaceMode.resolve(phase: .loading, resultCount: 1) == .results)
  #expect(SearchWorkspaceMode.resolve(phase: .failed("Synthetic failure."), resultCount: 1) == .results)
}

@Test func retainedResultsStayVisibleForTimeoutAndFailure() {
  #expect(SearchWorkspaceMode.resolve(phase: .timedOut, resultCount: 1) == .results)
  #expect(SearchWorkspaceMode.resolve(phase: .failed("Synthetic failure."), resultCount: 1) == .results)
}

@Test func retainedResultCopyNamesTheCommittedQueryAndFailure() {
  #expect(SearchResultsContextCopy.retained(.loading, query: "old", failure: nil) == "Showing results for old while searching")
  #expect(SearchResultsContextCopy.retained(.timedOut, query: "old", failure: nil) == "Showing results for old. The replacement search timed out.")
  #expect(SearchResultsContextCopy.retained(.failed("bad"), query: "old", failure: "Bad source.") == "Showing results for old. bad")
}

@Test func widePaneAppearsOnlyForAnOpenAttempt() {
  #expect(!SearchWorkspacePaneVisibility.showsRecord(for: .idle))
  #expect(SearchWorkspacePaneVisibility.showsRecord(for: .loading))
  #expect(SearchWorkspacePaneVisibility.showsRecord(for: .failed("Synthetic failure.")))
}

@Test func searchWorkspaceCopyUsesCorrectSingularAndPluralCounts() {
  #expect(SearchWorkspaceCopy.usefulResults(1) == "Showing 1 useful result.")
  #expect(SearchWorkspaceCopy.usefulResults(2) == "Showing 2 useful results.")

  let calendar = SkippedSource(
    sourceID: "calendar",
    surface: "Calendar",
    reason: "Search is not supported."
  )
  let notes = SkippedSource(
    sourceID: "notes",
    surface: "Notes",
    reason: "Allow Notes access."
  )
  #expect(SearchWorkspaceCopy.skippedOutcome(for: [calendar]) == "Calendar: Search is not supported.")
  #expect(
    SearchWorkspaceCopy.skippedOutcome(for: [calendar, notes])
      == "Calendar: Search is not supported. 1 more source was skipped."
  )
}

@Test func resultBoundsUseTruthfulSingularAndPluralCopy() {
  #expect(SearchResultBounds.copy(resultCount: 0, resultLimit: 20) == "Showing no results")
  #expect(SearchResultBounds.copy(resultCount: 1, resultLimit: 20) == "Showing 1 result")
  #expect(SearchResultBounds.copy(resultCount: 2, resultLimit: 20) == "Showing 2 results")
  #expect(SearchResultBounds.copy(resultCount: 24, resultLimit: 20) == "Showing 20 results")
}

@Test func escapeReturnsFromRecordToResultsThenFieldThenHome() {
  #expect(SearchEscapeAction.resolve(showsRecord: true, focus: .field) == .closeRecord)
  #expect(SearchEscapeAction.resolve(showsRecord: false, focus: .results) == .focusField)
  #expect(SearchEscapeAction.resolve(showsRecord: false, focus: .field) == .dismiss)
  #expect(SearchEscapeAction.resolve(showsRecord: false, focus: nil) == .dismiss)
}

@Test func partialEmptySearchLeadsWithTheResultWithoutChangingScopedFailureCopy() {
  let failure = "Contacts: This source is not ready yet."
  #expect(
    SearchWorkspaceCopy.partialNoMatches(failureGuidance: failure, isScoped: false)
      == "No matches in available sources. Contacts: This source is not ready yet."
  )
  #expect(
    SearchWorkspaceCopy.partialNoMatches(failureGuidance: failure, isScoped: true)
      == failure
  )
  #expect(
    SearchWorkspaceCopy.partialNoMatches(failureGuidance: nil, isScoped: false)
      == "No matches in available sources. Some sources failed."
  )
}

@Test func terminalSearchOutcomesHaveClearHeadingsAndSourceAwareDetails() {
  let skipped = SkippedSource(
    sourceID: "calendar",
    surface: "Calendar",
    reason: "Search is not supported."
  )
  let failure = "Photos: This source is not ready yet."

  #expect(SearchWorkspaceCopy.outcomeTitle(for: .complete) == "No matches")
  #expect(
    SearchWorkspaceCopy.outcomeDetail(
      for: .complete,
      failureGuidance: nil,
      skippedSources: [],
      isScoped: false,
      timeoutSeconds: 10
    ) == "No matches in available sources."
  )
  #expect(
    SearchWorkspaceCopy.outcomeDetail(
      for: .complete,
      failureGuidance: nil,
      skippedSources: [],
      isScoped: true,
      timeoutSeconds: 10
    ) == "No matches in this source."
  )
  #expect(SearchWorkspaceCopy.outcomeTitle(for: .failed(failure)) == "Search unavailable")
  #expect(
    SearchWorkspaceCopy.outcomeDetail(
      for: .failed(failure),
      failureGuidance: nil,
      skippedSources: [],
      isScoped: true,
      timeoutSeconds: 10
    ) == failure
  )
  #expect(SearchWorkspaceCopy.outcomeTitle(for: .timedOut) == "Search timed out")
  #expect(
    SearchWorkspaceCopy.outcomeDetail(
      for: .timedOut,
      failureGuidance: nil,
      skippedSources: [],
      isScoped: false,
      timeoutSeconds: 10
    ) == "Search stopped after 10 seconds."
  )
  #expect(SearchWorkspaceCopy.outcomeTitle(for: .skipped) == "Search unavailable")
  #expect(
    SearchWorkspaceCopy.outcomeDetail(
      for: .skipped,
      failureGuidance: nil,
      skippedSources: [skipped],
      isScoped: true,
      timeoutSeconds: 10
    ) == "Calendar: Search is not supported."
  )
}
