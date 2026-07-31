import Foundation
import Testing

@testable import Trawl
@testable import TrawlClient
@testable import TrawlCore

@Suite(.serialized)
struct OnboardingTests {
  @Test func automaticUpdateTaskIdentityChangesWithDetectedApps() {
    let first = AutomaticUpdateTaskID(
      onboardingStage: .building,
      registeredTrawlers: [
        RegisteredTrawlerIdentity(registeredTrawlerIdentity: "imessage"),
        RegisteredTrawlerIdentity(registeredTrawlerIdentity: "whatsapp"),
      ]
    )
    let removed = AutomaticUpdateTaskID(
      onboardingStage: .building,
      registeredTrawlers: [
        RegisteredTrawlerIdentity(registeredTrawlerIdentity: "imessage")
      ]
    )
    let completed = AutomaticUpdateTaskID(
      onboardingStage: .complete,
      registeredTrawlers: [
        RegisteredTrawlerIdentity(registeredTrawlerIdentity: "imessage")
      ]
    )
    #expect(first != removed)
    #expect(removed != completed)
    #expect(
      !AutomaticUpdateTaskID(onboardingStage: .welcome, registeredTrawlers: []).shouldRun)
    #expect(
      !AutomaticUpdateTaskID(onboardingStage: .permission, registeredTrawlers: []).shouldRun)
    #expect(first.shouldRun)
    #expect(completed.shouldRun)
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

}
