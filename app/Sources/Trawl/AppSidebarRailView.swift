import AppKit
import SwiftUI
import TrawlClient
import TrawlCore

/// Responsive left app rail that stays fixed-order and never jumps.
/// Wide windows (>= 960pt) show icon, name, and count; compact windows (720–959pt)
/// shrink cleanly to a 56pt icon + count strip so Apps, Results, and Reader all
/// remain visible side by side.
struct AppSidebarRailView: View {
  @Environment(TrawlerIconStore.self) private var iconStore

  let mode: WorkspaceDestination
  let isCompact: Bool
  let trawlers: [RestingTrawler]
  let operabilityByID: [RegisteredTrawlerIdentity: AppOperabilityState]
  let searchMatchCountByID: [RegisteredTrawlerIdentity: UInt64]
  let totalAllAppsCount: UInt64
  let totalSearchResultsCount: UInt64
  let totalSearchCountIsLowerBound: Bool
  let hasActiveDateRange: Bool
  let selectedTrawlerID: RegisteredTrawlerIdentity?
  let hostDisplayName: String
  let hostSubtitle: String
  let isRemoteArchive: Bool
  let onSelectTrawler: (RegisteredTrawlerIdentity?) -> Void
  let onOpenHostSwitcher: () -> Void
  let onOpenPluginsSettings: () -> Void

  var body: some View {
    if isCompact {
      compactRail
    } else {
      wideRail
    }
  }

  private var wideRail: some View {
    VStack(spacing: 0) {
      HStack {
        Text("APPS")
          .font(.system(size: 10.5, weight: .semibold))
          .tracking(0.5)
          .foregroundStyle(TrawlDesign.Colors.mutedInk)
        Spacer()
        Text(mode == .overview ? "Items" : "Results")
          .font(.system(size: 10.5, weight: .medium))
          .foregroundStyle(TrawlDesign.Colors.mutedInk)
      }
      .padding(.horizontal, 14)
      .padding(.top, 12)
      .padding(.bottom, 8)

      ScrollView {
        VStack(spacing: 2) {
          allAppsWideRow
          ForEach(trawlers) { trawler in
            appWideRow(for: trawler)
          }
          if !isRemoteArchive {
            Button(action: onOpenPluginsSettings) {
              HStack {
                Text("Add a plugin…")
                  .font(.system(size: 12, weight: .medium))
                  .foregroundStyle(TrawlDesign.Colors.mutedInk)
                Spacer()
              }
              .padding(.horizontal, 10)
              .padding(.vertical, 7)
              .overlay(
                RoundedRectangle(cornerRadius: 8, style: .continuous)
                  .strokeBorder(
                    TrawlDesign.Colors.hairline,
                    style: StrokeStyle(lineWidth: 1, dash: [4, 3])
                  )
              )
            }
            .buttonStyle(.plain)
            .padding(.top, 8)
          }
        }
        .padding(.horizontal, 10)
        .padding(.bottom, 12)
      }

      Divider()

      Button(action: onOpenHostSwitcher) {
        HStack(spacing: 8) {
          VStack(alignment: .leading, spacing: 2) {
            Text(hostDisplayName)
              .font(.system(size: 12.5, weight: .semibold))
              .foregroundStyle(TrawlDesign.Colors.ink)
              .lineLimit(1)
            Text(hostSubtitle)
              .font(.system(size: 11))
              .foregroundStyle(TrawlDesign.Colors.mutedInk)
              .lineLimit(1)
          }
          Spacer()
          Image(systemName: "chevron.up.chevron.down")
            .font(.system(size: 10, weight: .medium))
            .foregroundStyle(TrawlDesign.Colors.mutedInk)
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 8)
        .background(
          TrawlDesign.Colors.paper,
          in: RoundedRectangle(cornerRadius: 9, style: .continuous)
        )
        .overlay(
          RoundedRectangle(cornerRadius: 9, style: .continuous)
            .stroke(TrawlDesign.Colors.hairline, lineWidth: 1)
        )
      }
      .buttonStyle(.plain)
      .padding(10)
    }
    .frame(width: TrawlDesign.wideSidebarWidth)
    .background(TrawlDesign.Colors.surface)
  }

