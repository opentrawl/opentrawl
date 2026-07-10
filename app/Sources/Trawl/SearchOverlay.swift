import SwiftUI
import TrawlClient
import TrawlCore

struct SearchOverlay: View {
  let onDismiss: () -> Void

  @State private var query = ""
  @State private var scope: SourceStatus?
  @State private var model: SearchModel
  @FocusState private var focused: Bool

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
    VStack(spacing: 12) {
      searchField
      SearchResults(phase: model.phase, results: model.results) { hit in
        Task { await model.open(hit) }
      }
      SearchOutput(phase: model.openPhase)
    }
    .frame(maxWidth: 520)
    .padding(16)
    .glassEffect(.regular, in: .rect(cornerRadius: TrawlDesign.panelCornerRadius))
    .onAppear { focused = true }
    .onKeyPress(.escape) {
      onDismiss()
      return .handled
    }
    .task(id: SearchKey(query: query, sourceID: scope?.id)) {
      await model.search(query, source: scope?.id)
    }
  }

  private var searchField: some View {
    HStack(spacing: 9) {
      Image(systemName: "magnifyingglass")
        .foregroundStyle(.secondary)
      if let scope {
        HStack(spacing: 5) {
          SourceIconView(sourceID: scope.id, size: 18)
          Text(scope.name)
            .font(.caption.weight(.medium))
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
        .background(.secondary.opacity(0.12), in: Capsule())
      }
      TextField(scope == nil ? "Search everything" : "Search this source", text: $query)
        .textFieldStyle(.plain)
        .focused($focused)
        .defaultFocus($focused, true, priority: .userInitiated)
        .onSubmit {
          if let first = model.results.first {
            Task { await model.open(first) }
          }
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

private struct SearchResults: View {
  let phase: SearchPhase
  let results: [SearchHit]
  let onSelect: (SearchHit) -> Void

  var body: some View {
    VStack(spacing: 8) {
      if !results.isEmpty {
        ScrollView {
          LazyVStack(spacing: 0) {
            ForEach(results) { hit in
              Button {
                onSelect(hit)
              } label: {
                SearchResultRow(hit: hit)
              }
              .buttonStyle(.plain)
            }
          }
        }
        .frame(maxHeight: 300)
        .background(.background.opacity(0.35), in: .rect(cornerRadius: 14))
      }
      SearchStatus(phase: phase, count: results.count)
    }
  }
}

private struct SearchResultRow: View {
  let hit: SearchHit

  var body: some View {
    HStack(alignment: .top, spacing: 11) {
      SourceIconView(sourceID: hit.sourceID, size: 28)
      VStack(alignment: .leading, spacing: 3) {
        Text(hit.title.isEmpty ? hit.sourceID : hit.title)
          .font(.body.weight(.semibold))
          .lineLimit(1)
        Text(hit.snippet)
          .font(.callout)
          .foregroundStyle(.secondary)
          .lineLimit(2)
      }
      Spacer(minLength: 8)
      Text(hit.whenDisplay)
        .font(.caption)
        .foregroundStyle(.tertiary)
    }
    .padding(.horizontal, 13)
    .padding(.vertical, 10)
    .contentShape(.rect)
  }
}

private struct SearchStatus: View {
  let phase: SearchPhase
  let count: Int

  var body: some View {
    HStack(alignment: .firstTextBaseline, spacing: 7) {
      switch phase {
      case .idle:
        Text("Search across your local sources.")
      case .loading:
        ProgressView()
          .controlSize(.small)
        Text("Searching. Stops after \(SearchModel.defaultWaitSeconds) seconds.")
      case .complete where count == 0:
        Text("No matches.")
      case .complete:
        Text("\(count) results.")
      case .partial where count == 0:
        Label(
          "Some sources failed; the others returned no matches.",
          systemImage: "exclamationmark.triangle")
      case .partial:
        Label(
          "Some sources failed. Showing \(count) useful results.",
          systemImage: "exclamationmark.triangle")
      case .failed(let message):
        Label(message, systemImage: "exclamationmark.circle")
      case .timedOut:
        Label(
          "Search stopped after \(SearchModel.defaultWaitSeconds) seconds.",
          systemImage: "clock.badge.exclamationmark")
      }
      Spacer(minLength: 8)
      Text("Up to \(SearchResponse.maximumResults)")
    }
    .font(.callout)
    .foregroundStyle(.secondary)
    .frame(maxWidth: .infinity, alignment: .leading)
    .padding(.horizontal, 4)
  }
}

private struct SearchOutput: View {
  let phase: SearchOpenPhase

  @ViewBuilder
  var body: some View {
    switch phase {
    case .idle:
      EmptyView()
    case .loading:
      HStack(spacing: 8) {
        ProgressView()
          .controlSize(.small)
        Text("Opening result")
      }
      .outputPanel()
    case .output(let output):
      VStack(alignment: .leading, spacing: 8) {
        Text("Result")
          .font(.callout.weight(.semibold))
          .foregroundStyle(.secondary)
        ScrollView {
          if output.isEmpty {
            Text("The source returned no output.")
              .foregroundStyle(.secondary)
          } else {
            Text(verbatim: output)
              .font(.system(.body, design: .monospaced))
              .textSelection(.enabled)
              .frame(maxWidth: .infinity, alignment: .leading)
          }
        }
        .frame(maxHeight: 220)
      }
      .outputPanel()
    case .failed(let message):
      Label(message, systemImage: "exclamationmark.circle")
        .outputPanel()
    }
  }
}

extension View {
  fileprivate func outputPanel() -> some View {
    padding(13)
      .frame(maxWidth: .infinity, alignment: .leading)
      .background(.background.opacity(0.35), in: .rect(cornerRadius: 14))
  }
}

private struct SearchKey: Hashable {
  let query: String
  let sourceID: String?
}
