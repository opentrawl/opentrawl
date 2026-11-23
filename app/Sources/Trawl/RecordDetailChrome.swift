import AppKit
import SwiftUI
import TrawlClient

struct RecordDetailHeader: View {
  let registeredTrawler: RegisteredTrawlerIdentity?
  let trawlerDisplayName: String?
  let title: String
  let subtitle: String?
  let trawlLink: GloballyRoutableTrawlLink?
  let showsAroundToggle: Bool
  let isShowingAround: Bool
  let onToggleAround: (() -> Void)?

  var body: some View {
    VStack(alignment: .leading, spacing: 10) {
      HStack(alignment: .center, spacing: 8) {
        if let registeredTrawler {
          TrawlerIconView(registeredTrawler: registeredTrawler, size: 20)
        }
        if let trawlerDisplayName, !trawlerDisplayName.isEmpty {
          Text(trawlerDisplayName)
            .font(.caption.weight(.semibold))
            .foregroundStyle(.secondary)
        }
        Spacer(minLength: 8)
        if let trawlLink, !trawlLink.globallyRoutableTrawlLink.isEmpty {
          CopyLinkPillButton(trawlLink: trawlLink)
        }
        if showsAroundToggle, let onToggleAround {
          Button(action: onToggleAround) {
            Label(
              OperationalCopy.Record.relatedAroundAction,
              systemImage: "clock.arrow.circlepath"
            )
            .font(.caption.weight(.medium))
          }
          .buttonStyle(.bordered)
          .controlSize(.small)
          .tint(isShowingAround ? TrawlDesign.brandRed : .secondary)
        }
      }
      VStack(alignment: .leading, spacing: 4) {
        Text(title)
          .font(.title2.weight(.semibold))
          .foregroundStyle(.primary)
          .textSelection(.enabled)
        if let subtitle, !subtitle.isEmpty {
          Text(subtitle)
            .font(.callout)
            .foregroundStyle(.secondary)
            .textSelection(.enabled)
        }
      }
    }
  }
}

struct CopyLinkPillButton: View {
  let trawlLink: GloballyRoutableTrawlLink

  @State private var copied = false
  @State private var resetTask: Task<Void, Never>?

  var body: some View {
    Button {
      NSPasteboard.general.clearContents()
      NSPasteboard.general.setString(trawlLink.globallyRoutableTrawlLink, forType: .string)
      copied = true
      resetTask?.cancel()
      resetTask = Task { @MainActor in
        try? await Task.sleep(for: .seconds(2))
        guard !Task.isCancelled else { return }
        copied = false
      }
    } label: {
      HStack(spacing: 5) {
        Image(systemName: copied ? "checkmark" : "link")
          .font(.caption2.weight(.semibold))
          .foregroundStyle(copied ? Color.green : .secondary)
        Text(copied ? OperationalCopy.Record.copiedLinkConfirmation : trawlLink.globallyRoutableTrawlLink)
          .font(.caption.monospaced())
          .foregroundStyle(copied ? Color.green : .secondary)
          .lineLimit(1)
      }
      .padding(.horizontal, 8)
      .padding(.vertical, 4)
      .background(TrawlDesign.surfaceBackground, in: Capsule())
      .overlay(Capsule().strokeBorder(TrawlDesign.hairlineBorder, lineWidth: 0.75))
    }
    .buttonStyle(.plain)
    .help("\(OperationalCopy.Record.copyLinkAction) (\(trawlLink.globallyRoutableTrawlLink))")
    .accessibilityLabel("\(OperationalCopy.Record.copyLinkAction) \(trawlLink.globallyRoutableTrawlLink)")
  }
}

struct AroundRelatedRecordsSection: View {
  let trawlLink: GloballyRoutableTrawlLink
  let helperController: TrawlHelperController
  let onOpenLink: ((GloballyRoutableTrawlLink, RecordAnchorIdentifier) -> Void)?

  @State private var isLoading = true
  @State private var rows: [AroundRelatedRecordRow] = []

