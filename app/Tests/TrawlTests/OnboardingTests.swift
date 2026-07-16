import Foundation
import Testing

@testable import Trawl

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
      flags.syncAppIDs(reportedAppIDs: ["gmail", "photos"])
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
      flags.syncAppIDs(reportedAppIDs: ["imessage", "gmail", "photos"])
        == ["imessage", "gmail", "photos"])
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
    onboarding.complete()
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
