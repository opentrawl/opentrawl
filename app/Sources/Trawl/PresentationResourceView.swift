import AppKit
import Observation
import SwiftUI
import TrawlClient

enum PresentationResourceLoadPhase {
  case loading
  case loaded(NSImage)
  case failed
}

@MainActor
@Observable
final class PresentationResourceLoader {
  private let client: any TrawlClient

  private(set) var phase: PresentationResourceLoadPhase = .loading
  private(set) var requestedResourceRef: String?
  private var activeRequestID: UUID?

  init(client: any TrawlClient) {
    self.client = client
  }

  func load(sourceID: String, resource: PresentationResource) async {
    let requestID = UUID()
    activeRequestID = requestID
    requestedResourceRef = resource.ref
    phase = .loading
    do {
      let response = try await client.resource(
        sourceID: sourceID,
        ref: resource.ref,
        maxBytes: ProcessTrawlClient.maximumResourceBytes
      )
      try Task.checkCancellation()
      guard activeRequestID == requestID else { return }
      guard response.ref == resource.ref,
        response.contentType.lowercased().hasPrefix("image/"),
        let image = NSImage(data: response.data)
      else {
        phase = .failed
        return
      }
      phase = .loaded(image)
    } catch is CancellationError {
      return
    } catch TrawlClientError.cancelled {
      return
    } catch {
      guard activeRequestID == requestID else { return }
      phase = .failed
    }
  }

  func visiblePhase(for resourceRef: String) -> PresentationResourceLoadPhase {
    guard requestedResourceRef == nil || requestedResourceRef == resourceRef else {
      return .loading
    }
    return phase
  }
}

struct PresentationResourceView: View {
  let sourceID: String
  let resource: PresentationResource
  let blockIndex: Int

  @State private var loader: PresentationResourceLoader

  init(
    client: any TrawlClient,
    sourceID: String,
    resource: PresentationResource,
    blockIndex: Int
  ) {
    self.sourceID = sourceID
    self.resource = resource
    self.blockIndex = blockIndex
    _loader = State(initialValue: PresentationResourceLoader(client: client))
  }

  var body: some View {
    VStack(alignment: .leading, spacing: 8) {
      Label(resource.label, systemImage: symbol)
        .font(.headline)
      if resource.kind == .image {
        imageContent
      } else {
        Text(kindLabel)
          .font(.callout)
          .foregroundStyle(.secondary)
      }
      metadata
    }
    .padding(10)
    .frame(maxWidth: .infinity, alignment: .leading)
    .background(.secondary.opacity(0.05), in: .rect(cornerRadius: 8))
    .id(
      PresentationElementID.sourceAnchor(resource.anchorID, fallback: .resource(blockIndex))
    )
    .task(id: resource.ref) {
      guard resource.kind == .image else { return }
      await loader.load(sourceID: sourceID, resource: resource)
    }
  }

  @ViewBuilder
  private var imageContent: some View {
    switch loader.visiblePhase(for: resource.ref) {
    case .loading:
      ProgressView("Loading image")
        .frame(maxWidth: .infinity, minHeight: 180)
    case .loaded(let image):
      Image(nsImage: image)
        .resizable()
        .scaledToFit()
        .frame(maxWidth: .infinity, maxHeight: 420)
        .accessibilityLabel("Image preview: \(resource.label)")
    case .failed:
      VStack(spacing: 10) {
        ContentUnavailableView(
          "Image unavailable",
          systemImage: "photo.badge.exclamationmark",
          description: Text("OpenTrawl could not load this image.")
        )
        Button("Try again") {
          Task { await loader.load(sourceID: sourceID, resource: resource) }
        }
      }
      .frame(maxWidth: .infinity, minHeight: 180)
    }
  }

  private var metadata: some View {
    ForEach(Array(resource.metadata.enumerated()), id: \.offset) { fieldIndex, field in
      LabeledContent(field.label, value: field.display)
        .font(.caption)
        .id(
          PresentationElementID.sourceAnchor(
            field.anchorID,
            fallback: .resourceField(block: blockIndex, field: fieldIndex)
          )
        )
    }
  }

  private var symbol: String {
    switch resource.kind {
    case .file: "doc"
    case .image: "photo"
    case .video: "video"
    case .audio: "waveform"
    }
  }

  private var kindLabel: String {
    switch resource.kind {
    case .file: "File"
    case .image: "Image"
    case .video: "Video"
    case .audio: "Audio"
    }
  }
}
