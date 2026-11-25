import AppKit
import SwiftUI
import TrawlClient

/// Renders the real picture for an opened Photos record, either from the local
/// picture cache or over authenticated HTTP (`GET /app/photo?link=...`) when
/// viewing a remote archive, with zoom and Preview handoff and an honest
/// inline failure banner when the photo cannot be loaded.
struct OpenedPhotoPreviewView: View {
  let trawlLink: GloballyRoutableTrawlLink
  let localCachedPictureFilePath: String?
  var pictureStatusFieldText: String?
  let isRemoteArchive: Bool
  let loadPhotoImageData: ((GloballyRoutableTrawlLink) async throws -> Data?)?
  var onRetryLocalOpen: (() -> Void)?

  @State private var loadState: PhotoLoadState = .loading
  @State private var isShowingZoomSheet = false
  @State private var retryAttempt: UInt64 = 0

  private static let failedFetchPrefix = "Fetching the picture failed:"
  private static let remoteEndpointPlaceholder = "Available via remote photo endpoint."
  private static let notFetchedYetText = "Not fetched yet."
  private static let evictedFromCacheText = "Fetched; no longer in the picture cache."

  private struct PhotoLoadIdentity: Hashable {
    let trawlLink: GloballyRoutableTrawlLink
    let localCachedPictureFilePath: String?
    let pictureStatusFieldText: String?
    let isRemoteArchive: Bool
    let retryAttempt: UInt64
  }

  private enum PhotoLoadState {
    case loading
    case loaded(NSImage, Data)
    case unavailable(reason: String)
  }

  static func localCachedPictureFilePath(
    from fields: [TrawlerSpecificCommandDetailPresentationField]
  ) -> String? {
    guard let text = pictureFieldText(from: fields), text.hasPrefix("/") else {
      return nil
    }
    return text
  }

  static func pictureFieldText(
    from fields: [TrawlerSpecificCommandDetailPresentationField]
  ) -> String? {
    for field in fields where field.fieldDisplayName == "Picture" {
      if case .text(let value) = field.fieldValue {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        if !trimmed.isEmpty {
          return trimmed
        }
      }
    }
    return nil
  }

  static func unavailableReason(
    fromPictureFieldText rawText: String?,
    isRemoteArchive: Bool = false
  ) -> String? {
    guard let trimmed = rawText?.trimmingCharacters(in: .whitespacesAndNewlines), !trimmed.isEmpty
    else {
      return nil
    }
    if trimmed.hasPrefix("/") || trimmed == remoteEndpointPlaceholder {
      return nil
    }
    if isRemoteArchive, trimmed == notFetchedYetText || trimmed == evictedFromCacheText {
      return nil
    }
    if trimmed.hasPrefix(failedFetchPrefix) {
      let detail = trimmed.dropFirst(failedFetchPrefix.count).trimmingCharacters(
        in: .whitespacesAndNewlines)
      return detail.isEmpty ? "Fetching the picture failed." : detail
    }
    return trimmed
  }

  static func shouldHideRawPicturePathField(
    _ field: TrawlerSpecificCommandDetailPresentationField,
    isPhotoRecord: Bool,
    isRemoteArchive: Bool = false
  ) -> Bool {
    guard isPhotoRecord, field.fieldDisplayName == "Picture" else { return false }
    if isRemoteArchive {
      return true
    }
    if case .text(let value) = field.fieldValue {
      let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
      return trimmed.hasPrefix("/") || trimmed == remoteEndpointPlaceholder
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

      case .unavailable(let reason):
        HStack(alignment: .center, spacing: TrawlDesign.Space.md) {
          Image(systemName: "photo.badge.exclamationmark")
            .font(TrawlDesign.Typography.symbolEmphasis)
            .foregroundStyle(TrawlDesign.Colors.warningAmberText)
            .accessibilityHidden(true)

          VStack(alignment: .leading, spacing: 2) {
            Text("Photo could not be loaded")
              .font(TrawlDesign.Typography.bodyEmphasis)
              .foregroundStyle(TrawlDesign.Colors.ink)
            Text(reason)
              .font(TrawlDesign.Typography.meta)
              .foregroundStyle(.secondary)
              .fixedSize(horizontal: false, vertical: true)
              .textSelection(.enabled)
          }

          Spacer(minLength: TrawlDesign.Space.sm)

          Button("Retry") {
            retryLoadPicture()
          }
          .buttonStyle(.bordered)
          .controlSize(.small)
          .fixedSize()
        }
        .padding(.horizontal, TrawlDesign.Space.md)
        .padding(.vertical, 12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(
          TrawlDesign.Colors.surface.opacity(0.6),
          in: TrawlDesign.Shape.surface
        )
        .overlay {
          TrawlDesign.Shape.surface
            .stroke(TrawlDesign.Colors.hairline, lineWidth: 1)
        }
      }
    }
    .task(
      id: PhotoLoadIdentity(
        trawlLink: trawlLink,
        localCachedPictureFilePath: localCachedPictureFilePath,
        pictureStatusFieldText: pictureStatusFieldText,
        isRemoteArchive: isRemoteArchive,
        retryAttempt: retryAttempt
      )
    ) {
      await loadPicture()
    }
  }

  private func retryLoadPicture() {
    if !isRemoteArchive, let onRetryLocalOpen {
      loadState = .loading
      onRetryLocalOpen()
      return
    }
    retryAttempt &+= 1
  }

  @MainActor
  private func loadPicture() async {
    if !isRemoteArchive {
      guard let localPath = localCachedPictureFilePath else {
        let reason =
          Self.unavailableReason(fromPictureFieldText: pictureStatusFieldText)
          ?? "The photo is not available in the local picture cache."
        loadState = .unavailable(reason: reason)
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
        loadState = .unavailable(reason: "The cached photo file could not be opened.")
      }
      return
    }

    if retryAttempt == 0,
      let alreadyFailedReason = Self.unavailableReason(
        fromPictureFieldText: pictureStatusFieldText,
        isRemoteArchive: true
      )
    {
      loadState = .unavailable(reason: alreadyFailedReason)
      return
    }

    guard let loadPhotoImageData else {
      loadState = .unavailable(reason: "This connection cannot fetch remote photo images.")
      return
    }
    loadState = .loading
    do {
      let data = try await loadPhotoImageData(trawlLink)
      guard !Task.isCancelled else { return }
      if let data, let image = NSImage(data: data) {
        loadState = .loaded(image, data)
      } else {
        loadState = .unavailable(reason: "The remote archive returned an unreadable photo.")
      }
    } catch is CancellationError {
      return
    } catch let error as TrawlClientError where error == .cancelled {
      return
    } catch {
      guard !Task.isCancelled else { return }
      let detail = error.localizedDescription.trimmingCharacters(in: .whitespacesAndNewlines)
      loadState = .unavailable(
        reason: detail.isEmpty ? "The remote archive could not deliver this photo." : detail
      )
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
