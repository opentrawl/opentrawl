import SwiftUI
import TrawlClient
import TrawlCore

enum TrawlerCardHealth {
  static func resolve(
    for trawler: RestingTrawler,
    progress: TrawlerArchiveUpdateProgressState?,
    isUnavailableLocally: Bool
  ) -> TrawlerHealthState {
    if case .waiting = progress { return .updating }
    if case .building = progress { return .updating }
    if case .finalising = progress { return .updating }
    if trawler.state == .comingSoon { return .comingSoon }
    if isUnavailableLocally, trawler.totalRecordCount == 0 {
      return .notInstalled
    }
    return trawler.healthState
  }

  static func dotColor(for health: TrawlerHealthState) -> Color {
    switch health {
    case .synced: .green
    case .updating: .blue
    case .needsFullDiskAccess, .needsSignIn, .needsSignedApp, .appClosed, .stale: .orange
    case .failed, .skipped: .red
    case .empty, .notInstalled, .comingSoon: Color.secondary.opacity(0.45)
    }
  }
}

struct ArchiveHomeOverviewView: View {
  let trawlers: [RestingTrawler]
  let selectedTrawler: RestingTrawler?
  let activeCategory: TrawlerFilterCategory
  let totalRecordCount: UInt64
  let searchableCount: Int
  let latestUpdateTime: Date?
  let isUpdating: Bool
  let updateProgress: [RegisteredTrawlerIdentity: TrawlerArchiveUpdateProgressState]
  let unavailableTrawlers: Set<RegisteredTrawlerIdentity>
  let needsFullDiskAccessRecovery: Bool
  let fdaAppNames: [String]
  let semanticStatus: LocalSemanticSearchSnapshot
  let onSelectTrawler: (RestingTrawler) -> Void
  let onUpdateTrawler: (RegisteredTrawlerIdentity) -> Void
  let onUpdateAll: () -> Void
  let onUnlockFullDiskAccess: () -> Void
  let onOpenSemanticPopover: () -> Void
  let onOpenConnectAI: () -> Void

  private var visibleTrawlers: [RestingTrawler] {
    if let selectedTrawler {
      return [selectedTrawler]
    }
    if activeCategory == .all {
      return trawlers
    }
    return trawlers.filter { activeCategory.matches($0.id) }
  }

  private let columns = [
    GridItem(.adaptive(minimum: 248, maximum: 340), spacing: 12)
  ]

  var body: some View {
    ScrollView {
      VStack(alignment: .leading, spacing: 18) {
        if needsFullDiskAccessRecovery {
          OptionalFullDiskAccessCard(
            fdaAppNames: fdaAppNames,
            onUnlock: onUnlockFullDiskAccess
          )
        }

        ArchiveOverviewMetricsStrip(
          totalRecordCount: totalRecordCount,
          searchableCount: searchableCount,
          totalAppCount: trawlers.count { $0.state != .comingSoon },
          latestUpdateTime: latestUpdateTime,
          isUpdating: isUpdating,
          semanticStatus: semanticStatus,
          onUpdateAll: onUpdateAll,
          onOpenSemanticPopover: onOpenSemanticPopover,
          onOpenConnectAI: onOpenConnectAI
        )

        if let selectedTrawler {
          ScopedAppOverviewCard(
            trawler: selectedTrawler,
            health: TrawlerCardHealth.resolve(
              for: selectedTrawler,
              progress: updateProgress[selectedTrawler.id],
              isUnavailableLocally: unavailableTrawlers.contains(selectedTrawler.id)
            ),
            onUpdate: { onUpdateTrawler(selectedTrawler.id) },
            onUnlockFullDiskAccess: onUnlockFullDiskAccess
          )
        } else {
          VStack(alignment: .leading, spacing: 10) {
            HStack {
              Text(
                activeCategory == .all
                  ? "Searchable archives"
                  : "\(activeCategory.displayName) archives"
              )
              .font(.system(size: 13, weight: .semibold))
              .foregroundStyle(.secondary)
              Spacer()
              Text("Click any app to scope Spotlight")
                .font(.caption)
                .foregroundStyle(.tertiary)
            }
            LazyVGrid(columns: columns, spacing: 12) {
              ForEach(visibleTrawlers) { trawler in
                let health = TrawlerCardHealth.resolve(
                  for: trawler,
                  progress: updateProgress[trawler.id],
                  isUnavailableLocally: unavailableTrawlers.contains(trawler.id)
                )
                ArchiveAppCard(
                  trawler: trawler,
                  health: health,
                  onSelect: { onSelectTrawler(trawler) },
                  onUpdate: { onUpdateTrawler(trawler.id) },
                  onUnlockFullDiskAccess: onUnlockFullDiskAccess
                )
              }
            }
          }
        }
      }
      .padding(20)
      .frame(maxWidth: TrawlDesign.searchWorkspaceMaximumWidth, alignment: .leading)
      .frame(maxWidth: .infinity, alignment: .leading)
    }
  }
}

