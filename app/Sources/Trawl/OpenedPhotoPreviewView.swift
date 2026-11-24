import AppKit
import SwiftUI
import TrawlClient

/// Renders the real picture for an opened Photos record, either from the local
/// picture cache or over authenticated HTTP (`GET /app/photo?link=...`) when
/// viewing a remote archive, with zoom and Preview handoff and text-only fallback.
struct OpenedPhotoPreviewView: View {
  let trawlLink: GloballyRoutableTrawlLink
  let localCachedPictureFilePath: String?
  let isRemoteArchive: Bool
  let loadPhotoImageData: ((GloballyRoutableTrawlLink) async throws -> Data?)?

  @State private var loadState: PhotoLoadState = .loading
  @State private var isShowingZoomSheet = false

  private struct PhotoLoadIdentity: Hashable {
    let trawlLink: GloballyRoutableTrawlLink
    let localCachedPictureFilePath: String?
    let isRemoteArchive: Bool
  }

  private enum PhotoLoadState {
    case loading
    case loaded(NSImage, Data)
    case unavailable
  }

  static func localCachedPictureFilePath(
    from fields: [TrawlerSpecificCommandDetailPresentationField]
  ) -> String? {
    for field in fields where field.fieldDisplayName == "Picture" {
      if case .text(let value) = field.fieldValue {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.hasPrefix("/") {
          return trimmed
        }
      }
    }
    return nil
  }

  static func shouldHideRawPicturePathField(
    _ field: TrawlerSpecificCommandDetailPresentationField,
    isPhotoRecord: Bool
  ) -> Bool {
    guard isPhotoRecord, field.fieldDisplayName == "Picture" else { return false }
    if case .text(let value) = field.fieldValue {
      return value.trimmingCharacters(in: .whitespacesAndNewlines).hasPrefix("/")
    }
    return false
  }

  var body: some View {
    Group {
      switch loadState {
      case .loading:
        HStack(spacing: TrawlDesign.Space.sm) {
          ProgressView()
            .controlSize(.small)
          Text("Loading photo…")
            .font(TrawlDesign.Typography.meta)
            .foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity)
        .frame(height: 180)
        .background(
          TrawlDesign.Colors.surface.opacity(0.6),
          in: TrawlDesign.Shape.surface
        )
        .overlay {
          TrawlDesign.Shape.surface
            .stroke(TrawlDesign.Colors.hairline, lineWidth: 1)
        }

      case .loaded(let image, let jpegData):
        VStack(alignment: .leading, spacing: TrawlDesign.Space.sm) {
          Button {
            isShowingZoomSheet = true
          } label: {
            Image(nsImage: image)
              .resizable()
              .aspectRatio(contentMode: .fit)
              .frame(maxWidth: .infinity, maxHeight: 460)
              .background(TrawlDesign.Colors.surface.opacity(0.45))
              .clipShape(TrawlDesign.Shape.surface)
              .overlay {
                TrawlDesign.Shape.surface
                  .stroke(TrawlDesign.Colors.hairline, lineWidth: 1)
              }
          }
          .buttonStyle(.plain)
          .help("Click to zoom")
          .accessibilityLabel("Opened photo. Click to zoom.")

          HStack(spacing: TrawlDesign.Space.sm) {
            Button {
              isShowingZoomSheet = true
            } label: {
              Label("Zoom", systemImage: "plus.magnifyingglass")
            }
            .buttonStyle(.bordered)
            .controlSize(.small)

            Button {
              openInPreview(jpegData: jpegData)
            } label: {
              Label("Open in Preview", systemImage: "arrow.up.forward.app")
            }
            .buttonStyle(.bordered)
            .controlSize(.small)

            Spacer()
          }
        }
        .sheet(isPresented: $isShowingZoomSheet) {
          OpenedPhotoZoomSheet(
            image: image,
            onOpenInPreview: { openInPreview(jpegData: jpegData) },
            onClose: { isShowingZoomSheet = false }
          )
        }

      case .unavailable:
        EmptyView()
      }
    }
    .task(
      id: PhotoLoadIdentity(
        trawlLink: trawlLink,
        localCachedPictureFilePath: localCachedPictureFilePath,
        isRemoteArchive: isRemoteArchive
      )
    ) {
      await loadPicture()
    }
  }

