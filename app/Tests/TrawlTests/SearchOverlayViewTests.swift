import AppKit
import SwiftUI
import Testing

@testable import Trawl
@testable import TrawlClient
@testable import TrawlCore

@Suite(.serialized)
struct SearchOverlayViewTests {
  @Test func productUsesOneFixed1120By760Frame() {
    let frame = CGSize(width: 1_120, height: 760)

    #expect(TrawlDesign.defaultWindow == frame)
    #expect(TrawlDesign.minimumWindow == frame)
    #expect(TrawlDesign.maximumWindow == frame)
    #expect(TrawlDesign.onboardingWindow == frame)
    #expect(!TrawlDesign.usesCompactSearchLayout(width: frame.width))
  }

  @MainActor
  @Test func fixedConstellationShowsEveryProductAppWithoutClippingOrOverlap() {
    let available = constellationSize(in: TrawlDesign.defaultWindow)
    let canvas = ConstellationView.canvasSize(in: available)
    let sourceIDs = [
      "calendar", "contacts", "gmail", "imessage", "notes", "photos", "telegram", "twitter",
      "whatsapp",
    ]
    let centre = ConstellationPoint(
      x: canvas.width / 2,
      y: canvas.height / 2 - min(27, canvas.height * 0.035)
    )
    let metrics = ConstellationLayoutMetrics.forSourceCount(
      sourceIDs.count,
      fitting: ConstellationPoint(x: canvas.width, y: canvas.height)
    )
    let placements = ConstellationOrbitLayout(
      sourceIDs: sourceIDs,
      size: ConstellationPoint(x: canvas.width, y: canvas.height),
      centre: centre,
      metrics: metrics
    ).placements()
    let bounds = ConstellationRect(x: 0, y: 0, width: canvas.width, height: canvas.height)

    #expect(canvas == available)
    #expect(canvas.width <= TrawlDesign.constellationMaximumWidth)
    #expect(canvas.height <= TrawlDesign.constellationMaximumHeight)
    #expect(placements.map(\.id).sorted() == sourceIDs.sorted())
    for placement in placements {
      #expect(bounds.contains(placement.labelRect))
    }
    for index in placements.indices {
      for otherIndex in placements.indices.dropFirst(index + 1) {
        #expect(!placements[index].labelRect.intersects(placements[otherIndex].labelRect))
      }
    }
    assertHeadlineLabelFits(
      title: "Search Twitter (X)",
      detail: "tweets · bookmarks · likes · mentions",
      metrics: metrics
    )
    assertHeadlineLabelFits(
      title: "Search Telegram",
      detail: "chats · folders · topics",
      metrics: metrics
    )
    #expect(ConstellationLabelLayout.titleLineLimit(for: 78) == 2)
    #expect(ConstellationLabelLayout.titleLineLimit(for: 68) == 2)
  }

