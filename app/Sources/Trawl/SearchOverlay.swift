import SwiftUI
import TrawlClient
import TrawlCore

struct SearchOverlay: View {
  let onDismiss: () -> Void
  @State private var query = ""
  @State private var scope: SourceStatus?
  @State private var model: SearchModel
  @State private var selectedResultID: SearchHit.ID?
  @FocusState private var focus: SearchFocus?
  init(
    client: any TrawlClient,
    initialScope: SourceStatus?,
    onDismiss: @escaping () -> Void
  ) {
    self.onDismiss = onDismiss
    _scope = State(initialValue: initialScope)
    _model = State(initialValue: SearchModel(client: client))
  }
  var body: some View {
    GeometryReader { proxy in
      let width = min(proxy.size.width, 760)
      let height = min(proxy.size.height, 560)
      VStack(spacing: 0) {
        SearchField(
          query: $query,
          scope: $scope,
          focus: $focus,
          onSubmit: openSelectedResult,
          onMoveToResults: focusResults,
          onDismiss: onDismiss
        )
        .padding(14)
        Divider()
        SearchWorkspace(
          isCompact: width < 680,
          phase: model.phase,
          results: model.results,
          selectedResultID: $selectedResultID,
          focus: $focus,
          openPhase: model.openPhase
        )
        Divider()
        SearchStatus(
          phase: model.phase,
          count: model.results.count,
          scopeName: scope?.name
        )
        .padding(.horizontal, 16)
        .frame(minHeight: 48)
      }
      .frame(width: width, height: height)
      .glassEffect(.regular, in: .rect(cornerRadius: TrawlDesign.panelCornerRadius))
      .position(x: proxy.size.width / 2, y: proxy.size.height / 2)
    }
    .onChange(of: selectedResultID) { _, resultID in
      guard let hit = model.results.first(where: { $0.id == resultID }) else { return }
      Task { await model.open(hit) }
    }
    .onKeyPress(.escape) {
      onDismiss()
      return .handled
    }
    .task(id: SearchKey(query: query, sourceID: scope?.id)) {
      selectedResultID = nil
      await model.search(query, source: scope?.id)
    }
  }
  private func focusResults() {
    guard let first = model.results.first else { return }
    if selectedResultID == nil {
      selectedResultID = first.id
    }
    focus = .results
  }
  private func openSelectedResult() {
    guard
      let hit = model.results.first(where: { $0.id == selectedResultID })
        ?? model.results.first
    else { return }
    if selectedResultID == hit.id {
      Task { await model.open(hit) }
    } else {
      selectedResultID = hit.id
    }
  }
}

private enum SearchFocus: Hashable {
  case field
  case results
}

private struct SearchField: View {
  @Binding var query: String
  @Binding var scope: SourceStatus?
  @FocusState.Binding var focus: SearchFocus?
  let onSubmit: () -> Void
  let onMoveToResults: () -> Void
  let onDismiss: () -> Void
  var body: some View {
    HStack(spacing: 9) {
      Image(systemName: "magnifyingglass")
        .foregroundStyle(.secondary)
      if let scope {
        HStack(spacing: 5) {
          SourceIconView(sourceID: scope.id, size: 18)
          Text(scope.name)
            .font(.caption.weight(.medium))
            .lineLimit(1)
          Button {
            self.scope = nil
          } label: {
            Image(systemName: "xmark")
          }
          .buttonStyle(.plain)
          .accessibilityLabel("Search every source")
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 5)
        .background(.secondary.opacity(0.1), in: Capsule())
      }
      TextField(scope == nil ? "Search everything" : "Search this source", text: $query)
        .textFieldStyle(.plain)
        .focused($focus, equals: .field)
        .defaultFocus($focus, .field, priority: .userInitiated)
        .onSubmit(onSubmit)
        .onKeyPress(.downArrow) {
          onMoveToResults()
          return .handled
        }
      Button(action: onDismiss) {
        Image(systemName: "xmark.circle.fill")
          .foregroundStyle(.secondary)
      }
      .buttonStyle(.plain)
      .accessibilityLabel("Close search")
    }
    .padding(.horizontal, 13)
    .frame(height: 44)
    .background(.secondary.opacity(0.08), in: Capsule())
  }
}

