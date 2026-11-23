import AppKit
import SwiftUI
import TrawlClient
import TrawlCore

struct ArchiveSidebarView: View {
  let trawlers: [RestingTrawler]
  let selectedTrawlerID: RegisteredTrawlerIdentity?
  let totalRecordCount: UInt64
  let updateProgress: [RegisteredTrawlerIdentity: TrawlerArchiveUpdateProgressState]
  let unavailableTrawlers: Set<RegisteredTrawlerIdentity>
  let needsFullDiskAccessRecovery: Bool
  let buildIdentity: BuildIdentity
  let isExperimental: Bool
  let onSelectAll: () -> Void
  let onSelectTrawler: (RestingTrawler) -> Void
  let onUpdateTrawler: (RegisteredTrawlerIdentity) -> Void
  let onUnlockFullDiskAccess: () -> Void

  private var builtInTrawlers: [RestingTrawler] {
    trawlers.filter { !BuiltInTrawlerRegistry.isExternalPlugin($0.id) }
  }

  private var externalPluginTrawlers: [RestingTrawler] {
    trawlers.filter { BuiltInTrawlerRegistry.isExternalPlugin($0.id) }
  }

  var body: some View {
    VStack(spacing: 0) {
      SidebarBrandHeader(totalRecordCount: totalRecordCount)
      Divider()
      ScrollView {
        VStack(alignment: .leading, spacing: 14) {
          SidebarAllAppsRow(
            totalRecordCount: totalRecordCount,
            isSelected: selectedTrawlerID == nil,
            onSelect: onSelectAll
          )

          if !builtInTrawlers.isEmpty {
            SidebarTrawlerSection(
              title: OperationalCopy.Home.builtInAppsSection,
              trawlers: builtInTrawlers,
              selectedTrawlerID: selectedTrawlerID,
              updateProgress: updateProgress,
              unavailableTrawlers: unavailableTrawlers,
              onSelect: onSelectTrawler,
              onUpdate: onUpdateTrawler
            )
          }

          if !externalPluginTrawlers.isEmpty {
            SidebarTrawlerSection(
              title: OperationalCopy.Home.pluginsSection,
              trawlers: externalPluginTrawlers,
              selectedTrawlerID: selectedTrawlerID,
              updateProgress: updateProgress,
              unavailableTrawlers: unavailableTrawlers,
              onSelect: onSelectTrawler,
              onUpdate: onUpdateTrawler
            )
          }
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 12)
      }

      Divider()
      SidebarFooter(
        needsFullDiskAccessRecovery: needsFullDiskAccessRecovery,
        buildIdentity: buildIdentity,
        isExperimental: isExperimental,
        onUnlockFullDiskAccess: onUnlockFullDiskAccess
      )
    }
    .frame(width: TrawlDesign.sidebarWidth)
    .background(TrawlDesign.sidebarBackground)
  }
}

private struct SidebarBrandHeader: View {
  let totalRecordCount: UInt64

  var body: some View {
    HStack(spacing: 9) {
      Image(nsImage: NSApplication.shared.applicationIconImage)
        .resizable()
        .scaledToFit()
        .frame(width: 24, height: 24)
        .accessibilityHidden(true)
      Text("OpenTrawl")
        .font(.system(size: 14, weight: .semibold))
        .foregroundStyle(.primary)
      Spacer(minLength: 4)
      if totalRecordCount > 0 {
        Text(TrawlerRestingCopy.compactCount(totalRecordCount))
          .font(.caption2.weight(.semibold).monospacedDigit())
          .foregroundStyle(.secondary)
          .padding(.horizontal, 7)
          .padding(.vertical, 2.5)
          .background(TrawlDesign.surfaceBackground, in: Capsule())
          .overlay(Capsule().strokeBorder(TrawlDesign.hairlineBorder, lineWidth: 0.5))
      }
    }
    .padding(.horizontal, 14)
    .frame(height: 48)
  }
}

private struct SidebarAllAppsRow: View {
  let totalRecordCount: UInt64
  let isSelected: Bool
  let onSelect: () -> Void

  var body: some View {
    Button(action: onSelect) {
      HStack(spacing: 9) {
        Image(systemName: "square.grid.2x2.fill")
          .font(.system(size: 13, weight: .semibold))
          .foregroundStyle(isSelected ? TrawlDesign.brandRed : .secondary)
          .frame(width: 20, height: 20)
        Text(OperationalCopy.Home.allApps)
          .font(.system(size: 13, weight: isSelected ? .semibold : .medium))
          .foregroundStyle(.primary)
        Spacer(minLength: 6)
        if totalRecordCount > 0 {
          Text(TrawlerRestingCopy.compactCount(totalRecordCount))
            .font(.caption.monospacedDigit())
            .foregroundStyle(.secondary)
        }
      }
      .padding(.horizontal, 9)
      .padding(.vertical, 7)
      .frame(maxWidth: .infinity, alignment: .leading)
      .background(
        isSelected ? TrawlDesign.brandRed.opacity(0.12) : Color.clear,
        in: RoundedRectangle(cornerRadius: 8, style: .continuous)
      )
      .contentShape(.rect)
    }
    .buttonStyle(.plain)
    .accessibilityAddTraits(isSelected ? .isSelected : [])
  }
}

private struct SidebarTrawlerSection: View {
  let title: String
  let trawlers: [RestingTrawler]
  let selectedTrawlerID: RegisteredTrawlerIdentity?
  let updateProgress: [RegisteredTrawlerIdentity: TrawlerArchiveUpdateProgressState]
  let unavailableTrawlers: Set<RegisteredTrawlerIdentity>
  let onSelect: (RestingTrawler) -> Void
  let onUpdate: (RegisteredTrawlerIdentity) -> Void

