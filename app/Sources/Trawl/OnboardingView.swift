import AppKit
import PermissionGuide
import SwiftUI
import TrawlClient
import TrawlCore

struct OnboardingView: View {
  let onboarding: OnboardingModel
  let appModel: AppModel
  let flags: AppFeatureFlags
  let appInstallations: MacAppInstallations
  let buildIdentity: BuildIdentity
  let aiInstruction: String
  let onFinish: () -> Void

  var body: some View {
    switch onboarding.stage {
    case .welcome:
      WelcomeStep(
        registeredTrawlerCatalog: appModel.registeredTrawlerCatalog,
        onContinue: {
          onboarding.showPermission(appModel: appModel)
        }
      )
    case .permission:
      PermissionStep(
        permissionCheck: onboarding.permissionCheck,
        permissionGuideIsOpen: onboarding.permissionGuideIsOpen,
        buildIdentity: buildIdentity,
        onBack: onboarding.showWelcome,
        onOpenSettings: {
          onboarding.requestPermission(appModel: appModel) {
            refreshedTrawlersToUpdate()
          }
        },
        onCheckAgain: {
          onboarding.checkPermission(
            appModel: appModel, registeredTrawlers: refreshedTrawlersToUpdate)
        },
        onContinue: {
          onboarding.continueWithVerifiedAccess(
            appModel: appModel,
            registeredTrawlers: refreshedTrawlersToUpdate
          )
        }
      )
    case .building:
      BuildStep(
        appModel: appModel,
        appInstallations: appInstallations,
        aiInstruction: aiInstruction,
        hasCopiedAIInstructions: onboarding.hasCopiedAIInstructions,
        onCopyAIInstructions: onboarding.didCopyAIInstructions,
        onBack: { onboarding.showPermission(appModel: appModel) },
        onRetryApp: {
          onboarding.retry(appModel: appModel, registeredTrawler: $0)
        },
        onRetryInitialLoad: {
          onboarding.retryInitialLoad(appModel: appModel) {
            refreshedTrawlersToUpdate()
          }
        },
        onPermissionRecovery: {
          onboarding.reopenPermissionRecovery(appModel: appModel) {
            refreshedTrawlersToUpdate()
          }
        },
        onStop: onboarding.stopUpdate,
        onFinish: onboarding.showCommandDemo
      )
      .task(id: reportedTrawlers) {
        onboarding.resumeInitialUpdateIfNeeded(
          appModel: appModel,
          registeredTrawlers: refreshedTrawlersToUpdate()
        )
      }
    case .commandDemo:
      OpenTrawlCommandDemoView(
        helperURL: TrawlRuntimeConfiguration().helperURL,
        onBack: onboarding.returnToBuilding,
        onFinish: onFinish
      )
    case .complete:
      EmptyView()
    }
  }

  private var reportedTrawlers: [RegisteredTrawlerIdentity] {
    appModel.displayedTrawlers
  }

  private func refreshedTrawlersToUpdate() -> [RegisteredTrawlerIdentity] {
    appInstallations.refresh(
      registeredTrawlerCatalog: appModel.registeredTrawlerCatalog
    )
    return flags.trawlersToUpdate(
      reportedTrawlers: appModel.updateCandidateTrawlers,
      unavailableTrawlers: appInstallations.unavailableTrawlers
    )
  }

}

struct WelcomeStep: View {
  var icon = NSApplication.shared.applicationIconImage
  let registeredTrawlerCatalog: [RegisteredTrawlerCatalogEntry]
  let onContinue: () -> Void

  var body: some View {
    TrawlFlowScaffold(
      page: .welcome,
      composition: .centred,
      contentWidth: TrawlDesign.onboardingPageWidth
    ) {
      OnboardingHeroLayout {
        VStack(spacing: 0) {
          WelcomeMark(icon: icon)
          OnboardingProse(
            title: HumanCopy.Welcome.title,
            lede: HumanCopy.Welcome.body,
            statement: HumanCopy.Welcome.privacy,
            centred: true
          )
          .padding(.top, 24)
          WelcomeAppIconRow(registeredTrawlerCatalog: registeredTrawlerCatalog)
            .padding(.top, TrawlDesign.onboardingBlockSpacing)
        }
      }
    } actions: {
      OnboardingActionRow(
        backAction: nil,
        secondaryTitle: nil,
        secondaryAction: nil,
        primaryTitle: HumanCopy.Welcome.primaryAction,
        primaryAction: onContinue
      )
    }
  }
}

private struct WelcomeAppIconRow: View {
  let registeredTrawlerCatalog: [RegisteredTrawlerCatalogEntry]

  private var searchableEntries: [RegisteredTrawlerCatalogEntry] {
    registeredTrawlerCatalog.filter {
      $0.registeredTrawlerReleaseState != .comingSoon
    }
  }

  private var comingSoonEntries: [RegisteredTrawlerCatalogEntry] {
    registeredTrawlerCatalog.filter {
      $0.registeredTrawlerReleaseState == .comingSoon
    }
  }