  var body: some View {
    VStack(alignment: .leading, spacing: 8) {
      HStack(spacing: 6) {
        Image(systemName: "clock.arrow.circlepath")
          .font(.caption.weight(.semibold))
          .foregroundStyle(TrawlDesign.brandRed)
        Text(OperationalCopy.Record.relatedAroundAction)
          .font(.caption.weight(.semibold))
          .foregroundStyle(.secondary)
        Spacer()
        if isLoading {
          ProgressView()
            .controlSize(.mini)
        }
      }
      if !isLoading && rows.isEmpty {
        Text("No related records found within 3 days.")
          .font(.caption)
          .foregroundStyle(.secondary)
      } else {
        VStack(spacing: 0) {
          ForEach(Array(rows.enumerated()), id: \.element.id) { index, row in
            AroundRelatedRowItem(row: row, onOpenLink: onOpenLink)
            if index < rows.count - 1 {
              Divider()
                .padding(.leading, 8)
            }
          }
        }
      }
    }
    .padding(12)
    .background(TrawlDesign.surfaceBackground, in: RoundedRectangle(cornerRadius: 10, style: .continuous))
    .overlay(
      RoundedRectangle(cornerRadius: 10, style: .continuous)
        .strokeBorder(TrawlDesign.hairlineBorder, lineWidth: 0.75)
    )
    .task(id: trawlLink) {
      isLoading = true
      rows = await helperController.loadRelatedRecords(around: trawlLink)
      isLoading = false
    }
  }
}

private struct AroundRelatedRowItem: View {
  let row: AroundRelatedRecordRow
  let onOpenLink: ((GloballyRoutableTrawlLink, RecordAnchorIdentifier) -> Void)?

  var body: some View {
    Button {
      if let link = row.trawlLink, let onOpenLink {
        onOpenLink(link, RecordAnchorIdentifier(recordAnchorIdentifier: "detail"))
      }
    } label: {
      HStack(alignment: .firstTextBaseline, spacing: 8) {
        Text(row.appDisplayName)
          .font(.caption.weight(.semibold))
          .foregroundStyle(.secondary)
          .frame(width: 72, alignment: .leading)
          .lineLimit(1)
        Text(row.summaryText)
          .font(.callout)
          .foregroundStyle(.primary)
          .lineLimit(1)
          .frame(maxWidth: .infinity, alignment: .leading)
        Text(row.whenText)
          .font(.caption.monospacedDigit())
          .foregroundStyle(.tertiary)
          .lineLimit(1)
      }
      .padding(.vertical, 5)
      .padding(.horizontal, 4)
      .contentShape(.rect)
    }
    .buttonStyle(.plain)
    .disabled(row.trawlLink == nil || onOpenLink == nil)
  }
}

struct RecordMetadataCard<Content: View>: View {
  @ViewBuilder let content: Content

  init(@ViewBuilder content: () -> Content) {
    self.content = content()
  }

  var body: some View {
    VStack(alignment: .leading, spacing: 8) {
      content
    }
    .padding(14)
    .frame(maxWidth: .infinity, alignment: .leading)
    .background(
      TrawlDesign.surfaceBackground,
      in: RoundedRectangle(cornerRadius: 10, style: .continuous)
    )
    .overlay(
      RoundedRectangle(cornerRadius: 10, style: .continuous)
        .strokeBorder(TrawlDesign.hairlineBorder, lineWidth: 0.75)
    )
  }
}

struct OptionalFullDiskAccessCard: View {
  let fdaAppNames: [String]
  let onUnlock: () -> Void

  private var descriptionText: String {
    guard !fdaAppNames.isEmpty else {
      return OperationalCopy.FullDiskAccess.optionalCardBody
    }
    let joined = fdaAppNames.formatted(.list(type: .and))
    return "Zero-FDA apps work right now. Grant Full Disk Access only if you also want to search \(joined)."
  }

  var body: some View {
    HStack(alignment: .center, spacing: 14) {
      Image(systemName: "lock.shield")
        .font(.title3.weight(.medium))
        .foregroundStyle(.orange)
        .frame(width: 32, height: 32)
        .background(.orange.opacity(0.12), in: RoundedRectangle(cornerRadius: 8, style: .continuous))
      VStack(alignment: .leading, spacing: 3) {
        Text(OperationalCopy.FullDiskAccess.optionalCardTitle)
          .font(.callout.weight(.semibold))
          .foregroundStyle(.primary)
        Text(descriptionText)
          .font(.caption)
          .foregroundStyle(.secondary)
          .fixedSize(horizontal: false, vertical: true)
      }
      Spacer(minLength: 12)
      Button(OperationalCopy.FullDiskAccess.unlockAction, action: onUnlock)
        .buttonStyle(.bordered)
        .controlSize(.small)
    }
    .padding(.horizontal, 14)
    .padding(.vertical, 12)
    .background(
      Color.orange.opacity(0.07),
      in: RoundedRectangle(cornerRadius: TrawlDesign.cardCornerRadius, style: .continuous)
    )
    .overlay(
      RoundedRectangle(cornerRadius: TrawlDesign.cardCornerRadius, style: .continuous)
        .strokeBorder(Color.orange.opacity(0.24), lineWidth: 0.75)
    )
  }
}
