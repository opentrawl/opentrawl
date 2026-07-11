import SwiftUI
import TrawlClient
import TrawlCore

struct ResultPreview: View {
  let hit: SearchHit?
  let sourceDisplayName: String
  let phase: SearchOpenPhase
  let response: OpenResponse?

  var body: some View {
    Group {
      switch phase {
      case .idle:
        ContentUnavailableView {
          Label(
            hit == nil ? "Choose a result" : "Opening a result shows it here",
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
      case .output:
        if let response {
          OpenedResultView(
            hit: hit,
            sourceDisplayName: sourceDisplayName,
            result: OpenedResult(rawOutput: response.output)
          )
            .id(response.openRef)
        } else {
          ContentUnavailableView("Result unavailable", systemImage: "exclamationmark.circle")
        }
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
  let sourceDisplayName: String
  let result: OpenedResult
  @State private var showsRawOutput = false

  var body: some View {
    ScrollView {
      VStack(alignment: .leading, spacing: 16) {
        if let hit {
          HStack(alignment: .top, spacing: 11) {
            SourceIconView(sourceID: hit.sourceID, size: 32)
            VStack(alignment: .leading, spacing: 3) {
              Text(sourceDisplayName)
                .font(.callout)
                .foregroundStyle(.secondary)
              Text(hit.title.isEmpty ? "Untitled result" : hit.title)
                .font(.headline)
              Text(hit.whenDisplay)
                .font(.callout)
                .foregroundStyle(.secondary)
            }
          }
        }
        if result.rawOutput.isEmpty {
          Text("The source returned no output.")
            .foregroundStyle(.secondary)
        } else {
          if let text = result.text {
            Text(verbatim: text)
              .font(.system(size: 13))
              .lineSpacing(5)
              .textSelection(.enabled)
              .frame(maxWidth: .infinity, alignment: .leading)
          } else {
            Text("This result contains binary data. Its exact bytes are shown as hexadecimal below.")
              .font(.system(size: 13))
              .foregroundStyle(.secondary)
          }
          Divider()
          DisclosureGroup("Raw output", isExpanded: $showsRawOutput) {
            Text(verbatim: result.text ?? result.hexadecimal)
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
