import SwiftUI
import TrawlClient
import TrawlCore

struct ResultPreview: View {
  let phase: SearchOpenPhase
  let response: OpenResponse?
  var body: some View {
    Group {
      switch phase {
      case .idle:
        ContentUnavailableView(
          "Choose a result", systemImage: "doc.text.magnifyingglass",
          description: Text("Opening a result shows it here."))
      case .loading: ProgressView("Opening result")
      case .output:
        if let response, let record = response.record {
          PresentationDocumentView(
            document: record.presentation, targetAnchorID: response.requestedAnchorID)
        } else {
          ContentUnavailableView("Result unavailable", systemImage: "exclamationmark.circle")
        }
      case .failed(let message), .timedOut(let message):
        ContentUnavailableView(
          "Result unavailable", systemImage: "exclamationmark.circle", description: Text(message))
      }
    }.frame(maxWidth: .infinity, maxHeight: .infinity)
  }
}