  var body: some View {
    VStack(spacing: TrawlDesign.onboardingElementSpacing) {
      Text(HumanCopy.Welcome.appsTitle)
        .trawlText(.sectionHeader)
      HStack(spacing: 32) {
        HStack(spacing: 20) {
          ForEach(searchableEntries, id: \.id) { entry in
            WelcomeAppIcon(entry: entry)
          }
        }
        if !comingSoonEntries.isEmpty {
          HStack(spacing: 20) {
            ForEach(comingSoonEntries, id: \.id) { entry in
              WelcomeAppIcon(entry: entry)
            }
          }
        }
      }
    }
  }
}

private struct WelcomeAppIcon: View {
  let entry: RegisteredTrawlerCatalogEntry

  private var isComingSoon: Bool {
    entry.registeredTrawlerReleaseState == .comingSoon
  }

  private var appName: String {
    entry.registeredTrawlerManifest.registeredTrawlerDisplayName
  }

  var body: some View {
    TrawlerIconView(registeredTrawler: entry.id, size: 48)
      .overlay(alignment: .bottomTrailing) {
        if isComingSoon {
          Image(systemName: "clock.fill")
            .font(.system(size: 9, weight: .semibold))
            .foregroundStyle(.white)
            .frame(width: 17, height: 17)
            .background(.secondary, in: Circle())
            .overlay(Circle().stroke(Color(nsColor: .windowBackgroundColor), lineWidth: 2))
            .offset(x: 3, y: 3)
            .accessibilityHidden(true)
        }
      }
      .opacity(isComingSoon ? 0.72 : 1)
      .help(isComingSoon ? "\(appName) · \(HumanCopy.AppStatus.comingSoon)" : appName)
      .accessibilityLabel(appName)
      .accessibilityValue(isComingSoon ? HumanCopy.AppStatus.comingSoon : "")
  }
}

private struct WelcomeMark: View {
  let icon: NSImage?

  var body: some View {
    Image(
      nsImage: icon ?? NSImage(systemSymbolName: "shippingbox.fill", accessibilityDescription: nil)!
    )
    .resizable()
    .scaledToFit()
    .frame(width: TrawlDesign.onboardingHeroIcon, height: TrawlDesign.onboardingHeroIcon)
    .accessibilityHidden(true)
  }
}

struct PermissionStep: View {
  @State private var copiedAuditPrompt = false

  var icon = NSApplication.shared.applicationIconImage
  let permissionCheck: PermissionCheckState
  var permissionGuideIsOpen = false
  let buildIdentity: BuildIdentity
  let onBack: () -> Void
  let onOpenSettings: () -> Void
  let onCheckAgain: () -> Void
  let onContinue: () -> Void

  var body: some View {
    TrawlFlowScaffold(
      page: .access,
      contentWidth: TrawlDesign.onboardingPageWidth
    ) {
      OnboardingTaskLayout {
        OnboardingProse(
          title: HumanCopy.FullDiskAccess.title,
          lede: HumanCopy.FullDiskAccess.body,
          statement: HumanCopy.FullDiskAccess.purpose
        )
      } task: {
        VStack(alignment: .leading, spacing: 20) {
          PermissionDragDemonstration(
            icon: icon ?? NSWorkspace.shared.icon(forFile: Bundle.main.bundlePath),
            appName: applicationNameInSystemSettings(),
            tileIsInSystemSettings: permissionGuideIsOpen,
            accessIsConfirmed: permissionCheck == .confirmed
          )
          PermissionStatus(state: permissionCheck)
            .frame(
              height: TrawlDesign.permissionStateSlotHeight,
              alignment: .topLeading
            )
        }
      } support: {
        OnboardingInformationGroup(title: HumanCopy.FullDiskAccess.trustGroupTitle) {
          TrustReview(
            buildIdentity: buildIdentity,
            copiedAuditPrompt: $copiedAuditPrompt
          )
        }
      }
    } actions: {
      OnboardingActionRow(
        backAction: onBack,
        secondaryTitle: nil,
        secondaryAction: nil,
        primaryTitle: permissionCheck == .confirmed
          ? OperationalCopy.SharedAction.continueAction
          : HumanCopy.FullDiskAccess.openAction,
        primaryAction: permissionCheck == .confirmed ? onContinue : onOpenSettings
      )
    }
  }
}

/// A miniature of System Settings as the person will see it: the Full Disk
/// Access list with an empty slot, and the docked guide below it holding the
/// app tile. A pointer drags the tile up, it lands flush in the slot, and its
/// switch turns on. Every shape sits on one grid so the tile is exactly a row.
private struct PermissionDragDemonstration: View {
  private enum Beat: CaseIterable {
    case resting, pointerOnTile, lifted, inSlot, switchedOn
  }

  private static let width: CGFloat = 300
  private static let inset: CGFloat = 10
  private static let rowHeight: CGFloat = 32
  private static let titleBarHeight: CGFloat = 26
  private static let slotTop = titleBarHeight + 2 * rowHeight + 6
  private static let guideTop = slotTop + rowHeight + 14
  private static let tileTopInGuide = guideTop + 24
  private static let height = tileTopInGuide + rowHeight + 12
  private static let rowWidth = width - 2 * inset

  @Environment(\.accessibilityReduceMotion) private var reduceMotion
  @State private var beat: Beat = .resting
  @State private var tileIsFadedForReset = false

