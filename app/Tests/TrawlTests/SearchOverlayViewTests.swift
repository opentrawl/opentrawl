import Foundation
import Testing

@testable import Trawl

@Suite(.serialized)
struct SearchOverlayViewTests {
  @Test func mainWindowDefaultsTo1360By860With1200By820MinimumAndFixedOnboarding() {
    let minimum = CGSize(width: 1_200, height: 820)
    let defaultSize = CGSize(width: 1_360, height: 860)

    #expect(TrawlDesign.defaultWindow == defaultSize)
    #expect(TrawlDesign.minimumWindow == minimum)
    #expect(TrawlDesign.onboardingWindow == minimum)
    #expect(!TrawlDesign.usesCompactSearchLayout(width: minimum.width))
    #expect(!TrawlDesign.usesCompactSearchLayout(width: defaultSize.width))
  }
}
