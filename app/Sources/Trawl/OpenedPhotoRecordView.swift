import AppKit
import SwiftUI
import TrawlClient

/// Full-bleed native macOS photo detail view with a single unified top action bar,
/// responsive canvas stage (side-by-side collapsible inspector on wide panes,
/// vertical photo-first stack on narrower panes), and generous zoom sheet.
struct OpenedPhotoRecordView: View {
  let openedRecord: TrawlerSpecificOpenedRecordPresentation
  let trawlLink: GloballyRoutableTrawlLink
  let isRemoteArchive: Bool
  let loadPhotoImageData: ((GloballyRoutableTrawlLink) async throws -> Data?)?
  var onRetryLocalOpen: (() -> Void)?
  @Binding var isInspectorVisible: Bool

  @State private var loadState: PhotoLoadState = .loading
  @State private var isShowingZoomSheet = false
  @State private var retryAttempt: UInt64 = 0

  private struct PhotoLoadIdentity: Hashable {
    let trawlLink: GloballyRoutableTrawlLink
    let localCachedPictureFilePath: String?
    let pictureStatusFieldText: String?
    let isRemoteArchive: Bool
    let retryAttempt: UInt64
  }

  fileprivate enum PhotoLoadState {
    case loading
    case loaded(NSImage, Data)
    case unavailable(reason: String)

    var loadedPhoto: (image: NSImage, jpegData: Data)? {
      if case .loaded(let image, let data) = self {
        return (image, data)
      }
      return nil
    }
  }

