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

  @MainActor
  @Test func detectorUsesOnlyAvailableCatalogueEntries() {
    let installations = MacAppInstallations(
      environment: [:],
      applicationIsInstalled: { _ in false }
    )
    installations.refresh(catalog: [
      SourceCatalogEntry(
        manifest: installationManifest(
          id: "notes", name: "Notes", bundleIdentifier: "com.apple.Notes"),
        releaseState: .available,
        enabled: true
      ),
      SourceCatalogEntry(
        manifest: installationManifest(
          id: "gmail", name: "Gmail", bundleIdentifier: "com.google.Gmail"),
        releaseState: .comingSoon,
        enabled: true
      ),
    ])

    #expect(installations.unavailableAppIDs == ["notes"])
    #expect(installations.isAvailable("gmail"))
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

    #expect(row.statusLabel == OperationalCopy.AppStatus.notInstalled)
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

    #expect(row.statusLabel == OperationalCopy.AppStatus.comingSoon)
    #expect(row.status == .neutral)
    #expect(!row.canRetry)
  }

  @Test func catalogueComingSoonWinsOverEnabledRuntimeStatus() {
    let row = AppBuildRowPresentation.resolve(
      appID: "gmail",
      name: "Gmail",
      counts: [SourceCount(id: "messages", label: "Messages", value: 12)],
      progress: .finished,
      failure: nil,
      skipped: nil,
      releaseState: .comingSoon,
      isInstalled: true,
      suppressPermissionFailure: false
    )

    #expect(row.statusLabel == OperationalCopy.AppStatus.comingSoon)
    #expect(row.status == .neutral)
    #expect(!row.canRetry)
  }

  @Test func automaticSyncTaskIdentityChangesWithDetectedApps() {
    let first = AutomaticSyncTaskID(
      onboardingStage: .building,
      appIDs: ["imessage", "whatsapp"]
    )
    let removed = AutomaticSyncTaskID(
      onboardingStage: .building,
      appIDs: ["imessage"]
    )
    let completed = AutomaticSyncTaskID(
      onboardingStage: .complete,
      appIDs: ["imessage"]
    )
    #expect(first != removed)
    #expect(removed != completed)
    #expect(!AutomaticSyncTaskID(onboardingStage: .welcome, appIDs: []).shouldRun)
    #expect(!AutomaticSyncTaskID(onboardingStage: .permission, appIDs: []).shouldRun)
    #expect(first.shouldRun)
    #expect(completed.shouldRun)
  }

  @MainActor
  @Test func onboardingResumesSameBuildAndKeepsAIConnectionInsideArchiveBuilding() {
    let suite = "OnboardingTests.\(UUID().uuidString)"
    let defaults = UserDefaults(suiteName: suite)!
    defer { defaults.removePersistentDomain(forName: suite) }
    let onboarding = OnboardingModel(
      defaults: defaults,
      checkpointOwner: "build-a",
      openFullDiskAccess: {}
    )

    #expect(onboarding.stage == .welcome)
    onboarding.showPermission()
    #expect(onboarding.stage == .permission)
    #expect(
      OnboardingModel(
        defaults: defaults,
        checkpointOwner: "build-a",
        openFullDiskAccess: {}
      ).stage == .permission
    )

    let appModel = AppModel(client: OnboardingClient())
    onboarding.startInitialSync(appModel: appModel, appIDs: [])
    #expect(onboarding.stage == .building)

    onboarding.showPermission(appModel: appModel)
    #expect(onboarding.stage == .permission)
    onboarding.showWelcome()
    #expect(onboarding.stage == .welcome)
    #expect(defaults.string(forKey: OnboardingModel.checkpointKey) == OnboardingStage.building.rawValue)
    onboarding.showPermission(appModel: appModel)
    onboarding.returnToBuilding()
    #expect(onboarding.stage == .building)

    onboarding.didCopyAIInstructions()
    #expect(onboarding.hasCopiedAIInstructions)
    #expect(onboarding.stage == .building)
    #expect(!onboarding.isComplete)

    onboarding.complete()
    #expect(onboarding.isComplete)
    #expect(OnboardingModel(defaults: defaults).isComplete)
  }

  @MainActor
  @Test func aNewBuildStartsAtWelcomeInsteadOfRestoringAnOldCheckpoint() {
    let suite = "OnboardingTests.\(UUID().uuidString)"
    let defaults = UserDefaults(suiteName: suite)!
    defer { defaults.removePersistentDomain(forName: suite) }
    defaults.set(OnboardingStage.permission.rawValue, forKey: OnboardingModel.checkpointKey)
    defaults.set("old-build", forKey: OnboardingModel.checkpointOwnerKey)

    let onboarding = OnboardingModel(
      defaults: defaults,
      checkpointOwner: "new-build",
      openFullDiskAccess: {}
    )

    #expect(onboarding.stage == .welcome)
    #expect(defaults.string(forKey: OnboardingModel.checkpointKey) == nil)
    #expect(defaults.string(forKey: OnboardingModel.checkpointOwnerKey) == nil)
  }

  @MainActor
  @Test func interruptedFreshBuildResumesOnlyTheRequestedAppsAfterRestart() async throws {
    let suite = "OnboardingTests.\(UUID().uuidString)"
    let defaults = UserDefaults(suiteName: suite)!
    defer { defaults.removePersistentDomain(forName: suite) }
    defaults.set(OnboardingStage.building.rawValue, forKey: OnboardingModel.checkpointKey)
    defaults.set("test-build", forKey: OnboardingModel.checkpointOwnerKey)

    let client = ResumeRecordingClient()
    let appModel = AppModel(client: client)
    let onboarding = OnboardingModel(
      defaults: defaults,
      checkpointOwner: "test-build",
      openFullDiskAccess: {}
    )

    #expect(onboarding.stage == .building)
    onboarding.resumeInitialSyncIfNeeded(
      appModel: appModel,
      appIDs: ["imessage", "notes"]
    )

    try await confirmation { resumed in
      while client.requestedAppIDBatches.isEmpty {
        try await Task.sleep(for: .milliseconds(10))
      }
      resumed()
    }
    #expect(client.requestedAppIDBatches == [["imessage", "notes"]])
    #expect(appModel.syncProgress["imessage"] == .finished)
    #expect(appModel.syncProgress["notes"] == .finished)

    onboarding.resumeInitialSyncIfNeeded(
      appModel: appModel,
      appIDs: ["imessage", "notes"]
    )
    #expect(client.requestedAppIDBatches == [["imessage", "notes"]])
  }

  @Test func aiInstructionNamesItsIntentAndDoesNotClaimToChangeConfiguration() {
    let instruction = AgentPrompts.connectAI
    #expect(instruction.hasPrefix("Help me start using OpenTrawl"))
    #expect(
      instruction.contains(
        "/Applications/OpenTrawl.app/Contents/Helpers/trawl"))
    #expect(instruction.contains("--help"))
    #expect(instruction.contains("Do not change any files or configuration"))
    #expect(instruction.contains("Only discuss or draft an integration if I explicitly ask"))
    #expect(instruction.contains("Wait for my explicit approval"))
    #expect(instruction.contains("A request to explore an option is not approval"))
    #expect(DraftCopy.ConnectAI.body.contains("does not install"))
    #expect(DraftCopy.ConnectAI.body.contains("settings"))
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

