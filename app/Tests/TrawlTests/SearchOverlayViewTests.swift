import Foundation
import Testing

@testable import Trawl

@Suite(.serialized)
struct SearchOverlayViewTests {
  @Test func productUsesOneFixed1120By760Frame() {
    let frame = CGSize(width: 1_120, height: 760)

    #expect(TrawlDesign.defaultWindow == frame)
    #expect(TrawlDesign.minimumWindow == frame)
    #expect(TrawlDesign.maximumWindow == frame)
    #expect(TrawlDesign.onboardingWindow == frame)
    #expect(!TrawlDesign.usesCompactSearchLayout(width: frame.width))
  }
}
