import Testing

@testable import Trawl
@testable import TrawlClient
@testable import TrawlCore

@Test func searchWorkspaceShowsOnlyItsFieldUntilThereIsAQueryOutcome() {
  #expect(SearchWorkspaceMode.resolve(phase: .idle, resultCount: 0) == .field)
  #expect(SearchWorkspaceMode.resolve(phase: .loading, resultCount: 0) == .outcome)
  #expect(SearchWorkspaceMode.resolve(phase: .complete, resultCount: 0) == .outcome)
  #expect(
    SearchWorkspaceMode.resolve(phase: .failed("Synthetic failure."), resultCount: 0) == .outcome)
}

@Test func emptyScopedSearchExplainsWhatToDo() {
  #expect(SearchWorkspaceFieldContent.resolve(isScoped: true) == .scopedPrompt)
  #expect(SearchWorkspaceFieldContent.resolve(isScoped: false) == .none)
}

@Test func searchWorkspaceKeepsUsefulPartialResultsInTheResultsLayout() {
  #expect(SearchWorkspaceMode.resolve(phase: .complete, resultCount: 1) == .results)
  #expect(SearchWorkspaceMode.resolve(phase: .partial, resultCount: 1) == .results)
  #expect(SearchWorkspaceMode.resolve(phase: .partial, resultCount: 0) == .outcome)
}

@Test func searchWorkspaceKeepsTheResultsLayoutWhileAReplacementSearchRuns() {
  #expect(SearchWorkspaceMode.resolve(phase: .loading, resultCount: 1) == .results)
  #expect(
    SearchWorkspaceMode.resolve(phase: .failed("Synthetic failure."), resultCount: 1) == .results)
}

@Test func retainedResultsStayVisibleForTimeoutAndFailure() {
  #expect(SearchWorkspaceMode.resolve(phase: .timedOut, resultCount: 1) == .results)
  #expect(
    SearchWorkspaceMode.resolve(phase: .failed("Synthetic failure."), resultCount: 1) == .results)
}

@Test func retainedResultCopyNamesTheCommittedQueryAndFailure() {
  #expect(
    SearchResultsContextCopy.retained(.loading, query: "old", failure: nil)
      == "Showing results for old while searching")
  #expect(
    SearchResultsContextCopy.retained(.timedOut, query: "old", failure: nil)
      == "Showing results for old. The replacement search timed out.")
  #expect(
    SearchResultsContextCopy.retained(.failed("bad"), query: "old", failure: "Bad source.")
      == "Showing results for old. bad")
}

@Test func compactRecordUsesTheSameSearchHierarchyAsTheResultsList() {
  #expect(
    SearchWorkspaceLayout.resolve(isCompact: true, showsCompactRecord: false, openPhase: .output)
      == .results
  )
  #expect(
    SearchWorkspaceLayout.resolve(isCompact: true, showsCompactRecord: true, openPhase: .output)
      == .compactRecord
  )
  #expect(
    SearchWorkspaceLayout.resolve(isCompact: false, showsCompactRecord: true, openPhase: .output)
      == .split
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
      == "Contacts: This source is not ready yet."
  )
  #expect(
    SearchWorkspaceCopy.partialNoMatches(failureGuidance: failure, isScoped: true)
      == failure
  )
  #expect(
    SearchWorkspaceCopy.partialNoMatches(failureGuidance: nil, isScoped: false)
      == "Some sources could not be searched."
  )
}