  let icon: NSImage
  let appName: String
  /// The guide has opened in System Settings and the tile flew there, so the
  /// page's socket stays empty and the loop rests.
  let tileIsInSystemSettings: Bool
  /// Access landed: the page shows the tile switched on in the list.
  let accessIsConfirmed: Bool

  var body: some View {
    HStack(alignment: .center, spacing: 32) {
      miniatureSettingsWindow
      VStack(alignment: .leading, spacing: 8) {
        Text(HumanCopy.FullDiskAccess.systemSettingsLabel)
          .trawlText(.sectionHeader)
        Text(HumanCopy.FullDiskAccess.addAppStep(appName: appName))
          .trawlText(.body)
          .foregroundStyle(.secondary)
          .fixedSize(horizontal: false, vertical: true)
      }
      .frame(maxWidth: .infinity, alignment: .leading)
    }
    .padding(24)
    .frame(maxWidth: .infinity)
    .background(Color.secondary.opacity(0.055), in: RoundedRectangle(cornerRadius: 14, style: .continuous))
    .task(id: StoryboardMode(reduceMotion: reduceMotion, tileIsInSystemSettings: tileIsInSystemSettings, accessIsConfirmed: accessIsConfirmed)) {
      await playStoryboard()
    }
    .accessibilityElement(children: .ignore)
    .accessibilityLabel(HumanCopy.FullDiskAccess.dragAccessibilityLabel(appName: appName))
  }

  private var tileIsInSlot: Bool { beat == .inSlot || beat == .switchedOn }
  private var tileIsHeld: Bool { beat == .lifted }