  var body: some View {
    let presentation = OpenedPhotoPresentation(
      detailPresentation: openedRecord.detailPresentation
    )
    let loadedPhoto = loadState.loadedPhoto

    GeometryReader { proxy in
      let usesSideBySide = OpenedPhotoLayout.usesSideBySideInspector(
        forPaneWidth: proxy.size.width
      )
      let contentHeight = max(
        0,
        proxy.size.height - OpenedPhotoLayout.topActionBarEstimatedHeight
      )

      VStack(spacing: 0) {
        OpenedPhotoTopActionBar(
          headerPrimaryText: presentation.headerPrimaryText,
          headerDateText: presentation.headerDateText,
          isFavourite: presentation.isFavourite,
          trawlLink: trawlLink,
          isPhotoLoaded: loadedPhoto != nil,
          showsInspectorToggle: usesSideBySide,
          isInspectorVisible: isInspectorVisible,
          onZoom: { isShowingZoomSheet = true },
          onOpenInPreview: {
            if let jpegData = loadedPhoto?.jpegData {
              openInPreview(
                jpegData: jpegData,
                localCachedPictureFilePath: presentation.localCachedPictureFilePath
              )
            }
          },
          onToggleInspector: {
            withAnimation(.easeInOut(duration: 0.18)) {
              isInspectorVisible.toggle()
            }
          }
        )

        Divider()

        if usesSideBySide {
          HStack(spacing: 0) {
            OpenedPhotoStageView(
              loadState: loadState,
              onZoom: { isShowingZoomSheet = true },
              onRetry: retryLoadPicture
            )
            .frame(maxWidth: .infinity, maxHeight: .infinity)

            if isInspectorVisible {
              Divider()
              OpenedPhotoInspectorView(
                presentation: presentation,
                isSideBySide: true
              )
              .frame(width: OpenedPhotoLayout.inspectorWidth)
              .frame(maxHeight: .infinity)
              .background(TrawlDesign.Colors.paper)
            }
          }
          .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else {
          ScrollView {
            VStack(spacing: 0) {
              OpenedPhotoStageView(
                loadState: loadState,
                onZoom: { isShowingZoomSheet = true },
                onRetry: retryLoadPicture
              )
              .frame(maxWidth: .infinity)
              .frame(
                height: OpenedPhotoLayout.verticalPhotoStageHeight(
                  forAvailableHeight: contentHeight
                )
              )

              Divider()

              OpenedPhotoInspectorView(
                presentation: presentation,
                isSideBySide: false
              )
              .frame(maxWidth: .infinity, alignment: .leading)
              .background(TrawlDesign.Colors.paper)
            }
          }
          .frame(maxWidth: .infinity, maxHeight: .infinity)
        }
      }
      .frame(width: proxy.size.width, height: proxy.size.height)
    }
    .sheet(isPresented: $isShowingZoomSheet) {
      if let loadedPhoto {
        OpenedPhotoZoomSheet(
          image: loadedPhoto.image,
          onOpenInPreview: {
            openInPreview(
              jpegData: loadedPhoto.jpegData,
              localCachedPictureFilePath: presentation.localCachedPictureFilePath
            )
          },
          onClose: { isShowingZoomSheet = false }
        )
      }
    }
    .task(
      id: PhotoLoadIdentity(
        trawlLink: trawlLink,
        localCachedPictureFilePath: presentation.localCachedPictureFilePath,
        pictureStatusFieldText: presentation.pictureStatusFieldText,
        isRemoteArchive: isRemoteArchive,
        retryAttempt: retryAttempt
      )
    ) {
      await loadPicture(
        localCachedPictureFilePath: presentation.localCachedPictureFilePath,
        pictureStatusFieldText: presentation.pictureStatusFieldText
      )
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
  private func loadPicture(
    localCachedPictureFilePath: String?,
    pictureStatusFieldText: String?
  ) async {
    if !isRemoteArchive {
      guard let localPath = localCachedPictureFilePath else {
        let reason =
          OpenedPhotoPresentation.unavailableReason(fromPictureFieldText: pictureStatusFieldText)
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
      let alreadyFailedReason = OpenedPhotoPresentation.unavailableReason(
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

  private func openInPreview(jpegData: Data, localCachedPictureFilePath: String?) {
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

private struct OpenedPhotoTopActionBar: View {
  let headerPrimaryText: String
  let headerDateText: String?
  let isFavourite: Bool
  let trawlLink: GloballyRoutableTrawlLink
  let isPhotoLoaded: Bool
  let showsInspectorToggle: Bool
  let isInspectorVisible: Bool
  let onZoom: () -> Void
  let onOpenInPreview: () -> Void
  let onToggleInspector: () -> Void

  var body: some View {
    HStack(spacing: 10) {
      HStack(spacing: 6) {
        if isFavourite {
          Image(systemName: "heart.fill")
            .font(.caption)
            .foregroundStyle(TrawlDesign.Colors.matchRed)
            .accessibilityLabel("Favourite")
        }
        headerTitleText
          .font(TrawlDesign.Typography.meta)
          .lineLimit(1)
          .truncationMode(.tail)
      }
      .frame(minWidth: 0, alignment: .leading)

      Spacer(minLength: 8)

      HStack(spacing: 6) {
        Button(action: onZoom) {
          Label("Zoom", systemImage: "plus.magnifyingglass")
        }
        .buttonStyle(.bordered)
        .controlSize(.small)
        .fixedSize()
        .disabled(!isPhotoLoaded)
        .help("Zoom photo")

        Button(action: onOpenInPreview) {
          Label("Preview", systemImage: "arrow.up.forward.app")
        }
        .buttonStyle(.bordered)
        .controlSize(.small)
        .fixedSize()
        .disabled(!isPhotoLoaded)
        .help("Open photo in Preview")

        if showsInspectorToggle {
          Button(action: onToggleInspector) {
            Label(
              "Info",
              systemImage: isInspectorVisible ? "info.circle.fill" : "info.circle"
            )
          }
          .buttonStyle(.bordered)
          .controlSize(.small)
          .fixedSize()
          .help(isInspectorVisible ? "Hide photo info" : "Show photo info")
        }

        CopyTrawlLinkButton(
          trawlLink: trawlLink,
          idleTitle: "Copy link",
          copiedTitle: "Copied"
        )
      }
      .layoutPriority(1)
    }
    .padding(.horizontal, 14)
    .padding(.vertical, 8)
    .background(TrawlDesign.Colors.surface.opacity(0.65))
  }

  private var headerTitleText: Text {
    let primary = Text(headerPrimaryText)
      .fontWeight(.medium)
      .foregroundStyle(TrawlDesign.Colors.ink)
    if let headerDateText, !headerDateText.isEmpty {
      let secondary = Text(" · \(headerDateText)")
        .foregroundStyle(.secondary)
      return Text("\(primary)\(secondary)")
    }
    return primary
  }
}

private struct OpenedPhotoStageView: View {
  let loadState: OpenedPhotoRecordView.PhotoLoadState
  let onZoom: () -> Void
  let onRetry: () -> Void

  var body: some View {
    ZStack {
      TrawlDesign.Colors.photoCanvas

      switch loadState {
      case .loading:
        HStack(spacing: TrawlDesign.Space.sm) {
          ProgressView()
            .controlSize(.small)
          Text("Loading photo…")
            .font(TrawlDesign.Typography.meta)
            .foregroundStyle(.secondary)
        }

      case .loaded(let image, _):
        Button(action: onZoom) {
          Image(nsImage: image)
            .resizable()
            .aspectRatio(contentMode: .fit)
            .clipShape(RoundedRectangle(cornerRadius: 4, style: .continuous))
            .shadow(color: Color.black.opacity(0.14), radius: 10, x: 0, y: 3)
        }
        .buttonStyle(.plain)
        .padding(OpenedPhotoLayout.stagePadding)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .help("Click to zoom")
        .accessibilityLabel("Opened photo. Click to zoom.")

      case .unavailable(let reason):
        OpenedPhotoUnavailableNotice(reason: reason, onRetry: onRetry)
          .padding(TrawlDesign.Space.lg)
      }
    }
    .clipped()
  }
}

private struct OpenedPhotoUnavailableNotice: View {
  let reason: String
  let onRetry: () -> Void

  var body: some View {
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

      Button("Retry", action: onRetry)
        .buttonStyle(.bordered)
        .controlSize(.small)
        .fixedSize()
    }
    .padding(.horizontal, TrawlDesign.Space.md)
    .padding(.vertical, 12)
    .frame(maxWidth: 460, alignment: .leading)
    .background(
      TrawlDesign.Colors.surface.opacity(0.85),
      in: TrawlDesign.Shape.surface
    )
    .overlay {
      TrawlDesign.Shape.surface
        .stroke(TrawlDesign.Colors.hairline, lineWidth: 1)
    }
  }
}

private struct OpenedPhotoInspectorView: View {
  let presentation: OpenedPhotoPresentation
  let isSideBySide: Bool

  var body: some View {
    if isSideBySide {
      ScrollView {
        OpenedPhotoInspectorContent(presentation: presentation)
          .padding(16)
      }
    } else {
      OpenedPhotoInspectorContent(presentation: presentation)
        .padding(18)
    }
  }
}

private struct OpenedPhotoInspectorContent: View {
  let presentation: OpenedPhotoPresentation

  var body: some View {
    VStack(alignment: .leading, spacing: 16) {
      if presentation.hasNarrativeContent {
        OpenedPhotoNarrativeSection(
          summary: presentation.summary,
          title: presentation.title,
          description: presentation.description,
          caption: presentation.caption
        )
        if !presentation.keyFacts.isEmpty
          || presentation.hasRecognisedTextSection
          || !presentation.technicalDetails.isEmpty
        {
          Divider()
        }
      }

      if !presentation.keyFacts.isEmpty {
        OpenedPhotoKeyFactsSection(facts: presentation.keyFacts)
      }

      if presentation.hasRecognisedTextSection {
        if !presentation.keyFacts.isEmpty {
          Divider()
        }
        OpenedPhotoRecognisedTextSection(
          recognisedText: presentation.recognisedText,
          visibleTextGroups: presentation.visibleTextGroups,
          copyableText: presentation.copyableRecognisedText
        )
      }

      if !presentation.technicalDetails.isEmpty {
        if !presentation.keyFacts.isEmpty || presentation.hasRecognisedTextSection {
          Divider()
        }
        OpenedPhotoTechnicalDetailsSection(details: presentation.technicalDetails)
      }

      if let footerText = presentation.footerText {
        Text(footerText)
          .font(TrawlDesign.Typography.smallLabel)
          .foregroundStyle(.secondary)
          .textSelection(.enabled)
      }
    }
    .frame(maxWidth: .infinity, alignment: .leading)
  }
}

private struct OpenedPhotoNarrativeSection: View {
  let summary: String?
  let title: String?
  let description: String?
  let caption: String?

  var body: some View {
    let headline = summary ?? title
    VStack(alignment: .leading, spacing: 8) {
      if let headline {
        Text(headline)
          .font(TrawlDesign.Typography.bodyEmphasis)
          .foregroundStyle(TrawlDesign.Colors.ink)
          .fixedSize(horizontal: false, vertical: true)
          .textSelection(.enabled)
      }
      if let title, summary != nil, title.caseInsensitiveCompare(summary ?? "") != .orderedSame {
        Text(title)
          .font(TrawlDesign.Typography.meta)
          .foregroundStyle(.secondary)
          .fixedSize(horizontal: false, vertical: true)
          .textSelection(.enabled)
      }
      if let description,
        headline?.caseInsensitiveCompare(description) != .orderedSame
      {
        Text(description)
          .font(TrawlDesign.Typography.body)
          .foregroundStyle(TrawlDesign.Colors.ink)
          .lineSpacing(3)
          .fixedSize(horizontal: false, vertical: true)
          .textSelection(.enabled)
      }
      if let caption,
        headline?.caseInsensitiveCompare(caption) != .orderedSame,
        description?.caseInsensitiveCompare(caption) != .orderedSame
      {
        Text(caption)
          .font(TrawlDesign.Typography.body)
          .foregroundStyle(.secondary)
          .lineSpacing(2)
          .fixedSize(horizontal: false, vertical: true)
          .textSelection(.enabled)
      }
    }
  }
}

private struct OpenedPhotoKeyFactsSection: View {
  let facts: [OpenedPhotoDetailField]

  var body: some View {
    VStack(alignment: .leading, spacing: 11) {
      ForEach(facts) { fact in
        VStack(alignment: .leading, spacing: 2) {
          Text(fact.label)
            .font(TrawlDesign.Typography.smallLabel)
            .foregroundStyle(.secondary)
          Text(fact.value)
            .font(
              fact.id == "camera" || fact.id == "date" || fact.id == "dimensions"
                ? TrawlDesign.Typography.meta.monospacedDigit()
                : TrawlDesign.Typography.body
            )
            .foregroundStyle(TrawlDesign.Colors.ink)
            .fixedSize(horizontal: false, vertical: true)
            .textSelection(.enabled)
        }
        .accessibilityElement(children: .combine)
      }
    }
  }
}

private struct OpenedPhotoRecognisedTextSection: View {
  let recognisedText: String?
  let visibleTextGroups: [OpenedPhotoVisibleTextGroup]
  let copyableText: String?

  @State private var copiedText = false

  var body: some View {
    VStack(alignment: .leading, spacing: 8) {
      HStack(spacing: 8) {
        Text("Recognised Text")
          .font(TrawlDesign.Typography.smallLabel.weight(.medium))
          .foregroundStyle(.secondary)

        Spacer()

        if let copyableText, !copyableText.isEmpty {
          Button {
            NSPasteboard.general.clearContents()
            NSPasteboard.general.setString(copyableText, forType: .string)
            copiedText = true
            Task { @MainActor in
              try? await Task.sleep(for: .seconds(2))
              copiedText = false
            }
          } label: {
            Label(
              copiedText ? "Copied" : "Copy text",
              systemImage: copiedText ? "checkmark" : "doc.on.doc"
            )
          }
          .buttonStyle(.bordered)
          .controlSize(.mini)
          .fixedSize()
          .help("Copy recognised text")
        }
      }

      if let recognisedText {
        Text(recognisedText)
          .font(TrawlDesign.Typography.meta)
          .foregroundStyle(TrawlDesign.Colors.ink)
          .lineSpacing(2)
          .fixedSize(horizontal: false, vertical: true)
          .textSelection(.enabled)
          .padding(10)
          .frame(maxWidth: .infinity, alignment: .leading)
          .background(
            TrawlDesign.Colors.surface.opacity(0.65),
            in: TrawlDesign.Shape.control
          )
          .overlay {
            TrawlDesign.Shape.control
              .stroke(TrawlDesign.Colors.hairline.opacity(0.6), lineWidth: 1)
          }
      } else if !visibleTextGroups.isEmpty {
        VStack(alignment: .leading, spacing: 8) {
          ForEach(visibleTextGroups) { group in
            VStack(alignment: .leading, spacing: 4) {
              Text(group.text)
                .font(TrawlDesign.Typography.meta)
                .foregroundStyle(TrawlDesign.Colors.ink)
                .lineSpacing(2)
                .fixedSize(horizontal: false, vertical: true)
                .textSelection(.enabled)
              if let english = group.englishRendering {
                Text(english)
                  .font(TrawlDesign.Typography.smallLabel)
                  .foregroundStyle(.secondary)
                  .fixedSize(horizontal: false, vertical: true)
                  .textSelection(.enabled)
              }
            }
            .padding(10)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(
              TrawlDesign.Colors.surface.opacity(0.65),
              in: TrawlDesign.Shape.control
            )
            .overlay {
              TrawlDesign.Shape.control
                .stroke(TrawlDesign.Colors.hairline.opacity(0.6), lineWidth: 1)
            }
          }
        }
      }

      if recognisedText != nil {
        let translations = visibleTextGroups.compactMap(\.englishRendering)
        if !translations.isEmpty {
          VStack(alignment: .leading, spacing: 2) {
            Text("In English")
              .font(TrawlDesign.Typography.smallLabel)
              .foregroundStyle(.secondary)
            Text(translations.joined(separator: "\n"))
              .font(TrawlDesign.Typography.meta)
              .foregroundStyle(TrawlDesign.Colors.ink)
              .fixedSize(horizontal: false, vertical: true)
              .textSelection(.enabled)
          }
        }
      }
    }
  }
}

private struct OpenedPhotoTechnicalDetailsSection: View {
  let details: [OpenedPhotoDetailField]

  @State private var isExpanded = false

  var body: some View {
    DisclosureGroup("Details", isExpanded: $isExpanded) {
      VStack(alignment: .leading, spacing: 9) {
        ForEach(details) { detail in
          VStack(alignment: .leading, spacing: 1) {
            Text(detail.label)
              .font(TrawlDesign.Typography.smallLabel)
              .foregroundStyle(.secondary)
            Text(detail.value)
              .font(TrawlDesign.Typography.meta)
              .foregroundStyle(TrawlDesign.Colors.ink)
              .fixedSize(horizontal: false, vertical: true)
              .textSelection(.enabled)
          }
          .accessibilityElement(children: .combine)
        }
      }
      .padding(.top, 8)
      .frame(maxWidth: .infinity, alignment: .leading)
    }
    .font(TrawlDesign.Typography.smallLabel.weight(.medium))
    .foregroundStyle(.secondary)
  }
}

struct CopyTrawlLinkButton: View {
  let trawlLink: GloballyRoutableTrawlLink
  var idleTitle: String = "Copy link for AI"
  var copiedTitle: String = "Link copied"

  @State private var copiedLink = false

  var body: some View {
    if !trawlLink.globallyRoutableTrawlLink.isEmpty {
      Button {
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(trawlLink.globallyRoutableTrawlLink, forType: .string)
        copiedLink = true
        Task { @MainActor in
          try? await Task.sleep(for: .seconds(2))
          copiedLink = false
        }
      } label: {
        Label(
          copiedLink ? copiedTitle : idleTitle,
          systemImage: copiedLink ? "checkmark" : "link"
        )
      }
      .buttonStyle(.bordered)
      .controlSize(.small)
      .fixedSize()
      .help("Copy a link your AI can open to read this item")
      .onChange(of: trawlLink) { _, _ in
        copiedLink = false
      }
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

        Button(action: onOpenInPreview) {
          Label("Open in Preview", systemImage: "arrow.up.forward.app")
        }
        .buttonStyle(.bordered)
        .controlSize(.small)
        .fixedSize()

        Button("Done", action: onClose)
          .buttonStyle(.borderedProminent)
          .controlSize(.small)
          .fixedSize()
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
            .padding(OpenedPhotoLayout.stagePadding)
        case .actual:
          ScrollView([.horizontal, .vertical]) {
            Image(nsImage: image)
              .padding(OpenedPhotoLayout.stagePadding)
          }
        case .double:
          ScrollView([.horizontal, .vertical]) {
            Image(nsImage: image)
              .resizable()
              .frame(
                width: max(1, image.size.width * 2),
                height: max(1, image.size.height * 2)
              )
              .padding(OpenedPhotoLayout.stagePadding)
          }
        }
      }
      .frame(maxWidth: .infinity, maxHeight: .infinity)
      .background(TrawlDesign.Colors.photoCanvas)
    }
    .frame(
      minWidth: OpenedPhotoLayout.zoomSheetMinimumSize.width,
      idealWidth: OpenedPhotoLayout.zoomSheetIdealSize.width,
      minHeight: OpenedPhotoLayout.zoomSheetMinimumSize.height,
      idealHeight: OpenedPhotoLayout.zoomSheetIdealSize.height
    )
  }
}
