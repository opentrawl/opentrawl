import Testing

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