  private var miniatureSettingsWindow: some View {
    ZStack(alignment: .topLeading) {
      titleBar
      placeholderRow(barWidth: 120, switchedOn: true)
        .offset(x: Self.inset, y: Self.titleBarHeight)
      placeholderRow(barWidth: 90, switchedOn: false)
        .offset(x: Self.inset, y: Self.titleBarHeight + Self.rowHeight)
      emptySlot
        .offset(x: Self.inset, y: Self.slotTop)
      dockedGuide
        .offset(x: Self.inset - 4, y: Self.guideTop)
      movingTile
      pointer
    }
    .frame(width: Self.width, height: Self.height, alignment: .topLeading)
    .background(Color(nsColor: .windowBackgroundColor), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
    .overlay(RoundedRectangle(cornerRadius: 10, style: .continuous).strokeBorder(.primary.opacity(0.1)))
    .shadow(color: .black.opacity(0.08), radius: 6, y: 2)
  }

  private var titleBar: some View {
    HStack(spacing: 5) {
      ForEach([Color.red, .yellow, .green], id: \.self) { colour in
        Circle().fill(colour.opacity(0.75)).frame(width: 7, height: 7)
      }
      Text(HumanCopy.FullDiskAccess.listName)
        .font(.system(size: 10, weight: .semibold))
        .foregroundStyle(.secondary)
        .padding(.leading, 6)
    }
    .padding(.horizontal, Self.inset)
    .frame(width: Self.width, height: Self.titleBarHeight, alignment: .leading)
  }

  private func placeholderRow(barWidth: CGFloat, switchedOn: Bool) -> some View {
    HStack(spacing: 10) {
      RoundedRectangle(cornerRadius: 4, style: .continuous)
        .fill(Color.secondary.opacity(0.18))
        .frame(width: 18, height: 18)
      Capsule()
        .fill(Color.secondary.opacity(0.16))
        .frame(width: barWidth, height: 6)
      Spacer(minLength: 0)
      miniatureSwitch(on: switchedOn)
    }
    .padding(.horizontal, 10)
    .frame(width: Self.rowWidth, height: Self.rowHeight)
    .overlay(alignment: .bottom) { Divider() }
  }

  private func miniatureSwitch(on: Bool) -> some View {
    Capsule()
      .fill(on ? Color.accentColor : Color.secondary.opacity(0.22))
      .frame(width: 24, height: 14)
      .overlay(alignment: on ? .trailing : .leading) {
        Circle().fill(.white).frame(width: 11, height: 11).padding(1.5)
      }
  }

  /// Dashed while waiting; it lights up as the tile comes near.
  private var emptySlot: some View {
    RoundedRectangle(cornerRadius: 8, style: .continuous)
      .strokeBorder(
        tileIsHeld ? Color.accentColor : Color.secondary.opacity(0.35),
        style: StrokeStyle(lineWidth: tileIsHeld ? 1.5 : 1, dash: [4])
      )
      .background(
        Color.accentColor.opacity(tileIsHeld ? 0.06 : 0),
        in: RoundedRectangle(cornerRadius: 8, style: .continuous)
      )
      .frame(width: Self.rowWidth, height: Self.rowHeight)
      .opacity(tileIsInSlot && !tileIsFadedForReset ? 0 : 1)
  }

  /// The guide as it docks at the bottom of System Settings, with the
  /// tile's socket left empty once the tile is picked up.
  private var dockedGuide: some View {
    VStack(alignment: .leading, spacing: 6) {
      Text("↑  " + HumanCopy.FullDiskAccess.dragInstruction(appName: appName))
        .font(.system(size: 9.5, weight: .medium))
        .foregroundStyle(.secondary)
        .lineLimit(1)
      RoundedRectangle(cornerRadius: 8, style: .continuous)
        .fill(Color.secondary.opacity(0.06))
        .frame(width: Self.rowWidth, height: Self.rowHeight)
        .background(ApplicationTileFlightOrigin())
    }
    .padding(.horizontal, 4)
    .padding(.top, 6)
    .padding(.bottom, 4)
    .frame(width: Self.rowWidth + 8, alignment: .leading)
    .background(Color.secondary.opacity(0.05), in: RoundedRectangle(cornerRadius: 10, style: .continuous))
  }

  private var movingTile: some View {
    ApplicationRowTile(
      icon: icon, appName: appName, width: Self.rowWidth, height: Self.rowHeight,
      filled: !tileIsInSlot
    )
    .background(
      Color(nsColor: .windowBackgroundColor),
      in: RoundedRectangle(cornerRadius: 8, style: .continuous)
    )
      .overlay(alignment: .trailing) {
        if tileIsInSlot {
          miniatureSwitch(on: beat == .switchedOn)
            .padding(.trailing, 10)
            .transition(.opacity)
        }
      }
      .scaleEffect(tileIsHeld ? 1.03 : 1)
      .shadow(color: .black.opacity(tileIsHeld ? 0.18 : 0), radius: 8, y: 4)
      .offset(x: Self.inset, y: tileTop)
      .opacity(tileIsInSystemSettings && !accessIsConfirmed || tileIsFadedForReset ? 0 : 1)
  }

  private var tileTop: CGFloat {
    switch beat {
    case .resting, .pointerOnTile: Self.tileTopInGuide
    case .lifted: Self.slotTop
    case .inSlot, .switchedOn: Self.slotTop
    }
  }

  private var pointer: some View {
    let tileGrip = CGPoint(x: Self.inset + Self.rowWidth * 0.62, y: tileTop + Self.rowHeight * 0.55)
    let restingPoint = CGPoint(x: Self.width - 24, y: Self.height - 6)
    let point = beat == .resting ? restingPoint : tileGrip
    return Image(systemName: "cursorarrow")
      .font(.system(size: 15, weight: .regular))
      .foregroundStyle(.primary)
      .shadow(color: .white, radius: 1)
      .offset(x: point.x, y: point.y)
      .opacity(beat == .resting || beat == .switchedOn ? 0 : 1)
  }

  /// One loop: rest, point at the tile, carry it up, drop it in the slot,
  /// switch it on, hold. Reduce Motion shows the resting frame only.
  private func playStoryboard() async {
    tileIsFadedForReset = false
    if accessIsConfirmed {
      beat = .switchedOn
      return
    }
    beat = .resting
    guard !reduceMotion, !tileIsInSystemSettings else { return }
    while !Task.isCancelled {
      try? await Task.sleep(for: .seconds(1.0))
      withAnimation(.easeInOut(duration: 0.55)) { beat = .pointerOnTile }
      try? await Task.sleep(for: .seconds(0.7))
      withAnimation(.easeInOut(duration: 0.75)) { beat = .lifted }
      try? await Task.sleep(for: .seconds(0.85))
      withAnimation(.spring(duration: 0.3, bounce: 0)) { beat = .inSlot }
      try? await Task.sleep(for: .seconds(0.5))
      withAnimation(.easeInOut(duration: 0.25)) { beat = .switchedOn }
      try? await Task.sleep(for: .seconds(2.0))
      // Fade out in the list and back in at the guide, so the tile never
      // slides back down with its switch still showing.
      withAnimation(.easeOut(duration: 0.25)) { tileIsFadedForReset = true }
      try? await Task.sleep(for: .seconds(0.3))
      beat = .resting
      withAnimation(.easeIn(duration: 0.3)) { tileIsFadedForReset = false }
    }
  }
}

private struct StoryboardMode: Equatable {
  let reduceMotion: Bool
  let tileIsInSystemSettings: Bool
  let accessIsConfirmed: Bool
}

private struct PermissionStatus: View {
  let state: PermissionCheckState

  var body: some View {
    HStack(alignment: .top, spacing: 10) {
      Image(systemName: symbol)
        .foregroundStyle(colour)
        .frame(width: 18)
        .accessibilityHidden(true)
      VStack(alignment: .leading, spacing: 5) {
        Text(label)
          .trawlText(.body)
        if state == .notConfirmed {
          Text(OperationalCopy.FullDiskAccess.instruction)
            .trawlText(.meta)
            .foregroundStyle(.secondary)
        }
      }
    }
  }

  private var label: String {
    switch state {
    case .idle: OperationalCopy.FullDiskAccess.idle
    case .checking: OperationalCopy.FullDiskAccess.checking
    case .confirmed: OperationalCopy.FullDiskAccess.confirmed
    case .notConfirmed: OperationalCopy.FullDiskAccess.notConfirmed
    }
  }

  private var symbol: String {
    switch state {
    case .idle: "lock.open"
    case .checking: "arrow.trianglehead.2.clockwise.rotate.90"
    case .confirmed: "checkmark.circle.fill"
    case .notConfirmed: "exclamationmark.triangle"
    }
  }

