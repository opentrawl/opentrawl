import AppKit
import SwiftUI
import TrawlClient
import TrawlCore

enum ArchiveSidebarSelection: Hashable {
  case overview
  case allItems
  case app(RegisteredTrawlerIdentity)
}

/// Native macOS sidebar (`List` + `.listStyle(.sidebar)` + `.badge()`).
/// Keeps app order fixed so items never jump, shows real macOS app icons,
/// and leaves host switching to the window toolbar and Overview hub.
struct AppSidebarRailView: View {
  @Environment(TrawlerIconStore.self) private var iconStore

  @Binding var selection: ArchiveSidebarSelection?
  let isSearching: Bool
  let trawlers: [RestingTrawler]
  let operabilityByID: [RegisteredTrawlerIdentity: AppOperabilityState]
  let searchMatchCountByID: [RegisteredTrawlerIdentity: AppSearchMatchCount]
  let allAppsSearchMatchCountText: String?
  let hasActiveDateRange: Bool
  let isEmptyBrowse: Bool
  let isRemoteArchive: Bool
  let onOpenPluginsSettings: () -> Void

  private var isFilteredSearch: Bool {
    isSearching && !isEmptyBrowse
  }

  private var totalArchivedItemCount: UInt64 {
    trawlers.reduce(UInt64(0)) { $0 + (operabilityByID[$1.id]?.archivedItemCount ?? 0) }
  }

  var body: some View {
    List(selection: $selection) {
      Section {
        Label("Overview", systemImage: "square.grid.2x2")
          .tag(ArchiveSidebarSelection.overview)

        allItemsRow
          .tag(ArchiveSidebarSelection.allItems)
      }

      Section("Apps") {
        ForEach(trawlers) { trawler in
          appRow(for: trawler)
            .tag(ArchiveSidebarSelection.app(trawler.id))
        }
      }

      if !isRemoteArchive {
        Section {
          Button(action: onOpenPluginsSettings) {
            Label("Browse plugins…", systemImage: "puzzlepiece.extension")
          }
          .buttonStyle(.plain)
          .foregroundStyle(.secondary)
        }
      }
    }
    .listStyle(.sidebar)
    .navigationSplitViewColumnWidth(min: 196, ideal: 224, max: 270)
  }

  @ViewBuilder
  private var allItemsRow: some View {
    let badgeText: String? = {
      if isFilteredSearch {
        return allAppsSearchMatchCountText
      }
      return totalArchivedItemCount > 0 ? totalArchivedItemCount.formatted() : nil
    }()
    if let badgeText {
      Label("All items", systemImage: "tray.full")
        .badge(Text(badgeText).monospacedDigit())
    } else {
      Label("All items", systemImage: "tray.full")
    }
  }

  private func appRow(for trawler: RestingTrawler) -> some View {
    let operability = operabilityByID[trawler.id]
    let isUndatedExcluded =
      isSearching
      && hasActiveDateRange
      && trawler.id.registeredTrawlerIdentity == "contacts"

    let badgeText: String? = {
      if isUndatedExcluded {
        return "No dates"
      }
      if isFilteredSearch {
        guard let count = searchMatchCountByID[trawler.id], count.searchMatchCount > 0 else {
          return nil
        }
        return count.text
      }
      let archivedItemCount = operability?.archivedItemCount ?? 0
      return archivedItemCount > 0 ? archivedItemCount.formatted() : nil
    }()

    return HStack(spacing: 8) {
      TrawlerIconView(registeredTrawler: trawler.id, size: 16)
      Text(trawler.registeredTrawlerDisplayName)
        .lineLimit(1)
      if !isUndatedExcluded && operability?.needsAttention == true {
        Circle()
          .fill(TrawlDesign.Colors.warningAmber)
          .frame(width: 6, height: 6)
          .accessibilityLabel("\(trawler.registeredTrawlerDisplayName) needs attention")
      }
    }
    .badge(badgeText.map { Text($0).monospacedDigit() })
    .help(
      isUndatedExcluded
        ? "Contacts have no dates, so date-based browsing leaves them out."
        : (operability?.statusLine ?? trawler.registeredTrawlerDisplayName)
    )
  }
}

struct OpenTrawlMarkIcon: View {
  let size: CGFloat

  private var resolvedIcon: NSImage? {
    if let named = NSImage(named: "AppIcon") ?? NSImage(named: "OpenTrawl") {
      return named
    }
    if let url = Bundle.main.url(forResource: "OpenTrawl", withExtension: "icns"),
      let image = NSImage(contentsOf: url)
    {
      return image
    }
    // Avoid showing the generic blue folder icon when running under swiftpm-testing-helper.
    if Bundle.main.bundlePath.hasSuffix(".app") {
      return NSApplication.shared.applicationIconImage
    }
    return nil
  }

  var body: some View {
    if let appIcon = resolvedIcon {
      Image(nsImage: appIcon)
        .resizable()
        .scaledToFit()
        .frame(width: size, height: size)
        .clipShape(
          RoundedRectangle(cornerRadius: size * TrawlDesign.Radius.appIconFraction, style: .continuous)
        )
    } else {
      ZStack {
        RoundedRectangle(
          cornerRadius: size * TrawlDesign.Radius.appIconFraction,
          style: .continuous
        )
        .fill(
          LinearGradient(
            colors: [Color(red: 0.16, green: 0.19, blue: 0.25), Color(red: 0.08, green: 0.10, blue: 0.14)],
            startPoint: .top,
            endPoint: .bottom
          )
        )
        Image(systemName: "square.stack.3d.up.fill")
          .font(.system(size: size * 0.48, weight: .semibold))
          .foregroundStyle(.white)
      }
      .frame(width: size, height: size)
    }
  }
}