  @MainActor
  private func loadPicture() async {
    if !isRemoteArchive {
      guard let localPath = localCachedPictureFilePath else {
        loadState = .unavailable
        return
      }
      loadState = .loading
      let fileURL = URL(fileURLWithPath: localPath)
      let loadedData: Data? = await Task.detached(priority: .userInitiated) {
        try? Data(contentsOf: fileURL)
      }.value
      guard !Task.isCancelled else { return }
      if let data = loadedData, let image = NSImage(data: data) {
        loadState = .loaded(image, data)
      } else {
        loadState = .unavailable
      }
      return
    }

    guard let loadPhotoImageData else {
      loadState = .unavailable
      return
    }
    loadState = .loading
    do {
      let data = try await loadPhotoImageData(trawlLink)
      guard !Task.isCancelled else { return }
      if let data, let image = NSImage(data: data) {
        loadState = .loaded(image, data)
      } else {
        loadState = .unavailable
      }
    } catch {
      guard !Task.isCancelled else { return }
      loadState = .unavailable
    }
  }

  private func openInPreview(jpegData: Data) {
    if !isRemoteArchive,
      let localPath = localCachedPictureFilePath,
      FileManager.default.fileExists(atPath: localPath)
    {
      NSWorkspace.shared.open(URL(fileURLWithPath: localPath))
      return
    }
    let sanitizedName = trawlLink.globallyRoutableTrawlLink
      .replacingOccurrences(of: "[^A-Za-z0-9._-]", with: "_", options: .regularExpression)
    let temporaryURL = FileManager.default.temporaryDirectory
      .appending(path: "opentrawl-photo-\(sanitizedName).jpg", directoryHint: .notDirectory)
    do {
      try jpegData.write(to: temporaryURL, options: [.atomic])
      NSWorkspace.shared.open(temporaryURL)
    } catch {
      return
    }
  }
}

private struct OpenedPhotoZoomSheet: View {
  let image: NSImage
  let onOpenInPreview: () -> Void
  let onClose: () -> Void

  @State private var zoomMode: ZoomMode = .fit

  private enum ZoomMode: String, CaseIterable, Identifiable {
    case fit = "Fit"
    case actual = "100%"
    case double = "200%"

    var id: String { rawValue }
  }

  var body: some View {
    VStack(spacing: 0) {
      HStack(spacing: TrawlDesign.Space.md) {
        Picker("Zoom", selection: $zoomMode) {
          ForEach(ZoomMode.allCases) { mode in
            Text(mode.rawValue).tag(mode)
          }
        }
        .pickerStyle(.segmented)
        .labelsHidden()
        .frame(width: 190)

        Spacer()

        Button {
          onOpenInPreview()
        } label: {
          Label("Open in Preview", systemImage: "arrow.up.forward.app")
        }
        .buttonStyle(.bordered)
        .controlSize(.small)

        Button("Done", action: onClose)
          .buttonStyle(.borderedProminent)
          .controlSize(.small)
          .keyboardShortcut(.cancelAction)
      }
      .padding(.horizontal, 16)
      .padding(.vertical, 12)

      Divider()

      Group {
        switch zoomMode {
        case .fit:
          Image(nsImage: image)
            .resizable()
            .aspectRatio(contentMode: .fit)
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .padding(16)
        case .actual:
          ScrollView([.horizontal, .vertical]) {
            Image(nsImage: image)
              .padding(16)
          }
        case .double:
          ScrollView([.horizontal, .vertical]) {
            Image(nsImage: image)
              .resizable()
              .frame(
                width: max(1, image.size.width * 2),
                height: max(1, image.size.height * 2)
              )
              .padding(16)
          }
        }
      }
      .frame(maxWidth: .infinity, maxHeight: .infinity)
      .background(TrawlDesign.Colors.surface.opacity(0.4))
    }
    .frame(minWidth: 720, idealWidth: 960, minHeight: 540, idealHeight: 720)
  }
}
