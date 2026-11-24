import Foundation
import Testing

@testable import Trawl

@Suite(.serialized)
struct SearchOverlayViewTests {
  @Test func mainWindowDefaultsTo1360By860With720By680MinimumAndFixedOnboarding() {
    let minimum = CGSize(width: 720, height: 680)
    let onboarding = CGSize(width: 960, height: 740)
    let defaultSize = CGSize(width: 1_360, height: 860)

    #expect(TrawlDesign.defaultWindow == defaultSize)
    #expect(TrawlDesign.minimumWindow == minimum)
    #expect(TrawlDesign.onboardingWindow == onboarding)
    #expect(!TrawlDesign.usesCompactSearchLayout(width: defaultSize.width))
  }
}
