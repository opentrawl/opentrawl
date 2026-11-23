import Foundation
import Testing

@testable import Trawl

@Suite(.serialized)
struct SearchOverlayViewTests {
  @Test func mainWorkspaceUsesResizableBoundsAndSplitLayoutAtDefaultWidth() {
    let defaultSize = CGSize(width: 1_200, height: 820)
    let minimumSize = CGSize(width: 920, height: 620)

    #expect(TrawlDesign.defaultWindow == defaultSize)
    #expect(TrawlDesign.minimumWindow == minimumSize)
    #expect(TrawlDesign.onboardingWindow == defaultSize)
    #expect(!TrawlDesign.usesCompactSearchLayout(width: defaultSize.width - TrawlDesign.sidebarWidth))
    #expect(TrawlDesign.usesCompactSearchLayout(width: 600))
  }
}