private final class ResumeRecordingClient: TrawlClient, @unchecked Sendable {
  private let lock = NSLock()
  private var requestedBatches: [[String]] = []

  var requestedAppIDBatches: [[String]] {
    lock.withLock { requestedBatches }
  }

  func status() async throws -> StatusResponse {
    StatusResponse(sources: [], failures: [], skippedSources: [], outcome: .complete)
  }

  func requestPhotos() async throws -> StatusResponse { fatalError() }
  func sync() async throws -> SyncResponse { fatalError() }

  func sync(
    sourceIDs: [String],
    progress: @escaping @Sendable (SyncProgress) -> Void
  ) async throws -> SyncResponse {
    lock.withLock { requestedBatches.append(sourceIDs) }
    let results = sourceIDs.map {
      SyncSourceResult(sourceID: $0, sourceName: $0, outcome: .complete, failure: nil)
    }
    for result in results {
      progress(.building(sourceID: result.sourceID))
      progress(.finalising(sourceID: result.sourceID))
    }
    return SyncResponse(sources: results, failures: [], outcome: .complete)
  }

  func search(_: String, source _: String?) async throws -> SearchResponse { fatalError() }

  func open(sourceID _: String, ref _: String, anchorID _: String) async throws -> OpenResponse {
    fatalError()
  }
}
