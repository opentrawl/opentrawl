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
