import AppKit
import SwiftUI
import Testing

@testable import Trawl
@testable import TrawlClient
@testable import TrawlCore

@Suite(.serialized)
struct SearchOverlayViewTests {
  @MainActor
  @Test func mountedSearchOverlayReturnsFocusWhenSearchStarts() async throws {
    let model = SearchModel(client: MountedSearchClient(), debounce: .seconds(1))
    let overlay = SearchOverlay(
      model: model,
      initialScope: nil,
      onDismiss: {}
    )
    let host = NSHostingView(rootView: overlay)
    let window = NSWindow(
      contentRect: NSRect(x: 0, y: 0, width: 800, height: 600),
      styleMask: [.titled],
      backing: .buffered,
      defer: false
    )
    window.contentView = host
    window.makeKeyAndOrderFront(nil)
    try await Task.sleep(for: .milliseconds(50))

    let searchField = window.firstResponder
    #expect(searchField != nil)
    #expect(window.makeFirstResponder(host))
    #expect(window.firstResponder === host)

    let search = Task { await model.search("focus", source: nil) }
    try await Task.sleep(for: .milliseconds(50))

    #expect(window.firstResponder != nil)
    search.cancel()
    await search.value
    window.orderOut(nil)
  }

  @MainActor
  @Test func mountedSearchResultsListHandlesReturnForTheSelectedResult() {
    let hit = SearchHit(
      sourceID: "calendar",
      openRef: "calendar:event/return",
      shortRef: "return",
      timeRFC3339: "",
      time: nil,
      anchorID: "match",
      summary: ResultSummary(title: "Synthetic event", subtitle: "Avery Example"),
      evidence: [
        .field(
          label: "Event match", name: "event",
          value: [SearchTextRun(text: "Synthetic", matched: true)])
      ],
      allDay: false,
      availability: nil,
      unread: nil
    )
    let recorder = ReturnRecorder()
    let driver = MountedReturnDriver()
    let iconStore = SourceIconStore()
    let host = NSHostingView(
      rootView: MountedSearchResultsList(
        hit: hit,
        onFocused: { driver.searchResultsFocused() },
        onReturn: { recorder.count += 1 }
      )
      .environment(iconStore)
    )
    let window = MountedKeyWindow(
      contentRect: NSRect(x: 0, y: 0, width: 800, height: 600),
      styleMask: [.titled],
      backing: .buffered,
      defer: false
    )
    driver.window = window
    window.contentView = host
    defer {
      window.orderOut(nil)
    }

    window.makeKeyAndOrderFront(nil)
    driver.windowBecameKey()
    let deadline = Date().addingTimeInterval(1)
    while !driver.didDispatchReturn && Date() < deadline {
      RunLoop.main.run(mode: .default, before: Date().addingTimeInterval(0.01))
    }

    #expect(driver.didDispatchReturn)
    #expect(driver.hadFirstResponderAtDispatch)
    #expect(recorder.count == 1)
  }
}

@MainActor
private final class ReturnRecorder {
  var count = 0
}

@MainActor
private final class MountedReturnDriver {
  weak var window: NSWindow?
  private(set) var didDispatchReturn = false
  private(set) var hadFirstResponderAtDispatch = false

  private var hasResultsFocus = false
  private var hasKeyWindow = false

  func searchResultsFocused() {
    hasResultsFocus = true
    dispatchReturnIfReady()
  }

  func windowBecameKey() {
    hasKeyWindow = true
    dispatchReturnIfReady()
  }

  private func dispatchReturnIfReady() {
    guard hasResultsFocus, hasKeyWindow, !didDispatchReturn, let window else { return }
    guard
      let event = NSEvent.keyEvent(
        with: .keyDown,
        location: .zero,
        modifierFlags: [],
        timestamp: 0,
        windowNumber: window.windowNumber,
        context: nil,
        characters: "\r",
        charactersIgnoringModifiers: "\r",
        isARepeat: false,
        keyCode: 36
      )
    else {
      NSApplication.shared.stop(nil)
      return
    }

    hadFirstResponderAtDispatch = window.firstResponder != nil
    didDispatchReturn = true
    window.sendEvent(event)
    NSApplication.shared.stop(nil)
  }
}

@MainActor
private final class MountedKeyWindow: NSWindow {}

private struct MountedSearchResultsList: View {
  let hit: SearchHit
  let onFocused: @MainActor @Sendable () -> Void
  let onReturn: () -> Void
  @State private var selectedResultID: SearchHit.ID?
  @FocusState private var focus: SearchFocus?

  init(
    hit: SearchHit,
    onFocused: @escaping @MainActor @Sendable () -> Void,
    onReturn: @escaping () -> Void
  ) {
    self.hit = hit
    self.onFocused = onFocused
    self.onReturn = onReturn
    _selectedResultID = State(initialValue: hit.id)
  }

  var body: some View {
    SearchResultsList(
      phase: .complete,
      results: [hit],
      sourceDisplayName: { _ in "Calendar" },
      failureGuidance: nil,
      hasTimeoutFailure: false,
      committedQuery: nil,
      resultLimit: 20,
      title: { _ in "Synthetic" },
      selectedResultID: $selectedResultID,
      focus: $focus,
      onReturn: onReturn,
      onOpen: { _ in },
      onSelectionChanged: { _ in }
    )
    .onAppear { focus = .results }
    .onChange(of: focus) { _, newFocus in
      guard newFocus == .results else { return }
      RunLoop.main.perform {
        MainActor.assumeIsolated(onFocused)
      }
    }
  }
}

private struct MountedSearchClient: TrawlClient {
  func status() async throws -> StatusResponse { fatalError() }
  func requestPhotos() async throws -> StatusResponse { fatalError() }
  func sync() async throws -> SyncResponse { fatalError() }
  func search(_: String, source _: String?) async throws -> SearchResponse {
    SearchResponse(
      order: .recency,
      sources: [],
      hits: [],
      failures: [],
      skippedSources: [],
      outcome: .complete,
      resultLimit: 20,
      truncated: false
    )
  }
  func open(sourceID _: String, ref _: String, anchorID _: String) async throws -> OpenResponse {
    fatalError()
  }
}