  private var colour: Color {
    switch state {
    case .confirmed: .green
    case .notConfirmed: .orange
    case .idle, .checking: .secondary
    }
  }
}

private struct TrustReview: View {
  let buildIdentity: BuildIdentity
  @Binding var copiedAuditPrompt: Bool

  var body: some View {
    VStack(alignment: .leading, spacing: 12) {
      Text(HumanCopy.FullDiskAccess.trustGroupBody)
        .trawlText(.body)
        .lineSpacing(2)
        .foregroundStyle(.secondary)
        .fixedSize(horizontal: false, vertical: true)
      HStack(spacing: 10) {
        Button {
          NSWorkspace.shared.open(BuildIdentity.repositoryURL)
        } label: {
          Label(
            HumanCopy.FullDiskAccess.readCodeAction,
            systemImage: "arrow.up.right.square"
          )
        }
        Button {
          NSPasteboard.general.clearContents()
          NSPasteboard.general.setString(
            AgentPrompts.auditBuild(buildIdentity),
            forType: .string
          )
          copiedAuditPrompt = true
        } label: {
          Label(
            copiedAuditPrompt
              ? HumanCopy.FullDiskAccess.copiedAuditPromptAction
              : HumanCopy.FullDiskAccess.copyAuditPromptAction,
            systemImage: copiedAuditPrompt ? "checkmark" : "doc.on.doc"
          )
        }
        .disabled(copiedAuditPrompt)
      }
      .controlSize(.small)
      .buttonStyle(.bordered)
      .buttonBorderShape(.capsule)
      .tint(.primary)
    }
  }
}

struct BuildStep: View {
  let appModel: AppModel
  let appInstallations: MacAppInstallations
  let aiInstruction: String
  let hasCopiedAIInstructions: Bool
  let onCopyAIInstructions: () -> Void
  let onBack: () -> Void
  let onRetryApp: (RegisteredTrawlerIdentity) -> Void
  let onRetryInitialLoad: () -> Void
  let onPermissionRecovery: () -> Void
  let onStop: () -> Void
  let onFinish: () -> Void

  private var canFinishSetup: Bool {
    !appModel.isUpdating
      && !hasGlobalPermissionFailure
      && (hasSearchableArchive || hasNoAvailableApps)
  }

  private var hasSearchableArchive: Bool {
    appModel.trawlerStatuses.contains { trawlerStatus in
      trawlerStatus.archiveContentCountsAfterLastSuccessfullyCompletedUpdate.contains {
        $0.archiveContentCount > 0
      }
    }
      || appModel.trawlerArchiveUpdateResults.contains { trawlerArchiveUpdateResult in
        !appModel.updateOperationFailures.contains {
          $0.failedTrawler == trawlerArchiveUpdateResult.registeredTrawler
        }
      }
  }

  private var hasNoAvailableApps: Bool {
    let activeTrawlers =
      appModel.trawlerStatuses.map(\.id)
      + appModel.statusOperationFailures.map(\.failedTrawler)
    let statusIsSettled =
      appModel.phase == .ready
      || (appModel.phase == .partial && appModel.statusOperationFailures.isEmpty)
    return statusIsSettled
      && activeTrawlers.allSatisfy { !appInstallations.isAvailable($0) }
  }

  private var hasGlobalPermissionFailure: Bool {
    appModel.needsFullDiskAccessRecovery
  }

  private var archiveIsReady: Bool {
    canFinishSetup && !appModel.isUpdating
  }

  var body: some View {
    TrawlFlowScaffold(page: .archive) {
      VStack(alignment: .leading, spacing: TrawlDesign.onboardingSectionSpacing) {
        OnboardingProse(
          title: archiveIsReady
            ? HumanCopy.ArchiveBuild.readyTitle
            : HumanCopy.ArchiveBuild.title,
          lede: archiveIsReady
            ? HumanCopy.ArchiveBuild.readyBody
            : HumanCopy.ArchiveBuild.body
        )
        .frame(height: 100, alignment: .topLeading)
        AIConnectionPanel(
          hasCopied: hasCopiedAIInstructions,
          onCopy: copyAIInstructions
        )
        ArchiveBuildStatus(
          appModel: appModel,
          appInstallations: appInstallations,
          comingSoonEntries: comingSoonEntries,
          hasGlobalPermissionFailure: hasGlobalPermissionFailure,
          onRetryApp: onRetryApp,
          onRetryInitialLoad: onRetryInitialLoad,
          onPermissionRecovery: onPermissionRecovery
        )
      }
    } actions: {
      OnboardingActionRow(
        backAction: onBack,
        secondaryTitle: appModel.isUpdating ? OperationalCopy.SharedAction.cancel : nil,
        secondaryAction: appModel.isUpdating ? onStop : nil,
        primaryTitle: HumanCopy.ArchiveBuild.startSearchingAction,
        primaryAction: onFinish,
        primaryDisabled: !canFinishSetup
      )
    }
  }

  private func copyAIInstructions() {
    NSPasteboard.general.clearContents()
    NSPasteboard.general.setString(aiInstruction, forType: .string)
    onCopyAIInstructions()
  }