  @MainActor
  @Test func mountedSearchOverlayReturnsFocusWhenSearchStarts() async throws {
    let client = MountedSearchClient()
    let model = SearchModel(client: client, debounce: .seconds(1))
    let overlay = SearchOverlay(
      model: model,
      client: client,
      scope: .constant(nil),
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
  @Test(
    .disabled(
      if: ProcessInfo.processInfo.environment["OPENTRAWL_SKIP_WINDOW_FOCUS_TESTS"] == "1",
      "requires an interactive AppKit window-focus session"
    )
  )
  func mountedSearchResultsListHandlesReturnForTheSelectedResult() {
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
        onReturn: { recorder.count += 1 },
        onEscape: {}
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

  @MainActor
  @Test(
    .disabled(
      if: ProcessInfo.processInfo.environment["OPENTRAWL_SKIP_WINDOW_FOCUS_TESTS"] == "1",
      "requires an interactive AppKit window-focus session"
    )
  )
  func mountedSearchOverlayClosesOpenedRecordOnEscapeWithoutDismissing() async throws {
    let hit = SearchHit(
      sourceID: "calendar",
      openRef: "calendar:event/escape",
      shortRef: "escape",
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
    let client = MountedOpenedSearchClient(hit: hit)
    let model = SearchModel(client: client, debounce: .zero)
    await model.search("synthetic", source: nil)
    await model.open(hit)
    #expect(model.openPhase == .output)

    let recorder = EscapeRecorder()
    let host = NSHostingView(
      rootView: SearchOverlay(
        model: model,
        client: client,
        scope: .constant(nil),
        initialQuery: "synthetic",
        onDismiss: { recorder.count += 1 }
      )
      .environment(SourceIconStore())
    )
    let window = NSWindow(
      contentRect: NSRect(x: 0, y: 0, width: 800, height: 600),
      styleMask: [.titled],
      backing: .buffered,
      defer: false
    )
    window.contentView = host
    window.makeKeyAndOrderFront(nil)
    defer { window.orderOut(nil) }

    try sendMountedKey("\u{1B}", keyCode: 53, to: window)
    try await Task.sleep(for: .milliseconds(20))

    #expect(model.openPhase == .idle)
    #expect(recorder.count == 0)
  }

  @MainActor
  @Test func mountedSearchOverlayDismissesFromTheFocusedFieldOnEscape() async throws {
    let client = MountedSearchClient()
    let model = SearchModel(client: client, debounce: .seconds(1))
    let recorder = EscapeRecorder()
    let host = NSHostingView(
      rootView: SearchOverlay(
        model: model,
        client: client,
        scope: .constant(nil),
        onDismiss: { recorder.count += 1 }
      )
    )
    let window = NSWindow(
      contentRect: NSRect(x: 0, y: 0, width: 800, height: 600),
      styleMask: [.titled],
      backing: .buffered,
      defer: false
    )
    window.contentView = host
    window.makeKeyAndOrderFront(nil)
    defer { window.orderOut(nil) }
    try await Task.sleep(for: .milliseconds(50))

    let event = try #require(
      NSEvent.keyEvent(
        with: .keyDown,
        location: .zero,
        modifierFlags: [],
        timestamp: 0,
        windowNumber: window.windowNumber,
        context: nil,
        characters: "\u{1B}",
        charactersIgnoringModifiers: "\u{1B}",
        isARepeat: false,
        keyCode: 53
      )
    )
    window.sendEvent(event)
    try await Task.sleep(for: .milliseconds(20))

    #expect(recorder.count == 1)
  }

  @MainActor
  @Test func mountedSearchOverlayKeepsTheWorkspaceOpenWhenTheBackdropIsClicked() async throws {
    let client = MountedSearchClient()
    let model = SearchModel(client: client, debounce: .seconds(1))
    let recorder = BackdropDismissRecorder()
    let scope = try mountedRestingSource(id: "telegram", surface: "Telegram")
    let host = NSHostingView(
      rootView: MountedSearchDismissHarness(
        client: client,
        model: model,
        scope: scope,
        recorder: recorder
      )
      .environment(SourceIconStore())
    )
    let window = NSWindow(
      contentRect: NSRect(x: 0, y: 0, width: 800, height: 600),
      styleMask: [.titled],
      backing: .buffered,
      defer: false
    )
    window.contentView = host
    window.makeKeyAndOrderFront(nil)
    defer { window.orderOut(nil) }
    try await Task.sleep(for: .milliseconds(50))

    let location = NSPoint(x: 12, y: 12)
    let down = try #require(
      NSEvent.mouseEvent(
        with: .leftMouseDown,
        location: location,
        modifierFlags: [],
        timestamp: 0,
        windowNumber: window.windowNumber,
        context: nil,
        eventNumber: 0,
        clickCount: 1,
        pressure: 1
      )
    )
    let up = try #require(
      NSEvent.mouseEvent(
        with: .leftMouseUp,
        location: location,
        modifierFlags: [],
        timestamp: 0,
        windowNumber: window.windowNumber,
        context: nil,
        eventNumber: 0,
        clickCount: 1,
        pressure: 1
      )
    )
    window.sendEvent(down)
    window.sendEvent(up)
    try await Task.sleep(for: .milliseconds(20))

    #expect(recorder.count == 0)
  }
}

@MainActor
private final class ReturnRecorder {
  var count = 0
}

@MainActor
private final class EscapeRecorder {
  var count = 0
}

@MainActor
private final class BackdropDismissRecorder {
  var count = 0
  var query: String?
  var scopeID: String?

  func dismiss(query: String, scope: RestingSource?) {
    count += 1
    self.query = query
    scopeID = scope?.id
  }
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
  let onEscape: () -> Void
  @State private var selectedResultID: SearchHit.ID?
  @FocusState private var focus: SearchFocus?

  init(
    hit: SearchHit,
    onFocused: @escaping @MainActor @Sendable () -> Void,
    onReturn: @escaping () -> Void,
    onEscape: @escaping () -> Void
  ) {
    self.hit = hit
    self.onFocused = onFocused
    self.onReturn = onReturn
    self.onEscape = onEscape
    _selectedResultID = State(initialValue: hit.id)
  }