private struct SearchWorkspace: View {
  let isCompact: Bool
  let phase: SearchPhase
  let results: [SearchHit]
  @Binding var selectedResultID: SearchHit.ID?
  @FocusState.Binding var focus: SearchFocus?
  let openPhase: SearchOpenPhase
  var body: some View {
    if isCompact {
      VStack(spacing: 0) {
        SearchResults(
          phase: phase,
          results: results,
          selectedResultID: $selectedResultID,
          focus: $focus
        )
        .frame(height: 188)
        Divider()
        SearchPreview(
          hit: selectedHit,
          phase: openPhase
        )
      }
    } else {
      HStack(spacing: 0) {
        SearchResults(
          phase: phase,
          results: results,
          selectedResultID: $selectedResultID,
          focus: $focus
        )
        .frame(width: 306)
        Divider()
        SearchPreview(
          hit: selectedHit,
          phase: openPhase
        )
      }
    }
  }
  private var selectedHit: SearchHit? {
    results.first(where: { $0.id == selectedResultID })
  }
}

private struct SearchResults: View {
  let phase: SearchPhase
  let results: [SearchHit]
  @Binding var selectedResultID: SearchHit.ID?
  @FocusState.Binding var focus: SearchFocus?
  var body: some View {
    Group {
      if results.isEmpty {
        SearchStatePlaceholder(phase: phase)
      } else {
        List(results, selection: $selectedResultID) { hit in
          SearchResultRow(hit: hit)
            .tag(hit.id)
        }
        .listStyle(.plain)
        .scrollContentBackground(.hidden)
        .focused($focus, equals: .results)
        .focusEffectDisabled()
        .tint(TrawlDesign.brandRed)
        .overlay {
          RoundedRectangle(cornerRadius: 9)
            .stroke(
              focus == .results ? TrawlDesign.brandRed.opacity(0.45) : .clear,
              lineWidth: 1
            )
            .padding(5)
            .allowsHitTesting(false)
        }
      }
    }
    .frame(maxWidth: .infinity, maxHeight: .infinity)
  }
}

private struct SearchStatePlaceholder: View {
  let phase: SearchPhase
  var body: some View {
    VStack(spacing: 9) {
      if case .loading = phase {
        ProgressView()
          .controlSize(.small)
      } else {
        Image(systemName: symbol)
      }
      Text(title)
    }
    .font(.callout)
    .foregroundStyle(.secondary)
    .multilineTextAlignment(.center)
    .padding()
  }
  private var title: LocalizedStringResource {
    switch phase {
    case .idle: "Search your sources"
    case .loading: "Searching"
    case .complete: "No matches"
    case .partial: "No matches from available sources"
    case .failed: "Search failed"
    case .timedOut: "Search timed out"
    }
  }
  private var symbol: String {
    switch phase {
    case .idle, .complete, .loading: "magnifyingglass"
    case .partial: "exclamationmark.triangle"
    case .failed: "exclamationmark.circle"
    case .timedOut: "clock.badge.exclamationmark"
    }
  }
}

private struct SearchResultRow: View {
  let hit: SearchHit
  var body: some View {
    HStack(alignment: .top, spacing: 10) {
      SourceIconView(sourceID: hit.sourceID, size: 26)
      VStack(alignment: .leading, spacing: 3) {
        HStack(alignment: .firstTextBaseline, spacing: 8) {
          Text(hit.title.isEmpty ? hit.sourceID : hit.title)
            .font(.body.weight(.semibold))
            .lineLimit(1)
          Spacer(minLength: 4)
          Text(hit.whenDisplay)
            .font(.caption)
            .foregroundStyle(.tertiary)
        }
        Text(hit.snippet)
          .font(.callout)
          .foregroundStyle(.secondary)
          .lineLimit(2)
      }
    }
    .padding(.vertical, 7)
    .contentShape(.rect)
    .accessibilityElement(children: .combine)
  }
}

private struct SearchPreview: View {
  let hit: SearchHit?
  let phase: SearchOpenPhase
  var body: some View {
    Group {
      switch phase {
      case .idle:
        ContentUnavailableView {
          Label(
            hit == nil ? "Choose a result" : "Open this result",
            systemImage: "doc.text.magnifyingglass"
          )
        } description: {
          Text(hit == nil ? "Its full contents will appear here." : "Press Return to open it.")
        }
      case .loading:
        VStack(spacing: 10) {
          ProgressView()
            .controlSize(.small)
          Text("Opening result")
            .foregroundStyle(.secondary)
        }
      case .output(let output):
        OpenedResultView(hit: hit, output: output)
          .id(output)
      case .failed(let message):
        ContentUnavailableView {
          Label("Result unavailable", systemImage: "exclamationmark.circle")
        } description: {
          Text(message)
        }
      }
    }
    .frame(maxWidth: .infinity, maxHeight: .infinity)
  }
}