  private var allAppsWideRow: some View {
    let selected = selectedTrawlerID == nil
    let countText: String = {
      if mode == .overview {
        return totalAllAppsCount.formatted()
      }
      if totalSearchResultsCount == 0 {
        return "0"
      }
      return totalSearchCountIsLowerBound
        ? "\(totalSearchResultsCount.formatted())+"
        : totalSearchResultsCount.formatted()
    }()

    return Button {
      onSelectTrawler(nil)
    } label: {
      HStack(spacing: 9) {
        OpenTrawlMarkIcon(size: 19)
        Text("All apps")
          .font(.system(size: 13, weight: selected ? .semibold : .medium))
          .foregroundStyle(selected ? TrawlDesign.Colors.selectBlue : TrawlDesign.Colors.ink)
        Spacer()
        Text(countText)
          .font(TrawlDesign.Typography.monoSmall.weight(.medium))
          .monospacedDigit()
          .foregroundStyle(selected ? TrawlDesign.Colors.selectBlue : TrawlDesign.Colors.mutedInk)
      }
      .padding(.horizontal, 10)
      .padding(.vertical, 7)
      .background(
        selected ? TrawlDesign.Colors.selectBlueSurface : Color.clear,
        in: RoundedRectangle(cornerRadius: 8, style: .continuous)
      )
    }
    .buttonStyle(.plain)
    .padding(.bottom, 4)
  }

  private func appWideRow(for trawler: RestingTrawler) -> some View {
    let selected = selectedTrawlerID == trawler.id
    let operability = operabilityByID[trawler.id]
    let isUndatedExcluded =
      mode == .search && hasActiveDateRange && trawler.id.registeredTrawlerIdentity == "contacts"

    let countDisplay: String = {
      if isUndatedExcluded { return "No dates" }
      if mode == .search {
        return (searchMatchCountByID[trawler.id] ?? 0).formatted()
      }
      return (operability?.archivedItemCount ?? 0).formatted()
    }()

    return Button {
      onSelectTrawler(trawler.id)
    } label: {
      HStack(spacing: 9) {
        TrawlerIconView(registeredTrawler: trawler.id, size: 19)
        Text(trawler.registeredTrawlerDisplayName)
          .font(.system(size: 13, weight: selected ? .semibold : .regular))
          .foregroundStyle(selected ? TrawlDesign.Colors.selectBlue : TrawlDesign.Colors.ink)
          .lineLimit(1)
        Spacer(minLength: 6)
        if !isUndatedExcluded && operability?.needsAttention == true {
          Circle()
            .fill(TrawlDesign.Colors.warningAmber)
            .frame(width: 6, height: 6)
            .accessibilityLabel("\(trawler.registeredTrawlerDisplayName) needs attention")
        }
        Text(countDisplay)
          .font(
            isUndatedExcluded
              ? .system(size: 11)
              : TrawlDesign.Typography.monoSmall
          )
          .monospacedDigit()
          .foregroundStyle(
            selected
              ? TrawlDesign.Colors.selectBlue
              : TrawlDesign.Colors.mutedInk
          )
      }
      .padding(.horizontal, 10)
      .padding(.vertical, 6)
      .background(
        selected ? TrawlDesign.Colors.selectBlueSurface : Color.clear,
        in: RoundedRectangle(cornerRadius: 8, style: .continuous)
      )
      .contentShape(Rectangle())
    }
    .buttonStyle(.plain)
    .help(
      isUndatedExcluded
        ? "Contacts have no dates, so a date range leaves them out."
        : (operability?.statusLine ?? trawler.registeredTrawlerDisplayName)
    )
  }

  private var compactRail: some View {
    ScrollView {
      VStack(spacing: 4) {
        compactAllAppsButton
        ForEach(trawlers) { trawler in
          compactAppButton(for: trawler)
        }
      }
      .padding(.vertical, 8)
      .padding(.horizontal, 5)
    }
    .frame(width: TrawlDesign.compactSidebarWidth)
    .background(TrawlDesign.Colors.surface)
  }