  private var comingSoonEntries: [RegisteredTrawlerCatalogEntry] {
    appModel.registeredTrawlerCatalog.filter {
      $0.registeredTrawlerReleaseState == .comingSoon
    }
  }
}

private struct ArchiveBuildStatus: View {
  let appModel: AppModel
  let appInstallations: MacAppInstallations
  let comingSoonEntries: [RegisteredTrawlerCatalogEntry]
  let hasGlobalPermissionFailure: Bool
  let onRetryApp: (RegisteredTrawlerIdentity) -> Void
  let onRetryInitialLoad: () -> Void
  let onPermissionRecovery: () -> Void

  var body: some View {
    if hasGlobalPermissionFailure {
      PermissionRecoveryBanner(action: onPermissionRecovery)
    } else if appModel.blockingFailureMessage != nil, appModel.displayedTrawlers.isEmpty {
      InitialLoadRecovery(action: onRetryInitialLoad)
    } else {
      ArchiveTrawlerSummary(
        appModel: appModel,
        appInstallations: appInstallations,
        comingSoonEntries: comingSoonEntries,
        onRetryApp: onRetryApp
      )
    }
  }
}

private struct ArchiveTrawlerSummary: View {
  let appModel: AppModel
  let appInstallations: MacAppInstallations
  let comingSoonEntries: [RegisteredTrawlerCatalogEntry]
  let onRetryApp: (RegisteredTrawlerIdentity) -> Void

  private var availableTrawlers: [RegisteredTrawlerIdentity] {
    appModel.displayedTrawlers.filter {
      appModel.catalogEntry(for: $0)?.registeredTrawlerReleaseState != .comingSoon
    }
  }

  private var presentations: [(RegisteredTrawlerIdentity, AppBuildRowPresentation)] {
    availableTrawlers.map { ($0, presentation(for: $0)) }
  }

  private var searchableCount: Int {
    presentations.count { $0.1.status == .success || $0.1.status == .warning }
  }

  var body: some View {
    VStack(alignment: .leading, spacing: 10) {
      HStack(alignment: .firstTextBaseline) {
        Text(HumanCopy.ArchiveBuild.yourAppsTitle)
          .trawlText(.sectionHeader)
        Spacer()
        ArchiveProgressSummary(
          searchableCount: searchableCount,
          totalCount: presentations.count
        )
      }
      if !availableTrawlers.isEmpty {
        AppBuildList(
          registeredTrawlers: availableTrawlers,
          comingSoonEntries: [],
          appModel: appModel,
          appInstallations: appInstallations,
          suppressPermissionFailures: false,
          onRetryApp: onRetryApp
        )
      }
      if !comingSoonEntries.isEmpty {
        Text(HumanCopy.ArchiveBuild.moreAppsTitle)
          .trawlText(.sectionHeader)
          .padding(.top, TrawlDesign.onboardingSubgroupSpacing)
        AppBuildList(
          registeredTrawlers: [],
          comingSoonEntries: comingSoonEntries,
          appModel: appModel,
          appInstallations: appInstallations,
          suppressPermissionFailures: false,
          onRetryApp: onRetryApp
        )
      }
    }
  }

  private func presentation(
    for registeredTrawler: RegisteredTrawlerIdentity
  ) -> AppBuildRowPresentation {
    let trawlerStatus = appModel.trawlerStatuses.first {
      $0.id == registeredTrawler
    }
    let updateFailure = appModel.updateOperationFailures.first {
      $0.failedTrawler == registeredTrawler
    }
    let failure = updateFailure
      ?? appModel.statusOperationFailures.first {
        $0.failedTrawler == registeredTrawler
      }
    let skipped = appModel.trawlersSkippedFromStatus.first {
      $0.skippedTrawler == registeredTrawler
    }
    let catalogEntry = appModel.catalogEntry(for: registeredTrawler)
    return AppBuildRowPresentation.resolve(
      name: catalogEntry?.registeredTrawlerManifest.registeredTrawlerDisplayName
        ?? trawlerStatus?.registeredTrawlerManifest.registeredTrawlerDisplayName
        ?? failure?.registeredTrawlerDisplayName
        ?? skipped?.registeredTrawlerDisplayName
        ?? registeredTrawler.registeredTrawlerIdentity,
      counts:
        trawlerStatus?.archiveContentCountsAfterLastSuccessfullyCompletedUpdate ?? [],
      progress: appModel.updateProgress[registeredTrawler],
      failure: failure,
      archiveUpdateFailed: updateFailure != nil,
      skipped: skipped,
      releaseState: catalogEntry?.registeredTrawlerReleaseState,
      isInstalled: appInstallations.isAvailable(registeredTrawler),
      suppressPermissionFailure: false
    )
  }
}

private struct ArchiveProgressSummary: View {
  let searchableCount: Int
  let totalCount: Int

  var body: some View {
    Text(summary)
      .trawlText(.meta)
      .foregroundStyle(.secondary)
  }

  private var summary: String {
    String(format: HumanCopy.ArchiveBuild.progressFormat, searchableCount, totalCount)
  }
}

private struct InitialLoadRecovery: View {
  let action: () -> Void