  var body: some View {
    SearchResultsList(
      phase: .complete,
      results: [hit],
      sourceDisplayName: { _ in "Calendar" },
      showsSourceDisplayName: true,
      failureGuidance: nil,
      committedQuery: nil,
      resultLimit: 20,
      title: { _ in "Synthetic" },
      selectedResultID: $selectedResultID,
      focus: $focus,
      onReturn: onReturn,
      onEscape: onEscape,
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

private struct MountedOpenedSearchClient: TrawlClient {
  let hit: SearchHit

  func status() async throws -> StatusResponse { fatalError() }
  func requestPhotos() async throws -> StatusResponse { fatalError() }
  func sync() async throws -> SyncResponse { fatalError() }
  func search(_: String, source _: String?) async throws -> SearchResponse {
    SearchResponse(
      order: .recency,
      sources: [],
      hits: [hit],
      failures: [],
      skippedSources: [],
      outcome: .complete,
      resultLimit: 20,
      truncated: false
    )
  }
  func open(sourceID: String, ref: String, anchorID: String) async throws -> OpenResponse {
    let presentation = PresentationDocument(
      title: "Synthetic event",
      primaryAnchorID: anchorID,
      blocks: [.prose(anchorID: anchorID, text: "Synthetic matching passage")],
      actions: [],
      facts: []
    )
    return OpenResponse(
      outcome: .complete,
      requestedRef: ref,
      requestedAnchorID: anchorID,
      record: OpenRecord(
        sourceID: sourceID,
        openRef: ref,
        typeURL: "type.googleapis.com/opentrawl.synthetic.Event",
        value: Data(),
        presentation: presentation
      ),
      failure: nil
    )
  }
}

@MainActor
private func sendMountedKey(_ characters: String, keyCode: UInt16, to window: NSWindow) throws {
  let event = try #require(
    NSEvent.keyEvent(
      with: .keyDown,
      location: .zero,
      modifierFlags: [],
      timestamp: 0,
      windowNumber: window.windowNumber,
      context: nil,
      characters: characters,
      charactersIgnoringModifiers: characters,
      isARepeat: false,
      keyCode: keyCode
    )
  )
  window.sendEvent(event)
}

@MainActor
private func constellationSize(in windowSize: CGSize) -> CGSize {
  CGSize(
    width: windowSize.width - TrawlDesign.contentInset * 2,
    height: windowSize.height - TrawlDesign.contentInset * 2
  )
}

@MainActor
private func assertHeadlineLabelFits(
  title: String,
  detail: String,
  metrics: ConstellationLayoutMetrics
) {
  let host = NSHostingView(
    rootView: SourceLabel(
      title: title,
      detail: detail,
      width: CGFloat(metrics.labelWidth),
      titleLineLimit: ConstellationLabelLayout.titleLineLimit(for: CGFloat(metrics.labelHeight)),
      isCompact: ConstellationLabelLayout.isCompact(for: CGFloat(metrics.labelHeight))
    )
  )
  let renderedSize = host.fittingSize
  #expect(abs(renderedSize.width - CGFloat(metrics.labelWidth)) <= 1)
  #expect(renderedSize.height <= CGFloat(metrics.labelHeight))
}

private struct MountedSearchDismissHarness: View {
  let client: any TrawlClient
  let model: SearchModel
  let initialScope: RestingSource
  let recorder: BackdropDismissRecorder
  @State private var query = "keep this query"
  @State private var scope: RestingSource?

  init(
    client: any TrawlClient,
    model: SearchModel,
    scope: RestingSource,
    recorder: BackdropDismissRecorder
  ) {
    self.client = client
    self.model = model
    self.initialScope = scope
    self.recorder = recorder
    _scope = State(initialValue: scope)
  }

  var body: some View {
    SearchOverlay(
      model: model,
      client: client,
      scope: $scope,
      initialQuery: query,
      onQueryChange: { query = $0 },
      onDismiss: { recorder.dismiss(query: query, scope: scope) }
    )
  }
}

private func mountedRestingSource(id: String, surface: String) throws -> RestingSource {
  var source = Trawl_Federation_V1_SourceStatus()
  source.manifest.sourceID = id
  source.manifest.displayName = surface
  source.state = "ok"
  var response = Trawl_Federation_V1_StatusResponse()
  response.outcome = .complete
  response.sources = [source]
  return SourceRestingCopy.sources(
    from: try response.model().sources,
    failures: [],
    skippedSources: []
  )[0]
}