private struct ArchiveOverviewMetricsStrip: View {
  let totalRecordCount: UInt64
  let searchableCount: Int
  let totalAppCount: Int
  let latestUpdateTime: Date?
  let isUpdating: Bool
  let semanticStatus: LocalSemanticSearchSnapshot
  let onUpdateAll: () -> Void
  let onOpenSemanticPopover: () -> Void
  let onOpenConnectAI: () -> Void

  var body: some View {
    HStack(spacing: 12) {
      MetricTile(
        title: "Local Archive",
        value: totalRecordCount > 0 ? "\(totalRecordCount.formatted()) records" : "No records yet",
        subtitle: "\(searchableCount) of \(totalAppCount) apps ready",
        systemImage: "internaldrive",
        accent: TrawlDesign.brandRed
      )

      Button(action: onOpenSemanticPopover) {
        MetricTile(
          title: OperationalCopy.Home.semanticSearchTitle,
          value: semanticStatus.isEnabled ? "Enabled" : "Off",
          subtitle: semanticStatus.chunkSummary
            ?? (semanticStatus.isAvailable ? "Hybrid keyword + vector search" : "Not configured"),
          systemImage: "brain.head.profile",
          accent: semanticStatus.isEnabled ? .green : .secondary
        )
      }
      .buttonStyle(.plain)

      Button(action: onOpenConnectAI) {
        MetricTile(
          title: "AI Access",
          value: "Scoped & read-only",
          subtitle: "Manage tokens, MCP & prompt",
          systemImage: "sparkles",
          accent: .purple
        )
      }
      .buttonStyle(.plain)

      Button(action: onUpdateAll) {
        MetricTile(
          title: "Freshness",
          value: isUpdating
            ? "Updating…"
            : (latestUpdateTime.map { $0.formatted(.relative(presentation: .named)) } ?? "Not synced"),
          subtitle: isUpdating ? "Syncing local archives" : "Click to refresh now",
          systemImage: "arrow.clockwise",
          accent: isUpdating ? .blue : .secondary
        )
      }
      .buttonStyle(.plain)
      .disabled(isUpdating)
    }
  }
}

private struct MetricTile: View {
  let title: String
  let value: String
  let subtitle: String
  let systemImage: String
  let accent: Color

  var body: some View {
    VStack(alignment: .leading, spacing: 5) {
      HStack(spacing: 6) {
        Image(systemName: systemImage)
          .font(.caption.weight(.semibold))
          .foregroundStyle(accent)
        Text(title)
          .font(.caption.weight(.medium))
          .foregroundStyle(.secondary)
          .lineLimit(1)
      }
      Text(value)
        .font(.system(size: 14, weight: .semibold))
        .foregroundStyle(.primary)
        .monospacedDigit()
        .lineLimit(1)
      Text(subtitle)
        .font(.caption2)
        .foregroundStyle(.tertiary)
        .lineLimit(1)
    }
    .padding(.horizontal, 13)
    .padding(.vertical, 11)
    .frame(maxWidth: .infinity, alignment: .leading)
    .background(
      TrawlDesign.surfaceBackground,
      in: RoundedRectangle(cornerRadius: TrawlDesign.cardCornerRadius, style: .continuous)
    )
    .overlay(
      RoundedRectangle(cornerRadius: TrawlDesign.cardCornerRadius, style: .continuous)
        .strokeBorder(TrawlDesign.hairlineBorder.opacity(0.8), lineWidth: 0.75)
    )
  }
}

private struct ArchiveAppCard: View {
  let trawler: RestingTrawler
  let health: TrawlerHealthState
  let onSelect: () -> Void
  let onUpdate: () -> Void
  let onUnlockFullDiskAccess: () -> Void

  @State private var isHovering = false