  var body: some View {
    VStack(alignment: .leading, spacing: 3) {
      Text(title.uppercased())
        .font(.system(size: 10.5, weight: .semibold))
        .tracking(0.5)
        .foregroundStyle(.tertiary)
        .padding(.horizontal, 9)
        .padding(.bottom, 3)

      ForEach(trawlers) { trawler in
        let effectiveHealth = TrawlerCardHealth.resolve(
          for: trawler,
          progress: updateProgress[trawler.id],
          isUnavailableLocally: unavailableTrawlers.contains(trawler.id)
        )
        SidebarTrawlerRow(
          trawler: trawler,
          health: effectiveHealth,
          isSelected: selectedTrawlerID == trawler.id,
          onSelect: { onSelect(trawler) },
          onUpdate: { onUpdate(trawler.id) }
        )
      }
    }
  }
}

private struct SidebarTrawlerRow: View {
  let trawler: RestingTrawler
  let health: TrawlerHealthState
  let isSelected: Bool
  let onSelect: () -> Void
  let onUpdate: () -> Void

  private var isDisabled: Bool {
    trawler.state == .comingSoon
  }

  var body: some View {
    Button {
      if !isDisabled { onSelect() }
    } label: {
      HStack(spacing: 8) {
        TrawlerIconView(registeredTrawler: trawler.id, size: 20)
          .opacity(isDisabled || health == .notInstalled ? 0.55 : 1)
        Text(trawler.registeredTrawlerDisplayName)
          .font(.system(size: 13, weight: isSelected ? .semibold : .regular))
          .foregroundStyle(isDisabled ? .secondary : .primary)
          .lineLimit(1)
        Spacer(minLength: 4)
        SidebarTrailingIndicator(
          compactCount: trawler.compactRecordCount,
          health: health
        )
      }
      .padding(.horizontal, 9)
      .padding(.vertical, 6)
      .frame(maxWidth: .infinity, alignment: .leading)
      .background(
        isSelected ? TrawlDesign.brandRed.opacity(0.12) : Color.clear,
        in: RoundedRectangle(cornerRadius: 8, style: .continuous)
      )
      .contentShape(.rect)
    }
    .buttonStyle(.plain)
    .disabled(isDisabled)
    .contextMenu {
      if !isDisabled {
        Button("Search \(trawler.registeredTrawlerDisplayName)", action: onSelect)
        Button("Update \(trawler.registeredTrawlerDisplayName)", action: onUpdate)
      }
    }
    .help(trawler.detail ?? trawler.registeredTrawlerDisplayName)
    .accessibilityLabel(trawler.registeredTrawlerDisplayName)
    .accessibilityValue(trawler.compactRecordCount ?? health.badgeText)
    .accessibilityAddTraits(isSelected ? .isSelected : [])
  }
}

private struct SidebarTrailingIndicator: View {
  let compactCount: String?
  let health: TrawlerHealthState

  var body: some View {
    HStack(spacing: 5) {
      if case .updating = health {
        ProgressView()
          .controlSize(.mini)
      } else if let compactCount {
        Text(compactCount)
          .font(.caption2.monospacedDigit())
          .foregroundStyle(.secondary)
      } else if health.requiresUserAttention {
        Image(systemName: health == .needsFullDiskAccess ? "lock.fill" : "exclamationmark.circle.fill")
          .font(.caption2)
          .foregroundStyle(TrawlerCardHealth.dotColor(for: health))
      }
      Circle()
        .fill(TrawlerCardHealth.dotColor(for: health))
        .frame(width: 6, height: 6)
        .accessibilityHidden(true)
    }
  }
}

private struct SidebarFooter: View {
  let needsFullDiskAccessRecovery: Bool
  let buildIdentity: BuildIdentity
  let isExperimental: Bool
  let onUnlockFullDiskAccess: () -> Void

  var body: some View {
    VStack(alignment: .leading, spacing: 8) {
      if needsFullDiskAccessRecovery {
        Button(action: onUnlockFullDiskAccess) {
          HStack(spacing: 6) {
            Image(systemName: "lock.shield")
              .font(.caption.weight(.semibold))
              .foregroundStyle(.orange)
            Text("Unlock Messages & Notes")
              .font(.caption.weight(.medium))
              .foregroundStyle(.primary)
            Spacer(minLength: 4)
            Image(systemName: "chevron.right")
              .font(.caption2.weight(.semibold))
              .foregroundStyle(.tertiary)
          }
          .padding(.horizontal, 9)
          .padding(.vertical, 6)
          .background(Color.orange.opacity(0.09), in: RoundedRectangle(cornerRadius: 7, style: .continuous))
          .overlay(
            RoundedRectangle(cornerRadius: 7, style: .continuous)
              .strokeBorder(Color.orange.opacity(0.24), lineWidth: 0.75)
          )
        }
        .buttonStyle(.plain)
      }
      HStack(spacing: 6) {
        if isExperimental {
          Text("DEV")
            .font(.system(size: 9.5, weight: .bold, design: .monospaced))
            .foregroundStyle(TrawlDesign.brandRed)
            .padding(.horizontal, 5)
            .padding(.vertical, 1.5)
            .background(TrawlDesign.brandRed.opacity(0.12), in: Capsule())
        }
        Text(buildIdentity.displayName)
          .font(.caption2.monospaced())
          .foregroundStyle(.tertiary)
          .textSelection(.enabled)
        Spacer()
      }
    }
    .padding(.horizontal, 12)
    .padding(.vertical, 10)
  }
}
