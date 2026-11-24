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
  var connectionStore: ArchiveConnectionStore? = nil
  var onConnectedToRemoteArchive: (() -> Void)? = nil
  var onSignInGmail: (() -> Void)? = nil
  var onSignInX: (() -> Void)? = nil
  var onOpenSourceApp: ((String) -> Void)? = nil
  let onFinish: () -> Void

  @State private var isShowingRemoteConnectionSheet = false

  var body: some View {
    Group {
      switch onboarding.stage {
      case .welcome:
        WelcomeStep(
          registeredTrawlerCatalog: appModel.registeredTrawlerCatalog,
          secondaryTitle: connectionStore != nil
            ? "Search an archive on another Mac…"
            : nil,
          onSecondaryAction: connectionStore != nil
            ? { isShowingRemoteConnectionSheet = true }
            : nil,
          onContinue: {
            onboarding.showChooseApps()
          }
        )
      case .chooseApps:
        ChooseAppsStep(
          initialChoice: appModel.fullDiskAccessChoice,
          buildIdentity: buildIdentity,
          showsRemoteArchiveLink: connectionStore != nil,
          onBack: onboarding.showWelcome,
          onConnectRemoteArchive: { isShowingRemoteConnectionSheet = true },
          onIncludeFullDiskAccess: {
            onboarding.includeFullDiskAccessApps(appModel: appModel)
          },
          onLeaveOutFullDiskAccess: {
            onboarding.leaveOutFullDiskAccessApps(
              appModel: appModel,
              registeredTrawlers: refreshedTrawlersToUpdate
            )
          }
        )
      case .permission:
        PermissionStep(
          permissionCheck: onboarding.permissionCheck,
          permissionGuideIsOpen: onboarding.permissionGuideIsOpen,
          buildIdentity: buildIdentity,
          onBack: onboarding.showChooseApps,
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
          },
          onContinueWithoutAccess: {
            onboarding.continueWithoutFullDiskAccess(
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
          skippedFullDiskAccess: onboarding.skippedFullDiskAccess,
          onCopyAIInstructions: onboarding.didCopyAIInstructions,
          onBack: onboarding.showChooseApps,
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
          onSignInGmail: onSignInGmail,
          onSignInX: onSignInX,
          onOpenSourceApp: onOpenSourceApp,
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
    .sheet(isPresented: $isShowingRemoteConnectionSheet) {
      if let connectionStore {
        RemoteArchiveConnectionView(
          connectionStore: connectionStore,
          showsLocalOption: false,
          onConnectionChanged: {
            onConnectedToRemoteArchive?()
          },
          onDismiss: {
            isShowingRemoteConnectionSheet = false
          }
        )
      }
    }
  }

  private var reportedTrawlers: [RegisteredTrawlerIdentity] {
    appModel.displayedTrawlers
  }

  private func refreshedTrawlersToUpdate() -> [RegisteredTrawlerIdentity] {
    appInstallations.refresh(
      registeredTrawlerCatalog: appModel.registeredTrawlerCatalog
    )
    let candidates = flags.trawlersToUpdate(
      reportedTrawlers: appModel.updateCandidateTrawlers,
      unavailableTrawlers: appInstallations.unavailableTrawlers
    )
    if appModel.fullDiskAccessChoice == .leftOut {
      return candidates.filter {
        !AppOperabilityState.fullDiskAccessTrawlerIDs.contains($0.registeredTrawlerIdentity)
      }
    }
    return candidates
  }
}

struct ChooseAppsStep: View {
  let initialChoice: FullDiskAccessChoice
  let buildIdentity: BuildIdentity
  let showsRemoteArchiveLink: Bool
  let onBack: () -> Void
  let onConnectRemoteArchive: () -> Void
  let onIncludeFullDiskAccess: () -> Void
  let onLeaveOutFullDiskAccess: () -> Void

  @State private var includeLocalMessageApps: Bool
  @State private var copiedAuditPrompt = false

  init(
    initialChoice: FullDiskAccessChoice,
    buildIdentity: BuildIdentity,
    showsRemoteArchiveLink: Bool,
    onBack: @escaping () -> Void,
    onConnectRemoteArchive: @escaping () -> Void,
    onIncludeFullDiskAccess: @escaping () -> Void,
    onLeaveOutFullDiskAccess: @escaping () -> Void
  ) {
    self.initialChoice = initialChoice
    self.buildIdentity = buildIdentity
    self.showsRemoteArchiveLink = showsRemoteArchiveLink
    self.onBack = onBack
    self.onConnectRemoteArchive = onConnectRemoteArchive
    self.onIncludeFullDiskAccess = onIncludeFullDiskAccess
    self.onLeaveOutFullDiskAccess = onLeaveOutFullDiskAccess
    _includeLocalMessageApps = State(initialValue: initialChoice != .leftOut)
  }

  var body: some View {
    TrawlFlowScaffold(
      page: .chooseApps,
      contentWidth: TrawlDesign.onboardingPageWidth
    ) {
      VStack(alignment: .leading, spacing: 18) {
        OnboardingProse(
          title: "Choose what OpenTrawl reads.",
          lede:
            "macOS treats five apps differently from the rest. Decide now whether to include them, and you can change your mind in Settings at any time."
        )

        VStack(alignment: .leading, spacing: 10) {
          choiceCard(
            selected: includeLocalMessageApps,
            title: "Include Messages, Mail, Notes, WhatsApp, and Telegram",
            detail:
              "macOS puts these five behind one Full Disk Access switch. It is all five or none, and it lets OpenTrawl read any file your Mac account can read. OpenTrawl only reads these five apps and never changes them. Reads Telegram from the App Store, not Telegram Desktop."
          ) {
            includeLocalMessageApps = true
          }

          choiceCard(
            selected: !includeLocalMessageApps,
            title: "Leave those five out for now",
            detail:
              "Skips Full Disk Access. You can still search Photos, Calendar, and Contacts (macOS asks for each one on its own), plus Gmail and X when you sign in."
          ) {
            includeLocalMessageApps = false
          }
        }

        VStack(alignment: .leading, spacing: 6) {
          Text("Other apps you can include")
            .trawlText(.sectionHeader)
          Text(
            "Photos, Calendar, and Contacts ask for macOS permission one by one. Gmail opens Google in your browser (read-only; OpenTrawl cannot send or change mail). X opens X sign-in to read your posts, bookmarks, likes, mentions, and older messages."
          )
          .font(.system(size: 13))
          .foregroundStyle(.secondary)
          .fixedSize(horizontal: false, vertical: true)
        }

        OnboardingInformationGroup(title: HumanCopy.FullDiskAccess.trustGroupTitle) {
          VStack(alignment: .leading, spacing: 10) {
            Text(HumanCopy.FullDiskAccess.trustGroupBody)
              .font(.system(size: 13))
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
    } actions: {
      OnboardingActionRow(
        backAction: onBack,
        secondaryTitle: showsRemoteArchiveLink ? "Search an archive on another Mac…" : nil,
        secondaryAction: showsRemoteArchiveLink ? onConnectRemoteArchive : nil,
        primaryTitle: includeLocalMessageApps
          ? "Continue to Full Disk Access"
          : "Build archive without those five",
        primaryAction: {
          if includeLocalMessageApps {
            onIncludeFullDiskAccess()
          } else {
            onLeaveOutFullDiskAccess()
          }
        }
      )
    }
  }

  private func choiceCard(
    selected: Bool,
    title: String,
    detail: String,
    action: @escaping () -> Void
  ) -> some View {
    Button(action: action) {
      HStack(alignment: .top, spacing: 12) {
        Image(systemName: selected ? "largecircle.fill.circle" : "circle")
          .font(.system(size: 16, weight: .semibold))
          .foregroundStyle(selected ? TrawlDesign.Colors.selectBlue : .secondary)
          .padding(.top, 2)
        VStack(alignment: .leading, spacing: 4) {
          Text(title)
            .font(.system(size: 14, weight: .semibold))
            .foregroundStyle(TrawlDesign.Colors.ink)
          Text(detail)
            .font(.system(size: 12.5))
            .foregroundStyle(.secondary)
            .fixedSize(horizontal: false, vertical: true)
        }
        Spacer(minLength: 0)
      }
      .padding(14)
      .frame(maxWidth: .infinity, alignment: .leading)
      .background(
        selected ? TrawlDesign.Colors.selectBlueSurface : TrawlDesign.Colors.surface.opacity(0.6),
        in: RoundedRectangle(cornerRadius: 11, style: .continuous)
      )
      .overlay(
        RoundedRectangle(cornerRadius: 11, style: .continuous)
          .stroke(
            selected ? TrawlDesign.Colors.selectBlue.opacity(0.5) : TrawlDesign.Colors.hairline,
            lineWidth: selected ? 1.5 : 1
          )
      )
    }
    .buttonStyle(.plain)
  }
}

struct WelcomeStep: View {
  var icon = NSApplication.shared.applicationIconImage
  let registeredTrawlerCatalog: [RegisteredTrawlerCatalogEntry]
  var secondaryTitle: String? = nil
  var onSecondaryAction: (() -> Void)? = nil
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
        secondaryTitle: secondaryTitle,
        secondaryAction: onSecondaryAction,
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

  private var allEntriesInDisplayOrder: [RegisteredTrawlerCatalogEntry] {
    searchableEntries + comingSoonEntries
  }

  private var iconRows: [[RegisteredTrawlerCatalogEntry]] {
    let entries = allEntriesInDisplayOrder
    guard entries.count > 8 else { return entries.isEmpty ? [] : [entries] }
    let firstRowCount = (entries.count + 1) / 2
    return [Array(entries.prefix(firstRowCount)), Array(entries.dropFirst(firstRowCount))]
  }

  var body: some View {
    VStack(spacing: TrawlDesign.onboardingElementSpacing) {
      Text(HumanCopy.Welcome.appsTitle)
        .trawlText(.sectionHeader)
      VStack(spacing: 16) {
        ForEach(Array(iconRows.enumerated()), id: \.offset) { _, row in
          HStack(spacing: 20) {
            ForEach(row, id: \.id) { entry in
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
