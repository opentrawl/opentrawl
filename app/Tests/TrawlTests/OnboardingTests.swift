import Foundation
import Testing

@testable import Trawl
@testable import TrawlClient

@Suite(.serialized)
struct OnboardingTests {
  @Test func betaFlagsExposeOnlyTheFivePromisedApps() {
    let flags = AppFeatureFlags.current(environment: [:], defaults: isolatedDefaults())

    #expect(flags.includes("contacts"))
    #expect(flags.includes("imessage"))
    #expect(flags.includes("notes"))
    #expect(flags.includes("telegram"))
    #expect(flags.includes("whatsapp"))
    #expect(!flags.includes("gmail"))
    #expect(!flags.includes("calendar"))
    #expect(!flags.includes("photos"))
    #expect(!flags.includes("twitter"))
    #expect(
      flags.syncAppIDs(
        reportedAppIDs: ["gmail", "photos"],
        installedAppIDs: AppFeatureFlags.betaAppIDs
      )
        == ["imessage", "whatsapp", "telegram", "notes", "contacts"])
  }

  @Test func experimentalAppsCanBeEnabledLocallyWithoutARemoteService() {
    let flags = AppFeatureFlags.current(
      environment: ["OPENTRAWL_ENABLE_EXPERIMENTAL_APPS": "1"],
      defaults: isolatedDefaults()
    )

    #expect(flags.enabledAppIDs == nil)
    #expect(flags.includes("gmail"))
    #expect(flags.includes("twitter"))
    #expect(
      flags.syncAppIDs(
        reportedAppIDs: ["imessage", "gmail", "photos"],
        installedAppIDs: ["imessage", "photos"]
      )
        == ["imessage", "gmail", "photos"])
  }

  @MainActor
  @Test func detectorFindsInstalledAppsAndMasksOneOrMoreAtItsBoundary() {
    #expect(MacAppCatalog.bundleIdentifier(for: "telegram") == "ru.keepcoder.Telegram")
    let allBundles = Set(MacAppCatalog.apps.map(\.bundleIdentifier))
    let allInstalled = MacAppInstallations(
      environment: [:],
      applicationIsInstalled: allBundles.contains
    )
    #expect(allInstalled.installedAppIDs.isSuperset(of: AppFeatureFlags.betaAppIDs))

    let oneAbsent = MacAppInstallations(
      environment: [MacAppInstallations.absentAppIDsEnvironmentKey: "whatsapp"],
      applicationIsInstalled: allBundles.contains
    )
    #expect(!oneAbsent.isInstalled("whatsapp"))
    #expect(oneAbsent.isInstalled("telegram"))

    let severalAbsent = MacAppInstallations(
      environment: [MacAppInstallations.absentAppIDsEnvironmentKey: " whatsapp, TELEGRAM "],
      applicationIsInstalled: allBundles.contains
    )
    #expect(!severalAbsent.isInstalled("whatsapp"))
    #expect(!severalAbsent.isInstalled("telegram"))
    #expect(severalAbsent.isInstalled("notes"))
  }

  @MainActor
  @Test func detectorRefreshObservesLaterInstallationAndRemoval() {
    let lookup = MutableBundleLookup()
    let installations = MacAppInstallations(
      environment: [:],
      applicationIsInstalled: lookup.contains
    )
    #expect(installations.installedAppIDs.isEmpty)

    lookup.bundleIDs = ["net.whatsapp.WhatsApp"]
    installations.refresh()
    #expect(installations.installedAppIDs == ["whatsapp"])

    lookup.bundleIDs = ["ru.keepcoder.Telegram"]
    installations.refresh()
    #expect(installations.installedAppIDs == ["telegram"])
  }

  @Test func productionSyncCandidatesIncludeOnlyInstalledBetaApps() {
    let flags = AppFeatureFlags(enabledAppIDs: AppFeatureFlags.betaAppIDs)
    #expect(
      flags.syncAppIDs(
        reportedAppIDs: ["imessage", "whatsapp", "gmail"],
        installedAppIDs: ["imessage", "notes"]
      ) == ["imessage", "notes"])
  }

  @Test func experimentalOnlineAppsRemainEligibleWithoutMacBundles() {
    let flags = AppFeatureFlags(enabledAppIDs: nil)
    #expect(
      flags.syncAppIDs(
        reportedAppIDs: ["imessage", "gmail", "photos", "twitter"],
        installedAppIDs: ["imessage"]
      ) == ["imessage", "gmail", "twitter"])
    #expect(
      flags.onboardingAppIDs(reportedAppIDs: ["gmail"])
        == AppFeatureFlags.betaAppOrder + ["gmail"])
  }

  @Test func absentRowKeepsExistingArchiveCountsWithoutShowingAStaleFailure() {
    let counts = [SourceCount(id: "messages", label: "Messages", value: 42)]
    let row = AppSyncRowPresentation(
      name: "WhatsApp",
      counts: counts,
      detail: "Full Disk Access is required.",
      progress: .failed("Permission denied."),
      isInstalled: false
    )

    #expect(row.counts == counts)
    #expect(row.visibleDetail == nil)
    #expect(row.progressLabel == OnboardingStrings.notInstalled)
    #expect(!row.progressIsFailure)
  }

  @Test func automaticSyncTaskIdentityChangesWithDetectedApps() {
    let first = AutomaticSyncTaskID(
      isOnboardingComplete: true,
      appIDs: ["imessage", "whatsapp"]
    )
    let removed = AutomaticSyncTaskID(
      isOnboardingComplete: true,
      appIDs: ["imessage"]
    )
    #expect(first != removed)
  }

  @MainActor
  @Test func onboardingFollowsTheTrustArchiveAgentJourneyAndPersistsCompletion() {
    let suite = "OnboardingTests.\(UUID().uuidString)"
    let defaults = UserDefaults(suiteName: suite)!
    defer { defaults.removePersistentDomain(forName: suite) }
    let onboarding = OnboardingModel(defaults: defaults)

    #expect(onboarding.stage == .welcome)
    onboarding.showTrust()
    #expect(onboarding.stage == .trust)
    onboarding.showReady()
    #expect(onboarding.stage == .ready)
    onboarding.showAgent()
    #expect(onboarding.stage == .agent)
    onboarding.didCopyAgentInstruction()
    #expect(onboarding.isComplete)
    #expect(OnboardingModel(defaults: defaults).isComplete)
  }

  @Test func agentInstructionNamesTheBundledCLIAndDoesNotClaimToInstallAnything() {
    #expect(
      OnboardingStrings.agentInstruction.contains(
        "/Applications/OpenTrawl.app/Contents/Helpers/trawl"))
    #expect(OnboardingStrings.agentInstruction.contains("--help"))
    #expect(OnboardingStrings.agentDoesNotInstall.contains("does not install"))
  }

  private func isolatedDefaults() -> UserDefaults {
    let suite = "OnboardingTests.\(UUID().uuidString)"
    let defaults = UserDefaults(suiteName: suite)!
    defaults.removePersistentDomain(forName: suite)
    return defaults
  }
}

private final class MutableBundleLookup {
  var bundleIDs: Set<String> = []

  func contains(_ bundleID: String) -> Bool {
    bundleIDs.contains(bundleID)
  }
}