  private var isComingSoon: Bool {
    trawler.state == .comingSoon
  }

  private var primarySubtitle: String {
    if let counts = trawler.formattedRecordCountsSummary {
      return counts
    }
    switch health {
    case .needsFullDiskAccess:
      return "Grant Full Disk Access to index"
    case .needsSignIn:
      return "Sign in to sync this app"
    case .needsSignedApp:
      return "Signed app build needed for live sync"
    case .appClosed:
      return "Open \(trawler.registeredTrawlerDisplayName) to refresh"
    case .notInstalled:
      return HumanCopy.AppStatus.notInstalled
    case .comingSoon:
      return HumanCopy.AppStatus.comingSoon
    case .failed(let message):
      return message
    case .skipped(let reason):
      return reason
    case .updating:
      return "Updating local archive…"
    case .synced, .stale, .empty:
      return "No records archived yet"
    }
  }

  var body: some View {
    VStack(alignment: .leading, spacing: 10) {
      HStack(alignment: .center, spacing: 10) {
        TrawlerIconView(registeredTrawler: trawler.id, size: 32)
          .opacity(isComingSoon || health == .notInstalled ? 0.55 : 1)
        VStack(alignment: .leading, spacing: 2) {
          HStack(spacing: 5) {
            Text(trawler.registeredTrawlerDisplayName)
              .font(.system(size: 14, weight: .semibold))
              .foregroundStyle(.primary)
              .lineLimit(1)
            if BuiltInTrawlerRegistry.isExternalPlugin(trawler.id) {
              Text("Plugin")
                .font(.system(size: 9.5, weight: .semibold))
                .foregroundStyle(.secondary)
                .padding(.horizontal, 5)
                .padding(.vertical, 1)
                .background(Color.secondary.opacity(0.14), in: Capsule())
            }
          }
          StatusBadgePill(health: health)
        }
        Spacer(minLength: 4)
        if !isComingSoon {
          if health == .needsFullDiskAccess {
            Button(OperationalCopy.FullDiskAccess.unlockAction, action: onUnlockFullDiskAccess)
              .buttonStyle(.bordered)
              .controlSize(.mini)
          } else if health != .notInstalled {
            Button(action: onUpdate) {
              Image(systemName: "arrow.clockwise")
                .font(.caption.weight(.semibold))
                .foregroundStyle(.secondary)
                .frame(width: 24, height: 24)
                .background(
                  isHovering ? Color.primary.opacity(0.06) : Color.clear,
                  in: Circle()
                )
            }
            .buttonStyle(.plain)
            .help("Update \(trawler.registeredTrawlerDisplayName)")
            .disabled(health == .updating)
          }
        }
      }

      Text(primarySubtitle)
        .font(.callout)
        .foregroundStyle(trawler.formattedRecordCountsSummary != nil ? .primary : .secondary)
        .monospacedDigit()
        .lineLimit(2)
        .frame(maxWidth: .infinity, minHeight: 20, alignment: .leading)

      HStack(alignment: .center, spacing: 8) {
        if let lastUpdated = trawler.lastUpdatedTime {
          Text("Updated \(lastUpdated.formatted(.relative(presentation: .named)))")
            .font(.caption2)
            .foregroundStyle(.tertiary)
            .lineLimit(1)
        } else if !isComingSoon {
          Text(
            trawler.accessModel == .zeroFullDiskAccess
              ? OperationalCopy.Home.zeroFDABadge
              : OperationalCopy.Home.macFDABadge
          )
          .font(.caption2)
          .foregroundStyle(.tertiary)
        }
        Spacer(minLength: 4)
        if trawler.lastUpdatedTime != nil {
          Text(
            trawler.accessModel == .zeroFullDiskAccess
              ? OperationalCopy.Home.zeroFDABadge
              : OperationalCopy.Home.macFDABadge
          )
          .font(.caption2)
          .foregroundStyle(.tertiary)
        }
      }
    }
    .padding(13)
    .frame(maxWidth: .infinity, alignment: .leading)
    .background(
      TrawlDesign.surfaceBackground,
      in: RoundedRectangle(cornerRadius: TrawlDesign.cardCornerRadius, style: .continuous)
    )
    .overlay(
      RoundedRectangle(cornerRadius: TrawlDesign.cardCornerRadius, style: .continuous)
        .strokeBorder(
          isHovering && !isComingSoon
            ? TrawlDesign.brandRed.opacity(0.38)
            : TrawlDesign.hairlineBorder.opacity(0.85),
          lineWidth: isHovering && !isComingSoon ? 1 : 0.75
        )
    )
    .contentShape(.rect)
    .onHover { hovering in
      isHovering = hovering
    }
    .onTapGesture {
      if !isComingSoon { onSelect() }
    }
    .accessibilityElement(children: .combine)
    .accessibilityLabel("\(trawler.registeredTrawlerDisplayName), \(primarySubtitle)")
    .accessibilityHint(isComingSoon ? "Coming soon" : "Search \(trawler.registeredTrawlerDisplayName)")
  }
}