  private var compactAllAppsButton: some View {
    let selected = selectedTrawlerID == nil
    let count = mode == .overview ? totalAllAppsCount : totalSearchResultsCount
    return Button {
      onSelectTrawler(nil)
    } label: {
      VStack(spacing: 1) {
        OpenTrawlMarkIcon(size: 18)
        Text("All")
          .font(.system(size: 9, weight: .semibold))
          .foregroundStyle(selected ? TrawlDesign.Colors.selectBlue : TrawlDesign.Colors.mutedInk)
        Text(CompactCountFormatter.format(count))
          .font(.system(size: 9.5, weight: selected ? .bold : .medium, design: .monospaced))
          .monospacedDigit()
          .foregroundStyle(selected ? TrawlDesign.Colors.selectBlue : TrawlDesign.Colors.mutedInk)
      }
      .frame(width: 46, height: 48)
      .background(
        selected ? TrawlDesign.Colors.selectBlueSurface : Color.clear,
        in: RoundedRectangle(cornerRadius: 8, style: .continuous)
      )
    }
    .buttonStyle(.plain)
    .help("All apps · \(count.formatted())")
    .accessibilityLabel("All apps, \(count.formatted())")
  }

  private func compactAppButton(for trawler: RestingTrawler) -> some View {
    let selected = selectedTrawlerID == trawler.id
    let operability = operabilityByID[trawler.id]
    let isUndatedExcluded =
      mode == .search && hasActiveDateRange && trawler.id.registeredTrawlerIdentity == "contacts"
    let count =
      mode == .search
      ? (searchMatchCountByID[trawler.id] ?? 0)
      : (operability?.archivedItemCount ?? 0)

    return Button {
      onSelectTrawler(trawler.id)
    } label: {
      ZStack(alignment: .topTrailing) {
        VStack(spacing: 2) {
          TrawlerIconView(registeredTrawler: trawler.id, size: 19)
          if !isUndatedExcluded {
            Text(CompactCountFormatter.format(count))
              .font(.system(size: 9.5, weight: selected ? .bold : .medium, design: .monospaced))
              .monospacedDigit()
              .foregroundStyle(
                selected ? TrawlDesign.Colors.selectBlue : TrawlDesign.Colors.mutedInk
              )
          }
        }
        .frame(width: 46, height: 42)
        if operability?.needsAttention == true {
          Circle()
            .fill(TrawlDesign.Colors.warningAmber)
            .frame(width: 6, height: 6)
            .padding(4)
        }
      }
      .background(
        selected ? TrawlDesign.Colors.selectBlueSurface : Color.clear,
        in: RoundedRectangle(cornerRadius: 8, style: .continuous)
      )
    }
    .buttonStyle(.plain)
    .help(
      isUndatedExcluded
        ? "\(trawler.registeredTrawlerDisplayName) · Contacts have no dates, so a date range leaves them out."
        : "\(trawler.registeredTrawlerDisplayName) · \(operability?.statusLine ?? "\(count.formatted())")"
    )
    .accessibilityLabel("\(trawler.registeredTrawlerDisplayName), \(count.formatted())")
  }
}

struct OpenTrawlMarkIcon: View {
  let size: CGFloat

  var body: some View {
    if let appIcon = NSApplication.shared.applicationIconImage {
      Image(nsImage: appIcon)
        .resizable()
        .scaledToFit()
        .frame(width: size, height: size)
        .clipShape(
          RoundedRectangle(cornerRadius: size * TrawlDesign.Radius.appIconFraction, style: .continuous)
        )
    } else {
      Image(systemName: "diamond.fill")
        .font(.system(size: size * 0.72, weight: .semibold))
        .foregroundStyle(TrawlDesign.Colors.matchRed)
        .frame(width: size, height: size)
    }
  }
}

enum CompactCountFormatter {
  static func format(_ count: UInt64) -> String {
    if count >= 1_000_000 {
      return String(format: "%.1fM", Double(count) / 1_000_000)
    }
    if count >= 10_000 {
      return String(format: "%.0fK", Double(count) / 1_000)
    }
    if count >= 1_000 {
      return String(format: "%.1fK", Double(count) / 1_000)
    }
    return "\(count)"
  }
}
