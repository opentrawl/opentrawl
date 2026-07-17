import Foundation
import Testing

@testable import Trawl

struct BuildIdentityTests {
  private let commit = "cca479dc70f9cbf4ba387ac8d7aae1f96769f290"

  @Test func buildIdentityPresentsTheVersionAndPinnedSource() throws {
    let identity = BuildIdentity(version: "0.1.0", gitCommit: commit)

    #expect(identity.displayName == "OpenTrawl 0.1.0 · cca479d")
    #expect(
      identity.sourceURL?.absoluteString == "https://github.com/opentrawl/opentrawl/tree/\(commit)")
  }

  @Test func auditPromptIsPinnedAndKeepsPreReleaseCodeOutOfTheBetaVerdict() {
    let identity = BuildIdentity(version: "0.1.0", gitCommit: commit)
    let prompt = OnboardingStrings.auditPrompt(for: identity)

    #expect(prompt.contains(commit))
    #expect(prompt.contains("has no telemetry or analytics"))
    #expect(prompt.contains("does not run servers"))
    #expect(prompt.contains("it requests that media from Telegram"))
    #expect(prompt.contains("ignore disabled or feature-flagged pre-release features"))
    #expect(prompt.contains("standalone crawler commands the beta does not offer"))
    #expect(prompt.contains("Not part of the production beta"))
    #expect(prompt.contains("continue with the source review"))
    #expect(prompt.contains("Do not treat that alone as a privacy problem."))
  }

  @Test func localChangesAreVisibleAndDoNotPretendTheCommitIsTheExactBuild() {
    let identity = BuildIdentity(
      version: "0.1.0",
      gitCommit: commit,
      hasLocalChanges: true
    )
    let prompt = OnboardingStrings.auditPrompt(for: identity)

    #expect(identity.displayName == "OpenTrawl 0.1.0 · cca479d+changes")
    #expect(prompt.contains("based on Git commit"))
    #expect(prompt.contains("includes uncommitted changes"))
    #expect(prompt.contains("The link does not show this build's local changes."))
    #expect(!prompt.contains("built from Git commit"))
    #expect(!prompt.contains("at the exact commit above"))
  }
}