  var body: some View {
    ContentUnavailableView {
      Label(OperationalCopy.AppStatus.appsUnavailable, systemImage: "exclamationmark.triangle")
    } description: {
      Text(OperationalCopy.AppStatus.statusCheckFailed)
      Text(OperationalCopy.AppStatus.statusCheckRecovery)
    } actions: {
      Button(OperationalCopy.SharedAction.retry, action: action)
        .buttonStyle(.borderedProminent)
    }
  }
}

private struct AIConnectionPanel: View {
  let hasCopied: Bool
  let onCopy: () -> Void

  var body: some View {
    VStack(alignment: .leading, spacing: 8) {
      Text(HumanCopy.ConnectAI.title)
        .trawlText(.sectionHeader)
      Text(HumanCopy.ConnectAI.body)
        .trawlText(.body)
        .foregroundStyle(.secondary)
        .fixedSize(horizontal: false, vertical: true)
      copyButton
    }
  }

  private var copyButton: some View {
    Button(action: onCopy) {
      Label(
        HumanCopy.ConnectAI.copyAction,
        systemImage: hasCopied ? "checkmark" : "doc.on.doc"
      )
    }
    .buttonStyle(.bordered)
    .buttonBorderShape(.capsule)
    .controlSize(.small)
    .tint(.primary)
  }
}

struct PermissionRecoveryBanner: View {
  let action: () -> Void

  var body: some View {
    HStack(spacing: 12) {
      Image(systemName: "lock.trianglebadge.exclamationmark")
        .foregroundStyle(.orange)
      VStack(alignment: .leading, spacing: 3) {
        Text(OperationalCopy.FullDiskAccess.needed)
          .trawlText(.sectionHeader)
        Text(OperationalCopy.FullDiskAccess.instruction)
          .trawlText(.body)
          .foregroundStyle(.secondary)
      }
      Spacer()
      Button(OperationalCopy.FullDiskAccess.open, action: action)
    }
    .padding(16)
    .background(.orange.opacity(0.08), in: RoundedRectangle(cornerRadius: 10))
  }
}

private struct AppBuildList: View {
  let registeredTrawlers: [RegisteredTrawlerIdentity]
  let comingSoonEntries: [RegisteredTrawlerCatalogEntry]
  let appModel: AppModel
  let appInstallations: MacAppInstallations
  let suppressPermissionFailures: Bool
  let onRetryApp: (RegisteredTrawlerIdentity) -> Void

  var body: some View {
    VStack(spacing: 0) {
      ForEach(registeredTrawlers, id: \.self) { registeredTrawler in
        let presentation = presentation(for: registeredTrawler)
        ArchiveAppRow(
          registeredTrawler: registeredTrawler,
          name: presentation.name,
          status: presentation.status,
          statusLabel: presentation.statusLabel,
          recoveryTitle: presentation.canRetry ? HumanCopy.AppStatus.retry : nil,
          recovery: presentation.canRetry ? { onRetryApp(registeredTrawler) } : nil,
          recoveryDisabled: appModel.isUpdating
        )
        Divider()
      }
      ForEach(comingSoonEntries, id: \.id) { entry in
        ArchiveAppRow(
          registeredTrawler: entry.id,
          name: entry.registeredTrawlerManifest.registeredTrawlerDisplayName,
          status: .neutral,
          statusLabel: HumanCopy.AppStatus.comingSoon,
          accessibilityStatus: HumanCopy.AppStatus.comingSoon,
          symbolOverride: "clock",
          recoveryTitle: nil,
          recovery: nil,
          recoveryDisabled: true
        )
        Divider()
      }
    }
  }

  private func presentation(
    for registeredTrawler: RegisteredTrawlerIdentity
  ) -> AppBuildRowPresentation {
    let trawlerStatus = appModel.trawlerStatuses.first {
      $0.id == registeredTrawler
    }
    let updateFailure = appModel.updateOperationFailures.first {
      $0.failedTrawler == registeredTrawler
    }
    let failure = updateFailure
      ?? appModel.statusOperationFailures.first {
        $0.failedTrawler == registeredTrawler
      }
    let skipped = appModel.trawlersSkippedFromStatus.first {
      $0.skippedTrawler == registeredTrawler
    }
    let catalogEntry = appModel.catalogEntry(for: registeredTrawler)
    return AppBuildRowPresentation.resolve(
      name: catalogEntry?.registeredTrawlerManifest.registeredTrawlerDisplayName
        ?? trawlerStatus?.registeredTrawlerManifest.registeredTrawlerDisplayName
        ?? failure?.registeredTrawlerDisplayName
        ?? skipped?.registeredTrawlerDisplayName
        ?? registeredTrawler.registeredTrawlerIdentity,
      counts:
        trawlerStatus?.archiveContentCountsAfterLastSuccessfullyCompletedUpdate ?? [],
      progress: appModel.updateProgress[registeredTrawler],
      failure: failure,
      archiveUpdateFailed: updateFailure != nil,
      skipped: skipped,
      releaseState: catalogEntry?.registeredTrawlerReleaseState,
      isInstalled: appInstallations.isAvailable(registeredTrawler),
      suppressPermissionFailure: suppressPermissionFailures
    )
  }
}

private struct ArchiveAppRow: View {
  let registeredTrawler: RegisteredTrawlerIdentity
  let name: String
  let status: TrawlStatus
  let statusLabel: String?
  var accessibilityStatus: String? = nil
  var symbolOverride: String? = nil
  let recoveryTitle: String?
  let recovery: (() -> Void)?
  let recoveryDisabled: Bool

