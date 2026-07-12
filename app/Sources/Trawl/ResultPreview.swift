import SwiftUI
import TrawlClient
import TrawlCore

struct ResultPreview: View {
  let phase: SearchOpenPhase
  let response: OpenResponse?
  var body: some View {
    Group {
      switch phase {
      case .idle: ContentUnavailableView("Choose a result", systemImage: "doc.text.magnifyingglass", description: Text("Opening a result shows it here."))
      case .loading: ProgressView("Opening result")
      case .output: if let record = response?.record { PresentationDocumentView(sourceID: record.sourceID, openRef: record.openRef, document: record.presentation) } else { ContentUnavailableView("Result unavailable", systemImage: "exclamationmark.circle") }
      case .failed(let message), .timedOut(let message): ContentUnavailableView("Result unavailable", systemImage: "exclamationmark.circle", description: Text(message))
      }
    }.frame(maxWidth: .infinity, maxHeight: .infinity)
  }
}
