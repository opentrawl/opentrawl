import AppKit
import SwiftUI
import TrawlClient
import TrawlCore

/// Responsive left app rail that stays fixed-order and never jumps.
/// Wide windows (>= 960pt) show icon, name, and count; compact windows (720–959pt)
/// shrink cleanly to a 56pt icon + count strip while keeping host switcher and plugins reachable.
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
  let isEmptyBrowse: Bool
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
          .font(TrawlDesign.Typography.caption)
          .tracking(0.5)
          .foregroundStyle(TrawlDesign.Colors.mutedInk)
        Spacer()
        Text(mode == .overview ? "Items" : "Results")
          .font(TrawlDesign.Typography.meta)
          .foregroundStyle(TrawlDesign.Colors.mutedInk)
      }
      .padding(.horizontal, TrawlDesign.Space.md)
      .padding(.top, TrawlDesign.Space.md)
      .padding(.bottom, TrawlDesign.Space.sm)

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
                  .font(TrawlDesign.Typography.meta)
                  .foregroundStyle(TrawlDesign.Colors.mutedInk)
                Spacer()
              }
              .padding(.horizontal, TrawlDesign.Space.sm)
              .padding(.vertical, TrawlDesign.Space.sm)
              .overlay(
                TrawlDesign.Shape.control
                  .strokeBorder(
                    TrawlDesign.Colors.hairline,
                    style: StrokeStyle(lineWidth: 1, dash: [4, 3])
                  )
              )
            }
            .buttonStyle(.plain)
            .padding(.top, TrawlDesign.Space.sm)
          }
        }
        .padding(.horizontal, TrawlDesign.Space.sm)
        .padding(.bottom, TrawlDesign.Space.md)
      }

      Divider()

      Button(action: onOpenHostSwitcher) {
        HStack(spacing: TrawlDesign.Space.sm) {
          VStack(alignment: .leading, spacing: 2) {
            Text(hostDisplayName)
              .font(TrawlDesign.Typography.cardTitle)
              .foregroundStyle(TrawlDesign.Colors.ink)
              .lineLimit(1)
            Text(hostSubtitle)
              .font(TrawlDesign.Typography.meta)
              .foregroundStyle(TrawlDesign.Colors.mutedInk)
              .lineLimit(1)
          }
          Spacer()
          Image(systemName: "chevron.up.chevron.down")
            .font(TrawlDesign.Typography.monoAxis)
            .foregroundStyle(TrawlDesign.Colors.mutedInk)
        }
        .padding(.horizontal, TrawlDesign.Space.sm)
        .padding(.vertical, TrawlDesign.Space.sm)
        .background(TrawlDesign.Colors.paper, in: TrawlDesign.Shape.control)
        .overlay(TrawlDesign.Shape.control.strokeBorder(TrawlDesign.Colors.hairline, lineWidth: 1))
      }
      .buttonStyle(.plain)
      .padding(TrawlDesign.Space.sm)
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
      HStack(spacing: TrawlDesign.Space.sm) {
        OpenTrawlMarkIcon(size: 18)
        Text("All apps")
          .font(TrawlDesign.Typography.laneLabel)
          .foregroundStyle(selected ? TrawlDesign.Colors.selectBlue : TrawlDesign.Colors.ink)
        Spacer()
        Text(countText)
          .font(TrawlDesign.Typography.monoSmall)
          .monospacedDigit()
          .foregroundStyle(selected ? TrawlDesign.Colors.selectBlue : TrawlDesign.Colors.mutedInk)
      }
      .padding(.horizontal, TrawlDesign.Space.sm)
      .padding(.vertical, 6)
      .background(selected ? TrawlDesign.Colors.selectBlueSurface : Color.clear, in: TrawlDesign.Shape.control)
    }
    .buttonStyle(.plain)
    .padding(.bottom, TrawlDesign.Space.xs)
  }

  private func appWideRow(for trawler: RestingTrawler) -> some View {
    let selected = selectedTrawlerID == trawler.id
    let operability = operabilityByID[trawler.id]
    let isUndatedExcluded =
      mode == .search
      && (hasActiveDateRange || isEmptyBrowse)
      && trawler.id.registeredTrawlerIdentity == "contacts"

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
      HStack(spacing: TrawlDesign.Space.sm) {
        TrawlerIconView(registeredTrawler: trawler.id, size: 18)
        Text(trawler.registeredTrawlerDisplayName)
          .font(TrawlDesign.Typography.laneLabel)
          .foregroundStyle(selected ? TrawlDesign.Colors.selectBlue : TrawlDesign.Colors.ink)
          .lineLimit(1)
        Spacer(minLength: TrawlDesign.Space.xs)
        if !isUndatedExcluded && operability?.needsAttention == true {
          Circle()
            .fill(TrawlDesign.Colors.warningAmber)
            .frame(width: 6, height: 6)
            .accessibilityLabel("\(trawler.registeredTrawlerDisplayName) needs attention")
        }
        Text(countDisplay)
          .font(TrawlDesign.Typography.monoSmall)
          .monospacedDigit()
          .foregroundStyle(selected ? TrawlDesign.Colors.selectBlue : TrawlDesign.Colors.mutedInk)
      }
      .padding(.horizontal, TrawlDesign.Space.sm)
      .padding(.vertical, 6)
      .background(selected ? TrawlDesign.Colors.selectBlueSurface : Color.clear, in: TrawlDesign.Shape.control)
      .contentShape(Rectangle())
    }
    .buttonStyle(.plain)
    .help(
      isUndatedExcluded
        ? "Contacts have no dates, so date-based browsing leaves them out."
        : (operability?.statusLine ?? trawler.registeredTrawlerDisplayName)
    )
  }

  private var compactRail: some View {
    VStack(spacing: 0) {
      ScrollView {
        VStack(spacing: TrawlDesign.Space.xs) {
          compactAllAppsButton
          ForEach(trawlers) { trawler in
            compactAppButton(for: trawler)
          }
        }
        .padding(.vertical, TrawlDesign.Space.sm)
        .padding(.horizontal, TrawlDesign.Space.xs)
      }

      Divider()

      Menu {
        Button("Switch archive host…", action: onOpenHostSwitcher)
        if !isRemoteArchive {
          Button("Add a plugin…", action: onOpenPluginsSettings)
        }
      } label: {
        Image(systemName: "ellipsis")
          .font(TrawlDesign.Typography.body)
          .foregroundStyle(TrawlDesign.Colors.mutedInk)
          .frame(width: 46, height: 32)
      }
      .menuStyle(.borderlessButton)
      .menuIndicator(.hidden)
      .padding(.vertical, TrawlDesign.Space.xs)
      .accessibilityLabel("Archive options")
    }
    .frame(width: TrawlDesign.compactSidebarWidth)
    .background(TrawlDesign.Colors.surface)
  }

  private var compactAllAppsButton: some View {
    let selected = selectedTrawlerID == nil
    let count = mode == .overview ? totalAllAppsCount : totalSearchResultsCount
    let formattedCount = CompactCountFormatter.format(
      count,
      isLowerBound: mode == .search && totalSearchCountIsLowerBound
    )
    return Button {
      onSelectTrawler(nil)
    } label: {
      VStack(spacing: 1) {
        OpenTrawlMarkIcon(size: 18)
        Text("All")
          .font(TrawlDesign.Typography.monoAxis)
          .foregroundStyle(selected ? TrawlDesign.Colors.selectBlue : TrawlDesign.Colors.mutedInk)
        Text(formattedCount)
          .font(TrawlDesign.Typography.monoAxis)
          .monospacedDigit()
          .foregroundStyle(selected ? TrawlDesign.Colors.selectBlue : TrawlDesign.Colors.mutedInk)
      }
      .frame(width: 46, height: 48)
      .background(selected ? TrawlDesign.Colors.selectBlueSurface : Color.clear, in: TrawlDesign.Shape.control)
    }
    .buttonStyle(.plain)
    .help("All apps · \(count.formatted())")
    .accessibilityLabel("All apps, \(count.formatted())")
  }

  private func compactAppButton(for trawler: RestingTrawler) -> some View {
    let selected = selectedTrawlerID == trawler.id
    let operability = operabilityByID[trawler.id]
    let isUndatedExcluded =
      mode == .search
      && (hasActiveDateRange || isEmptyBrowse)
      && trawler.id.registeredTrawlerIdentity == "contacts"
    let count =
      mode == .search
      ? (searchMatchCountByID[trawler.id] ?? 0)
      : (operability?.archivedItemCount ?? 0)

    return Button {
      onSelectTrawler(trawler.id)
    } label: {
      ZStack(alignment: .topTrailing) {
        VStack(spacing: 2) {
          TrawlerIconView(registeredTrawler: trawler.id, size: 18)
          Text(isUndatedExcluded ? "—" : CompactCountFormatter.format(count))
            .font(TrawlDesign.Typography.monoAxis)
            .monospacedDigit()
            .foregroundStyle(selected ? TrawlDesign.Colors.selectBlue : TrawlDesign.Colors.mutedInk)
        }
        .frame(width: 46, height: 42)
        if !isUndatedExcluded && operability?.needsAttention == true {
          Circle()
            .fill(TrawlDesign.Colors.warningAmber)
            .frame(width: 6, height: 6)
            .padding(TrawlDesign.Space.xs)
        }
      }
      .background(selected ? TrawlDesign.Colors.selectBlueSurface : Color.clear, in: TrawlDesign.Shape.control)
    }
    .buttonStyle(.plain)
    .help(
      isUndatedExcluded
        ? "\(trawler.registeredTrawlerDisplayName) · Contacts have no dates."
        : "\(trawler.registeredTrawlerDisplayName) · \(operability?.statusLine ?? "\(count.formatted())")"
    )
    .accessibilityLabel(
      isUndatedExcluded
        ? "\(trawler.registeredTrawlerDisplayName), no dates"
        : "\(trawler.registeredTrawlerDisplayName), \(count.formatted())"
    )
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
  static func format(_ count: UInt64, isLowerBound: Bool = false) -> String {
    let base = Int64(clamping: count).formatted(
      .number.notation(.compactName).precision(.fractionLength(0...1))
    )
    return isLowerBound && count > 0 ? "\(base)+" : base
  }
}
