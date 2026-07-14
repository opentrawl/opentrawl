import SwiftUI
import TrawlClient
import TrawlCore

struct ResultPreview: View {
  let client: any TrawlClient
  let phase: SearchOpenPhase
  let response: OpenResponse?
  var body: some View {
    Group {
      switch phase {
      case .idle:
        EmptyView()
      case .loading: ProgressView("Opening result")
      case .output:
        if let response, let record = response.record {
          PresentationDocumentView(
            client: client,
            sourceID: record.sourceID,
            document: record.presentation,
            targetAnchorID: response.requestedAnchorID
          )
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
