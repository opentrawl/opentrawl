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
    #expect(!first.shouldRun)
    #expect(completed.shouldRun)
  }

  @MainActor
  @Test func onboardingCheckpointSurvivesAnAppUpdate() {
    let suite = "OnboardingTests.\(UUID().uuidString)"
    let defaults = UserDefaults(suiteName: suite)!
    defer { defaults.removePersistentDomain(forName: suite) }
    defaults.set(OnboardingStage.permission.rawValue, forKey: OnboardingModel.checkpointKey)

    let onboarding = OnboardingModel(
      defaults: defaults,
      openFullDiskAccess: {}
    )

    #expect(onboarding.stage == .permission)
  }

  @MainActor
  @Test func resetReturnsToWelcomeAndClearsCompletion() {
    let suite = "OnboardingTests.\(UUID().uuidString)"
    let defaults = UserDefaults(suiteName: suite)!
    defer { defaults.removePersistentDomain(forName: suite) }
    let onboarding = OnboardingModel(
      defaults: defaults,
      openFullDiskAccess: {}
    )
    onboarding.complete()
    #expect(onboarding.isComplete)

    onboarding.reset()

    #expect(onboarding.stage == .welcome)
    #expect(!defaults.bool(forKey: OnboardingModel.completionKey))
    #expect(defaults.string(forKey: OnboardingModel.checkpointKey) == nil)
  }

  @Test func aiInstructionNamesItsIntentAndDoesNotClaimToChangeConfiguration() {
    let instruction = AgentPrompts.connectAI
    #expect(instruction.hasPrefix("Help me start using OpenTrawl"))
    #expect(
      instruction.contains(
        "/Applications/OpenTrawl.app/Contents/Helpers/trawl"))
    #expect(instruction.contains("--help"))
    #expect(
      instruction.contains("Do not run update, install plugins, or change any files or configuration"))
    #expect(
      instruction.contains(
        "Once you have shown me the demo, ask me what I think and whether I want you to use OpenTrawl more often"
      ))
    #expect(instruction.contains("Wait for my explicit approval"))
    #expect(instruction.contains("A request to explore an option is not approval"))
  }

}
