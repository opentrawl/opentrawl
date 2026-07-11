import SwiftUI
import TrawlClient
import TrawlCore

struct RootView: View {
  @Bindable var model: AppModel

  let client: any TrawlClient
  let onRequestDiskAccess: () -> Void

  @State private var iconStore = SourceIconStore()
  @State private var searchScope: SourceStatus?
  @State private var isSearching = false
  @State private var constellationActivity: ConstellationActivity = .idle
  @State private var constellationTrafficEvent: ConstellationTrafficEvent?
  @State private var trafficClearTask: Task<Void, Never>?

  var body: some View {
    ZStack {
      CanvasBackground()
      home
        .opacity(isSearching ? TrawlDesign.backgroundContentOpacity : 1)
        .blur(radius: isSearching ? TrawlDesign.backgroundContentBlur : 0)
        .disabled(isSearching)
        .allowsHitTesting(!isSearching)
        .accessibilityHidden(isSearching)

      if isSearching {
        Color.white.opacity(TrawlDesign.modalVeilOpacity)
          .ignoresSafeArea()
          .contentShape(.rect)
          .onTapGesture(perform: dismissSearch)
          .accessibilityHidden(true)
        SearchOverlay(
          client: client,
          initialScope: searchScope,
          sourceStatuses: model.sources,
          onTrafficChange: presentTraffic,
          onDismiss: dismissSearch
        )
        .padding(TrawlDesign.contentInset)
        .accessibilityElement(children: .contain)
        .accessibilityAddTraits(.isModal)
        .transition(.opacity.combined(with: .scale(scale: 0.98)))
      }
    }
    .environment(iconStore)
    .animation(.easeOut(duration: 0.16), value: isSearching)
    .toolbar {
      ToolbarItem {
        if !isSearching {
          Button {
            Task { await syncNow() }
          } label: {
            if model.isSyncing {
              ProgressView()
                .controlSize(.small)
            } else {
              Label("Sync now", systemImage: "arrow.triangle.2.circlepath")
            }
          }
          .disabled(model.isSyncing)
          .help("Sync now")
        }
      }
    }
  }

  @ViewBuilder
  private var home: some View {
    switch model.phase {
    case .loading where model.sources.isEmpty:
      ProgressView("Loading sources")
        .controlSize(.large)
    case .failed(let message) where model.sources.isEmpty:
      FailureView(message: message) {
        Task { await model.refresh() }
      }
    case .loading, .ready, .failed:
      ZStack(alignment: .top) {
        ConstellationView(
          sources: model.sources,
          activity: constellationActivity,
          trafficEvent: constellationTrafficEvent,
          onSelectEverything: { showSearch(scope: nil) },
          onSelectSource: { showSearch(scope: $0) }
        )
        .padding(TrawlDesign.contentInset)
        VStack(spacing: 8) {
          if let message = statusMessage {
            StatusBanner(message: message)
          }
          if model.diskAccess == .denied {
            PermissionBanner(action: onRequestDiskAccess)
          }
        }
        .padding(TrawlDesign.contentInset)
      }
    }
  }

  private var statusMessage: String? {
    if let syncMessage = model.syncMessage {
      return syncMessage
    }
    switch model.phase {
    case .failed(let message):
      return message
    case .loading, .ready:
      break
    }
    switch model.completion {
    case .complete: return nil
    case .partial: return "Some source status checks failed."
    case .failed: return "No source status check succeeded."
    }
  }

  private func showSearch(scope: SourceStatus?) {
    searchScope = scope
    isSearching = true
  }

  private func dismissSearch() {
    presentTraffic(.idle, nil)
    isSearching = false
  }

  private func syncNow() async {
    let requestedSourceIDs = Set(model.sources.map(\.id))
    presentTraffic(.syncing(sourceIDs: requestedSourceIDs), nil)
    await model.syncNow()
    let failedSourceIDs = Set(model.syncFailures.map(\.sourceID))
    let usefulSourceIDs = Set(
      model.syncResults.lazy
        .filter { $0.outcome != .failed }
        .map(\.sourceID)
    )
    presentTraffic(
      failedSourceIDs.isEmpty ? .idle : .failed(sourceIDs: failedSourceIDs),
      ConstellationTrafficEvent(
        requestedSourceIDs: requestedSourceIDs,
        usefulSourceIDs: usefulSourceIDs,
        failedSourceIDs: failedSourceIDs
      )
    )
  }

  private func presentTraffic(
    activity: ConstellationActivity,
    event: ConstellationTrafficEvent?
  ) {
    trafficClearTask?.cancel()
    constellationActivity = activity
    constellationTrafficEvent = event
    guard event != nil else { return }
    trafficClearTask = Task { @MainActor in
      try? await Task.sleep(for: .seconds(4))
      guard !Task.isCancelled else { return }
      constellationActivity = .idle
      constellationTrafficEvent = nil
    }
  }
}

private struct CanvasBackground: View {
  var body: some View {
    Color.white
      .ignoresSafeArea()
  }
}

private struct FailureView: View {
  let message: String
  let retry: () -> Void

  var body: some View {
    ContentUnavailableView {
      Label("Sources unavailable", systemImage: "exclamationmark.triangle")
    } description: {
      Text(message)
    } actions: {
      Button("Try again", action: retry)
    }
  }
}

private struct StatusBanner: View {
  let message: String

  var body: some View {
    Label(message, systemImage: "exclamationmark.triangle.fill")
      .font(.callout)
      .foregroundStyle(.secondary)
      .padding(.horizontal, 14)
      .padding(.vertical, 9)
      .glassEffect(.regular, in: Capsule())
  }
}

private struct PermissionBanner: View {
  let action: () -> Void

  var body: some View {
    HStack(spacing: 12) {
      Label("OpenTrawl needs Full Disk Access to read local sources.", systemImage: "lock.fill")
        .font(.callout)
      Button("Give access", action: action)
    }
    .padding(.horizontal, 14)
    .padding(.vertical, 9)
    .glassEffect(.regular, in: Capsule())
  }
}