private struct OpenedResultView: View {
  let hit: SearchHit?
  let output: String
  @State private var showsRawOutput = false
  var body: some View {
    let document = OpenedResult(output: output)
    ScrollView {
      VStack(alignment: .leading, spacing: 16) {
        if let hit {
          HStack(alignment: .top, spacing: 11) {
            SourceIconView(sourceID: hit.sourceID, size: 32)
            VStack(alignment: .leading, spacing: 3) {
              Text(hit.title.isEmpty ? hit.sourceID : hit.title)
                .font(.headline)
              Text(hit.whenDisplay)
                .font(.callout)
                .foregroundStyle(.secondary)
            }
          }
        }
        if output.isEmpty {
          Text("The source returned no output.")
            .foregroundStyle(.secondary)
        } else {
          if !document.metadata.isEmpty {
            ResultMetadata(rows: document.metadata)
          }
          if !document.body.isEmpty {
            Text(verbatim: document.body)
              .font(.body)
              .lineSpacing(4)
              .textSelection(.enabled)
              .frame(maxWidth: .infinity, alignment: .leading)
          }
          Divider()
          DisclosureGroup("Raw output", isExpanded: $showsRawOutput) {
            Text(verbatim: output)
              .font(.system(.callout, design: .monospaced))
              .textSelection(.enabled)
              .frame(maxWidth: .infinity, alignment: .leading)
              .padding(.top, 8)
          }
          .font(.callout)
          .foregroundStyle(.secondary)
        }
      }
      .padding(18)
      .frame(maxWidth: .infinity, alignment: .leading)
    }
  }
}

private struct ResultMetadata: View {
  let rows: [OpenedResult.Metadata]
  var body: some View {
    Grid(alignment: .leadingFirstTextBaseline, horizontalSpacing: 12, verticalSpacing: 6) {
      ForEach(rows) { row in
        GridRow {
          Text(row.label)
            .foregroundStyle(.secondary)
          Text(row.value)
            .textSelection(.enabled)
        }
      }
    }
    .font(.callout)
  }
}

private struct SearchStatus: View {
  let phase: SearchPhase
  let count: Int
  let scopeName: String?
  var body: some View {
    ViewThatFits(in: .horizontal) {
      HStack(alignment: .firstTextBaseline, spacing: 12) {
        status
        Spacer(minLength: 8)
        context
      }
      VStack(alignment: .leading, spacing: 3) {
        status
        context
      }
    }
    .font(.callout)
    .foregroundStyle(.secondary)
    .frame(maxWidth: .infinity, alignment: .leading)
  }
  @ViewBuilder
  private var status: some View {
    switch phase {
    case .idle:
      Text("Ready to search.")
    case .loading:
      HStack(spacing: 7) {
        ProgressView()
          .controlSize(.small)
        Text("Searching. Stops after \(SearchModel.defaultWaitSeconds) seconds.")
      }
    case .complete where count == 0:
      Text("No matches.")
    case .complete:
      Text("\(count) results.")
    case .partial where count == 0:
      Label(
        "Some sources failed; the others returned no matches.",
        systemImage: "exclamationmark.triangle"
      )
    case .partial:
      Label(
        "Some sources failed. Showing \(count) useful results.",
        systemImage: "exclamationmark.triangle"
      )
    case .failed(let message):
      Label(message, systemImage: "exclamationmark.circle")
    case .timedOut:
      Label(
        "Search stopped after \(SearchModel.defaultWaitSeconds) seconds.",
        systemImage: "clock.badge.exclamationmark"
      )
    }
  }
  private var context: some View {
    HStack(spacing: 10) {
      Label(scopeName ?? "All sources", systemImage: scopeName == nil ? "square.grid.2x2" : "scope")
        .lineLimit(1)
      Text("Up to \(SearchResponse.maximumResults)")
        .fixedSize()
    }
  }
}

private struct OpenedResult {
  struct Metadata: Identifiable {
    let id: Int
    let label: String
    let value: String
  }
  let metadata: [Metadata]
  let body: String
  init(output: String) {
    let lines = output.split(separator: "\n", omittingEmptySubsequences: false).map(String.init)
    guard let divider = lines.firstIndex(where: { $0.isEmpty }), divider > 0 else {
      metadata = []
      body = output
      return
    }
    let parsed = lines[..<divider].enumerated().compactMap { index, line -> Metadata? in
      guard let colon = line.firstIndex(of: ":") else { return nil }
      let label = line[..<colon].trimmingCharacters(in: .whitespaces)
      let value = line[line.index(after: colon)...].trimmingCharacters(in: .whitespaces)
      guard !label.isEmpty, !value.isEmpty else { return nil }
      return Metadata(id: index, label: label, value: value)
    }
    guard parsed.count == divider else {
      metadata = []
      body = output
      return
    }
    metadata = parsed
    body = lines.dropFirst(divider + 1).joined(separator: "\n")
      .trimmingCharacters(in: .newlines)
  }
}

private struct SearchKey: Hashable {
  let query: String
  let sourceID: String?
}