private struct ScopedAppOverviewCard: View {
  let trawler: RestingTrawler
  let health: TrawlerHealthState
  let onUpdate: () -> Void
  let onUnlockFullDiskAccess: () -> Void

  var body: some View {
    VStack(alignment: .leading, spacing: 14) {
      HStack(spacing: 12) {
        TrawlerIconView(registeredTrawler: trawler.id, size: 42)
        VStack(alignment: .leading, spacing: 3) {
          Text(trawler.registeredTrawlerDisplayName)
            .font(.title3.weight(.semibold))
          StatusBadgePill(health: health)
        }
        Spacer()
        if health == .needsFullDiskAccess {
          Button(OperationalCopy.FullDiskAccess.open, action: onUnlockFullDiskAccess)
            .buttonStyle(.borderedProminent)
            .controlSize(.small)
        } else {
          Button("Update \(trawler.registeredTrawlerDisplayName)", action: onUpdate)
            .buttonStyle(.bordered)
            .controlSize(.small)
            .disabled(health == .updating)
        }
      }

      if !trawler.archiveContentCounts.isEmpty {
        HStack(spacing: 12) {
          ForEach(trawler.archiveContentCounts.filter { $0.archiveContentCount > 0 }.prefix(4)) { count in
            VStack(alignment: .leading, spacing: 2) {
              Text(count.archiveContentCount.formatted())
                .font(.title3.weight(.semibold).monospacedDigit())
              Text(count.archiveContentKindDisplayName)
                .font(.caption)
                .foregroundStyle(.secondary)
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 8)
            .background(TrawlDesign.elevatedSurface, in: RoundedRectangle(cornerRadius: 8, style: .continuous))
            .overlay(
              RoundedRectangle(cornerRadius: 8, style: .continuous)
                .strokeBorder(TrawlDesign.hairlineBorder.opacity(0.6), lineWidth: 0.5)
            )
          }
        }
      }

      if let privacy = trawler.privacyBoundary {
        VStack(alignment: .leading, spacing: 4) {
          Text("Privacy boundary")
            .font(.caption.weight(.semibold))
            .foregroundStyle(.secondary)
          Text(privacy.archiveContentReadByTrawler)
            .font(.caption)
            .foregroundStyle(.secondary)
          if !privacy.archiveContentThatLeavesMachine.isEmpty {
            Text(privacy.archiveContentThatLeavesMachine)
              .font(.caption)
              .foregroundStyle(.tertiary)
          }
        }
      }

      Text("Type in the Spotlight bar above to search \(trawler.registeredTrawlerDisplayName).")
        .font(.callout)
        .foregroundStyle(.secondary)
    }
    .padding(18)
    .frame(maxWidth: .infinity, alignment: .leading)
    .background(
      TrawlDesign.surfaceBackground,
      in: RoundedRectangle(cornerRadius: TrawlDesign.cardCornerRadius, style: .continuous)
    )
    .overlay(
      RoundedRectangle(cornerRadius: TrawlDesign.cardCornerRadius, style: .continuous)
        .strokeBorder(TrawlDesign.hairlineBorder, lineWidth: 0.75)
    )
  }
}

private struct StatusBadgePill: View {
  let health: TrawlerHealthState

  var body: some View {
    HStack(spacing: 5) {
      if case .updating = health {
        ProgressView()
          .controlSize(.mini)
      } else {
        Circle()
          .fill(TrawlerCardHealth.dotColor(for: health))
          .frame(width: 6, height: 6)
      }
      Text(health.badgeText)
        .font(.caption2.weight(.medium))
        .foregroundStyle(health.requiresUserAttention ? TrawlerCardHealth.dotColor(for: health) : .secondary)
    }
  }
}
