import Foundation
import Testing

@testable import Trawl
@testable import TrawlClient
@testable import TrawlCore

@Suite(.serialized)
struct OnboardingTests {
  @Test func betaUsesTheGoReportedAppsWithoutMaintainingASecondEligibilityList() {
    let flags = AppFeatureFlags.current(environment: [:], defaults: isolatedDefaults())

    #expect(flags.mode == .beta)
    #expect(!flags.isExperimental)
    #expect(flags.includes("future-go-owned-app"))
    #expect(
      flags.syncAppIDs(
        reportedAppIDs: ["notes", "future-go-owned-app", "whatsapp", "notes"],
        unavailableAppIDs: ["whatsapp"]
      )
        == ["notes", "future-go-owned-app"])
  }

  @Test func explicitAllSourcesLaunchModeDoesNotChangeTheReportedAppContract() {
    let flags = AppFeatureFlags.current(
      environment: ["OPENTRAWL_ALL_SOURCES": "1"],
      defaults: isolatedDefaults()
    )

    #expect(flags.mode == .experimental)
    #expect(flags.isExperimental)
    #expect(
      flags.syncAppIDs(
        reportedAppIDs: ["imessage", "gmail", "photos", "gmail"],
        unavailableAppIDs: []
      )
        == ["imessage", "gmail", "photos"])
  }

  @MainActor
  @Test func detectorFindsInstalledAppsAndMasksOneOrMoreAtItsBoundary() {
    let manifests = installationManifests()
    let allBundles = Set(manifests.compactMap { $0.branding?.bundleIdentifier })
    let allInstalled = MacAppInstallations(
      environment: [:],
      applicationIsInstalled: allBundles.contains
    )
    allInstalled.refresh(manifests: manifests)
    #expect(allInstalled.installedAppIDs == ["notes", "telegram", "whatsapp"])

    let oneAbsent = MacAppInstallations(
      environment: [MacAppInstallations.absentAppIDsEnvironmentKey: "whatsapp"],
      applicationIsInstalled: allBundles.contains
    )
    oneAbsent.refresh(manifests: manifests)
    #expect(!oneAbsent.isInstalled("whatsapp"))
    #expect(oneAbsent.isInstalled("telegram"))

    let severalAbsent = MacAppInstallations(
      environment: [MacAppInstallations.absentAppIDsEnvironmentKey: " whatsapp, TELEGRAM "],
      applicationIsInstalled: allBundles.contains
    )
    severalAbsent.refresh(manifests: manifests)
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
    let manifests = installationManifests()
    installations.refresh(manifests: manifests)
    #expect(installations.installedAppIDs.isEmpty)

    lookup.bundleIDs = ["net.whatsapp.WhatsApp"]
    installations.refresh()
    #expect(installations.installedAppIDs == ["whatsapp"])

    lookup.bundleIDs = ["ru.keepcoder.Telegram"]
    installations.refresh()
    #expect(installations.installedAppIDs == ["telegram"])
  }

  @Test func syncCandidatesPreserveHelperOrderAndFilterOnlyUnavailableMacApps() {
    let flags = AppFeatureFlags(mode: .beta)
    #expect(
      flags.syncAppIDs(
        reportedAppIDs: ["gmail", "imessage", "whatsapp", "gmail", "notes"],
        unavailableAppIDs: ["whatsapp"]
      ) == ["gmail", "imessage", "notes"])
  }

  @Test func absentRowKeepsExistingArchiveCountsWithoutShowingAStaleFailure() {
    let counts = [SourceCount(id: "messages", label: "Messages", value: 42)]
    let failure = SourceFailure(
      sourceID: "whatsapp",
      sourceName: "WhatsApp",
      code: .permission,
      message: "Synthetic permission failure.",
      remedy: "Synthetic recovery."
    )
    let row = AppBuildRowPresentation.resolve(
      appID: "whatsapp",
      name: "WhatsApp",
      counts: counts,
      progress: .failed("Synthetic permission failure."),
      failure: failure,
      skipped: nil,
      isInstalled: false,
      suppressPermissionFailure: false
    )

    #expect(row.counts == "42 messages")
    #expect(row.detail == nil)
    #expect(row.statusLabel == OperationalCopy.notInstalled)
    #expect(row.status == .neutral)
    #expect(!row.canRetry)
  }

  @Test func skippedSourceIsComingSoonAndCannotRetry() {
    let row = AppBuildRowPresentation.resolve(
      appID: "photos",
      name: "Photos",
      counts: [],
      progress: nil,
      failure: nil,
      skipped: SkippedSource(
        sourceID: "photos",
        surface: "Photos",
        reason: "Synthetic helper-owned skip."
      ),
      isInstalled: true,
      suppressPermissionFailure: false
    )

    #expect(row.statusLabel == OperationalCopy.comingSoon)
    #expect(row.status == .neutral)
    #expect(!row.canRetry)
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
  @Test func onboardingResumesPermissionAndKeepsAIConnectionInsideArchiveBuilding() {
    let suite = "OnboardingTests.\(UUID().uuidString)"
    let defaults = UserDefaults(suiteName: suite)!
    defer { defaults.removePersistentDomain(forName: suite) }
    let onboarding = OnboardingModel(defaults: defaults, openFullDiskAccess: {})

    #expect(onboarding.stage == .welcome)
    onboarding.showPermission()
    #expect(onboarding.stage == .permission)
    #expect(OnboardingModel(defaults: defaults, openFullDiskAccess: {}).stage == .permission)

    let appModel = AppModel(client: OnboardingClient())
    onboarding.startInitialSync(appModel: appModel, appIDs: [])
    #expect(onboarding.stage == .building)

    onboarding.didCopyAIInstructions()
    #expect(onboarding.hasCopiedAIInstructions)
    #expect(onboarding.stage == .building)
    #expect(!onboarding.isComplete)

    onboarding.complete()
    #expect(onboarding.isComplete)
    #expect(OnboardingModel(defaults: defaults).isComplete)
  }

  @Test func aiInstructionNamesItsIntentAndDoesNotClaimToChangeConfiguration() {
    let instruction = AgentPrompts.connectAI(
      helperCommand: "/Applications/OpenTrawl.app/Contents/Helpers/trawl"
    )
    #expect(instruction.hasPrefix("Intent:"))
    #expect(
      instruction.contains(
        "/Applications/OpenTrawl.app/Contents/Helpers/trawl"))
    #expect(instruction.contains("--help"))
    #expect(instruction.contains("Do not install a skill"))
    #expect(instruction.contains("asking for approval first"))
    #expect(HumanCopy.aiDoesNotInstall.contains("does not install"))
    #expect(HumanCopy.aiDoesNotInstall.contains("AI configuration"))
  }

  @Test func protectedCopyAndPromptFilesDeclareTheirHardBoundaries() throws {
    let trawlSources = URL(fileURLWithPath: #filePath)
      .deletingLastPathComponent()
      .deletingLastPathComponent()
      .deletingLastPathComponent()
      .appending(path: "Sources/Trawl")
    let humanCopy = try String(
      contentsOf: trawlSources.appending(path: "HumanCopy.swift"),
      encoding: .utf8
    )
    let agentPrompts = try String(
      contentsOf: trawlSources.appending(path: "AgentPrompts.swift"),
      encoding: .utf8
    )
    let folderRules = try String(
      contentsOf: trawlSources.appending(path: "AGENTS.md"),
      encoding: .utf8
    )

    #expect(humanCopy.contains("AGENTS MUST NEVER EDIT THESE STRINGS"))
    #expect(humanCopy.contains("THIS FILE MUST ALWAYS REMAIN TRACKED AND COMMITTED"))
    #expect(agentPrompts.contains("OFFICIAL GPT-5.6"))
    #expect(agentPrompts.contains("STATE ITS ACTUAL INTENT INSIDE THE PROMPT"))
    #expect(folderRules.contains("HumanCopy.swift"))
    #expect(folderRules.contains("AgentPrompts.swift"))
    #expect(folderRules.contains("ADS-STE100"))
    #expect(AgentPrompts.auditBuild(.init(version: "test", gitCommit: nil)).hasPrefix("Intent:"))
  }

  private func isolatedDefaults() -> UserDefaults {
    let suite = "OnboardingTests.\(UUID().uuidString)"
    let defaults = UserDefaults(suiteName: suite)!
    defaults.removePersistentDomain(forName: suite)
    return defaults
  }

  private func installationManifests() -> [SourceManifest] {
    [
      installationManifest(
        id: "notes", name: "Notes", bundleIdentifier: "com.apple.Notes"),
      installationManifest(
        id: "telegram", name: "Telegram", bundleIdentifier: "ru.keepcoder.Telegram"),
      installationManifest(
        id: "whatsapp", name: "WhatsApp", bundleIdentifier: "net.whatsapp.WhatsApp"),
    ]
  }

  private func installationManifest(
    id: String,
    name: String,
    bundleIdentifier: String
  ) -> SourceManifest {
    SourceManifest(
      sourceID: id,
      displayName: name,
      branding: Branding(
        symbolName: "",
        accentColor: "",
        iconPath: "",
        bundleIdentifier: bundleIdentifier
      ),
      headlines: [],
      capabilities: []
    )
  }
}

private struct OnboardingClient: TrawlClient {
  func status() async throws -> StatusResponse {
    StatusResponse(sources: [], failures: [], skippedSources: [], outcome: .complete)
  }

  func requestPhotos() async throws -> StatusResponse { fatalError() }
  func sync() async throws -> SyncResponse { fatalError() }
  func search(_: String, source _: String?) async throws -> SearchResponse { fatalError() }
  func open(sourceID _: String, ref _: String, anchorID _: String) async throws -> OpenResponse {
    fatalError()
  }
}

private final class MutableBundleLookup {
  var bundleIDs: Set<String> = []

  func contains(_ bundleID: String) -> Bool {
    bundleIDs.contains(bundleID)
  }
}