  var body: some View {
    HStack(spacing: 8) {
      TrawlerIconView(
        registeredTrawler: registeredTrawler,
        size: 22)
      Text(name)
        .trawlText(.body)
      Spacer(minLength: 12)
      if status == .working {
        Image(systemName: "arrow.trianglehead.2.clockwise.rotate.90")
          .foregroundStyle(.secondary)
          .accessibilityHidden(true)
      } else if statusLabel != nil, status != .neutral || symbolOverride != nil {
        Image(systemName: symbolOverride ?? status.symbol)
          .foregroundStyle(status.colour)
          .accessibilityHidden(true)
      }
      if let statusLabel {
        Text(statusLabel)
          .trawlText(.meta)
          .foregroundStyle(status == .success ? Color.secondary : status.colour)
      }
      if let recoveryTitle, let recovery {
        Button(recoveryTitle, action: recovery)
          .controlSize(.small)
          .disabled(recoveryDisabled)
      }
    }
    .frame(
      maxWidth: .infinity,
      minHeight: TrawlDesign.onboardingRowHeight,
      alignment: .leading
    )
    .accessibilityElement(children: .combine)
    .accessibilityLabel(name)
    .accessibilityValue(accessibilityStatus ?? statusLabel ?? "")
  }
}

struct AppBuildRowPresentation: Equatable {
  let name: String
  let status: TrawlStatus
  let statusLabel: String
  let canRetry: Bool

  static func resolve(
    name: String,
    counts: [ArchiveContentCountAfterLastSuccessfullyCompletedUpdate],
    progress: TrawlerArchiveUpdateProgressState?,
    failure: TrawlerOperationFailure?,
    archiveUpdateFailed: Bool,
    skipped: TrawlerSkippedFromOperation?,
    releaseState: RegisteredTrawlerReleaseState? = nil,
    isInstalled: Bool,
    suppressPermissionFailure: Bool
  ) -> AppBuildRowPresentation {
    if releaseState == .comingSoon {
      return AppBuildRowPresentation(
        name: name, status: .neutral,
        statusLabel: HumanCopy.AppStatus.comingSoon, canRetry: false
      )
    }
    if skipped != nil {
      return AppBuildRowPresentation(
        name: name, status: .neutral,
        statusLabel: OperationalCopy.AppStatus.notAvailable, canRetry: false
      )
    }
    guard isInstalled else {
      return AppBuildRowPresentation(
        name: name, status: .neutral,
        statusLabel: HumanCopy.AppStatus.notInstalled, canRetry: false
      )
    }
    if suppressPermissionFailure, failure?.failureCode == .permission {
      return AppBuildRowPresentation(
        name: name, status: .neutral,
        statusLabel: OperationalCopy.AppStatus.waiting, canRetry: false
      )
    }
    if case .building = progress {
      return AppBuildRowPresentation(
        name: name, status: .working,
        statusLabel: OperationalCopy.AppStatus.building, canRetry: false
      )
    }
    if case .finalising = progress {
      return AppBuildRowPresentation(
        name: name, status: .working,
        statusLabel: OperationalCopy.AppStatus.finalising, canRetry: false
      )
    }
    if let failure {
      let hasArchive = counts.contains { $0.archiveContentCount > 0 }
      if hasArchive {
        return AppBuildRowPresentation(
          name: name,
          status: .success,
          statusLabel: archiveUpdateFailed
            ? OperationalCopy.AppStatus.searchableWithFailedUpdate
            : OperationalCopy.AppStatus.searchable,
          canRetry: false
        )
      }
      return AppBuildRowPresentation(
        name: name,
        status: .failure,
        statusLabel: archiveUpdateFailed
          ? OperationalCopy.AppStatus.updateFailed
          : OperationalCopy.AppStatus.notSearchable,
        canRetry: failure.failureCode != .authentication
          && failure.failureCode != .invalidInput
      )
    }
    if case .failed = progress {
      let hasArchive = counts.contains { $0.archiveContentCount > 0 }
      return AppBuildRowPresentation(
        name: name,
        status: hasArchive ? .success : .failure,
        statusLabel: hasArchive
          ? OperationalCopy.AppStatus.searchableWithFailedUpdate
          : OperationalCopy.AppStatus.updateFailed,
        canRetry: true
      )
    }
    if case .finished = progress {
      return AppBuildRowPresentation(
        name: name, status: .success,
        statusLabel: OperationalCopy.AppStatus.searchable,
        canRetry: false
      )
    }
    if counts.contains(where: { $0.archiveContentCount > 0 }) {
      return AppBuildRowPresentation(
        name: name, status: .success,
        statusLabel: OperationalCopy.AppStatus.searchable, canRetry: false
      )
    }
    return AppBuildRowPresentation(
      name: name, status: .neutral,
      statusLabel: OperationalCopy.AppStatus.waiting, canRetry: false
    )
  }
}
